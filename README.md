# Block Storage (BS) CSI Driver

A Container Storage Interface (CSI) driver for vCloud Block Storage.

**Driver Name:** `bs.csi.vnetwork.dev`

## Features

- Dynamic volume provisioning
- Volume expansion
- Volume snapshots
- ReadWriteOnce access mode
- Filesystem (ext4, xfs) support

## Upgrading from Legacy CSI

If you're using the legacy CSI driver (`vcloud.csi.vnetwork.dev` from `https://upload.vnetwork.vn/technical/k8s.io/csi/manifests/csi.yaml`), see the [Upgrade Guide](../UPGRADE.md) for step-by-step instructions.

The upgrade:
- Preserves all existing PVCs and data
- Adds snapshot and restore capabilities
- Maintains backward compatibility

## Installation

### Prerequisites

- Kubernetes 1.20+
- vCloud storage backend with API access
- kubectl configured for your cluster

### Deploy

1. Create the config secret:

```bash
kubectl create secret generic vcloud-config \
  --namespace kube-system \
  --from-file=cloud-config=/path/to/cloud-config
```

Example `cloud-config` file:

```ini
[vCloud]
MGMT_URL=https://api.vcloud.example.com
PROVIDER_TOKEN=your-provider-token
```

2. Deploy the CSI driver:

```bash
kubectl apply -f deploy/kubernetes/
```

3. Verify the deployment:

```bash
kubectl get pods -n kube-system -l app=csi-bs-driver
```

## Usage

### Create a StorageClass

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: csi-bs
provisioner: bs.csi.vnetwork.dev
parameters:
  type: SSD
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: Immediate
```

### Create a PersistentVolumeClaim

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: csi-bs
  resources:
    requests:
      storage: 10Gi
```

### Use the volume in a Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
spec:
  containers:
    - name: app
      image: nginx
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: my-pvc
```

### Volume Snapshots

Volume snapshots allow you to create point-in-time copies of your persistent volumes. These snapshots can be used to restore data or create new volumes with existing data.

#### Prerequisites

Ensure the VolumeSnapshot CRDs and csi-snapshot-controller are installed:

```bash
# Check CRDs
kubectl get crd | grep snapshot

# Check csi-snapshot-controller
kubectl get pods -n kube-system | grep csi-snapshot-controller
```

#### Important Limitation

Snapshots can only be created when the volume is **not attached** to a running workload. This is a backend limitation. To create a snapshot:

1. Stop the Pod using the PVC
2. Create the snapshot
3. Restart the Pod

If you attempt to snapshot an attached volume, you'll receive:
```
cannot create snapshot while volume is attached to a running workload. Stop the pod using this volume first
```

#### Snapshot Naming Convention

Snapshots are stored in the backend using the Kubernetes VolumeSnapshot name directly. This provides:
- **Idempotency**: Re-creating a VolumeSnapshot with the same name reuses the existing backend snapshot
- **Simplicity**: Easy to correlate Kubernetes resources with backend storage

Examples:
- VolumeSnapshot named `my-snapshot` → Backend image named `my-snapshot`
- VolumeSnapshot named `db-backup-2026` → Backend image named `db-backup-2026`

To find snapshots in the backend, search for images matching your VolumeSnapshot names or images starting with `snapshot-`.

#### Create a VolumeSnapshot

1. First, stop the Pod using the PVC:

```bash
kubectl delete pod my-pod
```

2. Create the snapshot:

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: my-snapshot
spec:
  volumeSnapshotClassName: csi-bs-snapclass
  source:
    persistentVolumeClaimName: my-pvc
```

3. Wait for snapshot to be ready:

```bash
kubectl get volumesnapshot my-snapshot
# READYTOUSE should be "true"
```

4. Restart your Pod:

```bash
kubectl apply -f my-pod.yaml
```

#### List Snapshots

```bash
# List all snapshots
kubectl get volumesnapshots

# List with details
kubectl get volumesnapshots -o wide

# Describe a snapshot
kubectl describe volumesnapshot my-snapshot
```

#### Restore from Snapshot

