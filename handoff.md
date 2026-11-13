# Handoff: OpenTelemetry for the MCP gateway

> **Remove this file and `hack/otel-demo/` before opening a PR.** They live in
> the last commit on this branch (`chore: handoff notes ...`), so drop that
> commit.

## Goal

Make Obot's MCP gateway emit OpenTelemetry traces and metrics, following the
MCP and GenAI semantic conventions. A call such as
Claude Code → Obot vMCP → component server → upstream should show up as one
joined trace in Jaeger, Aspire or Langfuse, with spans named like
`tools/call <tool>`.

The work spans three repos. Each has a branch named **`otel-mcp-gateway`**,
pushed to the `cjellick` forks:

| Repo | Branch commits | What changed |
| --- | --- | --- |
| obot (`cjellick/obot`) | `912f34c48`, `6f97c2038`, `fcc95ceb9` + this handoff commit | Gateway spans and metrics, filter spans, storage span naming and gating, a fix to the span name of an aborted handler |
| mmmcp (`cjellick/mmmcp`) | `8011058` | Composite (vMCP) server and component-client spans; trace context propagated across the go-sdk |
| nah (`cjellick/nah`) | `c8ce06d` | `WrapTransport` option on the router and runtime clients, so Obot can silence the controllers' storage spans |

None of this has been reviewed or opened as a PR. Per CLAUDE.md, never push to
`obot-platform/obot`, and use no closing keywords.

## Blocking before a PR

1. **Remove the `replace` lines in `go.mod`** (`github.com/obot-platform/mmmcp => ../mmmcp`,
   `github.com/obot-platform/nah => ../nah`, each marked TODO). To do that:
   - land and tag the mmmcp and nah changes,
   - bump both in Obot,
   - run `go mod tidy`.

   To build this branch locally until then, check out mmmcp and nah next to
   `obot/`, both on branch `otel-mcp-gateway`.
2. Add a unit test for nah's `WrapTransport` (there is none yet).
3. Write user docs (`docs/`) covering:
   - how to enable OTel: the standard `OTEL_*` env vars, which `autoexport` reads,
   - the new `--trace-storage-requests` / `OBOT_SERVER_TRACE_STORAGE_REQUESTS` flag,
   - the span and attribute reference.
4. Consider an ADR, since this adds a new observability surface.

## What the Obot change does (13 files, +995/−15; ~600 code, ~390 tests)

- **`pkg/api/handlers/mcpgateway/telemetry.go`** (new). This holds most of the change:
  - `startMCPOperation(req, hop)` peeks the JSON-RPC body and starts a SERVER
    span named `tools/call <tool>`, `prompts/get <name>` or `<method>`.
  - It sets these attributes:
    - `mcp.method.name`, `mcp.session.id`, `mcp.protocol.version`
    - `gen_ai.tool.name`, `gen_ai.operation.name=execute_tool`, `gen_ai.prompt.name`
    - `jsonrpc.request.id`, `error.type`, `rpc.response.status_code`
    - Obot's own: `obot.mcp.hop` (`gateway`|`component`), `obot.mcp.server.id`/`name`/`runtime`, `obot.mcp.client.name`, `obot.mcp.blocked`, `user.id`
  - `mcpResponseObserver` wraps the ResponseWriter and parses JSON or SSE
    responses to find the matching JSON-RPC result or error. A tool result with
    `isError` sets `error.type=tool_error`.
  - Records the histogram `mcp.server.operation.duration`.
  - `tracedTransport` / `TracedHTTPClient()` wrap outbound transports in
    otelhttp so `traceparent` is propagated.
  - `startFilterSpan` creates a span per guardrail or webhook filter. Its outcome is one of ok, rejected, mutated, skipped or error.
- **`handler.go`**: `Proxy` starts the operation for non-composite requests.
  The composite (vMCP) path is traced by mmmcp instead. `handler.go` also
  injects the trace context before the composite handler, and gives mmmcp
  `TracedHTTPClient()`.
- **`proxy_hooks.go`**: wires in the filter spans.
- **`pkg/storage/tracing.go`** (new): names storage spans
  (`storage get vmcps`, `storage update mcpclientsessions/status`, …). It
  records them **only if `--trace-storage-requests` is set**. Without that,
  there are about 90 storage spans per tool call.
  - The setting is applied to the storage server client, and through nah's new
    `WrapTransport` to the `obot-controller` and `obot-every-replica` routers
    (`pkg/services/config.go`).
- **`pkg/api/server/server.go`**: `traced()` now names the span from
  `r.Pattern` in a defer. Before, a `ReverseProxy` that panicked with
  `ErrAbortHandler` left spans named `obot/http`. The test is in `flush_test.go`.

### mmmcp

- `component.WithoutValues` stripped the active span; it now keeps it.
- The go-sdk runs handlers on the *connection* context, so
  `tracingMiddleware()`, registered first, extracts the parent span from
  `RequestExtra.Header`.
- Component client calls (`CallTool`, `GetPrompt`, `ReadResource`,
  `Discover`) get CLIENT spans that carry `mmmcp.component.name`.
- The code is in `component/telemetry.go`, `telemetry.go` and
  `component/http/client.go`. There is an end-to-end test in `telemetry_test.go`.

## Known issues and ideas (none agreed to yet; ask Craig before doing them)

- **Protocol fallbacks are tagged as errors.** For each component call, mmmcp:
  - opens a new session,
  - tries `server/discover` (gitmcp.io returns **400**),
  - then `initialize`, `notifications/initialized`, `tools/call`,
  - then a `DELETE` (**405**).

  So the 400 and 405 show up as error spans. They should probably be marked not-an-error.
- **Each component call opens a new session**, which costs about 1s. Traces make
  this obvious. It's an mmmcp design issue, not a tracing one.
