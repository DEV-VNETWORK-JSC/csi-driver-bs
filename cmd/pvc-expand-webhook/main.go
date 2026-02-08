package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

// WebhookServer holds the webhook server configuration
type WebhookServer struct {
	client *kubernetes.Clientset
	server *http.Server
}

func init() {
	_ = corev1.AddToScheme(scheme)
	_ = admissionv1.AddToScheme(scheme)
}

func main() {
	var tlsCert, tlsKey string
	var port int

	flag.StringVar(&tlsCert, "tls-cert", "/etc/webhook/certs/tls.crt", "TLS certificate file")
	flag.StringVar(&tlsKey, "tls-key", "/etc/webhook/certs/tls.key", "TLS key file")
	flag.IntVar(&port, "port", 8443, "Webhook server port")
	flag.Parse()

	klog.InitFlags(nil)

	// Create Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to get in-cluster config: %v", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Load TLS certificates
	cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
	if err != nil {
		klog.Fatalf("Failed to load TLS certificates: %v", err)
	}

	ws := &WebhookServer{
		client: client,
	}

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/validate", ws.handleValidate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	ws.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	// Start server
	go func() {
		klog.Infof("Starting PVC Expand Webhook server on port %d", port)
		if err := ws.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			klog.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	klog.Info("Shutting down webhook server...")
	ws.server.Shutdown(context.Background())
}

func (ws *WebhookServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	klog.V(4).Info("Received validation request")

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}

	// Decode AdmissionReview
	ar := &admissionv1.AdmissionReview{}
	if _, _, err := codecs.UniversalDeserializer().Decode(body, nil, ar); err != nil {
		klog.Errorf("Failed to decode AdmissionReview: %v", err)
		http.Error(w, "Failed to decode request", http.StatusBadRequest)
		return
	}

	// Process the request
	response := ws.validate(ar)

	// Send response
	ar.Response = response
	ar.Response.UID = ar.Request.UID

	respBytes, err := json.Marshal(ar)
	if err != nil {
		klog.Errorf("Failed to marshal response: %v", err)
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func (ws *WebhookServer) validate(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := ar.Request

	// Only handle PVC updates
	if req.Kind.Kind != "PersistentVolumeClaim" || req.Operation != admissionv1.Update {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	klog.V(2).Infof("Validating PVC update: %s/%s", req.Namespace, req.Name)

	// Decode old and new PVC
	oldPVC := &corev1.PersistentVolumeClaim{}
	newPVC := &corev1.PersistentVolumeClaim{}

	if err := json.Unmarshal(req.OldObject.Raw, oldPVC); err != nil {
		klog.Errorf("Failed to decode old PVC: %v", err)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	if err := json.Unmarshal(req.Object.Raw, newPVC); err != nil {
		klog.Errorf("Failed to decode new PVC: %v", err)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	// Check if this is a resize request (storage size increased)
	oldSize := oldPVC.Spec.Resources.Requests[corev1.ResourceStorage]
	newSize := newPVC.Spec.Resources.Requests[corev1.ResourceStorage]

	if newSize.Cmp(oldSize) <= 0 {
		// Not expanding, allow
		klog.V(4).Infof("PVC %s/%s: not a resize request, allowing", req.Namespace, req.Name)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	klog.V(2).Infof("PVC %s/%s: resize detected from %s to %s", req.Namespace, req.Name, oldSize.String(), newSize.String())

	// Check if PVC is bound to a PV
	if oldPVC.Status.Phase != corev1.ClaimBound {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: fmt.Sprintf("PVC %s/%s is not bound to a volume", req.Namespace, req.Name),
			},
		}
	}

	pvName := oldPVC.Spec.VolumeName
	if pvName == "" {
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	// Get PV to find volume handle
	pv, err := ws.client.CoreV1().PersistentVolumes().Get(context.Background(), pvName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get PV %s: %v", pvName, err)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	// Only validate vCloud CSI volumes
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != "vcloud.csi.vnetwork.dev" {
		klog.V(4).Infof("PVC %s/%s: not a vCloud CSI volume, allowing", req.Namespace, req.Name)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	volumeHandle := pv.Spec.CSI.VolumeHandle

	// Check if volume is attached (has VolumeAttachment)
	attached, err := ws.isVolumeAttached(volumeHandle)
	if err != nil {
		klog.Errorf("Failed to check VolumeAttachment: %v", err)
		// On error, allow and let CSI handle it
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	if !attached {
		klog.Infof("PVC %s/%s: volume %s is not attached, blocking resize", req.Namespace, req.Name, volumeHandle)
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Code:    http.StatusForbidden,
				Message: fmt.Sprintf("volume must be attached to a running workload before it can be expanded. Create a Pod using this PVC first."),
			},
		}
	}

	klog.V(2).Infof("PVC %s/%s: volume is attached, allowing resize", req.Namespace, req.Name)
	return &admissionv1.AdmissionResponse{Allowed: true}
}

func (ws *WebhookServer) isVolumeAttached(volumeHandle string) (bool, error) {
	// List all VolumeAttachments
	vaList, err := ws.client.StorageV1().VolumeAttachments().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list VolumeAttachments: %w", err)
	}

	for _, va := range vaList.Items {
		if va.Spec.Source.PersistentVolumeName != nil {
			// Get PV to check volume handle
			pv, err := ws.client.CoreV1().PersistentVolumes().Get(context.Background(), *va.Spec.Source.PersistentVolumeName, metav1.GetOptions{})
			if err != nil {
				continue
			}
			if pv.Spec.CSI != nil && pv.Spec.CSI.VolumeHandle == volumeHandle {
				// Check if attachment is successful
				if va.Status.Attached {
					return true, nil
				}
			}
		}
	}

	return false, nil
}
