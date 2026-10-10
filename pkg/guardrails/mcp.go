package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	types2 "github.com/obot-platform/obot/apiclient/types"
	gateway "github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/principal"
	kuser "k8s.io/apiserver/pkg/authentication/user"
)

const (
	// MCP methods whose content guardrail filters check.
	MCPMethodToolsCall     = "tools/call"
	MCPMethodResourcesRead = "resources/read"
	MCPMethodPromptsGet    = "prompts/get"
)

// MCPFilter is a guardrail filter: an MCP filter that Obot evaluates in-process with
// guardrail profiles (detectors) instead of calling a filter MCP server.
type MCPFilter struct {
	// ID and Name identify the filter on guardrail events.
	ID     string
	Name   string
	Config types2.MCPGuardrailFilterConfig
}

// MCPMessage is an MCP request or response that a guardrail filter checks.
type MCPMessage struct {
	Identity Identity
	// Server is the MCP server's display name, and ServerID its ID.
	Server   string
	ServerID string
	Headers  map[string]string
	// Method is the request's method, and Name its tool or prompt name or resource URI.
	Method string
	Name   string
	// Stage is StageInput for a request and StageOutput for its response.
	Stage string
	// Params are the request's params, at both stages.
	Params json.RawMessage
	// Result is the response's result, at the output stage.
	Result json.RawMessage
}

// MCPDecision is the outcome of a guardrail filter.
type MCPDecision struct {
	// Applied reports whether the filter checked the message: it covers the message's
	// direction and caller, its condition held, and the message was sampled.
	Applied bool
	Blocked bool
	Message string
	// Params and Result are the redacted request params or response result; nil when
	// unchanged.
	Params json.RawMessage
	Result json.RawMessage
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

// filterRule converts a guardrail filter into a rule. A detector that doesn't exist fails
// like one that can't be evaluated, so a fail-closed filter blocks.
func filterRule(f MCPFilter, profiles map[string]*compiledProfile) *compiledRule {
	c := f.Config
	applyTo := types.GuardrailApplyBoth
	switch c.Direction {
	case types2.MCPGuardrailDirectionRequest:
		applyTo = types.GuardrailApplyInput
	case types2.MCPGuardrailDirectionResponse:
		applyTo = types.GuardrailApplyOutput
	}
	subjects := make([]types.GuardrailRuleSubject, 0, len(c.Subjects))
	for _, s := range c.Subjects {
		subjects = append(subjects, types.GuardrailRuleSubject{Type: s.Type, ID: s.ID})
	}
	r := &compiledRule{
		rule: types.GuardrailRule{
			ID:           f.ID,
			Name:         f.Name,
			Enabled:      true,
			Target:       types.GuardrailTargetMCP,
			ApplyTo:      applyTo,
			Action:       c.Action,
			Condition:    c.Condition,
			SamplingRate: c.SamplingRate,
			FailClosed:   c.FailClosed,
			BlockMessage: c.BlockMessage,
			ProfileIDs:   c.DetectorIDs,
			Subjects:     subjects,
		},
		allRedact: true,
	}
	for _, id := range c.DetectorIDs {
		p, ok := profiles[id]
		if !ok {
			p = &compiledProfile{profile: types.GuardrailProfile{ID: id, Name: id}, err: fmt.Errorf("detector %s does not exist", id)}
		}
		r.profiles = append(r.profiles, p)
		r.allRedact = r.allRedact && p.redacts
	}
	return r
}

// CheckMCPFilter runs a guardrail filter on an MCP request or response. Tool call arguments
// and results, resource contents, and prompt arguments and messages are checked; other
// methods carry no content to check and pass unchanged.
func (e *Engine) CheckMCPFilter(ctx context.Context, f MCPFilter, msg MCPMessage) MCPDecision {
	_, profiles, err := e.loadAll(ctx)
	if err != nil {
		// Unlike a detector failing (which the filter's fail-closed setting decides), this means
		// no detector can run at all. Block, as any filter that errors does and as model
		// guardrails do when their rules can't be loaded.
		slog.Error("failed to load guardrail detectors; blocking", "filter", f.Name, "error", err)
		return MCPDecision{Applied: true, Blocked: true, Message: fmt.Sprintf("Blocked: the %q guardrail filter could not be evaluated.", f.Name)}
	}

	var params map[string]any
	if len(msg.Params) > 0 {
		if err := decodeJSON(msg.Params, &params); err != nil {
			// The server might still accept what can't be checked here, so don't forward it.
			return MCPDecision{Applied: true, Blocked: true, Message: fmt.Sprintf("Blocked: the %q guardrail filter could not read the request's params.", f.Name)}
		}
	}
	arguments, _ := params["arguments"].(map[string]any)
	if arguments == nil {
		arguments = map[string]any{}
	}
	headers := msg.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	vars := map[string]any{
		"mcp_server": msg.Server, "mcp_method": msg.Method, "mcp_tool": msg.Name,
		"mcp_arguments": arguments, "headers": headers,
	}

	var plan Plan
	e.addToPlan(&plan, filterRule(f, profiles), types.GuardrailTargetMCP, msg.Stage, msg.Identity, vars)
	if plan.Empty() {
		return MCPDecision{}
	}

	var (
		doc     any
		texts   []jsonText
		role    = RoleToolCall
		history []Segment
	)
	switch msg.Stage {
	case StageInput:
		doc = params
		if params != nil && (msg.Method == MCPMethodToolsCall || msg.Method == MCPMethodPromptsGet) {
			texts = fieldTexts(params, "arguments")
		}
	case StageOutput:
		role = RoleToolResult
		if len(msg.Result) > 0 {
			if err := decodeJSON(msg.Result, &doc); err != nil {
				return MCPDecision{Applied: true, Blocked: true, Message: fmt.Sprintf("Blocked: the %q guardrail filter could not read the response.", f.Name)}
			}
		}
		switch msg.Method {
		case MCPMethodToolsCall:
			texts = toolResultTexts(&doc)
			if raw, ok := params["arguments"]; ok {
				if args, err := json.Marshal(raw); err == nil {
					history = []Segment{{Role: RoleToolCall, Name: msg.Name, Text: string(args)}}
				}
			}
		case MCPMethodResourcesRead:
			texts = resourceContentsTexts(doc)
		case MCPMethodPromptsGet:
			texts = promptMessagesTexts(doc)
		}
	}

	segments := make([]Segment, len(texts))
	for i, t := range texts {
		segments[i] = Segment{Role: role, Name: msg.Name, Text: t.get()}
	}
	resource := msg.Name
	if msg.Server != "" {
		resource = msg.Server + "/" + msg.Name
	}
	d := e.Evaluate(ctx, plan, Request{
		Target: types.GuardrailTargetMCP, Stage: msg.Stage, Identity: msg.Identity, Vars: vars,
		Segments: segments, History: history, Resource: resource,
	})
	out := MCPDecision{Applied: true, Blocked: d.Blocked, Message: d.Message}
	if d.Blocked || !d.Redacted {
		return out
	}
	for i, t := range texts {
		if i < len(d.Segments) {
			t.set(d.Segments[i].Text)
		}
	}
	changed, err := json.Marshal(doc)
	if err != nil {
		return MCPDecision{Applied: true, Blocked: true, Message: fmt.Sprintf("Blocked: the %q guardrail filter could not encode the redacted message.", f.Name)}
	}
	if msg.Stage == StageInput {
		out.Params = changed
	} else {
		out.Result = changed
	}
	return out
}

func decodeJSON(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(v)
}

// collectJSONText returns every string value (not key) in a JSON document, depth first.
func collectJSONText(v *any) []jsonText {
	return collectTexts(func() any { return *v }, func(n any) { *v = n })
}

// fieldTexts returns every string value in a field of a JSON object.
func fieldTexts(m map[string]any, key string) []jsonText {
	if _, ok := m[key]; !ok {
		return nil
	}
	return collectTexts(func() any { return m[key] }, func(n any) { m[key] = n })
}

func collectTexts(get func() any, set func(any)) []jsonText {
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
	walk(get, set)
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
			if block, ok := item.(map[string]any); ok {
				out = append(out, contentBlockTexts(block)...)
			}
		}
	}
	return append(out, fieldTexts(result, "structuredContent")...)
}

