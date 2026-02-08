package vcloud

import (
	"context"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/klog/v2"
)

// IdentityServer implements the CSI Identity service
type IdentityServer struct {
	csi.UnimplementedIdentityServer
	Driver *Driver
}

// NewIdentityServer creates a new IdentityServer
func NewIdentityServer(d *Driver) *IdentityServer {
	return &IdentityServer{
		Driver: d,
	}
}

// GetPluginInfo returns metadata about the plugin
func (ids *IdentityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	klog.V(4).Infof("GetPluginInfo called")

	if ids.Driver.name == "" {
		return nil, status.Error(codes.Unavailable, "driver name is not configured")
	}

	return &csi.GetPluginInfoResponse{
		Name:          ids.Driver.name,
		VendorVersion: ids.Driver.version,
	}, nil
}

// GetPluginCapabilities returns the capabilities of the plugin
func (ids *IdentityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	klog.V(4).Infof("GetPluginCapabilities called")

	caps := []*csi.PluginCapability{
		{
			Type: &csi.PluginCapability_Service_{
				Service: &csi.PluginCapability_Service{
					Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
				},
			},
		},
		{
			Type: &csi.PluginCapability_VolumeExpansion_{
				VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
					Type: csi.PluginCapability_VolumeExpansion_ONLINE,
				},
			},
		},
	}

	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: caps,
	}, nil
}

// Probe checks if the plugin is healthy and ready
func (ids *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	klog.V(6).Infof("Probe called")

	return &csi.ProbeResponse{
		Ready: wrapperspb.Bool(true),
	}, nil
}
