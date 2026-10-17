# shellcheck shell=bash
# Sourced by the k8s scripts.
KIT_TARGET=k8s
. "$(dirname "${BASH_SOURCE[0]}")/../common.sh"
K8S_DIR="$KIT_DIR/k8s"
CLUSTER="${KIND_CLUSTER:-obot-qa}"
NS="${OBOT_NAMESPACE:-obot}"
RELEASE=obot
DSN="postgres://obot:obot@postgres.$NS.svc:5432/obot?sslmode=disable"
export KUBECONFIG="${KUBECONFIG:-$STATE_DIR/kubeconfig}"

ensure_cluster() {
  if ! kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
    kind create cluster --name "$CLUSTER" --config "$K8S_DIR/kind.yaml" --kubeconfig "$KUBECONFIG" --wait 120s
  fi
  kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}

# load_image IMAGE: copy a locally built image onto the kind node. (Published images, such as
# Postgres and released Obot, are pulled by the node itself.)
load_image() {
  kind load docker-image "$1" --name "$CLUSTER"
}

deploy_postgres() {
  kubectl -n "$NS" apply -f "$K8S_DIR/postgres.yaml" >/dev/null
  kubectl -n "$NS" rollout status statefulset/postgres --timeout=300s
}

# The secret the chart's pods load: everything in $QA_ENV_FILE, plus the DSN and the kit's
# owner setup token. Values are never printed.
make_secret() {
  kubectl -n "$NS" create secret generic obot-qa --from-env-file="$QA_ENV_FILE" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local extra
  extra="$(jq -n --arg dsn "$DSN" '{stringData: {OBOT_SERVER_DSN: $dsn}}')"
  if has_var OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL; then
    extra="$(jq --rawfile t <(setup_token) '.stringData.OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_SETUP_TOKEN = ($t | rtrimstr("\n"))' <<<"$extra")"
  fi
  kubectl -n "$NS" patch secret obot-qa --type merge --patch-file /dev/stdin <<<"$extra" >/dev/null
  echo "secret $NS/obot-qa updated from QA_ENV_FILE"
}

# image_values REPO TAG: helm args for the image.
image_args() {
  local image="$1"
  echo "--set-string image.repository=${image%:*} --set-string image.tag=${image##*:}"
}

wait_obot() {
  kubectl -n "$NS" rollout status deployment/"$RELEASE" --timeout=1200s
  kubectl -n "$NS" get pods -l app.kubernetes.io/name=obot -o wide
}

# check_health: /api/healthz through the Service, via a short-lived port-forward.
check_health() {
  local port="${1:-18081}"
  kubectl -n "$NS" port-forward svc/"$RELEASE" "$port:80" >/dev/null 2>&1 &
  local pf=$!
  sleep 3
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$port/api/healthz")"
  kill "$pf" 2>/dev/null || true
  echo "GET /api/healthz via svc/$RELEASE: $code"
  [ "$code" = 200 ]
  sleep 10
  check_gateway
}

# check_gateway: flags a replica whose embedded LLM gateway failed to start. Known issue in the
# PR: when replicas start together and the <db>_llm_gateway database doesn't exist yet, one can
# lose the CREATE DATABASE race (SQLSTATE 23505) and keep serving with the gateway down. The
# pod stays Ready. Workaround: delete that pod.
check_gateway() {
  local bad=0 p
  for p in $(kubectl -n "$NS" get pods -l app.kubernetes.io/name=obot -o name); do
    if kubectl -n "$NS" logs "$p" | grep -q 'failed to start embedded'; then
      echo "!! $p: the LLM gateway failed to start:" >&2
      kubectl -n "$NS" logs "$p" | grep 'failed to start embedded' | jq -r '.error' >&2
      bad=1
    fi
    if kubectl -n "$NS" logs "$p" --previous >/dev/null 2>&1; then
      echo "!! $p restarted; why its last run exited (record it):" >&2
      kubectl -n "$NS" logs "$p" --previous | grep -E '"message":"(exiting|failed to start embedded)' \
        | jq -r '.error' >&2 || true
    fi
  done
  if [ "$bad" = 0 ]; then
    echo "LLM gateway started on every replica"
  else
    echo "!! Record it (known: concurrent CREATE DATABASE race), then: kubectl -n $NS delete <pod>" >&2
  fi
}

print_k8s_access() {
  echo
  echo "Reach it with:  KUBECONFIG=$KUBECONFIG kubectl -n $NS port-forward svc/$RELEASE 8080:80"
  print_access "http://localhost:8080"
  echo "Logs:           KUBECONFIG=$KUBECONFIG kubectl -n $NS logs -l app.kubernetes.io/name=obot --prefix --tail=-1"
}
