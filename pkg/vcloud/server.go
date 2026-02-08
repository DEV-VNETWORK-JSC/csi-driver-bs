package vcloud

import (
	"context"
	"net"
	"os"
	"sync"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// NonBlockingGRPCServer defines a non-blocking gRPC server interface
type NonBlockingGRPCServer interface {
	// Start starts the gRPC server in a goroutine
	Start(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer, testMode bool)
	// Wait waits for the server to exit
	Wait()
	// Stop stops the server
	Stop()
	// ForceStop stops the server forcefully
	ForceStop()
}

// nonBlockingGRPCServer implements NonBlockingGRPCServer
type nonBlockingGRPCServer struct {
	wg     sync.WaitGroup
	server *grpc.Server
}

// NewNonBlockingGRPCServer creates a new NonBlockingGRPCServer
func NewNonBlockingGRPCServer() NonBlockingGRPCServer {
	return &nonBlockingGRPCServer{}
}

// Start starts the gRPC server
func (s *nonBlockingGRPCServer) Start(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer, testMode bool) {
	s.wg.Add(1)
	go s.serve(endpoint, ids, cs, ns, testMode)
}

// Wait waits for the server to exit
func (s *nonBlockingGRPCServer) Wait() {
	s.wg.Wait()
}

// Stop stops the server gracefully
func (s *nonBlockingGRPCServer) Stop() {
	if s.server != nil {
		s.server.GracefulStop()
	}
}

// ForceStop stops the server forcefully
func (s *nonBlockingGRPCServer) ForceStop() {
	if s.server != nil {
		s.server.Stop()
	}
}

func (s *nonBlockingGRPCServer) serve(endpoint string, ids csi.IdentityServer, cs csi.ControllerServer, ns csi.NodeServer, testMode bool) {
	defer s.wg.Done()

	// Parse protocol and address from endpoint
	proto, addr, err := parseEndpoint(endpoint)
	if err != nil {
		klog.Fatalf("Failed to parse endpoint: %v", err)
	}

	// For unix sockets, remove existing socket file
	if proto == "unix" {
		if err := os.Remove(addr); err != nil && !os.IsNotExist(err) {
			klog.Fatalf("Failed to remove existing socket file %s: %v", addr, err)
		}
	}

	// Create listener
	listener, err := net.Listen(proto, addr)
	if err != nil {
		klog.Fatalf("Failed to listen on %s://%s: %v", proto, addr, err)
	}

	// Create gRPC server with interceptor
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(logGRPC),
	}
	s.server = grpc.NewServer(opts...)

	// Register CSI services
	if ids != nil {
		csi.RegisterIdentityServer(s.server, ids)
		klog.V(2).Info("Identity server registered")
	}
	if cs != nil {
		csi.RegisterControllerServer(s.server, cs)
		klog.V(2).Info("Controller server registered")
	}
	if ns != nil {
		csi.RegisterNodeServer(s.server, ns)
		klog.V(2).Info("Node server registered")
	}

	klog.V(2).Infof("Listening for connections on address: %s://%s", proto, addr)

	if err := s.server.Serve(listener); err != nil {
		klog.Fatalf("Failed to serve: %v", err)
	}
}

// parseEndpoint parses a CSI endpoint into protocol and address
func parseEndpoint(endpoint string) (string, string, error) {
	// Handle unix:// prefix
	if len(endpoint) > 7 && endpoint[:7] == "unix://" {
		return "unix", endpoint[7:], nil
	}
	// Handle unix: prefix (without double slash)
	if len(endpoint) > 5 && endpoint[:5] == "unix:" {
		return "unix", endpoint[5:], nil
	}
	// Handle tcp:// prefix
	if len(endpoint) > 6 && endpoint[:6] == "tcp://" {
		return "tcp", endpoint[6:], nil
	}
	// Default to unix socket
	return "unix", endpoint, nil
}

// logGRPC is a gRPC interceptor that logs requests and responses
func logGRPC(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	klog.V(4).Infof("GRPC call: %s", info.FullMethod)
	klog.V(6).Infof("GRPC request: %+v", req)

	resp, err := handler(ctx, req)

	if err != nil {
		klog.Errorf("GRPC error: %v", err)
	} else {
		klog.V(6).Infof("GRPC response: %+v", resp)
	}

	return resp, err
}
