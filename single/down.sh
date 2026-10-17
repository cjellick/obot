#!/usr/bin/env bash
# Stops the single-node container. --wipe also deletes its data and the MCP server containers
# it started, so the next up.sh is a fresh install.
set -euo pipefail
KIT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
NAME="${OBOT_CONTAINER:-obot-qa}"

docker rm -f "$NAME" >/dev/null 2>&1 || true
if [ "${1:-}" = "--wipe" ]; then
  ids="$(docker ps -aq --filter label=mcp.server.id)"
  # shellcheck disable=SC2086 # one id per word
  if [ -n "$ids" ]; then docker rm -f $ids >/dev/null; fi
  docker volume rm obot-qa-data >/dev/null 2>&1 || true
  rm -f "$KIT_DIR/.state/setup-token-single" "$KIT_DIR"/.state/cookies-localhost_"${OBOT_PORT:-8080}".txt
  echo "wiped: container, obot-qa-data volume, MCP server containers, kit state"
fi
