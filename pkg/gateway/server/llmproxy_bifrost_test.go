package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/obot-platform/obot/pkg/gateway/client"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
)

type fakeRoundTripper struct {
	resp *http.Response
}

func TestResolveBifrostRoute(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		header      map[string]string
		wantBase    string
		wantDialect llmtypes.Dialect
		wantErr     bool
	}{
		{
			name:        "anthropic messages",
			path:        "v1/messages",
			wantBase:    "/anthropic",
			wantDialect: llmtypes.DialectAnthropicMessages,
		},
		{
			name:        "anthropic token counting",
			path:        "v1/messages/count_tokens",
			wantBase:    "/anthropic",
			wantDialect: llmtypes.DialectAnthropicMessages,
		},
		{
			name:        "openai responses",
			path:        "v1/responses",
			wantBase:    "/",
			wantDialect: llmtypes.DialectOpenAIResponses,
		},
		{
			name:        "chat completions",
			path:        "v1/chat/completions",
			wantBase:    "/",
			wantDialect: llmtypes.DialectOpenAIChatCompletions,
		},
		{
			name:        "embeddings",
			path:        "v1/embeddings",
			wantBase:    "/",
			wantDialect: llmtypes.DialectOpenAIChatCompletions,
		},
		{
			name:    "legacy completions are not exposed",
			path:    "v1/completions",
			wantErr: true,
		},
		{
			name:    "image generation is not exposed",
			path:    "v1/images/generations",
			wantErr: true,
		},
		{
			name:     "openai model listing",
			path:     "v1/models",
			wantBase: "/",
		},
		{
			name:        "anthropic model listing",
			path:        "v1/models",
			header:      map[string]string{"anthropic-version": "2023-06-01"},
			wantBase:    "/anthropic",
			wantDialect: llmtypes.DialectAnthropicMessages,
		},
		{
			name:    "files are not exposed",
			path:    "v1/files",
			wantErr: true,
		},
		{
			name:    "responses retrieval is not exposed",
			path:    "v1/responses/resp_123",
			wantErr: true,
		},
		{
			name:    "bifrost management API is not reachable",
			path:    "api/providers",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/llm-proxy/"+tt.path, nil)
			req.SetPathValue("path", tt.path)
			for k, v := range tt.header {
				req.Header.Set(k, v)
			}
			route, err := resolveBifrostRoute(req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got route %+v", route)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if route.base != tt.wantBase || route.dialect != tt.wantDialect {
				t.Fatalf("got base %q dialect %q, want base %q dialect %q", route.base, route.dialect, tt.wantBase, tt.wantDialect)
			}
		})
	}
}

func TestChatCompletionsTokenUsageTracker(t *testing.T) {
	tracker := &chatCompletionsTokenUsageTracker{}
	tracker.addTokenUsage([]byte(`{"choices":[{"delta":{"content":"hi"}}],"usage":null}`))
	tracker.addTokenUsage([]byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":5}}}`))

	usage := tracker.getTokenUsage()
	if usage.InputTokens != 100 || usage.OutputTokens != 20 || usage.CacheReadTokens != 40 || usage.ThinkingTokens != 5 || usage.TotalTokens != 120 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestSanitizingTransport(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":                []string{"application/json"},
				"X-Bifrost-Provider":          []string{"openai"},
				"X-Bifrost-Resolved-Model":    []string{"gpt-4.1"},
				"X-Bifrost-Routing-Info-Key":  []string{"primary"},
				"Server":                      []string{"fasthttp"},
				"Access-Control-Allow-Origin": []string{"*"},
				"X-Request-Id":                []string{"abc"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"c1","is_bifrost_error":false,"extra_fields":{"routing_info":{"key":"primary"}},"choices":[]}`)),
		}
		out, err := sanitizingTransport{next: fakeRoundTripper{resp: resp}}.RoundTrip(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(out.Body)
		if strings.Contains(string(body), "extra_fields") || strings.Contains(string(body), "bifrost") || !strings.Contains(string(body), `"id":"c1"`) {
			t.Fatalf("unexpected body %s", body)
		}
		for name := range out.Header {
			if strings.Contains(strings.ToLower(name), "bifrost") || name == "Server" || strings.HasPrefix(name, "Access-Control-") {
				t.Fatalf("header %s was not removed", name)
			}
		}
		if out.Header.Get("X-Obot-LLM-Provider") != "openai" || out.Header.Get("X-Obot-LLM-Model") != "gpt-4.1" || out.Header.Get("X-Request-Id") != "abc" {
			t.Fatalf("unexpected headers %v", out.Header)
		}
	})

	t.Run("stream", func(t *testing.T) {
		stream := "data: {\"id\":\"c1\",\"extra_fields\":{\"routing_info\":{}},\"choices\":[]}\n\n" +
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"extra_fields\":{\"latency\":0}}}\n\n" +
			"event: ping\ndata: [DONE]\n\n"
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
		}
		out, err := sanitizingTransport{next: fakeRoundTripper{resp: resp}}.RoundTrip(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(out.Body)
		want := "data: {\"id\":\"c1\",\"choices\":[]}\n\n" +
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n" +
			"event: ping\ndata: [DONE]\n\n"
		if string(body) != want {
			t.Fatalf("got %q, want %q", body, want)
		}
	})
}

func (f fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return f.resp, nil
}

func TestWriteBudgetExceeded(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	exceeded := &client.LLMGatewayBudgetExceeded{
		Budget:  gatewaytypes.LLMGatewayBudget{Name: "Daily", PerUser: true},
		Limit:   gatewaytypes.LLMGatewayBudgetLimit{Metric: gatewaytypes.LLMGatewayBudgetMetricRequests, Amount: 3, Window: "1h"},
		Used:    3,
		ResetAt: now.Add(90 * time.Second),
	}
	tests := []struct {
		name     string
		path     string
		wantType string
	}{
		{name: "anthropic messages", path: "/api/llm-proxy/v1/messages", wantType: "rate_limit_error"},
		{name: "openai chat completions", path: "/api/llm-proxy/v1/chat/completions", wantType: "budget_exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeBudgetExceeded(rec, tt.path, exceeded, now)
			if rec.Code != http.StatusTooManyRequests || rec.Header().Get("X-Should-Retry") != "false" || rec.Header().Get("Retry-After") != "90" {
				t.Fatalf("unexpected response %d %v", rec.Code, rec.Header())
			}
			var body struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Type != tt.wantType || !strings.Contains(body.Error.Message, `"Daily"`) {
				t.Fatalf("unexpected body %s", rec.Body.String())
			}
		})
	}
}
