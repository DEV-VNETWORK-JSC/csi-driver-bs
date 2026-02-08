package vcloud

import (
	"context"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func TestNodeGetCapabilities(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ns := d.ns

	resp, err := ns.NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("NodeGetCapabilities failed: %v", err)
	}

	if resp == nil || len(resp.Capabilities) == 0 {
		t.Error("Expected capabilities in response")
	}

	// Verify expected capabilities
	expectedCaps := map[csi.NodeServiceCapability_RPC_Type]bool{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME: false,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME:        false,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS:     false,
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

func TestNodeGetInfo(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node-123",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ns := d.ns

	resp, err := ns.NodeGetInfo(context.Background(), &csi.NodeGetInfoRequest{})
	if err != nil {
		t.Fatalf("NodeGetInfo failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response")
	}

	if resp.NodeId != "test-node-123" {
		t.Errorf("Expected node ID 'test-node-123', got '%s'", resp.NodeId)
	}
}

func TestNodeStageVolume(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Replace with fake mounter
	d.ns.mounter = NewFakeMounter()
	ns := d.ns

	tests := []struct {
		name      string
		req       *csi.NodeStageVolumeRequest
		expectErr bool
	}{
		{
			name: "missing volume ID",
			req: &csi.NodeStageVolumeRequest{
				StagingTargetPath: "/tmp/staging",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
				},
				PublishContext: map[string]string{
					"devicePath": "/dev/sdb",
				},
			},
			expectErr: true,
		},
		{
			name: "missing staging target path",
			req: &csi.NodeStageVolumeRequest{
				VolumeId: "vol-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
				},
				PublishContext: map[string]string{
					"devicePath": "/dev/sdb",
				},
			},
			expectErr: true,
		},
		{
			name: "missing volume capability",
			req: &csi.NodeStageVolumeRequest{
				VolumeId:          "vol-123",
				StagingTargetPath: "/tmp/staging",
				PublishContext: map[string]string{
					"devicePath": "/dev/sdb",
				},
			},
			expectErr: true,
		},
		{
			name: "missing device path",
			req: &csi.NodeStageVolumeRequest{
				VolumeId:          "vol-123",
				StagingTargetPath: "/tmp/staging",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
				},
				PublishContext: map[string]string{},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ns.NodeStageVolume(context.Background(), tt.req)
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

func TestNodeUnstageVolume(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Replace with fake mounter
	d.ns.mounter = NewFakeMounter()
	ns := d.ns

	tests := []struct {
		name      string
		req       *csi.NodeUnstageVolumeRequest
		expectErr bool
	}{
		{
			name: "missing volume ID",
			req: &csi.NodeUnstageVolumeRequest{
				StagingTargetPath: "/tmp/staging",
			},
			expectErr: true,
		},
		{
			name: "missing staging target path",
			req: &csi.NodeUnstageVolumeRequest{
				VolumeId: "vol-123",
			},
			expectErr: true,
		},
		{
			name: "valid request - not mounted",
			req: &csi.NodeUnstageVolumeRequest{
				VolumeId:          "vol-123",
				StagingTargetPath: "/tmp/staging",
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ns.NodeUnstageVolume(context.Background(), tt.req)
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
