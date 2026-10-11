package guardrails

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/obot-platform/obot/pkg/bifrost"
)

// TestGatewayChecksRoutedModelAccess checks that a caller is only served by models they may
// use, whichever model the gateway's routing rules or fallbacks chose.
func TestGatewayChecksRoutedModelAccess(t *testing.T) {
	gw, up := startGateway(t, &fakeStore{})
	ctx := context.Background()
	for _, rule := range []map[string]any{
		{
			"name":           "premium to opus",
			"enabled":        true,
			"cel_expression": `"x-tier" in headers && headers["x-tier"] == "premium"`,
			"priority":       1,
			"scope":          "global",
			"targets":        []map[string]any{{"provider": "anthropic", "model": "claude-opus-4-8", "weight": 1}},
		},
		{
			"name":           "trial to opus, falling back",
			"enabled":        true,
			"cel_expression": `"x-tier" in headers && headers["x-tier"] == "trial"`,
			"priority":       2,
			"scope":          "global",
			"targets":        []map[string]any{{"provider": "anthropic", "model": "claude-opus-4-8", "weight": 1}},
			"fallbacks":      []string{"openai/gpt-4o-mini"},
		},
	} {
		if err := gw.Do(ctx, http.MethodPost, "/api/governance/routing-rules", rule, nil); err != nil {
			t.Fatal(err)
		}
	}

	// The caller may use OpenAI models only.
	openAIOnly := func(_ context.Context, model string) error {
		if strings.HasPrefix(model, "openai/") {
			return nil
		}
		return fmt.Errorf("user does not have permission to use model %q", model)
	}
	body := `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`

	tests := []struct {
		name       string
		tier       string
		access     AttemptCheck
		wantStatus int
		wantModel  string
		wantError  string
	}{
		{
			name:       "requested model",
			access:     openAIOnly,
			wantStatus: http.StatusOK,
			wantModel:  "gpt-4o-mini",
		},
		{
			name:       "routed to a model the caller may not use",
			tier:       "premium",
			access:     openAIOnly,
			wantStatus: http.StatusForbidden,
			wantError:  `user does not have permission to use model \"anthropic/claude-opus-4-8\"`,
		},
		{
			name:       "routed to a forbidden model, served by an allowed fallback",
			tier:       "trial",
			access:     openAIOnly,
			wantStatus: http.StatusOK,
			wantModel:  "gpt-4o-mini",
		},
		{
			name:       "routed to a model whose budget is spent",
			tier:       "premium",
			access:     budgetSpentFor("anthropic/"),
			wantStatus: http.StatusTooManyRequests,
			wantError:  `"type":"budget_exceeded"`,
		},
		{
			name:       "routed to a model whose budget is spent, served by a fallback with budget",
			tier:       "trial",
			access:     budgetSpentFor("anthropic/"),
			wantStatus: http.StatusOK,
			wantModel:  "gpt-4o-mini",
		},
		{
			name:       "routed to a model the caller may use",
			tier:       "premium",
			access:     func(context.Context, string) error { return nil },
			wantStatus: http.StatusOK,
			wantModel:  "claude-opus-4-8",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(up.prompts)
			code, out := callWithAccess(t, gw, body, tt.tier, tt.access)
			if code != tt.wantStatus {
				t.Fatalf("status %d, want %d: %s", code, tt.wantStatus, out)
			}
			if tt.wantError != "" && !strings.Contains(out, tt.wantError) {
				t.Fatalf("response %s, want it to explain %s", out, tt.wantError)
			}
			up.lock.Lock()
			sent := strings.Join(up.prompts[before:], "\n")
			up.lock.Unlock()
			if strings.Contains(sent, "claude-opus-4-8") && tt.wantModel != "claude-opus-4-8" {
				t.Fatalf("a forbidden model reached the provider: %s", sent)
			}
			if tt.wantModel != "" && !strings.Contains(sent, `"`+tt.wantModel+`"`) {
				t.Fatalf("want the request served by %s, the provider got: %s", tt.wantModel, sent)
			}
		})
	}
}

// budgetSpentFor refuses models with prefix as an exhausted budget would.
func budgetSpentFor(prefix string) AttemptCheck {
	return func(_ context.Context, model string) error {
		if strings.HasPrefix(model, prefix) {
			return &AttemptError{Status: http.StatusTooManyRequests, Type: "budget_exceeded", Message: "budget \"Opus\" exhausted"}
		}
		return nil
	}
}

func callWithAccess(t *testing.T, gw *bifrost.Engine, body, tier string, access AttemptCheck) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tier != "" {
		req.Header.Set("X-Tier", tier)
	}
	token, release := BindRequest(Identity{UserID: "alice"}, access)
	defer release()
	req.Header.Set(HeaderRequestToken, token)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b)
}
