package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mockOpenAI(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("upstream got %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model","owned_by":"openai"},{"id":"gpt-4.1","object":"model","owned_by":"openai"}]}`)
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["stream"] == true {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`+"\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from mock"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`)
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-4o-mini","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from responses","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

func do(t *testing.T, e *Engine, method, path, body string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderLogUserID, "user-123")
	e.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b)
}

func TestEngineE2E(t *testing.T) {
	up := mockOpenAI(t)
	defer up.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e := New()
	if err := e.Start(ctx, Options{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	code, body := do(t, e, "POST", "/api/providers", `{"provider":"openai","network_config":{"base_url":"`+up.URL+`","allow_private_network":true,"default_request_timeout_in_seconds":30}}`)
	t.Logf("create provider: %d %s", code, body)
	code, body = do(t, e, "POST", "/api/providers/openai/keys", `{"name":"k1","value":"sk-test-123","models":["*"],"weight":1}`)
	t.Logf("create key: %d %s", code, body)
	time.Sleep(time.Second)

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/models", ""},
		{"GET", "/api/providers/openai", ""},
		{"POST", "/v1/chat/completions", `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`},
		{"POST", "/v1/chat/completions", `{"model":"openai/gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`},
		{"POST", "/anthropic/v1/messages", `{"model":"openai/gpt-4o-mini","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`},
		{"POST", "/openai/v1/responses", `{"model":"openai/gpt-4o-mini","input":"hi"}`},
		{"POST", "/v1/responses", `{"model":"openai/gpt-4o-mini","input":"hi"}`},
	} {
		code, body := do(t, e, tc.method, tc.path, tc.body)
		if len(body) > 600 {
			body = body[:600]
		}
		t.Logf("%s %s -> %d %s", tc.method, tc.path, code, body)
	}
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		code, body = do(t, e, "GET", "/api/logs?limit=10", "")
		if strings.Contains(body, `"has_logs":true`) {
			break
		}
	}
	if len(body) > 1500 {
		body = body[:1500]
	}
	t.Logf("logs: %d %s", code, body)
}
