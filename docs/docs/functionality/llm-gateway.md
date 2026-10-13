---
title: LLM Gateway
---

## Overview

Obot reaches models through its built-in LLM gateway. The gateway serves every configured model behind one base URL, and clients call it with an **Obot API key** instead of provider credentials. Point an OpenAI- or Anthropic-compatible client, such as [Claude Code](#using-with-claude-code) or [Codex](#using-with-codex), at the gateway and call models by name.

The gateway forwards requests to the upstream provider and enforces per-user access:

- You never handle the provider's real API key. An administrator adds providers and their keys under **Models** → **Providers**, and Obot substitutes the key on each request.
- You can only call models that an administrator has granted you through a [Model Access Policy](./model-access-policies.md).
- The model list returned to your client (`/v1/models`) is scoped to the models you are allowed to use.

Obot's own features, such as hosted agents and LLM judge guardrails, use the same gateway.

## Unified gateway

The gateway serves models from OpenAI, Anthropic, Amazon Bedrock, Google Vertex AI and Gemini, Azure, Mistral, Groq, xAI, DeepSeek, OpenRouter, Cohere, vLLM, Ollama and more behind a single base URL:

```
https://<your-obot-host>/api/llm-proxy/v1
```

- **Any model, any API format.** Every model is available in the OpenAI Chat Completions (`/v1/chat/completions`), OpenAI Responses (`/v1/responses`) and Anthropic Messages (`/v1/messages`) formats, so Claude Code can use GPT models and the OpenAI SDK can use Claude.
- **Model names** are `provider/model`, for example `openai/gpt-4.1`, `anthropic/claude-sonnet-4-5` or `bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0`. Bare provider model IDs such as `claude-sonnet-4-5` also work, which is what Claude Code sends by default.
- **Fallbacks.** Add `"fallbacks": ["anthropic/claude-sonnet-4-5"]` to a request to fail over to other models when the primary one errors. Each fallback must also be a model you have access to. See [Routing rules and fallbacks](#routing-rules-and-fallbacks).
- **Endpoints.** `/v1/chat/completions`, `/v1/responses`, `/v1/responses/input_tokens`, `/v1/messages`, `/v1/messages/count_tokens`, `/v1/embeddings` and `/v1/models`.
- **Authentication and access.** Use an Obot API key (`obot login --scope llm --print-token`). You can call only the models granted to you by [Model Access Policies](./model-access-policies.md). [Guardrails](./guardrails.md), the LLM audit log and usage reporting all apply.
- **Budgets.** Usage is limited by the budgets on the **Budgets & Limits** tab of the **Models** page. A request that would exceed a budget gets `429` with the budget's name and when more becomes available.

The **Models** tab of the **Models** page lists the models you can use and shows ready-to-copy configuration for Claude Code, Codex, the OpenAI and Anthropic SDKs and `curl`. Find it under **AI Resources** → **Models** in the sidebar (route `/models`). If you don't have access to any models, contact an administrator to request access through a [Model Access Policy](./model-access-policies.md).

### Administering the gateway

Administrators manage the gateway from these pages:

| Where | What it does |
| --- | --- |
| **Usage** → **Gateway** | Requests, success rate, latency, tokens and cost over time, cost by provider and top models. |
| **Models** → **Providers** | Add upstream providers and their keys, and set the default models. Several keys on one provider are load-balanced by weight, and a key can be limited to specific models. A request moves to another of the provider's keys only when it is retried, so keep retries on for failover: new providers retry twice, with backoff from 500 ms to 5 s. Set base URLs (for self-hosted or proxied endpoints), timeouts, retries with backoff, concurrency and extra headers. Custom providers point at any OpenAI-, Anthropic-, Gemini- or Cohere-compatible endpoint. |
| **Models** → **Budgets & Limits** | Caps on spend (USD), total/input/output tokens or requests over a rolling window (e.g. the last 6 hours) or a calendar window (this hour, day, week or month, UTC). A budget either gives each assigned user their own allowance or is one pool shared by everyone assigned, can be limited to specific models, and can exempt administrators. Assign budgets to users, groups or everyone; every budget that applies to a request must have room. Obot enforces budgets from the usage it records in its own database, so they are exact across Obot replicas. |
| **Models** → **Guardrails** | Rules that block, redact or log sensitive or unwanted content in model requests and responses (including streams), using guardrail detectors, which **Manage Detectors** opens. |
| **Models** → **Guardrails** → **Manage Detectors** | **Detectors** configure the checks that guardrails run, for model rules and for [guardrail filters](./filters.md#guardrail-filters) on MCP traffic, with built-in secrets, PII and LLM-judge checks and integrations with Presidio, Azure AI Language and Content Safety, AWS Bedrock Guardrails, Google Model Armor, Check Point (Lakera), Patronus and CrowdStrike AIDR. See [Guardrails](./guardrails.md). |
| **Operations** → **Guardrail Events** | What guardrails blocked, redacted or logged in model and MCP traffic. |
| **Models** → **Routing** | Rules that match requests with a [CEL](https://github.com/google/cel-spec) expression (over `model`, `provider`, `headers`, ...) and send them to other providers or models, split traffic by weight, or define fallback chains. See [Routing rules and fallbacks](#routing-rules-and-fallbacks). |
| **Models** → **Access Policies** | Control which users and groups can use which models. See [Model Access Policies](./model-access-policies.md). |
| **Audit Logs** → **Gateway Requests** | Every routed request with its user, provider, key, retries, fallbacks, tokens, cost, latency and content. See [Audit Logs and Usage](./audit-logs-and-usage.md). |
| **Models** → **Settings** | Request logging and retention, content logging, queueing behavior, and whether Claude subscriptions may be used. |

Prometheus metrics for the gateway (requests, latency, tokens and cost by provider, model, key and budget) are served at `GET /api/llm-gateway/metrics` (metric names start with `obot_llm_gateway_`) to administrators, auditors and credentials allowed to read `/debug/metrics`.

Models from gateway providers are listed on the **Models** page under the *LLM Gateway* provider and are granted to users with Model Access Policies.

### Default models

Obot features such as hosted agents and LLM judge guardrails use default model aliases instead of specific models. Set them with **Set Default Models** on the **Providers** tab:

| Alias | Used for |
| --- | --- |
| **Language Model (Chat)** (`llm`) | The primary conversational model |
| **Language Model (Chat - Fast)** (`llm-mini`) | Quick responses and screening passes |
| **Vision** (`vision`) | Image understanding |
| **Image Generation** (`image-generation`) | Image generation tools |
| **Text Embedding** (`text-embedding`) | Embeddings |

Obot fills an unset alias with a preferred model as soon as a provider serves one:

- `llm` and `vision`: `openai/gpt-5.4`, then `anthropic/claude-sonnet-4-6`
- `llm-mini`: `openai/gpt-5-mini`, then `anthropic/claude-haiku-4-5`
- `image-generation`: `openai/dall-e-3`
- `text-embedding`: `openai/text-embedding-3-large`

Setting a default model does not grant anyone access to it. A [Model Access Policy](./model-access-policies.md#default-model-aliases) that grants the alias gives access to whichever model the alias points to.

### Provider notes

- **OpenAI and Anthropic keys at startup.** When the server's `OPENAI_API_KEY` or `ANTHROPIC_API_KEY` environment variable is set, Obot configures the gateway's `openai` or `anthropic` provider with that key at startup, and updates the key when the value changes. See [Server Configuration](../configuration/server-configuration.md).
- **Amazon Bedrock.** Add the **AWS Bedrock** provider with a Bedrock API key or an access key ID and secret access key, and the region. Anthropic models on Bedrock require you to submit Anthropic's use case details form in your AWS account first. Until you do, requests can fail with `Model use case details have not been submitted for this account`. See the [AWS model access documentation](https://docs.aws.amazon.com/bedrock/latest/userguide/model-access.html). Model availability depends on region; see the [AWS Bedrock Anthropic model cards](https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards-anthropic.html).
- **Azure.** Add the **Azure OpenAI / AI Foundry** provider with the resource endpoint and either an API key or the tenant ID, client ID and client secret of a Microsoft Entra service principal. Assign the service principal **Cognitive Services User** or **Foundry User** (formerly Azure AI User) on the Foundry resource. **Cognitive Services OpenAI User** can allow OpenAI requests while Anthropic requests fail with `Principal does not have access to API/Operation`. See [Configure keyless authentication with Microsoft Entra ID](https://learn.microsoft.com/en-us/azure/foundry/foundry-models/how-to/configure-entra-id).

  A key with neither uses Obot's own managed or workload identity. Its endpoint must then be an Azure OpenAI or AI Services host (`https://<resource>.openai.azure.com`, `.cognitiveservices.azure.com` or `.services.ai.azure.com`), and it can't request other scopes. Certificate checks can't be turned off, and a custom CA can't be added, on Azure, Vertex AI and Bedrock providers, since they may use Obot's own cloud identity.
- **Ollama, vLLM and other self-hosted models.** Add the built-in **Ollama** or **vLLM** provider with the server URL; they don't need a key. For other OpenAI-compatible services, such as LiteLLM, add a custom OpenAI-compatible provider with its base URL. When Obot runs in Docker and Ollama runs on the host, start Ollama with `OLLAMA_HOST=0.0.0.0` and use `http://host.docker.internal:11434` as the server URL. On Linux, also start the Obot container with `--add-host=host.docker.internal:host-gateway`.

### Server configuration

The gateway is always on. These settings control it:

| Flag / environment variable | Description |
| --- | --- |
| `--llm-gateway-data-dir` / `OBOT_SERVER_LLM_GATEWAY_DATA_DIR` | Where the gateway keeps its configuration (default `$XDG_DATA_HOME/obot/llm-gateway`). |
| `--llm-gateway-postgres-dsn` / `OBOT_SERVER_LLM_GATEWAY_POSTGRES_DSN` | Postgres database for gateway configuration and request logs. Must be a different database from Obot's. When Obot itself uses Postgres and this is unset, Obot creates a `<database>_llm_gateway` database on the same server (falling back to SQLite in the data dir if it can't), so every replica shares the gateway's state. When the configuration changes, every replica reloads it within seconds; requests already in flight finish first. |
| `--llm-gateway-encryption-key` / `OBOT_SERVER_LLM_GATEWAY_ENCRYPTION_KEY` | Key that encrypts provider credentials at rest. When unset, a key is generated: with Postgres it is stored in Obot's database (encrypted with Obot's own encryption configuration, if any) and shared by every replica; with SQLite it is stored in the data dir. |
| `--llm-gateway-log-retention-days` / `OBOT_SERVER_LLM_GATEWAY_LOG_RETENTION_DAYS` | Days of request logs to keep (default 30). |
| `--llm-gateway-log-level` / `OBOT_SERVER_LLM_GATEWAY_LOG_LEVEL` | Log level for the embedded gateway (default `warn`). |
| `--daily-user-input-token-limit` / `--daily-user-output-token-limit` | Seed the *Daily token limit* budget for everyone (administrators exempt) when the gateway first starts. After that, edit or remove the budget on the **Budgets & Limits** tab; changing these settings has no effect. |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` | Obot routes gateway providers through the environment's egress proxy, except endpoints `NO_PROXY` excludes or that are on localhost. A proxy set on a provider explicitly takes precedence. |

## Routing rules and fallbacks

A request can be served by a different model from the one the client asked for:

- A **routing rule** (**Models** → **Routing**) whose condition matches sends the request to its targets, splitting traffic between them by weight. Rules are checked in priority order, lowest number first, and the first match applies. A rule can also list fallbacks.
- A **fallback** is tried when the model before it fails. Fallbacks come from the request's `fallbacks` field or from the routing rule that matched. They aren't tried when the provider rejects the request as invalid (HTTP 400), since the fallback would reject it too.

Whichever model ends up serving a request:

- **The caller must be allowed to use it.** [Model access policies](./model-access-policies.md) (or, for a hosted agent, its configured models) are checked for the requested model, for each fallback in the request before it is sent, and for every model the gateway then tries, including routing rule targets and fallbacks. An attempt on a model the caller may not use is refused with HTTP 403 and isn't sent to the provider. If the request has a fallback the caller may use, it is tried next. Otherwise the caller gets the 403, for example `user does not have permission to use model "anthropic/claude-opus-4-8"`.

  A routing rule therefore can't give anyone a model their access policy doesn't include. To route requests for one model to another, grant the target model to the same users and groups. This also covers rules that match a request header: a client can set any header, but the header can only route it to models it may already use.

- **Its budgets must have room.** Every budget that applies to the caller and the model being tried is checked for each attempt (except attempts a user's [Claude subscription](#claude-code-with-a-claude-subscription) pays for), so a budget limited to a model applies whenever that model serves a request, however the request reached it. An attempt whose budget is exhausted is refused, and the gateway tries the next fallback, if any. If none can serve the request, the caller gets the budget's 429.

- **Usage is recorded against the model that served it.** The LLM audit log, usage reports and budgets record that model, price the request at its rates, and charge the budgets that apply to it. A request that was refused is recorded against the model that was requested.

The gateway's request log (**Audit Logs** → **Gateway Requests**) shows the routing rule that matched and the fallback that served each request.

## Before you begin

To use the gateway you need:

1. **A configured provider.** An administrator must add at least one provider with valid credentials under **Models** → **Providers**.
2. **Model access.** An administrator must grant you access to one or more models through a [Model Access Policy](./model-access-policies.md). The **Models** page shows exactly what you can call.
3. **The Obot CLI.** Install and set up the `obot` CLI to obtain an API key. See [Obot CLI Setup](../installation/cli-setup.md).

## Getting your API key

Your API key for the gateway must include LLM proxy access. Print one with the CLI:

```bash
obot login --url https://obot.example.com --scope llm --print-token
```

:::note
Add `--no-expiration` if you want a key that doesn't expire.
:::

## Quick start with curl

Use the model names shown on the **Models** page. These examples assume you have access to `openai/gpt-5.5` and `anthropic/claude-opus-4-8`.

```bash
export OBOT_API_KEY="$(obot login --url https://obot.example.com --scope llm --print-token)"

# OpenAI Chat Completions, with a fallback
curl https://obot.example.com/api/llm-proxy/v1/chat/completions \
  -H "Authorization: Bearer $OBOT_API_KEY" \
  -H "content-type: application/json" \
  -d '{"model":"openai/gpt-5.5","messages":[{"role":"user","content":"hi"}],"fallbacks":["anthropic/claude-opus-4-8"]}'

# OpenAI Responses
curl https://obot.example.com/api/llm-proxy/v1/responses \
  -H "Authorization: Bearer $OBOT_API_KEY" \
  -H "content-type: application/json" \
  -d '{"model":"openai/gpt-5.5","input":[{"role":"user","content":"hi"}]}'

# Anthropic Messages
curl https://obot.example.com/api/llm-proxy/v1/messages \
  -H "Authorization: Bearer $OBOT_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"anthropic/claude-opus-4-8","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}'

# List the models you can use
curl https://obot.example.com/api/llm-proxy/v1/models \
  -H "Authorization: Bearer $OBOT_API_KEY"
```

Any format works with any model. For example, send a Bedrock or Azure model to `/v1/messages` or `/v1/chat/completions` by its gateway name, such as `bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0`.

## Using with Claude Code

Claude Code can route through the gateway and discover which models you have access to.

```bash
# 1. Point Claude Code at the gateway and authenticate with your Obot API key.
export ANTHROPIC_BASE_URL="https://obot.example.com/api/llm-proxy"
export ANTHROPIC_AUTH_TOKEN="$(obot login --url https://obot.example.com --scope llm --print-token)"

# 2. (Optional) Pick a model. Any gateway model works, including non-Anthropic ones.
export ANTHROPIC_MODEL="anthropic/claude-sonnet-4-5"

# 3. (Optional) Discover the models you have access to at startup.
#    Claude Code queries the gateway's /v1/models endpoint and adds the results
#    to the /model picker. Requires Claude Code v2.1.129 or later.
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1

# 4. Launch Claude Code.
claude
```

Inside Claude Code:

- Run **`/model`** and select an Obot gateway model. Discovered models are labeled **"From gateway"**.
- Run **`/status`** to confirm which authentication method is active.

Claude Code reaches Bedrock and Azure models through the same base URL. Pass the gateway model name, for example `claude --model bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0`. You don't need Claude Code's Bedrock or Foundry modes.

### Claude Code with a Claude subscription

When an administrator turns on **Allow Claude subscriptions** in the **Settings** tab of the **Models** page, users signed in to Claude Code with a Claude Pro, Max, Team or Enterprise subscription can use it through the gateway. Their requests to Claude models are billed to their subscription instead of a provider key configured in Obot. Obot still authenticates the user, enforces model access and guardrails, and records the requests in the audit log and usage reports.

The user keeps their Claude Code login and sends their Obot API key in the `X-Obot-Api-Key` header:

```bash
export ANTHROPIC_BASE_URL="https://obot.example.com/api/llm-proxy"
export ANTHROPIC_CUSTOM_HEADERS="X-Obot-Api-Key: $(obot login --url https://obot.example.com --scope llm --print-token)"
# Don't set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN; they replace the subscription login.
claude
```

The gateway still needs the **Anthropic** provider configured with a working key. It lists Claude models through that key, and Claude Code's model names only resolve once they're listed.

Only requests from Claude Code to Anthropic models use the subscription, and the subscription token is only ever sent to Anthropic:

- It's only forwarded while the Anthropic provider sends requests straight to `https://api.anthropic.com`, with certificate checks on. If an administrator changes the provider's base URL or TLS settings, the token isn't forwarded and requests use Obot's key instead.
- A routing rule or fallback can't send a subscription request to another provider, including a custom provider built on Anthropic's API. That attempt is refused with HTTP 403.
- Requests the user sends to other models use Obot's configured keys.

Budgets don't apply to requests the subscription pays for: they aren't refused when a budget is exhausted, and they don't count toward any budget. Budgets still apply to everything else the user sends.

See the Claude Code documentation for details:

- [LLM gateway configuration](https://code.claude.com/docs/en/llm-gateway)
- [Model configuration](https://code.claude.com/docs/en/model-config) (the `/model` picker and gateway discovery)
- [Authentication](https://code.claude.com/docs/en/authentication) (`/login` and `/logout`)
- [Environment variables](https://code.claude.com/docs/en/env-vars)

## Using with Codex

Codex uses the OpenAI Responses API, which the gateway serves for every model. Define a custom model provider in your Codex config.

1. Add the following to `~/.codex/config.toml`:

   ```toml
   model = "openai/gpt-5.5"
   model_provider = "obot"

   [model_providers.obot]
   name = "Obot LLM Gateway"
   base_url = "https://obot.example.com/api/llm-proxy/v1"
   env_key = "OBOT_API_KEY"
   wire_api = "responses"
   ```

   For a local deployment, use `base_url = "http://localhost:8080/api/llm-proxy/v1"`.

2. Set your API key and start Codex:

   ```bash
   export OBOT_API_KEY="$(obot login --url https://obot.example.com --scope llm --print-token)"
   codex        # Codex CLI
   # or
   codex app    # Codex App
   ```

Set `model` to a model name shown on the **Models** page.

:::note
The provider ID (`obot` above) can be any name except the reserved IDs `openai`, `ollama`, and `lmstudio`. Codex's built-in web search is an OpenAI server-side tool. When you use a model that OpenAI doesn't serve, such as a Bedrock or Azure model, add `web_search = "disabled"` to the config.
:::

See the Codex documentation for details:

- [Configuration reference](https://developers.openai.com/codex/config-reference) (the `[model_providers]` keys)
- [Advanced configuration](https://developers.openai.com/codex/config-advanced) (custom providers and `wire_api`)

## Upgrading from model providers

Earlier versions of Obot reached models through per-provider model providers (OpenAI, Anthropic, Generic Responses Compatible, Amazon Bedrock, Azure and others), each with its own proxy route. These are gone. The gateway is now the only way Obot reaches models, and it can't be turned off.

### What happens on upgrade

The first time the new version starts, Obot moves your configuration to the gateway once:

1. **Credentials are imported.** The credentials of each configured model provider become a gateway provider:

   | Model provider | Gateway provider |
   | --- | --- |
   | OpenAI | `openai` |
   | Anthropic | `anthropic` |
   | Generic Responses Compatible | Custom provider `generic-responses` |
   | Amazon Bedrock, Amazon Bedrock API key | `bedrock` |
   | Azure, Azure Entra | `azure` |

   When both variants of Bedrock or Azure are configured, the second becomes another key of the same gateway provider. A gateway provider that already exists is left alone.

2. **References move to gateway models.** After each gateway model sync, default model aliases and model access policies that named an old model are re-pointed to the gateway model for the same upstream model (`provider/model`). A deactivated state, alias or cost override set on the old model carries over. Wildcard patterns in access policies, such as `claude-haiku-4-5*`, get the provider prefix (`anthropic/claude-haiku-4-5*`) of each provider whose models they matched.

3. **Old models and providers are deleted.** Some old models are kept until the gateway lists them: one that an alias, access policy or hosted agent still names, and one an administrator deactivated or gave an alias or cost override, so those settings carry over. An old provider is deleted once it has no models left. Its stored credentials are kept.

Other model providers, such as Ollama, Groq or vLLM, are not imported, and aliases, access policies and hosted agents that name their models are not re-pointed. Those models stay, but nothing serves them. Before upgrading, note these providers' settings, such as their endpoints. Then add them under **Models** → **Providers**, using the built-in Ollama or vLLM provider or a custom OpenAI-compatible provider, and point the aliases and policies at the new models. See [Provider notes](#provider-notes).

The upgrade can't be undone. Once aliases and policies name gateway models and the old providers are deleted, an earlier version of Obot can't serve them. Back up Obot's database before upgrading, and restore it to roll back.

### Client routes

| Route | Status |
| --- | --- |
| `/api/llm-proxy/v1` | Serves every model in the OpenAI Chat Completions, OpenAI Responses and Anthropic Messages formats. Use this for new setups. |
| `/api/llm-proxy/openai`, `/api/llm-proxy/anthropic` | Still work. They are served by the gateway, so existing Claude Code, Codex and SDK setups keep working. |
| `/api/llm-proxy/generic-responses`, `/api/llm-proxy/aws-bedrock`, `/api/llm-proxy/aws-bedrock-api-key`, `/api/llm-proxy/azure`, `/api/llm-proxy/azure-entra` | Removed. They return HTTP 410. Point these clients at `/api/llm-proxy/v1` and use gateway model names, such as `bedrock/anthropic.claude-sonnet-4-5-20250929-v1:0`. |

Claude Code's Bedrock Mantle and Foundry modes pointed at the removed routes. Switch to the standard setup in [Using with Claude Code](#using-with-claude-code).

### Other changes

- **`--disable-llm-gateway` / `OBOT_SERVER_DISABLE_LLM_GATEWAY` is removed.** The gateway is always on.
- **Daily token limits are replaced by budgets.** `--daily-user-input-token-limit` and `--daily-user-output-token-limit` only seed the *Daily token limit* budget when the gateway first starts. The `/api/users/{id}/remaining-token-usage` endpoint is removed.
- **The model provider configuration API is removed.** The configure, deconfigure, reveal and validate endpoints under `/api/model-providers` no longer exist. Listing and getting model providers still work, and return the gateway.
- **Message policies are LLM judge guardrails.** See [Message Policies](./message-policies.md).

## Limitations

- **Access is policy-bound.** You can only call models an administrator has granted you through a [Model Access Policy](./model-access-policies.md), and `/v1/models` returns only those models. A request for a model you don't have access to is rejected.
- **Send the exact model name.** Use the model name shown on the **Models** page exactly as displayed.
- **Claude Code model discovery.** Gateway model discovery is off by default and requires Claude Code v2.1.129 or later.
- **Audit logs can be exported.** Administrators can create one-time or scheduled exports for LLM gateway audit logs. See [Audit Log Export](../configuration/audit-log-export.md).

## Related topics

- [Model Access Policies](./model-access-policies.md) — grant users access to specific models
- [Guardrails](./guardrails.md) — block, redact or log content in model traffic
- [Obot CLI Setup](../installation/cli-setup.md) — install and configure the `obot` CLI
- [Audit Logs and Usage](./audit-logs-and-usage.md) — monitor token usage
- [Audit Log Export](../configuration/audit-log-export.md) — export LLM gateway audit logs
