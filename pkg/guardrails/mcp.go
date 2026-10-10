package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"

	types2 "github.com/obot-platform/obot/apiclient/types"
	gateway "github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/principal"
	kuser "k8s.io/apiserver/pkg/authentication/user"
)

// MCPCall is an MCP tool call that guardrails check: its arguments on the way to the
// server, and its result on the way back.
type MCPCall struct {
	Identity Identity
	// Server is the MCP server's display name, and ServerID its ID.
	Server   string
	ServerID string
	Tool     string
	// Arguments are the tool call's arguments.
	Arguments json.RawMessage
	Headers   map[string]string
}

// MCPDecision is the outcome of checking an MCP tool call or result.
type MCPDecision struct {
	Blocked bool
	Message string
	// Rules names the rules that applied, for auditing.
	Rules []string
	// Changed is the redacted arguments or result; nil when nothing changed.
	Changed json.RawMessage
}

// jsonText is one string in a JSON document, with a way to replace it.
type jsonText struct {
	get func() string
	set func(string)
}

// ResolveIdentity resolves whose budgets and guardrails apply to a request, and whether the
// caller is an admin. A hosted agent acts for its owner, so it gets the owner's ID, groups
// and role.
func ResolveIdentity(ctx context.Context, gatewayClient *gateway.Client, user kuser.Info) (Identity, bool, error) {
	userID := principal.ResourceOwnerID(user)
	groups := user.GetExtra()["auth_provider_groups"]
	isAdmin := slices.Contains(user.GetGroups(), types2.GroupAdmin) || slices.Contains(user.GetGroups(), types2.GroupOwner)
	if !principal.IsHostedAgent(user) {
		return Identity{UserID: userID, Groups: groups}, isAdmin, nil
	}
	ownerID, err := strconv.ParseUint(userID, 10, 64)
	if err != nil {
		return Identity{}, false, types2.NewErrForbidden("cannot determine the owner of this agent")
	}
	if groups, err = gatewayClient.ListGroupIDsForUser(ctx, uint(ownerID)); err != nil {
		return Identity{}, false, err
	}
	owner, err := gatewayClient.UserByID(ctx, userID)
	if err != nil {
		return Identity{}, false, err
	}
	return Identity{UserID: userID, Groups: groups}, owner.Role.HasRole(types2.RoleAdmin), nil
}

// HasMCPRules reports whether any rule targets MCP traffic, so callers can skip reading
// messages when none does.
func (e *Engine) HasMCPRules(ctx context.Context) bool {
	rules, err := e.load(ctx)
	if err != nil {
		// Let the check itself fail (closed) with the error.
		return true
	}
	return slices.ContainsFunc(rules, func(r *compiledRule) bool { return r.rule.Target == types.GuardrailTargetMCP })
}

func (c MCPCall) vars() map[string]any {
	args := map[string]any{}
	if len(c.Arguments) > 0 {
		_ = json.Unmarshal(c.Arguments, &args)
	}
	headers := c.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	return map[string]any{"mcp_server": c.Server, "mcp_tool": c.Tool, "mcp_arguments": args, "headers": headers}
}

func (c MCPCall) resource() string {
	if c.Server == "" {
		return c.Tool
	}
	return c.Server + "/" + c.Tool
}

// CheckMCPToolCall checks a tool call's arguments before they reach the MCP server.
func (e *Engine) CheckMCPToolCall(ctx context.Context, call MCPCall) MCPDecision {
	return e.checkMCP(ctx, call, StageInput, call.Arguments, RoleToolCall, nil)
}

// CheckMCPToolResult checks a tool call's result before it reaches the client.
func (e *Engine) CheckMCPToolResult(ctx context.Context, call MCPCall, result json.RawMessage) MCPDecision {
	var history []Segment
	if len(call.Arguments) > 0 {
		history = []Segment{{Role: RoleToolCall, Name: call.Tool, Text: string(call.Arguments)}}
	}
	return e.checkMCP(ctx, call, StageOutput, result, RoleToolResult, history)
}

