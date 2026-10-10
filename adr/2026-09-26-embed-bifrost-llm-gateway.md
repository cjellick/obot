# 2026-09-26: Embed Bifrost as Obot's LLM gateway engine

- **Status:** Accepted
- **Date:** 2026-09-26
- **Supersedes:** None
- **Superseded by:** None

## Related issues

None yet.

## Related ODPs

- 2026-09-27: Unified LLM gateway, budgets and guardrails — the design this decision implements. It is to be proposed in obot-design-proposals; a draft is at [`design-proposals/2026-09-27-llm-gateway-budgets-guardrails.md`](../design-proposals/2026-09-27-llm-gateway-budgets-guardrails.md).

## Context

Obot's LLM gateway was a set of per-provider reverse proxies under `/api/llm-proxy/<provider>`, each with its own credential handling, URL construction and response parsing. Every new provider needed new proxy code, and every route served only one API dialect. The gateway had none of the features that dedicated gateways offer: load balancing across keys, retries and fallbacks, spend budgets, rate limits, routing rules, caching, and request analytics.

[Bifrost](https://github.com/maximhq/bifrost) is an Apache-2.0 Go gateway that provides these features across 20+ providers and translates between the OpenAI Chat Completions, OpenAI Responses and Anthropic Messages APIs. It can run as a standalone service or be imported as Go modules.

## Decision

Obot imports Bifrost's HTTP transport (`github.com/maximhq/bifrost/transports`) and runs it inside the Obot process (`pkg/bifrost`).

- **No network listener.** Bifrost's fasthttp server is served over an in-memory listener. Obot reaches it through an `http.RoundTripper` that dials that listener, so nothing outside Obot can talk to Bifrost directly. Bifrost's own auth and virtual-key enforcement are therefore off.
- **Obot stays the policy layer.** `/api/llm-proxy/v1/{path}` (the root of the LLM proxy; per-provider routes keep their own prefixes) reuses the existing LLM proxy pipeline (`llmProviderProxy`) with a Bifrost backend. Obot still authenticates the caller and checks model access policies, including for every `fallbacks` model a request names. It also enforces budgets, and records its LLM audit log and token usage. Guardrails run inside the gateway as Obot plugins (see [the guardrails ADR](2026-09-26-guardrails.md)). Bifrost then does routing, key selection, retries, fallbacks, translation, governance, logging and caching.
- **One built-in model provider.** The `llm-gateway-model-provider` ModelProvider represents the gateway, with no daemon and no credential (like the local auth provider). Its models are listed from Bifrost as `provider/model` and synced into `Model` resources, with Bifrost's pricing projected into `ModelInfo`. Existing features that key on Models work without special cases, including access policies, default aliases, nanobot and hosted agents, the MCP tester and message policies.
- **Obot enforces budgets.** Budgets (`llm_gateway_budgets`) cap spend, tokens or requests over rolling or calendar windows. Each budget is either per user or one shared pool, optionally limited to certain models, and is assigned to users, groups or everyone. Before forwarding, Obot checks every applicable budget against usage recorded in `llm_gateway_budget_usage`, and it records each request's usage against the budgets it was admitted under. This replaces the daily token quota for gateway traffic; the configured quota is migrated once into a default budget. Obot also sends the caller's identity in `x-bf-lh-*` headers for Bifrost's request logs, and a per-user cache key.
- **Bifrost keeps its own configuration.** Providers, keys, virtual keys, routing rules, logs and plugin settings live in Bifrost's config and log stores. These are SQLite files in the Obot data directory when Obot uses SQLite. When Obot uses Postgres, they live in a `<database>_llm_gateway` database on the same server, which Obot creates if needed; `--llm-gateway-postgres-dsn` overrides this. Bifrost's table names collide with Obot's, so the stores cannot share Obot's schema.
- **Obot UI for everything.** Admin pages under `/llm-gateway` use a guarded pass-through, `/api/llm-gateway/admin/*` to Bifrost's `/api/*`. It exposes only providers, models, governance, logs, config, plugins, cache, routing and pricing. Bifrost's MCP, skills and prompt-repository APIs are not exposed, and Bifrost's own UI is not served.
- **Bifrost is not part of Obot's external interface.** No path, provider or model ID names it. The gateway strips Bifrost's response headers (which name upstream keys) and its `extra_fields`/`is_bifrost_error` body fields, from JSON and streaming responses alike. Client tuning headers use Obot names (`X-Obot-Cache-*`), and metrics are republished as `obot_llm_gateway_*`. That keeps the engine replaceable.
- **Migration.** On first boot with no gateway providers, the credentials of configured legacy model providers are imported into Bifrost. The same import is available as an explicit action. The legacy per-provider routes remain in place.

## Rationale

- **Budgets in Obot, not Bifrost virtual keys.** Open-source Bifrost keeps governance counters in memory per process and writes them to the database every 10 seconds with last-writer-wins, never folding in other replicas. Cross-replica synchronization is an Enterprise feature. With several Obot replicas a budget could be overspent up to about N times, and Bifrost's stored usage would undercount. Its per-user limits are also Enterprise-only; open-source limits attach to virtual keys, teams and customers. Obot already records exact per-request usage in its shared database, so enforcing budgets there is accurate across replicas, keys on Obot identities (including hosted agents, which spend their owner's budgets), and supports per-user and shared pools.
- **Egress proxy.** Bifrost's HTTP client ignores `HTTPS_PROXY`, so Obot copies the environment's proxy onto each provider (honoring `NO_PROXY`) through Bifrost's per-provider proxy setting.

