package client

// PerformAction defines the action type for API requests
type PerformAction string

const (
	// PerformCreateVolume is the action for creating a volume
	PerformCreateVolume PerformAction = "CREATE_VOLUME"

	// PerformDeleteVolume is the action for deleting a volume
	PerformDeleteVolume PerformAction = "DELETE_VOLUME"

	// PerformAttachVolume is the action for attaching a volume
	PerformAttachVolume PerformAction = "ATTACH_VOLUME"

	// PerformDetachVolume is the action for detaching a volume
	PerformDetachVolume PerformAction = "DETACH_VOLUME"

	// PerformExpandVolume is the action for expanding a volume
	PerformExpandVolume PerformAction = "EXPAND_VOLUME"

	// PerformGetVolume is the action for getting a volume
	PerformGetVolume PerformAction = "GET_VOLUME"

	// PerformCreateSnapshot is the action for creating a snapshot
	PerformCreateSnapshot PerformAction = "CREATE_SNAPSHOT"

	// PerformDeleteSnapshot is the action for deleting a snapshot
	PerformDeleteSnapshot PerformAction = "DELETE_SNAPSHOT"

	// PerformGetSnapshot is the action for getting a snapshot
	PerformGetSnapshot PerformAction = "GET_SNAPSHOT"

	// PerformListSnapshots is the action for listing snapshots
	PerformListSnapshots PerformAction = "LIST_SNAPSHOTS"
)

// VolumeRequest is the request payload for volume operations
type VolumeRequest struct {
	Perform    PerformAction `json:"perform"`
	Name       string        `json:"name,omitempty"`
	Capacity   int64         `json:"capacity,omitempty"`
	VolumeType string        `json:"volumeType,omitempty"`
	VolumeID   string        `json:"volumeID,omitempty"`
	NodeID     string        `json:"nodeID,omitempty"`
	SnapshotID string        `json:"snapshotID,omitempty"`
}

// VolumeResponse is the response from volume operations
type VolumeResponse struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Capacity   int64  `json:"capacity,omitempty"`
	VolumeType string `json:"volumeType,omitempty"`
	DevicePath string `json:"devicePath,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	SnapshotID string `json:"snapshotId,omitempty"`
	Error      string `json:"error,omitempty"`
	Message    string `json:"message,omitempty"`
}

// SnapshotRequest is the request payload for snapshot operations
type SnapshotRequest struct {
	Perform        PerformAction `json:"perform"`
	Name           string        `json:"name,omitempty"`
	SnapshotID     string        `json:"snapshotID,omitempty"`
	SourceVolumeID string        `json:"sourceVolumeID,omitempty"`
	MaxEntries     int32         `json:"maxEntries,omitempty"`
	StartingToken  string        `json:"startingToken,omitempty"`
}

// SnapshotResponse is the response from snapshot operations
type SnapshotResponse struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	SourceVolumeID string `json:"sourceVolumeId,omitempty"`
	SizeBytes      int64  `json:"sizeBytes,omitempty"`
	CreatedAt      int64  `json:"createdAt,omitempty"`
	ReadyToUse     bool   `json:"readyToUse,omitempty"`
	Error          string `json:"error,omitempty"`
	Message        string `json:"message,omitempty"`
}

// ListSnapshotsResponse is the response from list snapshots operation
type ListSnapshotsResponse struct {
	Snapshots []*SnapshotResponse `json:"snapshots,omitempty"`
	NextToken string              `json:"nextToken,omitempty"`
	Error     string              `json:"error,omitempty"`
	Message   string              `json:"message,omitempty"`
}

// Volume represents a vCloud storage volume
type Volume struct {
	ID         string
	Name       string
	SizeBytes  int64
	VolumeType string
	DevicePath string
	NodeID     string
	SnapshotID string
}

// Snapshot represents a volume snapshot
type Snapshot struct {
	ID             string
	Name           string
	SourceVolumeID string
	SizeBytes      int64
	CreatedAt      int64
	ReadyToUse     bool
}

// AttachResult contains the result of an attach operation
type AttachResult struct {
	DevicePath string
}
