#!/bin/bash

# Generate TLS certificates for the PVC Expand Webhook
# This script creates a self-signed CA and server certificate

set -e

SERVICE_NAME="pvc-expand-webhook"
NAMESPACE="kube-system"
SECRET_NAME="pvc-expand-webhook-tls"

# Create temporary directory
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

cd $TMPDIR

# Generate CA key and cert
openssl genrsa -out ca.key 2048
openssl req -new -x509 -days 3650 -key ca.key -out ca.crt -subj "/CN=pvc-expand-webhook-ca"

# Generate server key
openssl genrsa -out server.key 2048

# Generate server CSR
cat > server.conf << EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
prompt = no

[req_distinguished_name]
CN = ${SERVICE_NAME}.${NAMESPACE}.svc

[v3_req]
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local
EOF

openssl req -new -key server.key -out server.csr -config server.conf

# Generate server cert
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out server.crt -days 3650 -extensions v3_req -extfile server.conf

# Create Kubernetes secret
echo "Creating TLS secret in ${NAMESPACE}..."
kubectl create secret tls ${SECRET_NAME} \
    --cert=server.crt \
    --key=server.key \
    --namespace=${NAMESPACE} \
    --dry-run=client -o yaml | kubectl apply -f -

# Output CA bundle for webhook configuration
echo ""
echo "CA Bundle (base64 encoded) for ValidatingWebhookConfiguration:"
echo "---"
cat ca.crt | base64 | tr -d '\n'
echo ""
echo "---"
echo ""
echo "Add this to the 'caBundle' field in pvc-expand-webhook.yaml"
