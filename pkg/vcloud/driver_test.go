package vcloud

import (
	"testing"
)

func TestNewDriver(t *testing.T) {
	tests := []struct {
		name    string
		options *DriverOptions
		wantErr bool
	}{
		{
			name: "valid options with API credentials",
			options: &DriverOptions{
				Endpoint:      "unix:///tmp/csi.sock",
				APIUrl:        "https://api.example.com",
				ProviderToken: "test-token",
				NodeID:        "test-node",
				DriverName:    "test-driver",
				Version:       "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "valid options node only",
			options: &DriverOptions{
				Endpoint:   "unix:///tmp/csi.sock",
				NodeID:     "test-node",
				DriverName: "test-driver",
				Version:    "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "missing endpoint",
			options: &DriverOptions{
				NodeID:     "test-node",
				DriverName: "test-driver",
			},
			wantErr: true,
		},
		{
			name: "default driver name",
			options: &DriverOptions{
				Endpoint: "unix:///tmp/csi.sock",
				NodeID:   "test-node",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDriver(tt.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDriver() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d == nil {
				t.Error("NewDriver() returned nil driver")
			}
			if !tt.wantErr && tt.options.DriverName == "" && d.name != DefaultDriverName {
				t.Errorf("Expected default driver name %s, got %s", DefaultDriverName, d.name)
			}
		})
	}
}

func TestVolumeLocks(t *testing.T) {
	vl := NewVolumeLocks()

	// Test TryAcquire
	if !vl.TryAcquire("vol-1") {
		t.Error("TryAcquire should succeed for new volume")
	}

	// Test TryAcquire again on same volume
	if vl.TryAcquire("vol-1") {
		t.Error("TryAcquire should fail for already locked volume")
	}

	// Test TryAcquire on different volume
	if !vl.TryAcquire("vol-2") {
		t.Error("TryAcquire should succeed for different volume")
	}

	// Test Release
	vl.Release("vol-1")
	if !vl.TryAcquire("vol-1") {
		t.Error("TryAcquire should succeed after release")
	}
}

func TestDriverCapabilities(t *testing.T) {
	options := &DriverOptions{
		Endpoint: "unix:///tmp/csi.sock",
		NodeID:   "test-node",
	}

	d, err := NewDriver(options)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Check volume capabilities
	if len(d.vcap) == 0 {
		t.Error("Driver should have volume capabilities")
	}

	// Check controller capabilities
	if len(d.cscap) == 0 {
		t.Error("Driver should have controller capabilities")
	}

	// Check node capabilities
	if len(d.nscap) == 0 {
		t.Error("Driver should have node capabilities")
	}
}