Create a new PVC from an existing snapshot:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: restored-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: csi-bs
  resources:
    requests:
      storage: 10Gi
  dataSource:
    name: my-snapshot
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
```

The restored PVC will contain all data from the snapshot.

#### Delete a Snapshot

```bash
kubectl delete volumesnapshot my-snapshot
```

#### VolumeSnapshotClass

The default VolumeSnapshotClass is deployed with the CSI driver:

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: csi-bs-snapclass
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: bs.csi.vnetwork.dev
deletionPolicy: Delete
```

| Setting | Value | Description |
|---------|-------|-------------|
| `deletionPolicy` | `Delete` | Snapshot is deleted when VolumeSnapshot is deleted |
| `driver` | `bs.csi.vnetwork.dev` | CSI driver name |

#### Snapshot Deletion Behavior

**Important:** Snapshots are independent of the source PVC. The `deletionPolicy` controls what happens when the **VolumeSnapshot** is deleted, not when the source PVC is deleted.

| Policy | Behavior |
|--------|----------|
| `Delete` | When VolumeSnapshot is deleted, backend snapshot is also deleted |
| `Retain` | When VolumeSnapshot is deleted, backend snapshot is kept |

Deleting the source PVC does **not** automatically delete its snapshots. This is by design - you may want to keep snapshots for backup even after the original volume is deleted.

To clean up snapshots when deleting a PVC:

```bash
# Find snapshots from a specific PVC
kubectl get volumesnapshot -o wide | grep my-pvc

# Delete the snapshots
kubectl delete volumesnapshot my-snapshot

# Then delete the PVC
kubectl delete pvc my-pvc
```

### Restore from Backend Volume (Disaster Recovery)

If you accidentally delete a PVC or PV, you can restore access to the backend volume using the `volumeHandle`. The backend volume persists even when Kubernetes PV/PVC resources are deleted.

> **Important:** This only works with `reclaimPolicy: Retain`. If your StorageClass uses `reclaimPolicy: Delete`, the backend volume is automatically deleted when the PV is deleted, making recovery impossible. Always use `Retain` for critical data.

#### Scenario 1: PV is Released (PVC deleted, PV exists)

When a PVC is deleted but the PV still exists with `Released` status:

```bash
# Check PV status
kubectl get pv
# NAME               STATUS     CLAIM                  STORAGECLASS
# pv-example         Released   default/my-pvc         standard

# Clear the claimRef to make PV available again
kubectl patch pv pv-example -p '{"spec":{"claimRef":null}}'

# Create new PVC bound to the existing PV
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: restored-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: standard
  volumeName: pv-example
  resources:
    requests:
      storage: 10Gi
EOF
```

#### Scenario 2: PV is Deleted (Full Recovery)

When both PVC and PV are deleted, you can recreate them using the backend volume name as `volumeHandle`:

```bash
# Create PV with volumeHandle pointing to backend volume name
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pv-recovered
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: standard
  csi:
    driver: vcloud.csi.vnetwork.dev
    volumeHandle: <backend-volume-name>
    fsType: ext4
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: restored-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: standard
  volumeName: pv-recovered
  resources:
    requests:
      storage: 10Gi
EOF
```

**Finding the volumeHandle:**

The `volumeHandle` is the volume name in the OpenNebula backend. You can find it by:

1. **From existing PV** (before deletion):
   ```bash
   kubectl get pv <pv-name> -o jsonpath='{.spec.csi.volumeHandle}'
   ```

2. **From OpenNebula UI/CLI**: Look for images with names matching your PVC pattern (e.g., `pvc-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`)

3. **From backend API**: Query the storage backend for available volumes

#### Example: Full Recovery Workflow