- **Duplicate parsing with the audit code.** `peekMCPRequest` duplicates
  `readMCPProxyRequest`, and the response observer duplicates `auditSSEBody`.
  - Tracing can't just reuse `proxyAudit`, because component servers are
    marked `AuditLogIgnore` (`pkg/mcp/types.go`, "Component requests are
    audited at the vMCP level"). Tool-preview servers are too
    (`mcpcatalogs.go`).
  - In those cases `newProxyAudit` returns nil, and component hops are exactly
    where tracing has to work.
  - The fix would be to extract one shared parser that both audit and tracing
    use. That's roughly neutral on line count, with one parse instead of two.
- **No tool arguments or results on Obot's spans.** Langfuse shows Obot TOOL
  spans with no input or output. An opt-in flag could record
  `gen_ai.tool.call.arguments` / `gen_ai.tool.call.result`. They contain
  sensitive data, so the flag must default to off.

## Local demo (`hack/otel-demo/`)

This setup was verified end to end. It shows one joined trace from Claude
Code through Obot to the upstream server, metrics in Aspire, Langfuse
sessions with token counts, and 0 storage spans by default.

1. **Network and backends**:
   ```bash
   docker network create otel
   docker run -d --name obot-jaeger --network otel --network-alias jaeger -e JAEGER_LISTEN_HOST=0.0.0.0 \
     cr.jaegertracing.io/jaegertracing/jaeger:2.16.0 \
     --set=receivers.otlp.protocols.grpc.endpoint=0.0.0.0:4317 \
     --set=receivers.otlp.protocols.http.endpoint=0.0.0.0:4318      # UI :16686
   docker run -d --name obot-aspire --network otel --network-alias aspire \
     -e DASHBOARD__FRONTEND__AUTHMODE=Unsecured -e DASHBOARD__OTLP__AUTHMODE=Unsecured \
     -e DOTNET_DASHBOARD_UNSECURED_ALLOW_ANONYMOUS=true \
     mcr.microsoft.com/dotnet/aspire-dashboard:9.4                    # UI :18888, OTLP :18889
   (cd hack/otel-demo/langfuse && cp env.example .env && docker compose up -d)  # UI :3000
   docker run -d --name obot-otel-collector --network otel \
     -e NO_PROXY=localhost,127.0.0.1,jaeger,aspire,langfuse-web -e no_proxy=localhost,127.0.0.1,jaeger,aspire,langfuse-web \
     -v $PWD/hack/otel-demo/collector.yaml:/etc/otelcol-contrib/config.yaml:ro \
     -p 127.0.0.1:4317:4317 -p 127.0.0.1:4318:4318 \
     otel/opentelemetry-collector-contrib:0.128.0
   ```
   - The Jaeger and Aspire UIs weren't published on host ports. They were
     reached with a user-owned `socat` from the container IP, because discobox
     doesn't forward root-owned ports. Using `-p` is fine elsewhere.
   - Langfuse login: `admin@obot.local` / `obot-langfuse`. Project keys:
     `pk-lf-obot-local` / `sk-lf-obot-local`. The collector's Basic auth header
     is these keys base64-encoded.
   - **Discobox only:** it injects proxy env vars into containers, so container
     hostnames must be in `NO_PROXY`/`no_proxy`.
2. **Demo MCP servers** (instrumented themselves):
   ```bash
   cd hack/otel-demo/mcpdemo && go build -o /tmp/mcpdemo .
   OTEL_SERVICE_NAME=weather-mcp OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf /tmp/mcpdemo -server weather -addr 127.0.0.1:9123 &
   OTEL_SERVICE_NAME=notes-mcp   OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf /tmp/mcpdemo -server notes   -addr 127.0.0.1:9124 &
   ```
3. **Obot** (see `.claude/skills/run-obot` for sandbox gotchas). Run it in a subshell so `OTEL_SERVICE_NAME` doesn't leak into the Claude client:
   ```bash
   (export OTEL_SERVICE_NAME=obot OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
      OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=otlp OTEL_LOGS_EXPORTER=otlp \
      OBOT_SERVER_DISALLOW_LOCALHOST_MCP=false; make dev)
   ```
   Then create a vMCP with three components:
   - `http://localhost:9123/mcp`
   - `http://localhost:9124/mcp`
   - a remote entry for `https://gitmcp.io/obot-platform/obot`

   The `run-obot` skill has the curl calls for this.
4. **A real client.** Put the vMCP ID in `hack/otel-demo/claude-client/mcp.json`, then:
   ```bash
   hack/otel-demo/claude-client/ask-claude.sh "What's the weather in Paris? Then search the obot docs for 'composite'."
   ```
   - This turns on Claude Code's beta tracing, so its
     `claude_code.interaction` / `llm_request` / `tool` spans join Obot's trace
     through `traceparent`.
   - The `claude_code.interaction` root span only shows up some of the time.

### Collector pipelines (`collector.yaml`)

- Traces go to Jaeger and Aspire.
- A second pipeline, `traces/langfuse`, sends traces to Langfuse. Before
  exporting it:
  - drops the UI's own `GET /api/...` spans,
  - copies Claude Code's token attributes to `gen_ai.usage.*`,
  - maps Claude's prompt, output and tool content to
    `langfuse.observation.input`/`output`.

  Without those mappings, Langfuse shows "This trace has no input or output".
- Metrics and logs go to Aspire only.

## Verifying

```bash
go test ./pkg/api/handlers/mcpgateway/... ./pkg/storage/... ./pkg/api/server/...
make lint      # golangci-lint v2.14.0
(cd ../mmmcp && go test ./... && golangci-lint run)
(cd ../nah && go test ./... && golangci-lint run)
```

All of these passed on 2026-11-13.
