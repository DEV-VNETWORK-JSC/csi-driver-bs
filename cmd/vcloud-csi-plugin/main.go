package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gitlab.vnetwork.dev/golang/kubernetes/csi-driver-vcloud/pkg/vcloud"
	"gitlab.vnetwork.dev/golang/kubernetes/csi-driver-vcloud/pkg/vcloud/client"
	"k8s.io/klog/v2"
)

const (
	// DefaultDriverName is the default CSI driver name
	DefaultDriverName = "bs.csi.vnetwork.dev"

	// DefaultConfigPath is the default path to the config file
	DefaultConfigPath = "/etc/config/cloud-config"
)

var (
	// Version is set at build time
	Version = "dev"
	// GitCommit is set at build time
	GitCommit = "unknown"
)

func main() {
	// Driver options
	var (
		endpoint         = flag.String("endpoint", "unix:///var/lib/kubelet/plugins/"+DefaultDriverName+"/csi.sock", "CSI endpoint")
		configFile       = flag.String("config", DefaultConfigPath, "Path to config file")
		nodeID           = flag.String("nodeid", "", "Node ID (uses NODE_ID env if empty)")
		driverName       = flag.String("drivername", DefaultDriverName, "CSI driver name")
		mountPermissions = flag.Uint64("mount-permissions", 0, "Mounted folder permissions (0 uses default)")
		workingMountDir  = flag.String("working-mount-dir", "/tmp", "Working directory for mount operations")
		version          = flag.Bool("version", false, "Print version and exit")
	)

	// Initialize klog flags
	klog.InitFlags(nil)
	flag.Parse()

	if *version {
		fmt.Printf("Block Storage (BS) CSI Driver\n")
		fmt.Printf("  Version:    %s\n", Version)
		fmt.Printf("  Git Commit: %s\n", GitCommit)
		os.Exit(0)
	}

	// Get node ID from flag or environment
	nid := *nodeID
	if nid == "" {
		nid = os.Getenv("NODE_ID")
	}

	klog.V(2).Infof("Starting Block Storage CSI driver version %s", Version)
	klog.V(2).Infof("Driver name: %s", *driverName)
	klog.V(2).Infof("Endpoint: %s", *endpoint)
	klog.V(2).Infof("Node ID: %s", nid)

	// Load API credentials from config file
	var apiURL, providerToken string
	if _, err := os.Stat(*configFile); err == nil {
		cfg, err := client.LoadConfigFromFile(*configFile)
		if err != nil {
			klog.Warningf("Failed to load config file: %v, running in node-only mode", err)
		} else {
			apiURL = cfg.APIURL
			providerToken = cfg.ProviderToken
			klog.V(2).Info("Loaded config file, running in controller mode")
		}
	} else {
		klog.V(2).Info("Config file not found, running in node-only mode")
	}

	// Create driver options
	options := &vcloud.DriverOptions{
		DriverName:       *driverName,
		NodeID:           nid,
		Endpoint:         *endpoint,
		APIUrl:           apiURL,
		ProviderToken:    providerToken,
		Version:          Version,
		MountPermissions: *mountPermissions,
		WorkingMountDir:  *workingMountDir,
	}

	// Set up signal handler for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		klog.Infof("Received signal %v, shutting down", sig)
		klog.Flush()
		os.Exit(0)
	}()

	// Create driver
	d, err := vcloud.NewDriver(options)
	if err != nil {
		klog.Fatalf("Failed to create driver: %v", err)
	}

	// Run driver
	if err := d.Run(false); err != nil {
		klog.Fatalf("Driver failed: %v", err)
	}

	klog.V(2).Info("Driver stopped")
}
