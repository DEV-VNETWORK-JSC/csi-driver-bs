# Refactoring Plan for vCloud CSI Driver

This document outlines future improvements and refactoring opportunities for the vCloud CSI driver.

## Current State

The driver is structured following NFS CSI patterns:

```
csi-driver-vcloud/
├── cmd/vcloud-csi-plugin/
│   └── main.go                    # Entry point
├── pkg/vcloud/
│   ├── vcloud.go                  # Main driver struct
│   ├── controllerserver.go        # Controller service
│   ├── nodeserver.go              # Node service
│   ├── identityserver.go          # Identity service
│   ├── server.go                  # gRPC server
│   ├── mounter.go                 # Mount interface
│   ├── utils.go                   # Utility functions
│   └── client/
│       ├── client.go              # HTTP API client
│       ├── types.go               # API types
│       └── config.go              # INI config loader
└── deploy/kubernetes/             # Kubernetes manifests
```

## Refactoring Opportunities

### 1. API Client: Transition to RESTful Design

**Current:** Legacy single-endpoint API with `perform` field
```go
// POST /volumes with perform field
type VolumeRequest struct {
    Perform    PerformAction `json:"perform"`  // CREATE_VOLUME, DELETE_VOLUME, etc.
    Name       string        `json:"name,omitempty"`
    VolumeID   string        `json:"volumeID,omitempty"`
    ...
}
```

**Proposed:** Standard RESTful endpoints
```go
// Volume operations
POST   /volumes              - Create volume
GET    /volumes/{id}         - Get volume
DELETE /volumes/{id}         - Delete volume
POST   /volumes/{id}/attach  - Attach volume
POST   /volumes/{id}/detach  - Detach volume
POST   /volumes/{id}/expand  - Expand volume

// Snapshot operations
POST   /snapshots            - Create snapshot
GET    /snapshots/{id}       - Get snapshot
DELETE /snapshots/{id}       - Delete snapshot
GET    /snapshots            - List snapshots
```

**Benefits:**
- More intuitive API design
- Better HTTP method semantics (GET for read, DELETE for delete)
- Easier to cache GET requests
- Standard REST tooling compatibility

**Implementation Steps:**
1. Add new RESTful methods alongside existing legacy methods
2. Add feature flag to switch between legacy and RESTful modes
3. Update backend API to support RESTful endpoints
4. Deprecate legacy endpoints after migration

---

### 2. Improve Error Handling

**Current:** String-based error detection
```go
func IsNotFoundError(err error) bool {
    errStr := err.Error()
    return contains(errStr, "404") || contains(errStr, "not found")
}
```

**Proposed:** Typed errors
```go
// errors.go
type APIError struct {
    StatusCode int    `json:"statusCode"`
    Code       string `json:"code"`
    Message    string `json:"message"`
}

func (e *APIError) Error() string {
    return fmt.Sprintf("[%d] %s: %s", e.StatusCode, e.Code, e.Message)
}

func IsNotFoundError(err error) bool {
    var apiErr *APIError
    if errors.As(err, &apiErr) {
        return apiErr.StatusCode == 404 || apiErr.Code == "NOT_FOUND"
    }
    return false
}
```

**Benefits:**
- Type-safe error handling
- Consistent error codes from backend
- Better debugging with error details

---

### 3. Add Client Tests

**Current:** No tests for client package

**Proposed:** Add comprehensive test coverage

```go
// client/client_test.go
func TestCreateVolume(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verify request
        assert.Equal(t, "POST", r.Method)
        assert.Equal(t, "/volumes", r.URL.Path)

        var req VolumeRequest
        json.NewDecoder(r.Body).Decode(&req)
        assert.Equal(t, PerformCreateVolume, req.Perform)

        // Return response
        json.NewEncoder(w).Encode(map[string]interface{}{
            "id": "vol-123",
        })
    }))
    defer server.Close()

    client := NewClient(server.URL, "test-token")
    vol, err := client.CreateVolume(ctx, "test-vol", 1073741824, "SSD", "")

    assert.NoError(t, err)
    assert.Equal(t, "vol-123", vol.ID)
}
```

---

### 4. Add Retry Logic for API Calls

**Current:** No retry on transient failures

