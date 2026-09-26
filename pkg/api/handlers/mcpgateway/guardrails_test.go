package mcpgateway

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/guardrails"
	"github.com/obot-platform/obot/pkg/mcp"
)

const (
	testSecret = "AKIAIOSFODNN7EXAMPLE"
)

type guardrailStore struct {
	rules []types.GuardrailRule
}

func (s *guardrailStore) ListGuardrailRules(context.Context) ([]types.GuardrailRule, error) {
	return s.rules, nil
}

func (s *guardrailStore) ListGuardrailProfiles(context.Context) ([]types.GuardrailProfile, error) {
	return []types.GuardrailProfile{{ID: "secrets", Name: "secrets", Type: "secrets", Config: json.RawMessage(`{}`)}}, nil
}

func (s *guardrailStore) GuardrailProfileSecrets(context.Context, string) (map[string]string, error) {
	return nil, nil
}

func (s *guardrailStore) InsertGuardrailEvents(context.Context, []types.GuardrailEvent) error {
	return nil
}

func testGuard(t *testing.T, action string) *mcpGuard {
	t.Helper()
	store := &guardrailStore{rules: []types.GuardrailRule{{
		ID: "r", Name: "no-secrets", Enabled: true, Target: types.GuardrailTargetMCP, ApplyTo: types.GuardrailApplyBoth,
		Action: action, SamplingRate: 100, ProfileIDs: []string{"secrets"},
	}}}
	engine := guardrails.New(t.Context(), store, nil)
	guard := newMCPGuard(t.Context(), engine, guardrails.Identity{UserID: "alice"}, mcp.ServerConfig{MCPServerName: "ms1", MCPServerDisplayName: "files"}, nil)
	if guard == nil {
		t.Fatal("expected a guard when MCP rules exist")
	}
	return guard
}

func TestMCPGuardrailsRedact(t *testing.T) {
	guard := testGuard(t, types.GuardrailActionRedact)
	request := mustMCPHookRequest(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write","arguments":{"text":"key `+testSecret+`"}}}`)
	processor, err := newGuardedHookProcessor(request, guard, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, blocked, _ := processor.blockedRequest(); blocked {
		t.Fatal("redaction must not block")
	}
	body, _ := io.ReadAll(request.Body)
	if strings.Contains(string(body), testSecret) || !strings.Contains(string(body), "[REDACTED:AWS_ACCESS_KEY_ID]") {
		t.Fatalf("arguments not redacted before the server: %s", body)
	}

	response := mcpHookResponse(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"wrote ` + testSecret + `"}]}}`)
	if err := processor.filterResponse(response); err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	if strings.Contains(string(body), testSecret) || !strings.Contains(string(body), "[REDACTED:AWS_ACCESS_KEY_ID]") {
		t.Fatalf("result not redacted before the client: %s", body)
	}
}

func TestMCPGuardrailsBlock(t *testing.T) {
	guard := testGuard(t, types.GuardrailActionBlock)
	request := mustMCPHookRequest(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write","arguments":{"text":"key `+testSecret+`"}}}`)
	processor, err := newGuardedHookProcessor(request, guard, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, blocked, _ := processor.blockedRequest()
	if !blocked || !strings.Contains(string(body), "no-secrets") || strings.Contains(string(body), testSecret) {
		t.Fatalf("expected the tool call to be blocked, got %v %s", blocked, body)
	}

	// Clean calls pass, and a result containing a secret is replaced by an error.
	request = mustMCPHookRequest(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read","arguments":{"path":"a.txt"}}}`)
	processor, err = newGuardedHookProcessor(request, guard, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, blocked, _ := processor.blockedRequest(); blocked {
		t.Fatal("a clean call must not be blocked")
	}
	response := mcpHookResponse(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"` + testSecret + `"}]}}`)
	if err := processor.filterResponse(response); err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	if strings.Contains(string(body), testSecret) || !strings.Contains(string(body), `"error"`) {
		t.Fatalf("expected an error response, got %s", body)
	}

	// Other methods are not checked.
	request = mustMCPHookRequest(t, `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"p","arguments":{"x":"`+testSecret+`"}}}`)
	processor, err = newGuardedHookProcessor(request, guard, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, blocked, _ := processor.blockedRequest(); blocked {
		t.Fatal("only tool calls are checked")
	}
}
