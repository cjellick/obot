# 2026-09-26: Guardrails as Obot-owned checks in the LLM gateway and MCP gateway

- **Status:** Accepted
- **Date:** 2026-09-26
- **Supersedes:** None
- **Superseded by:** None

## Related issues

None yet.

## Related ODPs

None. This is a prototype; an ODP should be written before it ships.

## Context

Bifrost, the engine embedded as Obot's LLM gateway ([ADR](2026-09-26-embed-bifrost-llm-gateway.md)), has guardrails only in its Enterprise edition. They cover rules with CEL conditions, sampling, profiles for external services (AWS Bedrock Guardrails, Azure Content Safety, Google Model Armor, Lakera, Patronus, CrowdStrike and others), regex and secrets detection, and actions to block, redact or log. Obot already had message policies: natural-language rules checked by an LLM judge in its own LLM proxy. Obot's MCP gateway had configurable MCP hooks but nothing that inspects content.

## Decision

Obot implements guardrails itself (`pkg/guardrails`) and applies the same rules to LLM gateway traffic and to MCP tool calls.

- **Data model.** Rules and profiles live in Obot's database (`guardrail_rules`, `guardrail_profiles`). Profile credentials are kept in the encrypted credential store and are write-only through the API. Findings are recorded in `guardrail_events`, without the offending content, and follow the LLM audit log's retention.
- **Checkers are pluggable.** Each profile type registers a checker that reports findings and, where it can, the spans to redact. There are three built-in checkers (secrets, regex and PII, and an LLM judge) and integrations with eight external services. A redact rule whose checker can't locate a finding blocks instead.
- **LLM gateway: two Bifrost plugins.** An input plugin is registered before Bifrost's built-in plugins and an output plugin after them, so Bifrost's request logging only ever sees content after it has been checked.
  - **Streams.** The output plugin holds a stream while any rule that can change it applies. It blanks the deltas as they pass through logging, checks the complete response, then rewrites the held chunks, either redacted or ended with an error.
  - **Identity.** Obot's proxy passes the caller's identity to the plugins through a per-request token that maps to an in-process registry. It is not sent in a header anyone can forge.
- **MCP gateway: after hooks.** Guardrails run in the MCP proxy's hook processor after the configured hooks, on `tools/call` arguments and results. Their outcomes appear in the MCP audit log like hook results.
- **Message policies are folded in.** A message policy is an LLM judge profile used by a blocking rule. Existing policies are converted once at startup. The legacy per-provider LLM routes read judge rules from the guardrail tables. The message policy API becomes read-only.

## Rationale

- **Build rather than buy Enterprise.** The rule and profile model is small. The value is in the integrations and the enforcement points, and Obot needs those in the MCP gateway too, which Bifrost doesn't cover.
- **Plugins inside Bifrost rather than checks in Obot's proxy.** Only inside Bifrost can input be redacted before request logging and before fallbacks, retries and caching see it, and streamed output be held and rewritten in Bifrost's own chunk format for every API dialect.
- **One engine for both gateways.** Rules, conditions, events and the UI are shared, so an administrator configures, for example, secrets redaction once for models and tools.

## Consequences

- Rules can block or redact streamed responses only by holding them, which delays the first token until the response is complete. Log-only rules don't hold streams.
- External checkers add latency and cost to every guarded request. Sampling, conditions and fail-open (the default) limit the impact. Their request and response formats were written from vendor documentation and tested against mocks, not live services.
- The plugins depend on Bifrost's plugin ordering and stream-pausing APIs, which are not a stable contract.
- MessagePolicy resources remain but are unused. Removing them, and the message policy violation records, is follow-up work.

## References

- `pkg/guardrails/`: engine, checkers, LLM gateway plugins, MCP checks, message policy conversion
- `pkg/api/handlers/mcpgateway/guardrails.go`: MCP gateway integration
- `pkg/api/handlers/guardrails.go`: API
- `ui/user/src/routes/llm-gateway/guardrails/`: admin UI
- [Bifrost Enterprise guardrails](https://docs.getbifrost.ai/enterprise/guardrails)