**Proposed:** Add exponential backoff retry
```go
type Client struct {
    httpClient    *http.Client
    baseURL       string
    providerToken string
    retryConfig   RetryConfig
}

type RetryConfig struct {
    MaxRetries  int
    InitialWait time.Duration
    MaxWait     time.Duration
}

func (c *Client) doRequestWithRetry(ctx context.Context, method, endpoint string, payload interface{}) (map[string]interface{}, error) {
    var lastErr error
    for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
        resp, err := c.doRequest(ctx, method, endpoint, payload)
        if err == nil {
            return resp, nil
        }

        // Only retry on transient errors (5xx, network errors)
        if !isRetryable(err) {
            return nil, err
        }

        lastErr = err
        wait := c.retryConfig.InitialWait * time.Duration(1<<attempt)
        if wait > c.retryConfig.MaxWait {
            wait = c.retryConfig.MaxWait
        }

        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(wait):
        }
    }
    return nil, lastErr
}
```

---

### 5. Add Metrics and Observability

**Current:** Basic klog logging

**Proposed:** Add Prometheus metrics
```go
var (
    apiRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "vcloud_csi_api_request_duration_seconds",
            Help:    "Duration of API requests",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "endpoint", "status"},
    )

    apiRequestTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "vcloud_csi_api_request_total",
            Help: "Total number of API requests",
        },
        []string{"method", "endpoint", "status"},
    )
)

func (c *Client) doRequest(ctx context.Context, method, endpoint string, payload interface{}) (map[string]interface{}, error) {
    timer := prometheus.NewTimer(apiRequestDuration.WithLabelValues(method, endpoint, ""))
    defer timer.ObserveDuration()

    // ... existing logic ...

    apiRequestTotal.WithLabelValues(method, endpoint, strconv.Itoa(resp.StatusCode)).Inc()
}
```

---

### 6. Add Context Timeout Configuration

**Current:** Fixed 30-second timeout

**Proposed:** Configurable per-operation timeouts
```go
type Client struct {
    httpClient    *http.Client
    baseURL       string
    providerToken string
    timeouts      TimeoutConfig
}

type TimeoutConfig struct {
    Default    time.Duration
    Create     time.Duration
    Delete     time.Duration
    Attach     time.Duration
    Detach     time.Duration
    Expand     time.Duration
}

func DefaultTimeoutConfig() TimeoutConfig {
    return TimeoutConfig{
        Default: 30 * time.Second,
        Create:  60 * time.Second,
        Delete:  30 * time.Second,
        Attach:  120 * time.Second,
        Detach:  60 * time.Second,
        Expand:  120 * time.Second,
    }
}
```

---

### 7. Improve Volume Lock Granularity

**Current:** Lock key based on volumeID + path
```go
lockKey := fmt.Sprintf("%s-%s", volumeID, stagingTargetPath)
```

**Proposed:** Separate lock types for different operations
```go
type LockType string

const (
    LockTypeCreate  LockType = "create"
    LockTypeDelete  LockType = "delete"
    LockTypeAttach  LockType = "attach"
    LockTypeStage   LockType = "stage"
    LockTypePublish LockType = "publish"
)

type VolumeLocks struct {
    locks map[LockType]*sync.Map
}

func (vl *VolumeLocks) TryAcquire(lockType LockType, volumeID string) bool {
    // Allows concurrent stage and publish if they don't conflict
}
```

---

### 8. Add Health Check Endpoint

**Current:** No health check

**Proposed:** Add liveness/readiness endpoints
```go
func (d *Driver) RunHealthServer(port int) error {
    mux := http.NewServeMux()

    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        // Basic liveness check
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
        // Check vCloud API connectivity
        if d.vcloudClient != nil {
            if err := d.vcloudClient.HealthCheck(r.Context()); err != nil {
                w.WriteHeader(http.StatusServiceUnavailable)
                w.Write([]byte(err.Error()))
                return
            }
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })

    return http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}
```

---

## Priority Order

1. **High Priority (Security/Reliability)**
   - Add retry logic for API calls
   - Improve error handling with typed errors

2. **Medium Priority (Operability)**
   - Add client tests
   - Add health check endpoint
   - Add metrics and observability

3. **Low Priority (Future Improvements)**
   - Transition to RESTful API design
   - Add context timeout configuration
   - Improve volume lock granularity

---

## Migration Notes

When implementing RESTful API:
1. Backend must support both legacy and RESTful endpoints during transition
2. Add `--api-mode` flag: `legacy` (default) or `restful`
3. Update deployment manifests to use new flag
4. Monitor both endpoints during transition period
5. Deprecate legacy endpoints after 2 release cycles
