# Build stage
FROM --platform=linux/amd64 golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version info
ARG VERSION=dev
ARG GIT_COMMIT=unknown

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT}" \
    -o vcloud-csi-plugin \
    ./cmd/vcloud-csi-plugin

# Runtime stage
FROM --platform=linux/amd64 alpine:3.19

RUN apk add --no-cache \
    ca-certificates \
    e2fsprogs \
    e2fsprogs-extra \
    xfsprogs \
    blkid \
    mount \
    umount \
    util-linux

# Create non-root user (CSI requires root for mount operations)
# Keeping root for now as mount operations require it

COPY --from=builder /app/vcloud-csi-plugin /bin/vcloud-csi-plugin

ENTRYPOINT ["/bin/vcloud-csi-plugin"]
