package vcloud

import (
	"context"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func TestGetPluginInfo(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint:   "unix:///tmp/csi.sock",
		NodeID:     "test-node",
		DriverName: "test-driver",
		Version:    "1.0.0",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ids := d.ids

	resp, err := ids.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
	if err != nil {
		t.Fatalf("GetPluginInfo failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response")
	}

	if resp.Name != "test-driver" {
		t.Errorf("Expected driver name 'test-driver', got '%s'", resp.Name)
	}

	if resp.VendorVersion != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", resp.VendorVersion)
	}
}

func TestGetPluginCapabilities(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ids := d.ids

	resp, err := ids.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetPluginCapabilities failed: %v", err)
	}

	if resp == nil || len(resp.Capabilities) == 0 {
		t.Error("Expected capabilities in response")
	}

	// Verify CONTROLLER_SERVICE capability exists
	hasControllerService := false
	for _, cap := range resp.Capabilities {
		if service := cap.GetService(); service != nil {
			if service.Type == csi.PluginCapability_Service_CONTROLLER_SERVICE {
				hasControllerService = true
				break
			}
		}
	}

	if !hasControllerService {
		t.Error("Expected CONTROLLER_SERVICE capability")
	}
}

func TestProbe(t *testing.T) {
	d, err := NewDriver(&DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	})
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ids := d.ids

	resp, err := ids.Probe(context.Background(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response")
	}

	if resp.Ready == nil || !resp.Ready.Value {
		t.Error("Expected driver to be ready")
	}
}
