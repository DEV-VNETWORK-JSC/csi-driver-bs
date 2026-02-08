package vcloud

import (
	"context"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func TestCreateVolume(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	cs := d.cs

	tests := []struct {
		name      string
		req       *csi.CreateVolumeRequest
		expectErr bool
		errCode   string
	}{
		{
			name: "valid request",
			req: &csi.CreateVolumeRequest{
				Name: "test-volume",
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "missing name",
			req: &csi.CreateVolumeRequest{
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{},
						},
					},
				},
			},
			expectErr: true,
			errCode:   "InvalidArgument",
		},
		{
			name: "missing capabilities",
			req: &csi.CreateVolumeRequest{
				Name: "test-volume",
			},
			expectErr: true,
			errCode:   "InvalidArgument",
		},
		{
			name: "block volume not supported",
			req: &csi.CreateVolumeRequest{
				Name: "test-volume",
				VolumeCapabilities: []*csi.VolumeCapability{
					{
						AccessType: &csi.VolumeCapability_Block{
							Block: &csi.VolumeCapability_BlockVolume{},
						},
					},
				},
			},
			expectErr: true,
			errCode:   "InvalidArgument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := cs.CreateVolume(context.Background(), tt.req)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if resp == nil || resp.Volume == nil {
					t.Error("Expected volume response")
				}
			}
		})
	}
}

func TestDeleteVolume(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	cs := d.cs

	tests := []struct {
		name      string
		req       *csi.DeleteVolumeRequest
		expectErr bool
	}{
		{
			name: "valid request",
			req: &csi.DeleteVolumeRequest{
				VolumeId: "vol-123",
			},
			expectErr: false,
		},
		{
			name: "missing volume ID",
			req: &csi.DeleteVolumeRequest{
				VolumeId: "",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cs.DeleteVolume(context.Background(), tt.req)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestControllerGetCapabilities(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	cs := d.cs

	resp, err := cs.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("ControllerGetCapabilities failed: %v", err)
	}

	if resp == nil || len(resp.Capabilities) == 0 {
		t.Error("Expected capabilities in response")
	}

	// Verify expected capabilities
	expectedCaps := map[csi.ControllerServiceCapability_RPC_Type]bool{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME:   false,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME: false,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME:          false,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT: false,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS:         false,
	}

	for _, cap := range resp.Capabilities {
		if rpc := cap.GetRpc(); rpc != nil {
			expectedCaps[rpc.Type] = true
		}
	}

	for cap, found := range expectedCaps {
		if !found {
			t.Errorf("Expected capability %s not found", cap)
		}
	}
}
