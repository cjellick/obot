package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	llmtypes "github.com/obot-platform/obot/pkg/llm"
)

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
			req := httptest.NewRequest(http.MethodPost, "/api/llm-proxy/bifrost/"+tt.path, nil)
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
