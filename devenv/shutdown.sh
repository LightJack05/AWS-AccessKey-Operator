#!/usr/bin/bash
set -euxo pipefail

K8S_NAMESPACE="${K8S_NAMESPACE:-ansible-operator-system}"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"

$CONTAINER_TOOL compose -f "docker-compose.yml" down -v

kind delete cluster -n aws-accesskey-operator-dev || true
docker compose down || true
