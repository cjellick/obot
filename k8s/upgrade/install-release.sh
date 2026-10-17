#!/usr/bin/env bash
# Upgrade test, step 1: install the latest RELEASED Obot (image and chart from
# https://charts.obot.ai), 2 replicas on the in-cluster Postgres, with daily token limits set.
#   QA_ENV_FILE=... ./k8s/upgrade/install-release.sh
# Then configure the legacy content listed in README.md and run upgrade.sh.
set -euo pipefail
. "$(dirname "$0")/../lib.sh"
OLD_VERSION="${OBOT_OLD_VERSION:-v0.26.3}"
# Daily per-user token limits the old release enforces; the upgrade should carry them into
# the gateway's "Daily token limit" budget.
DAILY_INPUT="${OBOT_OLD_DAILY_INPUT_TOKEN_LIMIT:-50000}"
DAILY_OUTPUT="${OBOT_OLD_DAILY_OUTPUT_TOKEN_LIMIT:-20000}"

if helm status "$RELEASE" -n "$NS" >/dev/null 2>&1; then
  echo "release $RELEASE already exists; run ./k8s/down.sh first for a clean upgrade test" >&2
  exit 1
fi

# The same settings are passed to the upgrade, as an operator upgrading in place keeps them.
# Message policies are off by default in the released versions.
cat > "$STATE_DIR/upgrade-values.yaml" <<VALUES
config:
  OBOT_SERVER_ENABLE_MESSAGE_POLICIES: "true"
  OBOT_SERVER_DAILY_USER_INPUT_TOKEN_LIMIT: "$DAILY_INPUT"
  OBOT_SERVER_DAILY_USER_OUTPUT_TOKEN_LIMIT: "$DAILY_OUTPUT"
VALUES
echo "$OLD_VERSION" > "$STATE_DIR/old-version"

ensure_cluster
deploy_postgres
make_secret
helm upgrade --install "$RELEASE" obot --repo https://charts.obot.ai --version "$OLD_VERSION" -n "$NS" \
  -f "$K8S_DIR/values.yaml" -f "$STATE_DIR/upgrade-values.yaml" \
  --set-string image.repository=ghcr.io/obot-platform/obot --set-string image.tag="$OLD_VERSION"
wait_obot
check_health
print_k8s_access
echo
echo "Released Obot $OLD_VERSION is up, daily limits input=$DAILY_INPUT output=$DAILY_OUTPUT."
echo "Configure the legacy content (README.md, 'Upgrade test'), then run ./k8s/upgrade/upgrade.sh <PR chart dir>."
