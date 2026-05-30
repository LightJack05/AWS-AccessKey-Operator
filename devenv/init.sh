#!/usr/bin/bash
set -euxo pipefail

kubectl apply -f k8s/providerConfig.yaml

./scripts/create-aws-users.sh
