package vcloud

import (
	"context"
	"fmt"
	"strings"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"gitlab.vnetwork.dev/golang/kubernetes/csi-driver-vcloud/pkg/vcloud/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/klog/v2"
)

const (
	// MinVolumeSize is the minimum volume size (1GB)
	MinVolumeSize int64 = 1 * 1024 * 1024 * 1024

	// DefaultVolumeSize is the default volume size (10GB)
	DefaultVolumeSize int64 = 10 * 1024 * 1024 * 1024

	// MaxVolumeSize is the maximum volume size (16TB)
	MaxVolumeSize int64 = 16 * 1024 * 1024 * 1024 * 1024
)

// ControllerServer implements the CSI Controller service
type ControllerServer struct {
	csi.UnimplementedControllerServer
	Driver *Driver
}

// NewControllerServer creates a new ControllerServer
func NewControllerServer(d *Driver) *ControllerServer {
	return &ControllerServer{
		Driver: d,
	}
}

// CreateVolume creates a new volume
func (cs *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	name := req.GetName()
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume name must be provided")
	}

	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume volume capabilities must be provided")
	}

	klog.V(2).Infof("CreateVolume: name(%s)", name)

	// Acquire lock for this volume
	if acquired := cs.Driver.volumeLocks.TryAcquire(name); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, name)
	}
	defer cs.Driver.volumeLocks.Release(name)

	// Validate capabilities
	for _, cap := range volCaps {
		if cap.GetBlock() != nil {
			return nil, status.Error(codes.InvalidArgument, "block volume not supported")
		}
		if cap.GetMount() == nil {
			return nil, status.Error(codes.InvalidArgument, "mount volume capability required")
		}
	}

	// Determine volume size
	var size int64 = DefaultVolumeSize
	if capRange := req.GetCapacityRange(); capRange != nil {
		if capRange.GetRequiredBytes() > 0 {
			size = capRange.GetRequiredBytes()
		}
		if capRange.GetLimitBytes() > 0 && size > capRange.GetLimitBytes() {
			return nil, status.Error(codes.OutOfRange, "requested size exceeds limit")
		}
	}

	if size < MinVolumeSize {
		size = MinVolumeSize
	}
	if size > MaxVolumeSize {
		return nil, status.Errorf(codes.OutOfRange, "requested size %d exceeds maximum %d", size, MaxVolumeSize)
	}

	// Get volume type from parameters
	volumeType := DefaultVolumeType
	if params := req.GetParameters(); params != nil {
		if vt := GetValueFromMap(params, "type"); vt != "" {
			volumeType = vt
		}
	}

	// Check for snapshot source
	var snapshotID string
	if contentSource := req.GetVolumeContentSource(); contentSource != nil {
		if snapshot := contentSource.GetSnapshot(); snapshot != nil {
			snapshotID = snapshot.GetSnapshotId()
			klog.V(2).Infof("CreateVolume: creating from snapshot %s", snapshotID)

			// Verify snapshot exists
			if cs.Driver.vcloudClient != nil {
				_, err := cs.Driver.vcloudClient.GetSnapshot(ctx, snapshotID)
				if err != nil {
					if client.IsNotFoundError(err) {
						return nil, status.Errorf(codes.NotFound, "snapshot %s not found", snapshotID)
					}
					return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
				}
			}
		}
	}

	// Idempotency check - look for existing volume with same name
	if cs.Driver.vcloudClient != nil {
		existing, err := cs.Driver.vcloudClient.GetVolumeByName(ctx, name)
		if err == nil && existing != nil {
			klog.V(2).Infof("CreateVolume: volume %s already exists with ID %s", name, existing.ID)

			// Verify size matches (skip check if API didn't return size)
			if existing.SizeBytes > 0 && existing.SizeBytes != size {
				return nil, status.Errorf(codes.AlreadyExists,
					"volume %s already exists with different size: %d != %d",
					name, existing.SizeBytes, size)
			}

			// Use requested size if API didn't return size
			capacityBytes := existing.SizeBytes
			if capacityBytes == 0 {
				capacityBytes = size
			}

			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					VolumeId:      existing.ID,
					CapacityBytes: capacityBytes,
					VolumeContext: map[string]string{
						"name":       existing.Name,
						"volumeType": existing.VolumeType,
					},
				},
			}, nil
		}

		// Create the volume
		vol, err := cs.Driver.vcloudClient.CreateVolume(ctx, name, size, volumeType, snapshotID)
		if err != nil {
			if client.IsAlreadyExistsError(err) {
				return nil, status.Errorf(codes.AlreadyExists, "volume %s already exists", name)
			}
			return nil, status.Errorf(codes.Internal, "failed to create volume: %v", err)
		}

		klog.V(2).Infof("CreateVolume: created volume %s with ID %s", name, vol.ID)

		resp := &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      vol.ID,
				CapacityBytes: vol.SizeBytes,
				VolumeContext: map[string]string{
					"name":       vol.Name,
					"volumeType": vol.VolumeType,
				},
			},
		}

		// If volume was created from a snapshot, include the content source
		if snapshotID != "" {
			resp.Volume.ContentSource = &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Snapshot{
					Snapshot: &csi.VolumeContentSource_SnapshotSource{
						SnapshotId: snapshotID,
					},
				},
			}
		}

		return resp, nil
	}

	// No vCloud client - return mock response for testing
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      fmt.Sprintf("vol-%s", name),
			CapacityBytes: size,
			VolumeContext: map[string]string{
				"name":       name,
				"volumeType": volumeType,
			},
		},
	}, nil
}

