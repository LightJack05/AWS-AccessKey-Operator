#!/usr/bin/bash
set -euxo pipefail

K8S_NAMESPACE="${K8S_NAMESPACE:-aws-accesskey-operator-system}"

kind create cluster --config kind-config.yaml

OPERATOR_NAMESPACE="$K8S_NAMESPACE"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"
DEV_NETWORK="${DEV_NETWORK:-aws-accesskey-operator}"

SIGNING_KEY=$(openssl rand -hex 32)
ACCESS_KEY_ID=$(openssl rand -hex 10 | tr '[:lower:]' '[:upper:]')
SECRET_ACCESS_KEY=$(openssl rand -hex 20)

# Write security.toml with JWT signing key (required by IAM/STS service)
cat > "testdata/security.toml" <<EOF
[jwt.filer_signing]
key = "$SIGNING_KEY"
EOF

# Start SeaweedFS
$CONTAINER_TOOL compose up -d

# Wait for SeaweedFS master to be ready
echo "Waiting for SeaweedFS to be ready..."
until curl -sf http://127.0.0.1:9333/cluster/status > /dev/null 2>&1; do
  sleep 2
done

sleep 5

# Bootstrap the admin credentials directly via weed shell inside the container
$CONTAINER_TOOL exec seaweedfs-testenv sh -c \
  "echo 's3.configure -apply -user admin -access_key $ACCESS_KEY_ID -secret_key $SECRET_ACCESS_KEY -actions Admin' | weed shell"

# Get container IP on the kind network
SEAWEEDFS_IP=$($CONTAINER_TOOL inspect seaweedfs-testenv --format "{{(index .NetworkSettings.Networks \"$DEV_NETWORK\").IPAddress}}")

# Create operator namespace if it doesn't exist
kubectl create namespace "$OPERATOR_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: seaweedfs-admin
  namespace: $OPERATOR_NAMESPACE
stringData:
  creds: |
    [default]
    aws_access_key_id = "$ACCESS_KEY_ID"
    aws_secret_access_key = "$SECRET_ACCESS_KEY"
EOF

# Create headless service + endpoints for in-cluster access
kubectl apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: seaweedfs
  namespace: $OPERATOR_NAMESPACE
spec:
  clusterIP: None
  ports:
  - name: s3
    port: 8333
    protocol: TCP
---
apiVersion: v1
kind: Endpoints
metadata:
  name: seaweedfs
  namespace: $OPERATOR_NAMESPACE
subsets:
- addresses:
  - ip: $SEAWEEDFS_IP
  ports:
  - name: s3
    port: 8333
    protocol: TCP
EOF

# Configure AWS CLI profile
aws configure set aws_access_key_id "$ACCESS_KEY_ID" --profile seaweedfs-testenv
aws configure set aws_secret_access_key "$SECRET_ACCESS_KEY" --profile seaweedfs-testenv
aws configure set region us-east-1 --profile seaweedfs-testenv
aws configure set endpoint_url http://localhost:8333 --profile seaweedfs-testenv

echo ""
echo "SeaweedFS testenv is up."
echo "  S3  (in-cluster): http://seaweedfs.$OPERATOR_NAMESPACE.svc:8333"
echo "  S3  (local):      http://localhost:8333"
echo "  Credentials in k8s secret 'seaweedfs-admin' in namespace '$OPERATOR_NAMESPACE'"
echo "  AWS CLI profile:  seaweedfs-testenv"
