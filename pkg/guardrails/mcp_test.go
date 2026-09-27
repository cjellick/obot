package guardrails

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/obot-platform/obot/pkg/gateway/types"
)

func mcpRule(name, action, condition string, profiles ...string) types.GuardrailRule {
	r := rule(name, action, profiles...)
	r.Target = types.GuardrailTargetMCP
	r.Condition = condition
	return r
}

func TestMCPToolCallRedactsArguments(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{mcpRule("redact", types.GuardrailActionRedact, `mcp_tool == "send"`, "secrets")},
	}
	e := New(context.Background(), store, nil)
	call := MCPCall{Identity: Identity{UserID: "u"}, Server: "mail", Tool: "send", Arguments: json.RawMessage(`{"to":"bob","body":{"lines":["key ` + leakedKey + `"]},"n":1.50}`)}

	d := e.CheckMCPToolCall(context.Background(), call)
	if d.Blocked || d.Changed == nil {
		t.Fatalf("expected redacted arguments, got %+v", d)
	}
	if strings.Contains(string(d.Changed), leakedKey) || !strings.Contains(string(d.Changed), "[REDACTED:AWS_ACCESS_KEY_ID]") || !strings.Contains(string(d.Changed), `"n":1.50`) {
		t.Fatalf("unexpected arguments %s", d.Changed)
	}

	// The condition limits the rule to one tool.
	call.Tool = "read"
	if d := e.CheckMCPToolCall(context.Background(), call); d.Changed != nil || len(d.Rules) != 0 {
		t.Fatalf("rule should not apply to other tools: %+v", d)
	}
	// LLM traffic isn't affected by MCP rules.
	if !e.HasMCPRules(context.Background()) {
		t.Fatal("expected MCP rules")
	}
}

func TestMCPToolResult(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{mcpRule("redact", types.GuardrailActionRedact, "", "secrets")},
	}
	e := New(context.Background(), store, nil)
	call := MCPCall{Identity: Identity{UserID: "u"}, Server: "files", Tool: "read"}
	result := json.RawMessage(`{"content":[{"type":"text","text":"found ` + leakedKey + `"},{"type":"image","data":"` + leakedKey + `"}],"structuredContent":{"key":"` + leakedKey + `"}}`)

	d := e.CheckMCPToolResult(context.Background(), call, result)
	if d.Changed == nil {
		t.Fatalf("expected a redacted result, got %+v", d)
	}
	var out struct {
		Content []struct {
			Text string `json:"text"`
			Data string `json:"data"`
		} `json:"content"`
		StructuredContent map[string]string `json:"structuredContent"`
	}
	if err := json.Unmarshal(d.Changed, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Content[0].Text, leakedKey) || strings.Contains(out.StructuredContent["key"], leakedKey) {
		t.Fatalf("result not redacted: %s", d.Changed)
	}
	// Binary content isn't text and is left alone.
	if out.Content[1].Data != leakedKey {
		t.Fatalf("image data should be untouched: %s", d.Changed)
	}

	store.rules = []types.GuardrailRule{mcpRule("block", types.GuardrailActionBlock, "", "secrets")}
	// The default input scope (latest) must not skip tool call arguments.
	store.rules[0].InputScope = types.GuardrailScopeLatest
	e.Invalidate()
	if d := e.CheckMCPToolCall(context.Background(), MCPCall{Tool: "write", Arguments: json.RawMessage(`{"text":"` + leakedKey + `"}`)}); !d.Blocked {
		t.Fatalf("expected the tool call to be blocked, got %+v", d)
	}
	if d := e.CheckMCPToolResult(context.Background(), call, result); !d.Blocked || !strings.Contains(d.Message, "block") {
		t.Fatalf("expected a block, got %+v", d)
	}
}
