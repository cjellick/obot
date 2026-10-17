#!/usr/bin/env bash
# Single node: one Obot container on SQLite, with the Docker socket mounted so it runs MCP
# servers as sibling containers. Data lives in the obot-qa-data volume.
set -euo pipefail
KIT_TARGET=single
. "$(dirname "$0")/../common.sh"

NAME="${OBOT_CONTAINER:-obot-qa}"
PORT="${OBOT_PORT:-8080}"

if docker container inspect "$NAME" >/dev/null 2>&1; then
  echo "container $NAME already exists; run single/down.sh first (--wipe to start over)" >&2
  exit 1
fi

owner_args=()
if has_var OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL; then
  owner_args=(-e "OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_SETUP_TOKEN=$(setup_token)")
fi

# The image starts an embedded Postgres unless OBOT_SERVER_DSN is set; point it at SQLite.
docker run -d \
  --name "$NAME" \
  --env-file "$QA_ENV_FILE" \
  -v obot-qa-data:/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p "$PORT:8080" \
  --add-host=host.docker.internal:host-gateway \
  -e OBOT_SERVER_HOSTNAME="localhost:$PORT" \
  -e 'OBOT_SERVER_DSN=sqlite://file:/data/obot.db?_journal=WAL&cache=shared&_busy_timeout=30000' \
  -e OBOT_SERVER_ENABLE_AUTHENTICATION=true \
  -e OBOT_SERVER_LLM_GATEWAY_MODEL_DATA_URL="$OBOT_MODEL_DATA_URL" \
  -e OBOT_SERVER_LLM_GATEWAY_LOG_LEVEL=info \
  "${owner_args[@]}" \
  "$OBOT_IMAGE" >/dev/null

wait_healthy "http://localhost:$PORT"
print_access "http://localhost:$PORT"
echo "Logs: docker logs -f $NAME"
