package llmcore_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/llmcore"
	llmcoreclient "github.com/obot-platform/obot/pkg/llmcore/client"
	"github.com/stretchr/testify/require"
)

// fakeDecisionUpstream is an OpenAI-compatible provider: it answers gpt-6-luna on the native
// /v1/decisions endpoint and every other model on /v1/responses with the forced
// emit_decision function call Bifrost's emulation asks for.
type fakeDecisionUpstream struct {
	lock  sync.Mutex
	paths []string
}

func (f *fakeDecisionUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	// Only inference is recorded: the provider may probe its base URL in the background.
	if strings.HasSuffix(r.URL.Path, "/decisions") || strings.HasSuffix(r.URL.Path, "/responses") {
		f.lock.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.lock.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/decisions"):
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[`+
			`{"type":"choice","name":"verdict","choice":"approve","confidence":0.8,"probabilities":[{"value":"approve","probability":0.8},{"value":"deny","probability":0.2}]},`+
			`{"type":"predicate","name":"pii","probability":0.93}],`+
			`"usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23}}`)
	case strings.HasSuffix(r.URL.Path, "/responses"):
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		args, _ := json.Marshal(map[string]any{
			"verdict": map[string]any{"choice": "deny", "confidence": 0.7, "probabilities": map[string]float64{"approve": 0.3, "deny": 0.7}},
			"pii":     map[string]any{"value": 0.1},
		})
		out, _ := json.Marshal(map[string]any{
			"id": "resp_1", "object": "response", "created_at": 1, "status": "completed", "model": req.Model,
			"output": []any{map[string]any{
				"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "emit_decision",
				"arguments": string(args), "status": "completed",
			}},
			"usage": map[string]any{"input_tokens": 30, "output_tokens": 10, "total_tokens": 40},
		})
		_, _ = w.Write(out)
	default:
		http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
	}
}

func (f *fakeDecisionUpstream) last() string {
	f.lock.Lock()
	defer f.lock.Unlock()
	if len(f.paths) == 0 {
		return ""
	}
	return f.paths[len(f.paths)-1]
}

func newDecisionEngine(t *testing.T) (*llmcore.Engine, *fakeDecisionUpstream) {
	t.Helper()
	fake := &fakeDecisionUpstream{}
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)
	store := newTestStore(t)
	require.NoError(t, store.CreateProvider(t.Context(), "openai", llmcoreclient.Provider{
		Config: schemas.ProviderConfig{
			NetworkConfig: schemas.NetworkConfig{
				BaseURL:             upstream.URL,
				AllowPrivateNetwork: true,
				MaxRetries:          0,
				RetryBackoffInitial: time.Millisecond,
				RetryBackoffMax:     time.Millisecond,
			},
		},
		Keys: []schemas.Key{{ID: "key", Name: "key", Value: *schemas.NewSecretVar("secret"), Models: schemas.WhiteList{"*"}, Weight: 1}},
	}))
	engine, err := llmcore.New(t.Context(), store, llmcore.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Shutdown(context.Background())) })
	return engine, fake
}

const decisionRequestBody = `{"model":%q,"input":"Customer record: Jane Doe, jane@example.com","questions":[` +
	`{"type":"choice","name":"verdict","instructions":"Should this be shared?","choices":[{"value":"approve"},{"value":"deny"}]},` +
	`{"type":"predicate","name":"pii","instructions":"Does the input contain personal data?"}]}`

