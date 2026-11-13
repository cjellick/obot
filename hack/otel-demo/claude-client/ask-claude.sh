#!/bin/bash
# Runs headless Claude Code with only the Obot vMCP as its MCP server, exporting
# traces to Jaeger. Usage: ask-claude.sh "prompt"
# Do not inherit another service's identity from the calling shell.
unset OTEL_SERVICE_NAME OTEL_RESOURCE_ATTRIBUTES
export CLAUDE_CODE_ENABLE_TELEMETRY=1 CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
# Include prompts, model output, and tool inputs and outputs in spans.
export OTEL_LOG_USER_PROMPTS=1 OTEL_LOG_TOOL_DETAILS=1 OTEL_LOG_TOOL_CONTENT=1
export ENABLE_BETA_TRACING_DETAILED=1 BETA_TRACING_ENDPOINT=http://localhost:4318
export OTEL_TRACES_EXPORTER=otlp OTEL_METRICS_EXPORTER=none OTEL_LOGS_EXPORTER=none
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 OTEL_TRACES_EXPORT_INTERVAL=1000
cd "$(dirname "$0")"
exec claude -p "$1" --mcp-config ./mcp.json --strict-mcp-config --allowedTools mcp__obot < /dev/null
