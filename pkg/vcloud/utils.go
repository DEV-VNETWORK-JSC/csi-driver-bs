package vcloud

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// VolumeLocks implements a map with locks to prevent concurrent operations on the same volume
type VolumeLocks struct {
	locks map[string]bool
	mux   sync.Mutex
}

// NewVolumeLocks creates a new VolumeLocks instance
func NewVolumeLocks() *VolumeLocks {
	return &VolumeLocks{
		locks: make(map[string]bool),
	}
}

// TryAcquire tries to acquire the lock for operating on volumeID and returns true if successful.
// If another operation is already using volumeID, returns false.
func (vl *VolumeLocks) TryAcquire(volumeID string) bool {
	vl.mux.Lock()
	defer vl.mux.Unlock()

	if _, exists := vl.locks[volumeID]; exists {
		return false
	}
	vl.locks[volumeID] = true
	return true
}

// Release deletes the lock on volumeID
func (vl *VolumeLocks) Release(volumeID string) {
	vl.mux.Lock()
	defer vl.mux.Unlock()

	delete(vl.locks, volumeID)
}

// ExecFunc is a function that executes an operation
type ExecFunc func() error

// TimeoutFunc is a function that handles timeout
type TimeoutFunc func() error

// WaitUntilTimeout executes execFunc with a timeout
// If execFunc completes before timeout, returns its result
// If timeout occurs first, executes timeoutFunc and returns timeout error
func WaitUntilTimeout(timeout time.Duration, execFunc ExecFunc, timeoutFunc TimeoutFunc) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- execFunc()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if timeoutFunc != nil {
			if err := timeoutFunc(); err != nil {
				klog.Warningf("timeout function failed: %v", err)
			}
		}
		return fmt.Errorf("timeout after %v", timeout)
	}
}

// ValidateVolumeID validates a volume ID is not empty
func ValidateVolumeID(volumeID string) error {
	if len(volumeID) == 0 {
		return fmt.Errorf("volume ID is required")
	}
	return nil
}

// ValidateNodeID validates a node ID is not empty
func ValidateNodeID(nodeID string) error {
	if len(nodeID) == 0 {
		return fmt.Errorf("node ID is required")
	}
	return nil
}

// ValidateTargetPath validates a target path is not empty
func ValidateTargetPath(targetPath string) error {
	if len(targetPath) == 0 {
		return fmt.Errorf("target path is required")
	}
	return nil
}

// ValidateStagingTargetPath validates a staging target path is not empty
func ValidateStagingTargetPath(stagingTargetPath string) error {
	if len(stagingTargetPath) == 0 {
		return fmt.Errorf("staging target path is required")
	}
	return nil
}

// SetKeyValueInMap sets a key-value pair in a map (case-insensitive key matching)
func SetKeyValueInMap(m map[string]string, key, value string) {
	if m == nil {
		return
	}
	for k := range m {
		if strings.EqualFold(k, key) {
			m[k] = value
			return
		}
	}
	m[key] = value
}

// GetValueFromMap gets a value from a map with case-insensitive key matching
func GetValueFromMap(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// ChmodIfPermissionMismatch only chmods if current permissions don't match desired
func ChmodIfPermissionMismatch(targetPath string, mode os.FileMode) error {
	info, err := os.Stat(targetPath)
	if err != nil {
		return err
	}
	currentMode := info.Mode().Perm()
	if currentMode != mode {
		klog.V(4).Infof("chmod %s from %o to %o", targetPath, currentMode, mode)
		return os.Chmod(targetPath, mode)
	}
	return nil
}

// MakeDir creates a directory with the specified permissions
func MakeDir(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

// PathExists checks if a path exists
func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// WaitForPathNotExist waits until path does not exist or timeout
func WaitForPathNotExist(path string, timeout time.Duration) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeoutCh := time.After(timeout)

	for {
		select {
		case <-ticker.C:
			exists, err := PathExists(path)
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for path %s to not exist", path)
		}
	}
}

// CleanupPath removes a path and its parent directories if empty
func CleanupPath(path string, maxDepth int) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}

	// Try to remove empty parent directories
	parent := filepath.Dir(path)
	for i := 0; i < maxDepth; i++ {
		if parent == "/" || parent == "." {
			break
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			break
		}
		if len(entries) > 0 {
			break
		}
		if err := os.Remove(parent); err != nil {
			break
		}
		parent = filepath.Dir(parent)
	}
	return nil
}

// RoundUpBytes rounds up bytes to the nearest multiple of blockSize
func RoundUpBytes(volumeSizeBytes int64, blockSize int64) int64 {
	if blockSize == 0 {
		return volumeSizeBytes
	}
	return ((volumeSizeBytes + blockSize - 1) / blockSize) * blockSize
}

// RoundUpGiB rounds up bytes to the nearest GiB
func RoundUpGiB(volumeSizeBytes int64) int64 {
	const GiB = 1024 * 1024 * 1024
	return RoundUpBytes(volumeSizeBytes, GiB)
}

// BytesToGiB converts bytes to GiB
func BytesToGiB(bytes int64) int64 {
	const GiB = 1024 * 1024 * 1024
	return bytes / GiB
}

// GiBToBytes converts GiB to bytes
func GiBToBytes(gib int64) int64 {
	const GiB = 1024 * 1024 * 1024
	return gib * GiB
}
