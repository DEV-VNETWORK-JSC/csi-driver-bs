package vcloud

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

const (
	// DevicePathKey is the key for device path in publish context
	DevicePathKey = "devicePath"

	// DefaultMountPermissions is the default mount permissions
	DefaultMountPermissions = 0750

	// MountTimeout is the timeout for mount operations
	MountTimeout = 120 * time.Second
)

// NodeServer implements the CSI Node service
type NodeServer struct {
	csi.UnimplementedNodeServer
	Driver  *Driver
	mounter Mounter
}

// NewNodeServer creates a new NodeServer
func NewNodeServer(d *Driver, mounter Mounter) *NodeServer {
	return &NodeServer{
		Driver:  d,
		mounter: mounter,
	}
}

// NodeStageVolume stages a volume to a staging path (formats and mounts)
func (ns *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeStageVolume volume ID must be provided")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeStageVolume staging target path must be provided")
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "NodeStageVolume volume capability must be provided")
	}

	klog.V(2).Infof("NodeStageVolume: volumeID(%s) stagingTargetPath(%s)", volumeID, stagingTargetPath)

	// Acquire lock for this volume-staging pair
	lockKey := fmt.Sprintf("%s-%s", volumeID, stagingTargetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	// Get device path from publish context
	devicePath := req.GetPublishContext()[DevicePathKey]
	if devicePath == "" {
		return nil, status.Error(codes.InvalidArgument, "NodeStageVolume device path not found in publish context")
	}

	// Check if device exists
	if _, err := os.Stat(devicePath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "device %s not found", devicePath)
	}

	// Get mount capability
	mountCap := volCap.GetMount()
	if mountCap == nil {
		return nil, status.Error(codes.InvalidArgument, "NodeStageVolume mount capability required")
	}

	// Determine filesystem type
	fsType := mountCap.GetFsType()
	if fsType == "" {
		fsType = DefaultFSType
	}

	// Check if already mounted
	mounted, err := ns.mounter.IsMountPoint(stagingTargetPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, status.Errorf(codes.Internal, "failed to check mount point: %v", err)
		}
	}

	if mounted {
		klog.V(2).Infof("NodeStageVolume: volume %s already staged at %s", volumeID, stagingTargetPath)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// Create staging directory
	mountPermissions := ns.Driver.mountPermissions
	if mountPermissions == 0 {
		mountPermissions = DefaultMountPermissions
	}
	if err := MakeDir(stagingTargetPath, os.FileMode(mountPermissions)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create staging directory: %v", err)
	}

	// Format device if needed
	if err := ns.mounter.Format(devicePath, fsType); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to format device: %v", err)
	}

	// Mount device to staging path with timeout
	mountOptions := mountCap.GetMountFlags()
	execFunc := func() error {
		return ns.mounter.Mount(devicePath, stagingTargetPath, fsType, mountOptions)
	}
	timeoutFunc := func() error {
		klog.Warningf("mount timeout for volume %s", volumeID)
		return nil
	}

	if err := WaitUntilTimeout(MountTimeout, execFunc, timeoutFunc); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mount device: %v", err)
	}

	// Set permissions if needed
	if mountPermissions > 0 {
		if err := ChmodIfPermissionMismatch(stagingTargetPath, os.FileMode(mountPermissions)); err != nil {
			klog.Warningf("failed to set permissions on %s: %v", stagingTargetPath, err)
		}
	}

	klog.V(2).Infof("NodeStageVolume: volume %s staged successfully at %s", volumeID, stagingTargetPath)
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unstages a volume from the staging path
func (ns *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeUnstageVolume volume ID must be provided")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeUnstageVolume staging target path must be provided")
	}

	klog.V(2).Infof("NodeUnstageVolume: volumeID(%s) stagingTargetPath(%s)", volumeID, stagingTargetPath)

	// Acquire lock for this volume-staging pair
	lockKey := fmt.Sprintf("%s-%s", volumeID, stagingTargetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	// Check if mounted
	mounted, err := ns.mounter.IsMountPoint(stagingTargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			klog.V(2).Infof("NodeUnstageVolume: staging path %s does not exist, treating as already unstaged", stagingTargetPath)
			return &csi.NodeUnstageVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to check mount point: %v", err)
	}

	if !mounted {
		klog.V(2).Infof("NodeUnstageVolume: volume %s already unstaged from %s", volumeID, stagingTargetPath)
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	// Unmount
	if err := ns.mounter.Unmount(stagingTargetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount staging path: %v", err)
	}

	// Remove staging directory
	if err := os.RemoveAll(stagingTargetPath); err != nil {
		klog.Warningf("failed to remove staging directory %s: %v", stagingTargetPath, err)
	}

	klog.V(2).Infof("NodeUnstageVolume: volume %s unstaged successfully from %s", volumeID, stagingTargetPath)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume mounts the volume to the target path
func (ns *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume volume ID must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume target path must be provided")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume staging target path must be provided")
	}

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "NodePublishVolume volume capability must be provided")
	}

	klog.V(2).Infof("NodePublishVolume: volumeID(%s) targetPath(%s) stagingPath(%s)", volumeID, targetPath, stagingTargetPath)

	// Acquire lock for this volume-target pair
	lockKey := fmt.Sprintf("%s-%s", volumeID, targetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	// Check if staging path is mounted
	mounted, err := ns.mounter.IsMountPoint(stagingTargetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to check staging mount point: %v", err)
	}
	if !mounted {
		return nil, status.Error(codes.FailedPrecondition, "staging path is not mounted")
	}

	// Check if target already mounted
	mounted, err = ns.mounter.IsMountPoint(targetPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, status.Errorf(codes.Internal, "failed to check target mount point: %v", err)
		}
	}

	if mounted {
		klog.V(2).Infof("NodePublishVolume: volume %s already published at %s", volumeID, targetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	// Create target directory
	mountPermissions := ns.Driver.mountPermissions
	if mountPermissions == 0 {
		mountPermissions = DefaultMountPermissions
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), os.FileMode(mountPermissions)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create target directory: %v", err)
	}
	if err := os.MkdirAll(targetPath, os.FileMode(mountPermissions)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create target path: %v", err)
	}

	// Build mount options
	mountOptions := []string{"bind"}
	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	mountCap := volCap.GetMount()
	if mountCap != nil {
		mountOptions = append(mountOptions, mountCap.GetMountFlags()...)
	}

	// Bind mount from staging to target
	if err := ns.mounter.Mount(stagingTargetPath, targetPath, "", mountOptions); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to bind mount: %v", err)
	}

	klog.V(2).Infof("NodePublishVolume: volume %s published successfully at %s", volumeID, targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmounts the volume from the target path
func (ns *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeUnpublishVolume volume ID must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeUnpublishVolume target path must be provided")
	}

	klog.V(2).Infof("NodeUnpublishVolume: volumeID(%s) targetPath(%s)", volumeID, targetPath)

	// Acquire lock for this volume-target pair
	lockKey := fmt.Sprintf("%s-%s", volumeID, targetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, VolumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	// Check if mounted
	mounted, err := ns.mounter.IsMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			klog.V(2).Infof("NodeUnpublishVolume: target path %s does not exist, treating as already unpublished", targetPath)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to check mount point: %v", err)
	}

	if !mounted {
		klog.V(2).Infof("NodeUnpublishVolume: volume %s already unpublished from %s", volumeID, targetPath)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// Unmount
	if err := ns.mounter.Unmount(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target path: %v", err)
	}

	// Remove target directory
	if err := os.RemoveAll(targetPath); err != nil {
		klog.Warningf("failed to remove target directory %s: %v", targetPath, err)
	}

	klog.V(2).Infof("NodeUnpublishVolume: volume %s unpublished successfully from %s", volumeID, targetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats returns volume statistics
func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID must be provided")
	}

	volumePath := req.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path must be provided")
	}

	klog.V(4).Infof("NodeGetVolumeStats: volumeID(%s) volumePath(%s)", volumeID, volumePath)

	// Check if path exists
	if _, err := os.Stat(volumePath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume path %s not found", volumePath)
	}

	// Get filesystem stats
	var stat unix.Statfs_t
	if err := unix.Statfs(volumePath, &stat); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get filesystem stats: %v", err)
	}

	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	totalBytes := int64(stat.Blocks) * int64(stat.Bsize)
	usedBytes := totalBytes - availableBytes

	availableInodes := int64(stat.Ffree)
	totalInodes := int64(stat.Files)
	usedInodes := totalInodes - availableInodes

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Available: availableBytes,
				Total:     totalBytes,
				Used:      usedBytes,
				Unit:      csi.VolumeUsage_BYTES,
			},
			{
				Available: availableInodes,
				Total:     totalInodes,
				Used:      usedInodes,
				Unit:      csi.VolumeUsage_INODES,
			},
		},
	}, nil
}

