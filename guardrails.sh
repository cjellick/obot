#!/usr/bin/env bash
# Creates a guardrail for each integration whose credentials are in $QA_ENV_FILE, through the
# API, so keys never go into a UI form. Run ./api-login.sh first. Prints only names, types,
# ids and which credentials are configured.
#   ./guardrails.sh http://localhost:8080
# Then apply the guardrails to a profile in the UI (LLM Gateway > Profiles > Add Guardrail).
set -euo pipefail
BASE="${1:?usage: guardrails.sh <obot-url>}"
KIT_TARGET=none
. "$(dirname "$0")/common.sh"
JAR="$("$(dirname "$0")/api-login.sh" --jar "$BASE")"
[ -s "$JAR" ] || { echo "run ./api-login.sh $BASE first" >&2; exit 1; }

set -a; . "$QA_ENV_FILE"; set +a

# create <jq filter building the body from env.*>; values never appear on a command line.
create() {
  local body
  body="$(jq -n "$1")"
  printf '%s' "$body" | curl -sS -b "$JAR" -H 'Content-Type: application/json' --data @- \
    "$BASE/api/llm-gateway/guardrails" | jq -c 'if .id then {id, name, type, configuredSecrets} else . end'
}
set_() { [ -n "${!1:-}" ]; }

set_ LAKERA_API_KEY && create '{name: "QA Lakera", type: "lakera", config: {apiKey: env.LAKERA_API_KEY}}'
set_ PRESIDIO_ANALYZER_URL && create '{name: "QA Presidio", type: "presidio", config: {analyzerURL: env.PRESIDIO_ANALYZER_URL}}'
set_ AZURE_LANGUAGE_KEY && create '{name: "QA Azure Language PII", type: "azure-language-pii",
  config: {endpoint: env.AZURE_LANGUAGE_ENDPOINT, apiKey: env.AZURE_LANGUAGE_KEY}}'
set_ AZURE_CONTENT_SAFETY_KEY && create '{name: "QA Azure Content Safety", type: "azure-content-safety",
  config: {endpoint: env.AZURE_CONTENT_SAFETY_ENDPOINT, apiKey: env.AZURE_CONTENT_SAFETY_KEY, promptShields: true}}'
set_ BEDROCK_GUARDRAIL_ID && create '{name: "QA AWS Bedrock", type: "aws-bedrock",
  config: ({region: env.BEDROCK_GUARDRAIL_REGION, guardrailId: env.BEDROCK_GUARDRAIL_ID,
    guardrailVersion: (env.BEDROCK_GUARDRAIL_VERSION // "" | if . == "" then "DRAFT" else . end),
    accessKeyId: env.BEDROCK_GUARDRAIL_AWS_ACCESS_KEY_ID, secretAccessKey: env.BEDROCK_GUARDRAIL_AWS_SECRET_ACCESS_KEY,
    sessionToken: env.BEDROCK_GUARDRAIL_AWS_SESSION_TOKEN} | with_entries(select(.value != null and .value != "")))}'
if set_ GOOGLE_MODEL_ARMOR_TEMPLATE; then
  if set_ GOOGLE_MODEL_ARMOR_SA_KEY_FILE; then
    GOOGLE_MODEL_ARMOR_SA_JSON="$(cat "$GOOGLE_MODEL_ARMOR_SA_KEY_FILE")"
    export GOOGLE_MODEL_ARMOR_SA_JSON
  fi
  create '{name: "QA Google Model Armor", type: "google-model-armor",
    config: ({project: env.GOOGLE_MODEL_ARMOR_PROJECT, location: env.GOOGLE_MODEL_ARMOR_LOCATION,
      template: env.GOOGLE_MODEL_ARMOR_TEMPLATE, serviceAccountJSON: env.GOOGLE_MODEL_ARMOR_SA_JSON}
      | with_entries(select(.value != null and .value != "")))}'
fi
set_ PATRONUS_API_KEY && create '{name: "QA Patronus", type: "patronus",
  config: {apiKey: env.PATRONUS_API_KEY, evaluators: ["judge:patronus:is-harmful-advice"]}}'
set_ CROWDSTRIKE_AIDR_API_TOKEN && create '{name: "QA CrowdStrike AIDR", type: "crowdstrike-aidr",
  config: ({apiToken: env.CROWDSTRIKE_AIDR_API_TOKEN, baseURL: env.CROWDSTRIKE_AIDR_BASE_URL}
    | with_entries(select(.value != null and .value != "")))}'
echo "done; list them with: curl -s -b \"$JAR\" $BASE/api/llm-gateway/guardrails | jq '.items[] | {id,name,type}'"
