#!/usr/bin/env bash
# Logs in to Obot's API as the initial owner and saves the session cookie, so you can curl the
# admin API (see guardrails.sh) without typing anything into the UI:
#   ./api-login.sh http://localhost:8080 single     # or: ... k8s
#   curl -sf -b "$(./api-login.sh --jar http://localhost:8080)" http://localhost:8080/api/me
# On a fresh install it first activates the owner with the kit's setup token and sets the
# password to OBOT_QA_OWNER_PASSWORD from $QA_ENV_FILE. Prints no secret values.
set -euo pipefail

jar_for() { echo "$(cd "$(dirname "$0")" && pwd)/.state/cookies-$(echo "$1" | sed 's#^[a-z]*://##; s#[^A-Za-z0-9]#_#g').txt"; }
if [ "${1:-}" = "--jar" ]; then
  jar_for "$2"
  exit 0
fi

BASE="${1:?usage: api-login.sh <obot-url> <single|k8s>}"
KIT_TARGET="${2:?usage: api-login.sh <obot-url> <single|k8s>}"
. "$(dirname "$0")/common.sh"
JAR="$(jar_for "$BASE")"
(umask 077; : > "$JAR")

if ! has_var OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL || ! has_var OBOT_QA_OWNER_PASSWORD; then
  echo "QA_ENV_FILE needs OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL and OBOT_QA_OWNER_PASSWORD" >&2
  exit 1
fi
set -a; . "$QA_ENV_FILE"; set +a

post_json() { # path, json on stdin
  curl -s -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' --data @- "$BASE$1"
}

# 1. Activate the owner (only works once; later runs get 403 and skip ahead).
if [ -s "$STATE_DIR/setup-token-$KIT_TARGET" ]; then
  code="$(jq -n --rawfile t "$STATE_DIR/setup-token-$KIT_TARGET" '{setupToken: ($t | rtrimstr("\n"))}' | post_json /api/local-auth/activate)"
  if [ "$code" = 200 ]; then
    code="$(jq -n '{password: env.OBOT_QA_OWNER_PASSWORD}' | post_json /api/local-auth/change-password)"
    [ "$code" = 200 ] || { echo "setting the owner's password failed: HTTP $code" >&2; exit 1; }
    echo "activated the owner and set the password from OBOT_QA_OWNER_PASSWORD"
  fi
fi

# 2. Log in with the Local provider, the same way the login form does.
curl -s -o /dev/null -b "$JAR" -c "$JAR" "$BASE/oauth2/start?rd=%2F&obot-auth-provider=default%2Flocal-auth-provider"
curl -s -o /dev/null -b "$JAR" -c "$JAR" \
  --data-urlencode "email=$OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL" \
  --data-urlencode "password@"<(printf '%s' "$OBOT_QA_OWNER_PASSWORD") \
  --data-urlencode "rd=/" "$BASE/oauth2/start"

if curl -sf -b "$JAR" "$BASE/api/me" | jq -e '.email' >/dev/null; then
  echo "logged in as $(curl -sf -b "$JAR" "$BASE/api/me" | jq -r '.email + " (role " + (.role|tostring) + ")"')"
  echo "cookie jar: $JAR"
else
  echo "login failed" >&2
  exit 1
fi
