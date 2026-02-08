# Project info
PROJECT_NAME := vcloud-csi-driver
MODULE := gitlab.vnetwork.dev/golang/kubernetes/csi-driver-vcloud
BINARY_NAME := vcloud-csi-plugin

# Version info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags
LDFLAGS := -s -w \
	-X main.Version=$(VERSION) \
	-X main.GitCommit=$(GIT_COMMIT)

# Docker info
DOCKER_REGISTRY ?= k8s.io.reg.vnetwork.dev
DOCKER_IMAGE ?= $(DOCKER_REGISTRY)/techev/csi/csi-bs-driver
DOCKER_TAG ?= $(VERSION)

# Go settings
GO := go
GOFLAGS := -v
CGO_ENABLED := 0

.PHONY: all build clean test lint docker-build docker-push help

all: build

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

## build-linux: Build for Linux
build-linux:
	@echo "Building $(BINARY_NAME) for Linux..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME)-linux-amd64 ./cmd/$(BINARY_NAME)

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f coverage.out

## test: Run tests
test:
	@echo "Running tests..."
	$(GO) test -v -race -coverprofile=coverage.out ./...

## test-coverage: Run tests with coverage report
test-coverage: test
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## lint: Run linter
lint:
	@echo "Running linter..."
	golangci-lint run ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	goimports -w .

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./...

## mod-tidy: Tidy go modules
mod-tidy:
	@echo "Tidying modules..."
	$(GO) mod tidy

## mod-download: Download go modules
mod-download:
	@echo "Downloading modules..."
	$(GO) mod download

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image..."
	docker build \
		--platform linux/amd64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_IMAGE):latest \
		.

## docker-push: Push Docker image
docker-push: docker-build
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

## deploy: Deploy to Kubernetes
deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f deploy/kubernetes/

## undeploy: Remove from Kubernetes
undeploy:
	@echo "Removing from Kubernetes..."
	kubectl delete -f deploy/kubernetes/ --ignore-not-found

## version: Show version info
version:
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