// DeleteVolume deletes a volume
func (cs *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume volume ID must be provided")
	}

	klog.V(2).Infof("DeleteVolume: volumeID(%s)", volumeID)

	// Acquire lock for this volume
	if acquired := cs.Driver.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer cs.Driver.volumeLocks.Release(volumeID)

	if cs.Driver.vcloudClient != nil {
		err := cs.Driver.vcloudClient.DeleteVolume(ctx, volumeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				klog.V(2).Infof("DeleteVolume: volume %s not found, treating as already deleted", volumeID)
				return &csi.DeleteVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.Internal, "failed to delete volume: %v", err)
		}
	}

	klog.V(2).Infof("DeleteVolume: deleted volume %s", volumeID)
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume attaches a volume to a node
func (cs *ControllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume volume ID must be provided")
	}

	nodeID := req.GetNodeId()
	if len(nodeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume node ID must be provided")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume volume capability must be provided")
	}

	klog.V(2).Infof("ControllerPublishVolume: volumeID(%s) nodeID(%s)", volumeID, nodeID)

	// Acquire lock for this volume
	if acquired := cs.Driver.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer cs.Driver.volumeLocks.Release(volumeID)

	if cs.Driver.vcloudClient != nil {
		// Check if volume exists
		vol, err := cs.Driver.vcloudClient.GetVolume(ctx, volumeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
			}
			return nil, status.Errorf(codes.Internal, "failed to get volume: %v", err)
		}

		// Check if already attached to this node
		if vol.NodeID == nodeID {
			klog.V(2).Infof("ControllerPublishVolume: volume %s already attached to node %s", volumeID, nodeID)
			return &csi.ControllerPublishVolumeResponse{
				PublishContext: map[string]string{
					"devicePath": vol.DevicePath,
				},
			}, nil
		}

		// Check if attached to a different node
		if vol.NodeID != "" && vol.NodeID != nodeID {
			return nil, status.Errorf(codes.FailedPrecondition,
				"volume %s already attached to node %s", volumeID, vol.NodeID)
		}

		// Attach volume
		result, err := cs.Driver.vcloudClient.AttachVolume(ctx, volumeID, nodeID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to attach volume: %v", err)
		}

		klog.V(2).Infof("ControllerPublishVolume: attached volume %s to node %s at %s", volumeID, nodeID, result.DevicePath)

		return &csi.ControllerPublishVolumeResponse{
			PublishContext: map[string]string{
				"devicePath": result.DevicePath,
			},
		}, nil
	}

	// No vCloud client - return mock response
	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			"devicePath": "/dev/vdb",
		},
	}, nil
}

