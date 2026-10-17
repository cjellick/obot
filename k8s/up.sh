#!/usr/bin/env bash
# HA on kind: 2 Obot replicas from the PR's chart on an in-cluster Postgres.
#   QA_ENV_FILE=... ./k8s/up.sh /path/to/obot-pr-checkout/chart
# OBOT_IMAGE (default obot:llm-gateway) must already be built locally (see README).
set -euo pipefail
. "$(dirname "$0")/lib.sh"
CHART="${1:-${OBOT_CHART:-}}"
[ -f "$CHART/Chart.yaml" ] || { echo "usage: up.sh <path to the PR checkout's chart/ dir>" >&2; exit 1; }
docker image inspect "$OBOT_IMAGE" >/dev/null 2>&1 || { echo "image $OBOT_IMAGE not found; build it first (README)" >&2; exit 1; }

ensure_cluster
load_image "$OBOT_IMAGE"
deploy_postgres
make_secret
# shellcheck disable=SC2046
helm upgrade --install "$RELEASE" "$CHART" -n "$NS" -f "$K8S_DIR/values.yaml" \
  $(image_args "$OBOT_IMAGE") \
  --set-string config.OBOT_SERVER_LLM_GATEWAY_MODEL_DATA_URL="$OBOT_MODEL_DATA_URL"
wait_obot
check_health
print_k8s_access
