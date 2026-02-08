package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

const (
	// DefaultTimeout is the default HTTP client timeout
	DefaultTimeout = 30 * time.Second

	// VolumesEndpoint is the API endpoint for volume and snapshot operations
	VolumesEndpoint = "/volumes"
)

// Client is the vCloud API client
type Client struct {
	httpClient    *http.Client
	baseURL       string
	providerToken string
}

// NewClient creates a new vCloud API client
func NewClient(baseURL, providerToken string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		baseURL:       baseURL,
		providerToken: providerToken,
	}
}

// NewClientWithTimeout creates a new vCloud API client with custom timeout
func NewClientWithTimeout(baseURL, providerToken string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL:       baseURL,
		providerToken: providerToken,
	}
}

// doRequest performs an HTTP request to the vCloud API
func (c *Client) doRequest(ctx context.Context, method, endpoint string, payload interface{}) (map[string]interface{}, error) {
	url := c.baseURL + endpoint

	var reqBody io.Reader
	if payload != nil {
		jsonData, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
		klog.V(6).Infof("Request payload: %s", string(jsonData))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Provider-Token", c.providerToken)

	klog.V(4).Infof("HTTP %s %s", method, url)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			klog.Errorf("failed to close response body: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	klog.V(6).Infof("Response status: %d, body: %s", resp.StatusCode, string(respBody))

	// Only accept 200 OK (matching legacy behavior)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed, status code: %d, response: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return result, nil
}

// CreateVolume creates a new volume
func (c *Client) CreateVolume(ctx context.Context, name string, capacityBytes int64, volumeType string, snapshotID string) (*Volume, error) {
	if volumeType == "" {
		volumeType = "hiops" // Default from legacy
	}

	req := &VolumeRequest{
		Perform:    PerformCreateVolume,
		Name:       name,
		Capacity:   capacityBytes,
		VolumeType: volumeType,
		SnapshotID: snapshotID,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	// Extract volume ID from response
	id, ok := resp["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("invalid response format: missing volume id")
	}

	return &Volume{
		ID:         id,
		Name:       name,
		SizeBytes:  capacityBytes,
		VolumeType: volumeType,
		SnapshotID: snapshotID,
	}, nil
}

// DeleteVolume deletes a volume
func (c *Client) DeleteVolume(ctx context.Context, volumeID string) error {
	req := &VolumeRequest{
		Perform:  PerformDeleteVolume,
		VolumeID: volumeID,
	}

	_, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	return nil
}

// GetVolume gets a volume by ID
func (c *Client) GetVolume(ctx context.Context, volumeID string) (*Volume, error) {
	req := &VolumeRequest{
		Perform:  PerformGetVolume,
		VolumeID: volumeID,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get volume: %w", err)
	}

	vol, err := parseVolumeResponse(resp)
	if err != nil {
		return nil, err
	}

	// If API returned empty response (no ID), treat as not found
	if vol.ID == "" {
		return nil, fmt.Errorf("volume %s not found", volumeID)
	}

	return vol, nil
}

// GetVolumeByName gets a volume by name (for idempotency checks)
func (c *Client) GetVolumeByName(ctx context.Context, name string) (*Volume, error) {
	req := &VolumeRequest{
		Perform: PerformGetVolume,
		Name:    name,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, err // Return raw error for NotFound detection
	}

	vol, err := parseVolumeResponse(resp)
	if err != nil {
		return nil, err
	}

	// If API returned empty response (no ID), treat as not found
	if vol.ID == "" {
		return nil, nil
	}

	return vol, nil
}

// AttachVolume attaches a volume to a node
func (c *Client) AttachVolume(ctx context.Context, volumeID, nodeID string) (*AttachResult, error) {
	req := &VolumeRequest{
		Perform:  PerformAttachVolume,
		VolumeID: volumeID,
		NodeID:   nodeID,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to attach volume: %w", err)
	}

	// Extract device path from response
	devicePath, ok := resp["devicePath"].(string)
	if !ok || devicePath == "" {
		return nil, fmt.Errorf("invalid response format: missing devicePath")
	}

	return &AttachResult{
		DevicePath: devicePath,
	}, nil
}

// DetachVolume detaches a volume from a node
func (c *Client) DetachVolume(ctx context.Context, volumeID, nodeID string) error {
	req := &VolumeRequest{
		Perform:  PerformDetachVolume,
		VolumeID: volumeID,
		NodeID:   nodeID,
	}

	_, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return fmt.Errorf("failed to detach volume: %w", err)
	}

	return nil
}

// ExpandVolume expands a volume to a new size
func (c *Client) ExpandVolume(ctx context.Context, volumeID string, newCapacityBytes int64) (*Volume, error) {
	req := &VolumeRequest{
		Perform:  PerformExpandVolume,
		VolumeID: volumeID,
		Capacity: newCapacityBytes,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to expand volume: %w", err)
	}

	vol, err := parseVolumeResponse(resp)
	if err != nil {
		return nil, err
	}

	// Use requested size if backend didn't return capacity
	if vol.SizeBytes == 0 {
		vol.SizeBytes = newCapacityBytes
	}
	vol.ID = volumeID

	return vol, nil
}

// CreateSnapshot creates a snapshot of a volume
func (c *Client) CreateSnapshot(ctx context.Context, name, sourceVolumeID string) (*Snapshot, error) {
	req := &SnapshotRequest{
		Perform:        PerformCreateSnapshot,
		Name:           name,
		SourceVolumeID: sourceVolumeID,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}

	return parseSnapshotResponse(resp)
}

// DeleteSnapshot deletes a snapshot
func (c *Client) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	req := &SnapshotRequest{
		Perform:    PerformDeleteSnapshot,
		SnapshotID: snapshotID,
	}

	_, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	return nil
}

// GetSnapshot gets a snapshot by ID
func (c *Client) GetSnapshot(ctx context.Context, snapshotID string) (*Snapshot, error) {
	req := &SnapshotRequest{
		Perform:    PerformGetSnapshot,
		SnapshotID: snapshotID,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	return parseSnapshotResponse(resp)
}

// GetSnapshotByName gets a snapshot by name (for idempotency checks)
func (c *Client) GetSnapshotByName(ctx context.Context, name string) (*Snapshot, error) {
	req := &SnapshotRequest{
		Perform: PerformGetSnapshot,
		Name:    name,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, err // Return raw error for NotFound detection
	}

	return parseSnapshotResponse(resp)
}

// ListSnapshots lists snapshots with optional filtering
func (c *Client) ListSnapshots(ctx context.Context, sourceVolumeID string, maxEntries int32, startingToken string) ([]*Snapshot, string, error) {
	req := &SnapshotRequest{
		Perform:        PerformListSnapshots,
		SourceVolumeID: sourceVolumeID,
		MaxEntries:     maxEntries,
		StartingToken:  startingToken,
	}

	resp, err := c.doRequest(ctx, http.MethodPost, VolumesEndpoint, req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list snapshots: %w", err)
	}

	// Parse snapshots array
	var snapshots []*Snapshot
	if snapshotsData, ok := resp["snapshots"].([]interface{}); ok {
		for _, s := range snapshotsData {
			if snapMap, ok := s.(map[string]interface{}); ok {
				snap, err := parseSnapshotResponse(snapMap)
				if err == nil {
					snapshots = append(snapshots, snap)
				}
			}
		}
	}

	// Parse next token
	nextToken, _ := resp["nextToken"].(string)

	return snapshots, nextToken, nil
}

// parseVolumeResponse parses a volume from API response
func parseVolumeResponse(resp map[string]interface{}) (*Volume, error) {
	vol := &Volume{}

	if id, ok := resp["id"].(string); ok {
		vol.ID = id
	}
	if name, ok := resp["name"].(string); ok {
		vol.Name = name
	}
	if capacity, ok := resp["capacity"].(float64); ok {
		vol.SizeBytes = int64(capacity)
	}
	if volumeType, ok := resp["volumeType"].(string); ok {
		vol.VolumeType = volumeType
	}
	if devicePath, ok := resp["devicePath"].(string); ok {
		vol.DevicePath = devicePath
	}
	if nodeID, ok := resp["nodeId"].(string); ok {
		vol.NodeID = nodeID
	}
	if snapshotID, ok := resp["snapshotId"].(string); ok {
		vol.SnapshotID = snapshotID
	}

	return vol, nil
}

// parseSnapshotResponse parses a snapshot from API response
func parseSnapshotResponse(resp map[string]interface{}) (*Snapshot, error) {
	snap := &Snapshot{}

	if id, ok := resp["id"].(string); ok {
		snap.ID = id
	}
	if name, ok := resp["name"].(string); ok {
		snap.Name = name
	}
	if sourceVolumeID, ok := resp["sourceVolumeId"].(string); ok {
		snap.SourceVolumeID = sourceVolumeID
	}
	if sizeBytes, ok := resp["sizeBytes"].(float64); ok {
		snap.SizeBytes = int64(sizeBytes)
	}
	if createdAt, ok := resp["createdAt"].(float64); ok {
		snap.CreatedAt = int64(createdAt)
	}
	if readyToUse, ok := resp["readyToUse"].(bool); ok {
		snap.ReadyToUse = readyToUse
	}

	// Return nil if snapshot ID is empty (not found)
	if snap.ID == "" {
		return nil, nil
	}

	return snap, nil
}

// IsNotFoundError checks if error indicates resource not found
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// Check for 404 in error message
	errStr := err.Error()
	return contains(errStr, "404") || contains(errStr, "not found") || contains(errStr, "Not Found")
}

// IsAlreadyExistsError checks if error indicates resource already exists
func IsAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "409") || contains(errStr, "already exists") || contains(errStr, "Already Exists")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
