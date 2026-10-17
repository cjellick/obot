#!/usr/bin/env bash
# Upgrade test, step 2: back up the database, then `helm upgrade` the released install to the
# PR's chart and image. The Recreate strategy stops both old replicas first, so both new
# replicas start on the upgraded database at the same time.
#   QA_ENV_FILE=... ./k8s/upgrade/upgrade.sh /path/to/obot-pr-checkout/chart
set -euo pipefail
. "$(dirname "$0")/../lib.sh"
CHART="${1:-${OBOT_CHART:-}}"
[ -f "$CHART/Chart.yaml" ] || { echo "usage: upgrade.sh <path to the PR checkout's chart/ dir>" >&2; exit 1; }
docker image inspect "$OBOT_IMAGE" >/dev/null 2>&1 || { echo "image $OBOT_IMAGE not found; build it first (README)" >&2; exit 1; }
helm status "$RELEASE" -n "$NS" >/dev/null || { echo "no release to upgrade; run install-release.sh first" >&2; exit 1; }

backup="$STATE_DIR/backup-$(date +%Y%m%d-%H%M%S).sql"
(umask 077; kubectl -n "$NS" exec postgres-0 -- pg_dumpall -U obot > "$backup")
echo "backed up every database to $backup ($(wc -c < "$backup") bytes)"
echo "restore: kubectl -n $NS exec -i postgres-0 -- psql -U obot -d postgres < $backup  (into an empty Postgres)"

load_image "$OBOT_IMAGE"
make_secret
values=(-f "$K8S_DIR/values.yaml")
[ -f "$STATE_DIR/upgrade-values.yaml" ] && values+=(-f "$STATE_DIR/upgrade-values.yaml")
# shellcheck disable=SC2046
helm upgrade "$RELEASE" "$CHART" -n "$NS" "${values[@]}" \
  $(image_args "$OBOT_IMAGE") \
  --set-string config.OBOT_SERVER_LLM_GATEWAY_MODEL_DATA_URL="$OBOT_MODEL_DATA_URL" \
  --set deploymentStrategy.type=Recreate
wait_obot
check_health
echo
echo "Errors logged by the upgraded replicas (startup races such as leader election and"
echo "'cache is not started' are expected; anything else is worth a look):"
kubectl -n "$NS" logs -l app.kubernetes.io/name=obot --prefix --tail=-1 \
  | grep -E '"severity":"(ERROR|CRITICAL)"|"level":"(error|fatal|panic)"|panic:' \
  | grep -v -E 'cache is not started|lease lock|already exists' | cut -c1-300 | head -30 || true
print_k8s_access