func TestDecisionsEndpoint(t *testing.T) {
	engine, fake := newDecisionEngine(t)
	require.True(t, engine.ProviderConfigured(schemas.OpenAI))
	require.False(t, engine.ProviderConfigured(schemas.Anthropic))

	for _, tt := range []struct {
		name       string
		model      string
		wantPath   string
		wantChoice string
		wantPII    float64
		wantInput  float64
	}{
		{
			name:       "native",
			model:      "openai/gpt-6-luna",
			wantPath:   "/v1/decisions",
			wantChoice: "approve",
			wantPII:    0.93,
			wantInput:  20,
		},
		{
			name:       "emulated",
			model:      "openai/gpt-5-mini",
			wantPath:   "/v1/responses",
			wantChoice: "deny",
			wantPII:    0.1,
			wantInput:  30,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var observed []schemas.BifrostResponseExtraFields
			ctx := llmcore.WithResponseObserver(t.Context(), func(extra schemas.BifrostResponseExtraFields) { observed = append(observed, extra) })
			w := httptest.NewRecorder()
			body := strings.Replace(decisionRequestBody, "%q", `"`+tt.model+`"`, 1)
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body)).WithContext(ctx))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, tt.wantPath, fake.last())
			require.NotContains(t, w.Body.String(), "extra_fields")
			require.Len(t, observed, 1)
			require.Equal(t, "openai", w.Header().Get("X-Obot-LLM-Provider"))

			var resp struct {
				Answers []struct {
					Type        string   `json:"type"`
					Name        string   `json:"name"`
					Choice      string   `json:"choice"`
					Probability *float64 `json:"probability"`
				} `json:"answers"`
				Usage map[string]any `json:"usage"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Answers, 2)
			require.Equal(t, "verdict", resp.Answers[0].Name)
			require.Equal(t, tt.wantChoice, resp.Answers[0].Choice)
			require.InDelta(t, tt.wantPII, *resp.Answers[1].Probability, 1e-9)
			require.Equal(t, tt.wantInput, resp.Usage["input_tokens"])
		})
	}

	t.Run("invalid", func(t *testing.T) {
		for _, body := range []string{
			`{`,
			`{"model":"openai/gpt-6-luna","input":"x","questions":[]}`,
			`{"model":"gpt-6-luna","input":"x","questions":[{"type":"predicate","instructions":"?"}]}`,
		} {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(body)))
			require.Equal(t, http.StatusBadRequest, w.Code, body)
			require.Contains(t, w.Body.String(), `"error"`)
		}
	})
}

func TestInternalDecision(t *testing.T) {
	engine, fake := newDecisionEngine(t)
	questions := []schemas.DecisionQuestion{
		{Type: schemas.DecisionTypeChoice, Name: schemas.Ptr("verdict"), Instructions: schemas.NewDecisionText("?"), Choices: []schemas.DecisionChoice{
			{Value: schemas.DecisionScalar{Str: schemas.Ptr("approve")}},
			{Value: schemas.DecisionScalar{Str: schemas.Ptr("deny")}},
		}},
		{Type: schemas.DecisionTypePredicate, Name: schemas.Ptr("pii"), Instructions: schemas.NewDecisionText("?")},
	}
	input := schemas.DecisionInput{Text: schemas.Ptr("evidence")}

	resp, err := engine.Decision(t.Context(), "openai/gpt-6-luna", input, questions)
	require.NoError(t, err)
	require.Equal(t, "/v1/decisions", fake.last())
	require.Equal(t, "approve", *resp.Answers[0].Choice.Str)
	require.Equal(t, 20, resp.Usage.PromptTokens)

	resp, err = engine.Decision(t.Context(), "openai/gpt-5-mini", input, questions)
	require.NoError(t, err)
	require.Equal(t, "/v1/responses", fake.last())
	require.Equal(t, "deny", *resp.Answers[0].Choice.Str)
	require.Equal(t, 30, resp.Usage.PromptTokens)

	_, err = engine.Decision(t.Context(), "gpt-5-mini", input, questions)
	require.Error(t, err)
}

func TestNativeDecisions(t *testing.T) {
	require.True(t, llmcore.NativeDecisions(nil, schemas.OpenAI, "gpt-6-luna"))
	require.False(t, llmcore.NativeDecisions(nil, schemas.OpenAI, "gpt-5-mini"))
	require.False(t, llmcore.NativeDecisions(nil, schemas.Anthropic, "claude-haiku-4-5"))
	require.False(t, llmcore.NativeDecisions(nil, schemas.OpenRouter, "openai/gpt-6-luna"))
}
