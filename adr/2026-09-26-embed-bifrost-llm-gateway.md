# 2026-09-26: Embed Bifrost as Obot's LLM gateway engine

- **Status:** Accepted
- **Date:** 2026-09-26
- **Supersedes:** None
- **Superseded by:** None

## Related issues

None yet.

## Related ODPs

None. This is a prototype; an ODP should be written before it ships.

## Context

Obot's LLM gateway was a set of per-provider reverse proxies under `/api/llm-proxy/<provider>`, each with its own credential handling, URL construction and response parsing. Every new provider needed new proxy code, and every route served only one API dialect. The gateway had none of the features that dedicated gateways offer: load balancing across keys, retries and fallbacks, spend budgets, rate limits, routing rules, caching, and request analytics.

[Bifrost](https://github.com/maximhq/bifrost) is an Apache-2.0 Go gateway that provides these features across 20+ providers and translates between the OpenAI Chat Completions, OpenAI Responses and Anthropic Messages APIs. It can run as a standalone service or be imported as Go modules.

## Decision

Obot imports Bifrost's HTTP transport (`github.com/maximhq/bifrost/transports`) and runs it inside the Obot process (`pkg/bifrost`).

- **No network listener.** Bifrost's fasthttp server is served over an in-memory listener. Obot reaches it through an `http.RoundTripper` that dials that listener, so nothing outside Obot can talk to Bifrost directly. Bifrost's own auth and virtual-key enforcement are therefore off.
- **Obot stays the policy layer.** `/api/llm-proxy/v1/{path}` (the root of the LLM proxy; per-provider routes keep their own prefixes) reuses the existing LLM proxy pipeline (`llmProviderProxy`) with a Bifrost backend. Obot still authenticates the caller and checks model access policies, including for every `fallbacks` model a request names. It also enforces daily token quotas and message policies, and records its LLM audit log and token usage. Bifrost then does routing, key selection, retries, fallbacks, translation, governance, logging and caching.
- **One built-in model provider.** The `llm-gateway-model-provider` ModelProvider represents the gateway, with no daemon and no credential (like the local auth provider). Its models are listed from Bifrost as `provider/model` and synced into `Model` resources, with Bifrost's pricing projected into `ModelInfo`. Existing features that key on Models work without special cases, including access policies, default aliases, nanobot and hosted agents, the MCP tester and message policies.
- **Budgets map Obot identity to Bifrost virtual keys.** Obot stores budget bindings (user, group or everyone → virtual key) in its own database, `llm_gateway_budget_bindings`. On each request it attaches the most specific matching key's value, so that key's budgets, rate limits and provider/model allowlists apply. Obot also sends the caller's identity in `x-bf-lh-*` headers for Bifrost's request logs, and a per-user cache key.
- **Bifrost keeps its own configuration.** Providers, keys, virtual keys, routing rules, logs and plugin settings live in Bifrost's config and log stores. These are SQLite files in the Obot data directory when Obot uses SQLite. When Obot uses Postgres, they live in a `<database>_llm_gateway` database on the same server, which Obot creates if needed; `--llm-gateway-postgres-dsn` overrides this. Bifrost's table names collide with Obot's, so the stores cannot share Obot's schema.
- **Obot UI for everything.** Admin pages under `/llm-gateway` use a guarded pass-through, `/api/llm-gateway/admin/*` to Bifrost's `/api/*`. It exposes only providers, models, governance, logs, config, plugins, cache, routing and pricing. Bifrost's MCP, skills and prompt-repository APIs are not exposed, and Bifrost's own UI is not served.
- **Bifrost is not part of Obot's external interface.** No path, provider or model ID names it. The gateway strips Bifrost's response headers (which name upstream keys) and its `extra_fields`/`is_bifrost_error` body fields, from JSON and streaming responses alike. Client tuning headers use Obot names (`X-Obot-Cache-*`), and metrics are republished as `obot_llm_gateway_*`. That keeps the engine replaceable.
- **Migration.** On first boot with no gateway providers, the credentials of configured legacy model providers are imported into Bifrost. The same import is available as an explicit action. The legacy per-provider routes remain in place.

## Rationale

- **Embedding versus a sidecar.** Embedding avoids deploying, securing and versioning a second service. Running Bifrost's full transport rather than only its `core` library brings its governance, logging, routing and caching plugins and its management API, all of which would otherwise need reimplementing.
- **Obot as the policy front, not a Bifrost plugin.** Obot's authentication, access policies, message policies and audit log already exist and are tied to Obot's data model. Keeping them in front of Bifrost preserves them unchanged and keeps one enforcement point for every Obot principal type (users, API keys, hosted agents).
- **One synthetic provider.** Registering one provider with provider-qualified model IDs, instead of one ModelProvider per Bifrost provider, keeps Bifrost the source of truth for provider configuration. It avoids a two-way sync.

## Consequences

- Obot's `go.mod` now carries Bifrost's dependency tree. Bifrost's `transports` module is consumed by pseudo-version because its v2 tags don't use a `/v2` module path. Upgrading Bifrost is a dependency bump, but its internal APIs (`BifrostHTTPServer`, `lib.Config`) are not a stable contract.
- Each Obot replica runs its own Bifrost. The replicas share state through the Postgres stores, but in-memory state such as rate-limit counters is per replica until Bifrost's cluster mode is wired up. With SQLite, which is single-replica only, each replica would have separate state. The generated encryption key lives in the data dir. Multi-replica deployments must set `--llm-gateway-encryption-key` so every replica can decrypt provider keys.
- Routing rules can send a request to a model other than the one the user was authorized for; that is an administrator decision. Obot authorizes the requested model and fallbacks, not the routing target.
- Only endpoints Obot can meter and screen are exposed: Chat Completions, Responses, Messages, embeddings and model listing. Tool-call message policies are evaluated only on the Messages and Responses formats, so Chat Completions requests are refused for callers those policies apply to. Images, audio, rerank and batch APIs need metering before they can be exposed.
- Legacy per-provider routes and model providers still exist. Removing them is follow-up work once agents and clients have moved to the gateway.

## References

- `pkg/bifrost/`: engine, model listing, legacy import, virtual-key lookup, Postgres database setup
- `pkg/gateway/server/llmproxy_bifrost.go`: gateway backend
- `pkg/api/handlers/llmgateway.go`: management API
- `ui/user/src/routes/llm-gateway/`: admin UI
- [Bifrost documentation](https://docs.getbifrost.ai)
