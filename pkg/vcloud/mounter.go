package vcloud

import (
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
)

const (
	// DefaultFSType is the default filesystem type
	DefaultFSType = "ext4"

	// MaxMountRetries is the maximum number of mount retries
	MaxMountRetries = 5

	// MountRetryDelay is the delay between mount retries
	MountRetryDelay = 2 * time.Second
)

// Mounter is an interface for mount operations
type Mounter interface {
	// Format formats the device with the specified filesystem type
	Format(source, fsType string) error

	// Mount mounts the source to target with the specified filesystem type and options
	Mount(source, target, fsType string, options []string) error

	// Unmount unmounts the target
	Unmount(target string) error

	// IsMountPoint checks if the target is a mount point
	IsMountPoint(target string) (bool, error)

	// IsLikelyNotMountPoint checks if target is likely not a mount point
	IsLikelyNotMountPoint(target string) (bool, error)

	// IsBlockDevice checks if the path is a block device
	IsBlockDevice(path string) (bool, error)

	// GetDeviceFSType returns the filesystem type of the device
	GetDeviceFSType(device string) (string, error)

	// MountWithRetry mounts with retry logic
	MountWithRetry(source, target, fsType string, options []string) error
}

// mounter implements the Mounter interface
type mounter struct {
	mounter *mount.SafeFormatAndMount
}

// NewMounter creates a new Mounter
func NewMounter() Mounter {
	return &mounter{
		mounter: &mount.SafeFormatAndMount{
			Interface: mount.New(""),
			Exec:      utilexec.New(),
		},
	}
}

// Format formats the device with the specified filesystem type
func (m *mounter) Format(source, fsType string) error {
	if fsType == "" {
		fsType = DefaultFSType
	}

	klog.V(4).Infof("Formatting device %s with filesystem %s", source, fsType)

	// Check if device already has a filesystem
	existingFS, err := m.GetDeviceFSType(source)
	if err != nil {
		return fmt.Errorf("failed to get device filesystem type: %w", err)
	}

	if existingFS != "" {
		klog.V(4).Infof("Device %s already has filesystem %s, skipping format", source, existingFS)
		return nil
	}

	// Format the device
	mkfsCmd := fmt.Sprintf("mkfs.%s", fsType)
	args := []string{"-F", source}

	if fsType == "xfs" {
		args = []string{"-f", source}
	}

	cmd := osexec.Command(mkfsCmd, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to format device: %s, output: %s", err, string(output))
	}

	klog.V(2).Infof("Device %s formatted successfully with %s", source, fsType)
	return nil
}

// Mount mounts the source to target with the specified filesystem type and options
func (m *mounter) Mount(source, target, fsType string, options []string) error {
	if fsType == "" {
		fsType = DefaultFSType
	}

	klog.V(4).Infof("Mounting %s to %s (fsType: %s, options: %v)", source, target, fsType, options)

	return m.mounter.Mount(source, target, fsType, options)
}

// MountWithRetry mounts with retry logic for transient failures
func (m *mounter) MountWithRetry(source, target, fsType string, options []string) error {
	var lastErr error

	for attempt := 1; attempt <= MaxMountRetries; attempt++ {
		err := m.Mount(source, target, fsType, options)
		if err == nil {
			return nil
		}

		lastErr = err
		klog.Warningf("Mount %s to %s failed (attempt %d/%d): %v", source, target, attempt, MaxMountRetries, err)

		// Check if already mounted
		if strings.Contains(err.Error(), "already mounted") {
			mounted, checkErr := m.IsMountPoint(target)
			if checkErr == nil && mounted {
				klog.V(2).Infof("Target %s already mounted, treating as success", target)
				return nil
			}
		}

		// Check for device busy error - use exponential backoff
		if strings.Contains(err.Error(), "device or resource busy") {
			time.Sleep(MountRetryDelay * time.Duration(attempt))
			continue
		}

		if attempt < MaxMountRetries {
			time.Sleep(MountRetryDelay)
		}
	}

	return fmt.Errorf("mount failed after %d attempts: %w", MaxMountRetries, lastErr)
}

// Unmount unmounts the target
func (m *mounter) Unmount(target string) error {
	klog.V(4).Infof("Unmounting %s", target)

	return m.mounter.Unmount(target)
}

// IsMountPoint checks if the target is a mount point
func (m *mounter) IsMountPoint(target string) (bool, error) {
	return m.mounter.IsMountPoint(target)
}

// IsLikelyNotMountPoint checks if target is likely not a mount point
func (m *mounter) IsLikelyNotMountPoint(target string) (bool, error) {
	return m.mounter.IsLikelyNotMountPoint(target)
}

// IsBlockDevice checks if the path is a block device
func (m *mounter) IsBlockDevice(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	mode := info.Mode()
	return mode&os.ModeDevice != 0, nil
}

// GetDeviceFSType returns the filesystem type of the device using blkid
func (m *mounter) GetDeviceFSType(device string) (string, error) {
	cmd := osexec.Command("blkid", "-o", "value", "-s", "TYPE", device)
	output, err := cmd.Output()
	if err != nil {
		// Exit code 2 means no filesystem found
		if exitErr, ok := err.(*osexec.ExitError); ok && exitErr.ExitCode() == 2 {
			return "", nil
		}
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

// FakeMounter is a fake mounter for testing
type FakeMounter struct {
	MountPoints map[string]string
	FormatCalls []string
}

// NewFakeMounter creates a new FakeMounter for testing
func NewFakeMounter() *FakeMounter {
	return &FakeMounter{
		MountPoints: make(map[string]string),
		FormatCalls: make([]string, 0),
	}
}

// Format records the format call
func (m *FakeMounter) Format(source, fsType string) error {
	m.FormatCalls = append(m.FormatCalls, fmt.Sprintf("%s:%s", source, fsType))
	return nil
}

// Mount records the mount
func (m *FakeMounter) Mount(source, target, fsType string, options []string) error {
	m.MountPoints[target] = source
	return nil
}

// Unmount removes the mount point
func (m *FakeMounter) Unmount(target string) error {
	delete(m.MountPoints, target)
	return nil
}

// IsMountPoint checks if target is in mount points
func (m *FakeMounter) IsMountPoint(target string) (bool, error) {
	_, exists := m.MountPoints[target]
	return exists, nil
}

// IsLikelyNotMountPoint checks if target is likely not a mount point
func (m *FakeMounter) IsLikelyNotMountPoint(target string) (bool, error) {
	mounted, err := m.IsMountPoint(target)
	return !mounted, err
}

// IsBlockDevice always returns true for testing
func (m *FakeMounter) IsBlockDevice(path string) (bool, error) {
	return true, nil
}

// GetDeviceFSType returns ext4 for testing
func (m *FakeMounter) GetDeviceFSType(device string) (string, error) {
	return "ext4", nil
}

// MountWithRetry calls Mount for testing
func (m *FakeMounter) MountWithRetry(source, target, fsType string, options []string) error {
	return m.Mount(source, target, fsType, options)
}
