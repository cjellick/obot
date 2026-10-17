# QA kit: Obot LLM gateway PR

Everything needed to test [obot-platform/obot#8244](https://github.com/obot-platform/obot/pull/8244)
before it and the upgrade-server PR merge. Follow the test plan:
https://github.com/obot-platform/obot/issues/8248#issuecomment-6235058718

| Path | What |
| --- | --- |
| `model-data/` | The gateway's model prices and capabilities (converted from LiteLLM, MIT, see `LICENSE.litellm`), served from this branch until upgrade-server serves them |
| `qa.env.example` | Names of every credential the kit uses (no values) |
| `single/` | One container, SQLite, Docker socket mounted for MCP servers |
| `k8s/` | kind + in-cluster Postgres + 2 Obot replicas from the PR's chart |
| `k8s/upgrade/` | Latest release on kind, then `helm upgrade` to the PR |
| `api-login.sh`, `guardrails.sh` | Log in to the API as the owner; create guardrails from the credentials file |

## Prerequisites

Docker, `git`, `curl`, `jq`, `openssl`; for Kubernetes also `kind`, `kubectl`, `helm` (v3).
About 15 GB free disk for the image build (prune the build cache afterwards:
`docker builder prune -af`).

## 1. Build the PR image

```bash
git clone -b bifrost-llm-gateway https://github.com/cjellick/obot.git obot-pr
git clone -b qa-kit https://github.com/cjellick/obot.git qa-kit
cd obot-pr
docker build \
  --build-arg MODEL_DATA_URL=https://raw.githubusercontent.com/cjellick/obot/qa-kit/model-data \
  -t obot:llm-gateway .
```

`MODEL_DATA_URL` bundles the model prices into the binary; the build fails if they can't be
downloaded. At runtime the kit points `OBOT_SERVER_LLM_GATEWAY_MODEL_DATA_URL` at the same
URL, so the 6-hourly refresh works too. Check it loaded: the files exist in
`/data/.local/share/obot/llm-gateway/model-data/` (`pricing.json`, `model-parameters.json`)
and no `failed to refresh the LLM gateway's model prices` warning is logged.

Override defaults in your shell if needed: `OBOT_IMAGE` (default `obot:llm-gateway`),
`OBOT_MODEL_DATA_URL`.

## 2. Credentials

The product owner keeps the real values in a file outside any repo (chmod 600). You get its
**path**: `export QA_ENV_FILE=/path/to/qa.env`.

- **Never print the file or its values.** No `cat`, `grep`, `echo`, `env`, `set`, `set -x`,
  `printenv` or `docker inspect` on it or on what it sets. To see what's in it, read
  `qa.env.example` (same names, no values).
- Use it only by path. The kit passes it to `docker run --env-file`, to
  `kubectl create secret --from-env-file`, and to scripts that `set -a; . "$QA_ENV_FILE"; set +a`
  and reference `"$VAR"` / `env.VAR` without echoing.
- Never put keys in UI forms, issues, logs, screenshots or reports. Create guardrails with
  `guardrails.sh` (below), not the UI.
- `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are read by Obot itself: at startup it configures
  the gateway's `openai` and `anthropic` providers with them (and updates them when they
  change). No UI entry needed.
- The owner is bootstrapped with Local auth from `OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_EMAIL`.
  The kit generates the one-time setup token (kept in `.state/`, git-ignored) and passes it as
  `OBOT_SERVER_LOCAL_AUTH_INITIAL_OWNER_SETUP_TOKEN`; bootstrap-token login is then disabled.
  Either open the activation link the scripts print and choose a password, or run
  `./api-login.sh <url> <single|k8s>`, which activates the owner with
  `OBOT_QA_OWNER_PASSWORD` and saves an API session cookie.
- The **Claude subscription** test needs a human to log in to Claude Code; an agent can't do it.

### API access and guardrails

```bash
./api-login.sh http://localhost:8080 single         # or k8s
JAR="$(./api-login.sh --jar http://localhost:8080)"
curl -s -b "$JAR" http://localhost:8080/api/me | jq '{email, role}'
./guardrails.sh http://localhost:8080               # one guardrail per integration with credentials
```

`guardrails.sh` creates `QA Lakera`, `QA Presidio`, `QA Azure Language PII`,
`QA Azure Content Safety`, `QA AWS Bedrock`, `QA Google Model Armor`, `QA Patronus` and
`QA CrowdStrike AIDR` for whichever credentials are present, and prints only ids and
`configuredSecrets`. Then apply them to a profile in the UI (**LLM Gateway > Profiles >
Add Guardrail**); that needs no keys. To create one by hand, build the body from the
environment so the key never appears on a command line or in output:

```bash
set -a; . "$QA_ENV_FILE"; set +a
jq -n '{name: "Lakera", type: "lakera", config: {apiKey: env.LAKERA_API_KEY}}' \
  | curl -s -b "$JAR" -H 'Content-Type: application/json' --data @- \
      http://localhost:8080/api/llm-gateway/guardrails | jq '{id, name, configuredSecrets}'
```

The API is documented in the PR at `docs/docs/functionality/guardrails.md` (section API);
field names per type are in `pkg/guardrails/provider_*.go`. Bedrock and Model Armor
credentials use kit-specific names (`BEDROCK_GUARDRAIL_*`, `GOOGLE_MODEL_ARMOR_SA_KEY_FILE`)
so Obot's own AWS/Google SDK identity is never changed by the file.

### Other roles

Create the auditor, power user and basic user in the UI as the owner: **Modify** on the
Local auth provider card creates users (or `POST /api/local-auth/users` with
`{email, password, requirePasswordChange}`; the email domain must be one the Local provider
allows). Have each sign in once (they set a new password unless `requirePasswordChange` was
false), then set the role on the Users page. Keep their passwords out of reports.

## 3. Single node (SQLite)

```bash
./single/up.sh                 # http://localhost:8080 (OBOT_PORT=... to change)
docker logs -f obot-qa
./single/down.sh               # stop, keep data
./single/down.sh --wipe        # stop and delete data, MCP containers and kit state (reset)
```

## 4. HA on Kubernetes (kind)

```bash
./k8s/up.sh ../obot-pr/chart   # kind cluster obot-qa, Postgres, 2 replicas, prints access
export KUBECONFIG="$PWD/.state/kubeconfig"
kubectl -n obot port-forward svc/obot 8080:80
./k8s/down.sh                  # delete the cluster and kit state (reset)
```

Not production grade: one Postgres pod (user `obot`, a superuser, so Obot can create the
gateway's `obot_llm_gateway` database), no encryption provider, no persistence for `/data`.
After startup the scripts check that the gateway started on every replica (see Known issues).

## 5. Upgrade test (Kubernetes)

```bash
./k8s/down.sh                                   # start from nothing
./k8s/upgrade/install-release.sh                # latest release, 2 replicas, Postgres
```

Defaults: `OBOT_OLD_VERSION=v0.26.3` (latest release; its image is amd64 only, so on arm64
use `OBOT_OLD_VERSION=v0.26.2`), message policies enabled
(`OBOT_SERVER_ENABLE_MESSAGE_POLICIES=true`), daily per-user token limits
`OBOT_OLD_DAILY_INPUT_TOKEN_LIMIT=50000` / `OBOT_OLD_DAILY_OUTPUT_TOKEN_LIMIT=20000`.

Before upgrading, sign in to the old release (port-forward as above) and configure what the
upgrade has to carry over (expected results: `docs/docs/llm-gateway/upgrade.md` in the PR):

- Model providers: OpenAI/Anthropic come from the env keys; add others you have
  credentials for (Bedrock, Azure, Generic Responses Compatible) on the Model Providers page.
- Default model aliases, a deactivated model, a model alias or cost override.
- Model access policies (users, groups, models, a wildcard).
- Message policies, one for everyone and one for specific users/groups.
- Some chat/LLM usage so token usage and audit logs exist.

Then:

```bash
./k8s/upgrade/upgrade.sh ../obot-pr/chart
```

It backs up every database first (`.state/backup-<time>.sql`, via
`kubectl -n obot exec postgres-0 -- pg_dumpall -U obot`), loads the PR image, and runs
`helm upgrade` with the PR chart and `deploymentStrategy.type=Recreate`, so both old replicas
stop and both new replicas start on the upgraded database at the same time. It then waits
for both to be Ready, checks `/api/healthz` through the Service, checks the gateway on each
replica, and lists error log lines.

## Known issues (found while building this kit)

- **Startup races (fixed in the PR).** Replicas starting together used to race creating the
  gateway's database (`SQLSTATE 23505`, leaving one replica's gateway down) and the gateway's
  model provider (a replica exiting with `... "llm-gateway-model-provider" already exists`).
  Both are fixed. The scripts still check every replica: if one prints
  `!! ... the LLM gateway failed to start` or restarts, file it as a bug with its logs.
- Released versions can crash-loop once on a fresh 2-replica install
  (`pg_type_typname_nsp_index` duplicate key); they recover on restart.
- Expected startup noise on 2 replicas: leader election lease errors, `cache is not started`,
  one failed tunnel peer connection, MDM asset download failures without internet.
