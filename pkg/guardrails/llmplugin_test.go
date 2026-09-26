package guardrails

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/obot-platform/obot/pkg/bifrost"
	"github.com/obot-platform/obot/pkg/gateway/types"
)

const (
	leakedKey = "AKIAIOSFODNN7EXAMPLE"
)

// upstream is a mock OpenAI API whose answers contain a secret, and which records the
// prompts it receives.
type upstream struct {
	*httptest.Server
	lock    sync.Mutex
	prompts []string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.lock.Lock()
		u.prompts = append(u.prompts, string(body))
		u.lock.Unlock()
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gpt-4o-mini","object":"model"}]}`)
		case strings.HasSuffix(r.URL.Path, "/chat/completions") && req["stream"] == true:
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{"Your key ", "is " + leakedKey[:10], leakedKey[10:] + " ok"} {
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", part)
			}
			_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = fmt.Fprintf(w, `{"id":"c1","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Your key is %s ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`, leakedKey)
		default:
			http.NotFound(w, r)
		}
	}))
	return u
}

func (u *upstream) lastPrompt() string {
	u.lock.Lock()
	defer u.lock.Unlock()
	for _, p := range slices.Backward(u.prompts) {
		if strings.Contains(p, "messages") {
			return p
		}
	}
	return ""
}

func startGateway(t *testing.T, store *fakeStore) (*bifrost.Engine, *upstream) {
	t.Helper()
	up := newUpstream(t)
	t.Cleanup(up.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gw := bifrost.New()
	if err := gw.Start(ctx, bifrost.Options{DataDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := gw.Do(ctx, http.MethodPost, "/api/providers", map[string]any{
		"provider":       "openai",
		"network_config": map[string]any{"base_url": up.URL, "allow_private_network": true, "default_request_timeout_in_seconds": 30},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := gw.Do(ctx, http.MethodPost, "/api/providers/openai/keys", map[string]any{"name": "k", "value": "sk-test", "models": []string{"*"}, "weight": 1}, nil); err != nil {
		t.Fatal(err)
	}

	engine := New(ctx, store, nil)
	input, output := engine.Plugins()
	if err := gw.RegisterLLMPlugins(input, output); err != nil {
		t.Fatal(err)
	}
	// Request logging must only ever see checked content: the input plugin runs before it,
	// and the output plugin after it (so its response hook runs before logging's).
	order := gw.LLMPluginOrder()
	pos := map[string]int{}
	for i, name := range order {
		pos[name] = i
	}
	if order[0] != InputPluginName || pos[OutputPluginName] < pos["logging"] {
		t.Fatalf("guardrail plugins must surround request logging, got %v", order)
	}
	return gw, up
}

func call(t *testing.T, gw *bifrost.Engine, body string, bind bool) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bind {
		token, release := BindRequest(Identity{UserID: "alice"})
		defer release()
		req.Header.Set(HeaderRequestToken, token)
	}
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b)
}

func TestGatewayPluginsRedact(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{rule("redact-secrets", types.GuardrailActionRedact, "secrets")},
	}
	gw, up := startGateway(t, store)

	prompt := `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"my key is ` + leakedKey + `"}]}`

	// Input is redacted before it reaches the provider, and output before the caller.
	code, body := call(t, gw, prompt, true)
	if code != http.StatusOK || strings.Contains(body, leakedKey) || !strings.Contains(body, "[REDACTED:AWS_ACCESS_KEY_ID]") {
		t.Fatalf("non-streamed output not redacted: %d %s", code, body)
	}
	if p := up.lastPrompt(); strings.Contains(p, leakedKey) || !strings.Contains(p, "[REDACTED:AWS_ACCESS_KEY_ID]") {
		t.Fatalf("input not redacted before the provider: %s", p)
	}

	// A secret split across stream chunks is still found and redacted.
	code, body = call(t, gw, strings.Replace(prompt, `"messages"`, `"stream":true,"messages"`, 1), true)
	if code != http.StatusOK || strings.Contains(body, leakedKey) || strings.Contains(body, leakedKey[:10]) {
		t.Fatalf("streamed output not redacted: %d %s", code, body)
	}
	var streamed strings.Builder
	for line := range strings.SplitSeq(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) == nil {
			for _, c := range chunk.Choices {
				streamed.WriteString(c.Delta.Content)
			}
		}
	}
	if streamed.String() != "Your key is [REDACTED:AWS_ACCESS_KEY_ID] ok" {
		t.Fatalf("unexpected streamed text %q in %s", streamed.String(), body)
	}

	// Requests Obot didn't bind (e.g. the LLM judge's own calls) are not guarded.
	if _, body := call(t, gw, prompt, false); !strings.Contains(body, leakedKey) {
		t.Fatalf("unbound requests should pass untouched: %s", body)
	}

	// Log redaction: the gateway's request log never saw the secret.
	time.Sleep(3 * time.Second)
	var logs map[string]any
	if err := gw.Do(context.Background(), http.MethodGet, "/api/logs?limit=20&objects=chat_completion,chat_completion_stream", nil, &logs); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(logs["logs"])
	// Only the unbound request's input and output may contain the secret.
	if strings.Count(string(raw), leakedKey) > 2 {
		t.Fatalf("the request log contains unredacted guarded content: %s", raw)
	}
}

func TestGatewayPluginsBlock(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{rule("block-secrets", types.GuardrailActionBlock, "secrets")},
	}
	gw, up := startGateway(t, store)

	// Input block: the provider is never called.
	before := len(up.prompts)
	code, body := call(t, gw, `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"key `+leakedKey+`"}]}`, true)
	if code != http.StatusBadRequest || !strings.Contains(body, "block-secrets") {
		t.Fatalf("expected an input block, got %d %s", code, body)
	}
	if len(up.prompts) != before {
		t.Fatal("a blocked request must not reach the provider")
	}

	// Output block on a stream: none of the held content is delivered.
	code, body = call(t, gw, `{"model":"openai/gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hello"}]}`, true)
	if strings.Contains(body, leakedKey[:10]) || strings.Contains(body, "Your key") || !strings.Contains(body, "block-secrets") {
		t.Fatalf("expected the stream to end with a guardrail error and no content, got %d %s", code, body)
	}

	// Output block without streaming.
	code, body = call(t, gw, `{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`, true)
	if code != http.StatusBadRequest || strings.Contains(body, leakedKey) {
		t.Fatalf("expected an output block, got %d %s", code, body)
	}
}