- **Embedding versus a sidecar.** Embedding avoids deploying, securing and versioning a second service. Running Bifrost's full transport rather than only its `core` library brings its governance, logging, routing and caching plugins and its management API, all of which would otherwise need reimplementing.
- **Obot as the policy front, not a Bifrost plugin.** Obot's authentication, access policies, message policies and audit log already exist and are tied to Obot's data model. Keeping them in front of Bifrost preserves them unchanged and keeps one enforcement point for every Obot principal type (users, API keys, hosted agents).
- **One synthetic provider.** Registering one provider with provider-qualified model IDs, instead of one ModelProvider per Bifrost provider, keeps Bifrost the source of truth for provider configuration. It avoids a two-way sync.

## Consequences

- Obot's `go.mod` now carries Bifrost's dependency tree. Bifrost's `transports` module is consumed by pseudo-version because its v2 tags don't use a `/v2` module path. Upgrading Bifrost is a dependency bump, but its internal APIs (`BifrostHTTPServer`, `lib.Config`) are not a stable contract.
- Each Obot replica runs its own Bifrost, and the replicas share configuration and logs through the Postgres stores. Open-source Bifrost loads its configuration into memory at startup and only updates that copy for changes made through it. So, following the pattern used for auth and model provider daemons ([2026-09-01 ADR](2026-09-01-provider-daemon-sync.md)), every gateway configuration change advances an `llm-gateway` revision in the `ProviderSync` singleton. A controller on every replica then rebuilds that replica's gateway from the stores. New requests go to the rebuilt gateway; requests already in flight finish on the old one, which stops after up to 15 minutes. The replica that made the change does not reload unless it had missed an earlier one.
- Bifrost's own virtual-key budgets are not used, because they don't aggregate across replicas. With SQLite, which is single-replica only, each replica would have separate state.
- When no encryption key is configured, the generated key is stored in Obot's database for Postgres deployments, so every replica uses the same one. For SQLite deployments it is stored in the data dir.
- Routing rules can send a request to a model other than the one the user was authorized for; that is an administrator decision. Obot authorizes the requested model and fallbacks, not the routing target.
- Only endpoints Obot can meter and guard are exposed: Chat Completions, Responses, Messages, embeddings and model listing. Images, audio, rerank and batch APIs need metering before they can be exposed.
- Legacy per-provider routes and model providers still exist. Removing them is follow-up work once agents and clients have moved to the gateway.

## References

- `pkg/bifrost/`: engine, model listing, legacy import, virtual-key lookup, Postgres database setup
- `pkg/gateway/server/llmproxy_bifrost.go`: gateway backend
- `pkg/api/handlers/llmgateway.go`: management API
- `ui/user/src/routes/llm-gateway/`: admin UI
- [Bifrost documentation](https://docs.getbifrost.ai)