```bash
# 1. Identify the backend volume name (volumeHandle)
#    e.g., pvc-17c119d3-7394-48dc-a28c-4a20cd877ccd

# 2. Create PV and PVC
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pv-recovered
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: standard
  csi:
    driver: vcloud.csi.vnetwork.dev
    volumeHandle: pvc-17c119d3-7394-48dc-a28c-4a20cd877ccd
    fsType: ext4
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: restored-pvc
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: standard
  volumeName: pv-recovered
  resources:
    requests:
      storage: 10Gi
EOF

# 3. Create a pod to access the restored volume
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-restored
spec:
  containers:
  - name: busybox
    image: busybox
    command: ["sleep", "3600"]
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: restored-pvc
EOF

# 4. Verify data is intact
kubectl exec test-restored -- ls -la /data
```

### Volume Expansion

Volumes can be expanded by editing the PVC's storage request. The vCloud backend requires volumes to be attached to a running workload before expansion.

```bash
kubectl patch pvc my-pvc -p '{"spec":{"resources":{"requests":{"storage":"20Gi"}}}}'
```

## PVC Expand Validation Webhook (Optional)

The vCloud storage backend requires volumes to be attached to a node before they can be expanded. Without the webhook, expansion requests for unattached volumes will fail at the CSI driver level and retry indefinitely.

The PVC Expand Webhook provides Kubernetes API-level validation to block expansion requests immediately when the volume is not attached, providing clear feedback to users similar to how Kubernetes blocks PVC shrinking.

### Deploy the Webhook

1. Generate TLS certificates:

```bash
chmod +x deploy/kubernetes/webhook/generate-certs.sh
./deploy/kubernetes/webhook/generate-certs.sh
```

2. Update the `caBundle` in `deploy/kubernetes/webhook/pvc-expand-webhook.yaml` with the output from the script.

3. Deploy the webhook:

```bash
kubectl apply -f deploy/kubernetes/webhook/pvc-expand-webhook.yaml
```

4. Verify the deployment:

```bash
kubectl get pods -n kube-system -l app=pvc-expand-webhook
```

### How It Works

The webhook intercepts PVC UPDATE requests and:

1. Checks if the request is a storage expansion (new size > old size)
2. Verifies the PVC is bound to a PersistentVolume
3. Checks if the volume is a vCloud CSI volume (`bs.csi.vnetwork.dev`)
4. Queries VolumeAttachment resources to verify the volume is attached to a node
5. Blocks the request if the volume is not attached

### Example

When attempting to expand an unattached PVC:

```bash
$ kubectl patch pvc my-pvc -p '{"spec":{"resources":{"requests":{"storage":"20Gi"}}}}'
Error from server: admission webhook "pvc-expand.bs.csi.vnetwork.dev" denied the request:
volume must be attached to a running workload before it can be expanded. Create a Pod using this PVC first.
```

To expand a volume:
1. Create a Pod that uses the PVC
2. Wait for the Pod to be running (volume attached)
3. Patch the PVC to increase storage
4. The expansion will proceed through the CSI driver

### Webhook Configuration

| Setting | Value | Description |
|---------|-------|-------------|
| Failure Policy | `Ignore` | If the webhook is unavailable, PVC updates are allowed (fail-open) |
| Scope | `Namespaced` | Only validates PVCs, not cluster-scoped resources |
| Operations | `UPDATE` | Only intercepts PVC updates, not creates or deletes |

## Configuration

### Driver Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--endpoint` | `unix:///var/lib/kubelet/plugins/bs.csi.vnetwork.dev/csi.sock` | CSI endpoint |
| `--config` | `/etc/config/cloud-config` | Path to config file |
| `--node-id` | `$NODE_ID` | Node identifier |
| `--driver-name` | `bs.csi.vnetwork.dev` | CSI driver name |
| `--log-level` | `info` | Log level (debug, info, warn, error) |

### StorageClass Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `type` | `hiops` | Volume type (hiops, SSD, HDD) |

## Development

### Build

```bash
make build
```

### Run Tests

```bash
make test
```

### Build Docker Image

```bash
make docker-build
```

### Local Testing

```bash
# Start the driver
./bin/vcloud-csi-plugin \
  --endpoint unix:///tmp/csi.sock \
  --config /path/to/cloud-config \
  --node-id test-node

# Test with csc tool
csc identity plugin-info --endpoint unix:///tmp/csi.sock
```

## License

Apache 2.0
