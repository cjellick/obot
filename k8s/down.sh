#!/usr/bin/env bash
# Deletes the kind cluster (Obot, Postgres and all data) and the kit's k8s state.
set -euo pipefail
KIT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
kind delete cluster --name "${KIND_CLUSTER:-obot-qa}"
rm -f "$KIT_DIR/.state/kubeconfig" "$KIT_DIR/.state/setup-token-k8s" "$KIT_DIR"/.state/cookies-* \
  "$KIT_DIR/.state/upgrade-values.yaml" "$KIT_DIR/.state/old-version"
echo "kept database backups, if any: $KIT_DIR/.state/backup-*.sql"