func (e *Engine) checkMCP(ctx context.Context, call MCPCall, stage string, doc json.RawMessage, role string, history []Segment) MCPDecision {
	vars := call.vars()
	plan, err := e.Plan(ctx, types.GuardrailTargetMCP, stage, call.Identity, vars)
	if err != nil {
		slog.Error("failed to load guardrail rules; blocking the tool call", "error", err)
		return MCPDecision{Blocked: true, Message: "Tool call blocked: guardrails could not be evaluated."}
	}
	if plan.Empty() {
		return MCPDecision{}
	}

	var (
		parsed any
		texts  []jsonText
	)
	if len(doc) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(doc))
		decoder.UseNumber()
		if err := decoder.Decode(&parsed); err != nil {
			// Not JSON; check it as one piece of text.
			parsed = string(doc)
		}
		if stage == StageOutput {
			texts = toolResultTexts(&parsed)
		} else {
			texts = collectJSONText(&parsed)
		}
	}
	segments := make([]Segment, len(texts))
	for i, t := range texts {
		segments[i] = Segment{Role: role, Name: call.Tool, Text: t.get()}
	}

	d := e.Evaluate(ctx, plan, Request{
		Target: types.GuardrailTargetMCP, Stage: stage, Identity: call.Identity, Vars: vars,
		Segments: segments, History: history, Resource: call.resource(),
	})
	out := MCPDecision{Blocked: d.Blocked, Message: d.Message, Rules: plan.ruleNames()}
	if d.Blocked || !d.Redacted {
		return out
	}
	for i, t := range texts {
		if i < len(d.Segments) {
			t.set(d.Segments[i].Text)
		}
	}
	if s, ok := parsed.(string); ok && !json.Valid(doc) {
		out.Changed = json.RawMessage(s)
		return out
	}
	changed, err := json.Marshal(parsed)
	if err != nil {
		return MCPDecision{Blocked: true, Message: "Tool call blocked: redacted content could not be encoded.", Rules: out.Rules}
	}
	out.Changed = changed
	return out
}

func (p Plan) ruleNames() []string {
	names := make([]string, 0, len(p.broken)+len(p.rules))
	for _, r := range slices.Concat(p.broken, p.rules) {
		names = append(names, r.rule.Name)
	}
	return names
}

// collectJSONText returns every string value (not key) in a JSON document, depth first.
func collectJSONText(v *any) []jsonText {
	var out []jsonText
	var walk func(get func() any, set func(any))
	walk = func(get func() any, set func(any)) {
		switch val := get().(type) {
		case string:
			if val != "" {
				out = append(out, jsonText{get: func() string { return get().(string) }, set: func(s string) { set(s) }})
			}
		case map[string]any:
			keys := make([]string, 0, len(val))
			for k := range val {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				walk(func() any { return val[k] }, func(n any) { val[k] = n })
			}
		case []any:
			for i := range val {
				walk(func() any { return val[i] }, func(n any) { val[i] = n })
			}
		}
	}
	walk(func() any { return *v }, func(n any) { *v = n })
	return out
}

// toolResultTexts returns the text of an MCP CallToolResult: its text content blocks, the
// text of embedded resources, and the strings in its structured content. Other shapes are
// checked string by string.
func toolResultTexts(v *any) []jsonText {
	result, ok := (*v).(map[string]any)
	if !ok {
		return collectJSONText(v)
	}
	var out []jsonText
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				out = append(out, stringField(block, "text")...)
			case "resource":
				if res, ok := block["resource"].(map[string]any); ok {
					out = append(out, stringField(res, "text")...)
				}
			}
		}
	}
	if structured, ok := result["structuredContent"]; ok {
		out = append(out, collectJSONText(&structured)...)
		// structured is a copy of the interface; nested maps and slices are shared, but a
		// top-level string must be written back.
		if _, isString := structured.(string); isString {
			last := len(out) - 1
			out[last].set = func(s string) { result["structuredContent"] = s }
		}
	}
	return out
}

func stringField(m map[string]any, key string) []jsonText {
	if s, ok := m[key].(string); !ok || s == "" {
		return nil
	}
	return []jsonText{{get: func() string { return m[key].(string) }, set: func(s string) { m[key] = s }}}
}
