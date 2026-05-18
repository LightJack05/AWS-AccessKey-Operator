#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OPERATOR_NAMESPACE="aws-accesskey-operator-system"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"

$CONTAINER_TOOL compose -f "$SCRIPT_DIR/docker-compose.yml" down -v

kubectl delete service seaweedfs -n "$OPERATOR_NAMESPACE" --ignore-not-found
kubectl delete endpoints seaweedfs -n "$OPERATOR_NAMESPACE" --ignore-not-found
kubectl delete secret seaweedfs-admin -n "$OPERATOR_NAMESPACE" --ignore-not-found
