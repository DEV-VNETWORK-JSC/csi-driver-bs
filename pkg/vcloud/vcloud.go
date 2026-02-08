package vcloud

import (
	"fmt"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"gitlab.vnetwork.dev/golang/kubernetes/csi-driver-vcloud/pkg/vcloud/client"
	"k8s.io/klog/v2"
)

const (
	// DefaultDriverName is the default name for this CSI driver
	DefaultDriverName = "vcloud.csi.vnetwork.dev"

	// DefaultVolumeType is the default volume type
	DefaultVolumeType = "hiops"

	// VolumeOperationAlreadyExistsFmt is the error message format for concurrent operations
	VolumeOperationAlreadyExistsFmt = "an operation with the given Volume %s already exists"

	// SnapshotOperationAlreadyExistsFmt is the error message format for concurrent snapshot operations
	SnapshotOperationAlreadyExistsFmt = "an operation with the given Snapshot %s already exists"
)

// DriverOptions contains options for the driver
type DriverOptions struct {
	// DriverName is the name of the CSI driver
	DriverName string

	// NodeID is the identifier of the node
	NodeID string

	// Endpoint is the CSI endpoint
	Endpoint string

	// APIUrl is the vCloud API URL
	APIUrl string

	// ProviderToken is the vCloud provider token
	ProviderToken string

	// Version is the driver version
	Version string

	// MountPermissions is the default mount permissions
	MountPermissions uint64

	// WorkingMountDir is the working directory for mount operations
	WorkingMountDir string
}

// Driver implements the CSI driver for vCloud
type Driver struct {
	name    string
	nodeID  string
	version string

	endpoint         string
	mountPermissions uint64
	workingMountDir  string

	// vCloud client for API operations
	vcloudClient *client.Client

	// Servers
	ids *IdentityServer
	ns  *NodeServer
	cs  *ControllerServer

	// Capabilities
	vcap  []*csi.VolumeCapability_AccessMode
	cscap []*csi.ControllerServiceCapability
	nscap []*csi.NodeServiceCapability

	// Volume operation locks
	volumeLocks *VolumeLocks
}

// NewDriver creates a new CSI driver
func NewDriver(options *DriverOptions) (*Driver, error) {
	if options.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	driverName := options.DriverName
	if driverName == "" {
		driverName = DefaultDriverName
	}

	version := options.Version
	if version == "" {
		version = "dev"
	}

	workingMountDir := options.WorkingMountDir
	if workingMountDir == "" {
		workingMountDir = "/tmp"
	}

	klog.V(2).Infof("Driver: %s, Version: %s, NodeID: %s", driverName, version, options.NodeID)

	d := &Driver{
		name:             driverName,
		nodeID:           options.NodeID,
		version:          version,
		endpoint:         options.Endpoint,
		mountPermissions: options.MountPermissions,
		workingMountDir:  workingMountDir,
		volumeLocks:      NewVolumeLocks(),
	}

	// Initialize vCloud client if credentials provided
	if options.APIUrl != "" && options.ProviderToken != "" {
		d.vcloudClient = client.NewClient(options.APIUrl, options.ProviderToken)
		klog.V(2).Info("vCloud client initialized")
	}

	// Set up volume capabilities
	d.addVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
	})

	// Set up controller capabilities
	d.addControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
	})

	// Set up node capabilities
	d.addNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	})

	// Create servers
	d.ids = NewIdentityServer(d)
	d.ns = NewNodeServer(d, NewMounter())
	d.cs = NewControllerServer(d)

	return d, nil
}

// Run starts the CSI driver
func (d *Driver) Run(testMode bool) error {
	s := NewNonBlockingGRPCServer()
	s.Start(d.endpoint, d.ids, d.cs, d.ns, testMode)
	s.Wait()
	return nil
}

// addVolumeCapabilityAccessModes adds volume capability access modes
func (d *Driver) addVolumeCapabilityAccessModes(modes []csi.VolumeCapability_AccessMode_Mode) {
	for _, m := range modes {
		klog.V(4).Infof("Enabling volume access mode: %v", m.String())
		d.vcap = append(d.vcap, &csi.VolumeCapability_AccessMode{Mode: m})
	}
}

// addControllerServiceCapabilities adds controller service capabilities
func (d *Driver) addControllerServiceCapabilities(caps []csi.ControllerServiceCapability_RPC_Type) {
	for _, cap := range caps {
		klog.V(4).Infof("Enabling controller capability: %v", cap.String())
		d.cscap = append(d.cscap, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}
}

// addNodeServiceCapabilities adds node service capabilities
func (d *Driver) addNodeServiceCapabilities(caps []csi.NodeServiceCapability_RPC_Type) {
	for _, cap := range caps {
		klog.V(4).Infof("Enabling node capability: %v", cap.String())
		d.nscap = append(d.nscap, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}
}
