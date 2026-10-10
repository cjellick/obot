package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/obot-platform/obot/pkg/api"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
)

func TestGuardrailRulesAreLLMOnly(t *testing.T) {
	rule := gatewaytypes.GuardrailRule{Name: "r", Target: gatewaytypes.GuardrailTargetMCP}
	err := validateGuardrailRule(api.Context{}, &rule)
	if err == nil || !strings.Contains(err.Error(), "guardrail filter") {
		t.Fatalf("an MCP rule must be rejected, got %v", err)
	}
}

func TestCheckDestinationChange(t *testing.T) {
	existing := &gatewaytypes.GuardrailProfile{Type: "presidio", Config: json.RawMessage(`{"analyzerURL":"https://presidio.internal"}`)}
	stored := map[string]string{"apiKey": "secret"}

	tests := []struct {
		name    string
		config  string
		stored  map[string]string
		wantErr bool
	}{
		{
			name:    "redirecting the stored key is refused",
			config:  `{"analyzerURL":"https://attacker.example"}`,
			stored:  stored,
			wantErr: true,
		},
		{
			name:   "a new key may go to a new endpoint",
			config: `{"analyzerURL":"https://attacker.example","apiKey":"new"}`,
			stored: stored,
		},
		{
			name:   "other settings may change while keeping the key",
			config: `{"analyzerURL":"https://presidio.internal","scoreThreshold":0.9}`,
			stored: stored,
		},
		{
			name:   "nothing stored, nothing to protect",
			config: `{"analyzerURL":"https://elsewhere.example"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated := &gatewaytypes.GuardrailProfile{Type: "presidio", Config: json.RawMessage(tt.config)}
			err := checkDestinationChange(existing, updated, tt.stored)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckPluginChange(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		wantErr bool
	}{
		{name: "list plugins", method: "GET", path: "plugins"},
		{name: "configure the cache", method: "PUT", path: "plugins/semantic_cache", body: `{"enabled":true}`},
		{name: "create the cache", method: "POST", path: "plugins", body: `{"name":"semantic_cache"}`},
		{name: "disable guardrails", method: "PUT", path: "plugins/obot-guardrails-input", body: `{"enabled":false}`, wantErr: true},
		{name: "delete guardrails", method: "DELETE", path: "plugins/obot-guardrails-output", wantErr: true},
		{name: "replace guardrails", method: "POST", path: "plugins", body: `{"name":"obot-guardrails-input"}`, wantErr: true},
		{name: "other APIs are unaffected", method: "PUT", path: "config", body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkPluginChange(tt.method, tt.path, []byte(tt.body)); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
