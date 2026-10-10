package mcpgateway

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/obot-platform/obot/pkg/guardrails"
	"github.com/obot-platform/obot/pkg/mcp"
)

const (
	guardrailsHookName = "guardrails"
)

// mcpGuard applies guardrail rules to the tool calls of one proxied MCP exchange.
type mcpGuard struct {
	engine *guardrails.Engine
	call   guardrails.MCPCall
}

// newMCPGuard returns the guard for a request, or nil when no guardrail rule targets MCP
// traffic.
func newMCPGuard(ctx context.Context, engine *guardrails.Engine, identity guardrails.Identity, serverConfig mcp.ServerConfig, header http.Header) *mcpGuard {
	if engine == nil || !engine.HasMCPRules(ctx) {
		return nil
	}
	headers := map[string]string{}
	for k, v := range header {
		lower := strings.ToLower(k)
		if lower == "authorization" || lower == "cookie" || lower == "x-api-key" || lower == "proxy-authorization" {
			continue
		}
		headers[lower] = strings.Join(v, ",")
	}
	return &mcpGuard{
		engine: engine,
		call: guardrails.MCPCall{
			Identity: identity,
			Server:   cmp.Or(serverConfig.MCPServerDisplayName, serverConfig.MCPServerName),
			ServerID: serverConfig.MCPServerName,
			Headers:  headers,
		},
	}
}

// apply checks a tool call's arguments or a tool result. It returns the message with any
// redactions applied, a hook status when rules applied, and whether the message changed.
func (g *mcpGuard) apply(ctx context.Context, message mcp.Message, method, name, direction string, requestParams json.RawMessage) (mcp.Message, *hookStatus, bool) {
	if g == nil || method != "tools/call" {
		return message, nil, false
	}
	call := g.call
	call.Tool = name

	var decision guardrails.MCPDecision
	switch direction {
	case "request":
		var params map[string]json.RawMessage
		if json.Unmarshal(message.Params, &params) != nil {
			// The server might still accept what can't be checked here, so don't forward it.
			return message, &hookStatus{typeName: direction, method: method, name: guardrailsHookName, tool: name, status: "rejected", message: "Tool call blocked: its parameters could not be checked by guardrails."}, false
		}
		call.Arguments = params["arguments"]
		decision = g.engine.CheckMCPToolCall(ctx, call)
		if decision.Changed != nil {
			params["arguments"] = decision.Changed
			updated, err := json.Marshal(params)
			if err != nil {
				return message, &hookStatus{typeName: direction, method: method, name: guardrailsHookName, tool: name, status: "rejected", message: "Tool call blocked: redacted arguments could not be encoded."}, false
			}
			message.Params = updated
		}
	case "response":
		if message.Error != nil || len(message.Result) == 0 {
			return message, nil, false
		}
		var params struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(requestParams, &params)
		call.Arguments = params.Arguments
		decision = g.engine.CheckMCPToolResult(ctx, call, message.Result)
		if decision.Changed != nil {
			message.Result = decision.Changed
		}
	default:
		return message, nil, false
	}

	if len(decision.Rules) == 0 {
		return message, nil, false
	}
	status := &hookStatus{typeName: direction, method: method, name: guardrailsHookName, tool: strings.Join(decision.Rules, ","), status: "ok"}
	switch {
	case decision.Blocked:
		status.status = "rejected"
		status.message = decision.Message
	case decision.Changed != nil:
		status.status = "mutated"
		status.message = "redacted by guardrails"
	}
	return message, status, decision.Changed != nil && !decision.Blocked
}