// resourceContentsTexts returns the text of an MCP ReadResourceResult's text contents.
// Binary (blob) contents are not checked.
func resourceContentsTexts(v any) []jsonText {
	result, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	contents, _ := result["contents"].([]any)
	var out []jsonText
	for _, item := range contents {
		if content, ok := item.(map[string]any); ok {
			out = append(out, stringField(content, "text")...)
		}
	}
	return out
}

// promptMessagesTexts returns the text of an MCP GetPromptResult's messages.
func promptMessagesTexts(v any) []jsonText {
	result, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	messages, _ := result["messages"].([]any)
	var out []jsonText
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch content := message["content"].(type) {
		case map[string]any:
			out = append(out, contentBlockTexts(content)...)
		case []any:
			for _, c := range content {
				if block, ok := c.(map[string]any); ok {
					out = append(out, contentBlockTexts(block)...)
				}
			}
		}
	}
	return out
}

// contentBlockTexts returns the text of an MCP content block: a text block, or an embedded
// text resource.
func contentBlockTexts(block map[string]any) []jsonText {
	switch block["type"] {
	case "text":
		return stringField(block, "text")
	case "resource":
		if res, ok := block["resource"].(map[string]any); ok {
			return stringField(res, "text")
		}
	}
	return nil
}

func stringField(m map[string]any, key string) []jsonText {
	if s, ok := m[key].(string); !ok || s == "" {
		return nil
	}
	return []jsonText{{get: func() string { return m[key].(string) }, set: func(s string) { m[key] = s }}}
}