// ControllerUnpublishVolume detaches a volume from a node
func (cs *ControllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ControllerUnpublishVolume volume ID must be provided")
	}

	nodeID := req.GetNodeId()

	klog.V(2).Infof("ControllerUnpublishVolume: volumeID(%s) nodeID(%s)", volumeID, nodeID)

	// Acquire lock for this volume
	if acquired := cs.Driver.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer cs.Driver.volumeLocks.Release(volumeID)

	if cs.Driver.vcloudClient != nil {
		err := cs.Driver.vcloudClient.DetachVolume(ctx, volumeID, nodeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				klog.V(2).Infof("ControllerUnpublishVolume: volume %s not found, treating as already detached", volumeID)
				return &csi.ControllerUnpublishVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.Internal, "failed to detach volume: %v", err)
		}
	}

	klog.V(2).Infof("ControllerUnpublishVolume: detached volume %s from node %s", volumeID, nodeID)
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities validates volume capabilities
func (cs *ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities volume ID must be provided")
	}

	volCaps := req.GetVolumeCapabilities()
	if len(volCaps) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities volume capabilities must be provided")
	}

	klog.V(2).Infof("ValidateVolumeCapabilities: volumeID(%s)", volumeID)

	// Verify volume exists
	if cs.Driver.vcloudClient != nil {
		_, err := cs.Driver.vcloudClient.GetVolume(ctx, volumeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
			}
			return nil, status.Errorf(codes.Internal, "failed to get volume: %v", err)
		}
	}

	// Validate capabilities
	for _, cap := range volCaps {
		if cap.GetBlock() != nil {
			return &csi.ValidateVolumeCapabilitiesResponse{
				Message: "block volume not supported",
			}, nil
		}
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: volCaps,
		},
	}, nil
}

// ControllerExpandVolume expands a volume
func (cs *ControllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "ControllerExpandVolume volume ID must be provided")
	}

	capRange := req.GetCapacityRange()
	if capRange == nil {
		return nil, status.Error(codes.InvalidArgument, "ControllerExpandVolume capacity range must be provided")
	}

	newSize := capRange.GetRequiredBytes()
	if newSize <= 0 {
		return nil, status.Error(codes.InvalidArgument, "ControllerExpandVolume required bytes must be positive")
	}

	klog.V(2).Infof("ControllerExpandVolume: volumeID(%s) newSize(%d)", volumeID, newSize)

	// Acquire lock for this volume
	if acquired := cs.Driver.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer cs.Driver.volumeLocks.Release(volumeID)

	if cs.Driver.vcloudClient != nil {
		// Get current volume size from backend
		vol, err := cs.Driver.vcloudClient.GetVolume(ctx, volumeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				return nil, status.Errorf(codes.NotFound, "volume %s not found", volumeID)
			}
			// If GET_VOLUME not implemented, proceed with expand
			klog.V(2).Infof("ControllerExpandVolume: could not get current volume size, proceeding with expand: %v", err)
			vol = nil
		}

		// Check if expansion needed (like DigitalOcean pattern)
		if vol != nil && vol.SizeBytes > 0 && vol.SizeBytes >= newSize {
			klog.V(2).Infof("ControllerExpandVolume: volume %s already at or exceeds requested size (%d >= %d)", volumeID, vol.SizeBytes, newSize)
			// Still need to expand filesystem even if backend is already big enough
			return &csi.ControllerExpandVolumeResponse{
				CapacityBytes:         vol.SizeBytes,
				NodeExpansionRequired: true,
			}, nil
		}

		// Expand volume in backend
		expandedVol, err := cs.Driver.vcloudClient.ExpandVolume(ctx, volumeID, newSize)
		if err != nil {
			// Check if volume is not attached (OpenNebula requires attached volume for resize)
			errStr := err.Error()
			if strings.Contains(errStr, "CannotExpandUnattachedVolume") {
				klog.V(2).Infof("ControllerExpandVolume: volume %s is not attached, will retry when attached", volumeID)
				return nil, status.Errorf(codes.FailedPrecondition,
					"volume %s must be attached to a running workload before it can be expanded", volumeID)
			}
			// Other errors - return as internal error
			return nil, status.Errorf(codes.Internal, "failed to expand volume: %v", err)
		}

		// Use actual size from backend response
		capacityBytes := expandedVol.SizeBytes
		if capacityBytes == 0 {
			capacityBytes = newSize
		}

		klog.V(2).Infof("ControllerExpandVolume: expanded volume %s to %d bytes", volumeID, capacityBytes)

		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         capacityBytes,
			NodeExpansionRequired: true,
		}, nil
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         newSize,
		NodeExpansionRequired: true,
	}, nil
}