// NodeExpandVolume expands the filesystem on a volume
func (ns *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeExpandVolume volume ID must be provided")
	}

	volumePath := req.GetVolumePath()
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeExpandVolume volume path must be provided")
	}

	klog.V(2).Infof("NodeExpandVolume: volumeID(%s) volumePath(%s)", volumeID, volumePath)

	// Get the device for this mount point
	devicePath, err := getDeviceForMountPoint(volumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get device for mount point: %v", err)
	}

	// Get current filesystem type
	fsType, err := ns.mounter.GetDeviceFSType(devicePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get filesystem type: %v", err)
	}

	// Resize filesystem based on type
	var resizeCmd string
	var resizeArgs []string

	switch fsType {
	case "ext4", "ext3", "ext2":
		resizeCmd = "resize2fs"
		resizeArgs = []string{devicePath}
	case "xfs":
		resizeCmd = "xfs_growfs"
		resizeArgs = []string{volumePath}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported filesystem type: %s", fsType)
	}

	klog.V(2).Infof("NodeExpandVolume: resizing filesystem on %s with %s", devicePath, resizeCmd)

	cmd := osexec.Command(resizeCmd, resizeArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resize filesystem: %v, output: %s", err, string(output))
	}

	klog.V(2).Infof("NodeExpandVolume: filesystem expanded successfully on volume %s", volumeID)

	return &csi.NodeExpandVolumeResponse{
		CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
	}, nil
}

// NodeGetCapabilities returns node capabilities
func (ns *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	klog.V(4).Infof("NodeGetCapabilities called")

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: ns.Driver.nscap,
	}, nil
}

// NodeGetInfo returns information about the node
func (ns *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	klog.V(4).Infof("NodeGetInfo called")

	return &csi.NodeGetInfoResponse{
		NodeId: ns.Driver.nodeID,
	}, nil
}

// getDeviceForMountPoint finds the device for a given mount point
func getDeviceForMountPoint(mountPoint string) (string, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", fmt.Errorf("failed to read /proc/mounts: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == mountPoint {
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("mount point %s not found in /proc/mounts", mountPoint)
}
