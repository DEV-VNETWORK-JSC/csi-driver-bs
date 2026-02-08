# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the webhook binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o pvc-expand-webhook ./cmd/pvc-expand-webhook

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /app/pvc-expand-webhook /pvc-expand-webhook

ENTRYPOINT ["/pvc-expand-webhook"]