// CreateSnapshot creates a snapshot
func (cs *ControllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	name := req.GetName()
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateSnapshot name must be provided")
	}

	sourceVolumeID := req.GetSourceVolumeId()
	if len(sourceVolumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateSnapshot source volume ID must be provided")
	}

	klog.V(2).Infof("CreateSnapshot: name(%s) sourceVolumeID(%s)", name, sourceVolumeID)

	// Acquire lock for this snapshot
	if acquired := cs.Driver.volumeLocks.TryAcquire(name); !acquired {
		return nil, status.Errorf(codes.Aborted, SnapshotOperationAlreadyExistsFmt, name)
	}
	defer cs.Driver.volumeLocks.Release(name)

	if cs.Driver.vcloudClient != nil {
		// Idempotency check - first try by name
		existing, err := cs.Driver.vcloudClient.GetSnapshotByName(ctx, name)
		if err == nil && existing != nil {
			klog.V(2).Infof("CreateSnapshot: snapshot %s already exists with ID %s", name, existing.ID)

			// Verify source volume matches
			if existing.SourceVolumeID != sourceVolumeID {
				return nil, status.Errorf(codes.AlreadyExists,
					"snapshot %s already exists with different source volume", name)
			}

			return &csi.CreateSnapshotResponse{
				Snapshot: convertSnapshot(existing),
			}, nil
		}

		// Fallback idempotency check - list snapshots for source volume and check by name
		// This handles backends that don't support GET_SNAPSHOT by name
		if err != nil || existing == nil {
			klog.V(4).Infof("CreateSnapshot: GetSnapshotByName returned err=%v, existing=%v, trying list fallback", err, existing)
			snapshots, _, listErr := cs.Driver.vcloudClient.ListSnapshots(ctx, sourceVolumeID, 100, "")
			if listErr == nil {
				for _, snap := range snapshots {
					if snap.Name == name {
						klog.V(2).Infof("CreateSnapshot: found existing snapshot %s with ID %s via list", name, snap.ID)
						return &csi.CreateSnapshotResponse{
							Snapshot: convertSnapshot(snap),
						}, nil
					}
				}
			}
		}

		// Verify source volume exists
		vol, err := cs.Driver.vcloudClient.GetVolume(ctx, sourceVolumeID)
		if err != nil {
			if client.IsNotFoundError(err) {
				return nil, status.Errorf(codes.NotFound, "source volume %s not found", sourceVolumeID)
			}
			return nil, status.Errorf(codes.Internal, "failed to get source volume: %v", err)
		}

		// Create snapshot
		snap, err := cs.Driver.vcloudClient.CreateSnapshot(ctx, name, sourceVolumeID)
		if err != nil {
			errStr := err.Error()
			if client.IsAlreadyExistsError(err) {
				return nil, status.Errorf(codes.AlreadyExists, "snapshot %s already exists", name)
			}
			// Handle volume in use error - cannot snapshot while attached
			if strings.Contains(errStr, "VolumeInUse") {
				klog.V(2).Infof("CreateSnapshot: volume %s is in use, cannot create snapshot", sourceVolumeID)
				return nil, status.Errorf(codes.FailedPrecondition,
					"cannot create snapshot while volume is attached to a running workload. Stop the pod using this volume first")
			}
			// Handle volume not ready error
			if strings.Contains(errStr, "VolumeNotReady") {
				klog.V(2).Infof("CreateSnapshot: volume %s is not ready", sourceVolumeID)
				return nil, status.Errorf(codes.FailedPrecondition,
					"volume is not in ready state. Please wait and try again")
			}
			return nil, status.Errorf(codes.Internal, "failed to create snapshot: %v", err)
		}

		// If snapshot doesn't have size, use source volume size
		if snap.SizeBytes == 0 {
			snap.SizeBytes = vol.SizeBytes
		}

		klog.V(2).Infof("CreateSnapshot: created snapshot %s with ID %s", name, snap.ID)

		return &csi.CreateSnapshotResponse{
			Snapshot: convertSnapshot(snap),
		}, nil
	}

	// No vCloud client - return mock response
	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     fmt.Sprintf("snap-%s", name),
			SourceVolumeId: sourceVolumeID,
			SizeBytes:      DefaultVolumeSize,
			CreationTime:   timestamppb.Now(),
			ReadyToUse:     true,
		},
	}, nil
}

