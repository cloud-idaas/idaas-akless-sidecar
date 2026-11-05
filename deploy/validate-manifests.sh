#!/usr/bin/env bash
# Validate against the current cluster's schema and admission rules.
# The target namespace must already exist.
# Server-side dry run only: no resources are persisted.
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kubectl apply --dry-run=server --validate=strict --request-timeout=20s \
  -f "$DEPLOY_DIR/namespace.yaml" \
  -f "$DEPLOY_DIR/serviceaccount.yaml" \
  -f "$DEPLOY_DIR/configmap.yaml" \
  -f "$DEPLOY_DIR/deployment.yaml"