// DeleteSnapshot deletes a snapshot
func (cs *ControllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	snapshotID := req.GetSnapshotId()
	if len(snapshotID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "DeleteSnapshot snapshot ID must be provided")
	}

	klog.V(2).Infof("DeleteSnapshot: snapshotID(%s)", snapshotID)

	// Acquire lock for this snapshot
	if acquired := cs.Driver.volumeLocks.TryAcquire(snapshotID); !acquired {
		return nil, status.Errorf(codes.Aborted, SnapshotOperationAlreadyExistsFmt, snapshotID)
	}
	defer cs.Driver.volumeLocks.Release(snapshotID)

	if cs.Driver.vcloudClient != nil {
		err := cs.Driver.vcloudClient.DeleteSnapshot(ctx, snapshotID)
		if err != nil {
			if client.IsNotFoundError(err) {
				klog.V(2).Infof("DeleteSnapshot: snapshot %s not found, treating as already deleted", snapshotID)
				return &csi.DeleteSnapshotResponse{}, nil
			}
			return nil, status.Errorf(codes.Internal, "failed to delete snapshot: %v", err)
		}
	}

	klog.V(2).Infof("DeleteSnapshot: deleted snapshot %s", snapshotID)
	return &csi.DeleteSnapshotResponse{}, nil
}

// ListSnapshots lists snapshots
func (cs *ControllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	klog.V(2).Infof("ListSnapshots: snapshotID(%s) sourceVolumeID(%s)", req.GetSnapshotId(), req.GetSourceVolumeId())

	// If specific snapshot requested
	if snapshotID := req.GetSnapshotId(); snapshotID != "" {
		if cs.Driver.vcloudClient != nil {
			snap, err := cs.Driver.vcloudClient.GetSnapshot(ctx, snapshotID)
			if err != nil {
				if client.IsNotFoundError(err) {
					return &csi.ListSnapshotsResponse{}, nil
				}
				return nil, status.Errorf(codes.Internal, "failed to get snapshot: %v", err)
			}

			return &csi.ListSnapshotsResponse{
				Entries: []*csi.ListSnapshotsResponse_Entry{
					{Snapshot: convertSnapshot(snap)},
				},
			}, nil
		}
	}

	// List snapshots with optional filtering
	if cs.Driver.vcloudClient != nil {
		snapshots, nextToken, err := cs.Driver.vcloudClient.ListSnapshots(ctx, req.GetSourceVolumeId(), req.GetMaxEntries(), req.GetStartingToken())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to list snapshots: %v", err)
		}

		entries := make([]*csi.ListSnapshotsResponse_Entry, 0, len(snapshots))
		for _, snap := range snapshots {
			entries = append(entries, &csi.ListSnapshotsResponse_Entry{
				Snapshot: convertSnapshot(snap),
			})
		}

		return &csi.ListSnapshotsResponse{
			Entries:   entries,
			NextToken: nextToken,
		}, nil
	}

	return &csi.ListSnapshotsResponse{}, nil
}

// ControllerGetCapabilities returns controller capabilities
func (cs *ControllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	klog.V(4).Infof("ControllerGetCapabilities called")

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: cs.Driver.cscap,
	}, nil
}

// convertSnapshot converts a client.Snapshot to csi.Snapshot
func convertSnapshot(snap *client.Snapshot) *csi.Snapshot {
	return &csi.Snapshot{
		SnapshotId:     snap.ID,
		SourceVolumeId: snap.SourceVolumeID,
		SizeBytes:      snap.SizeBytes,
		CreationTime:   timestamppb.New(time.Unix(snap.CreatedAt, 0)),
		ReadyToUse:     snap.ReadyToUse,
	}
}
