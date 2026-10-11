//go:build e2e

package llmgateway

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/obot-platform/obot/tests/e2e/harness"
	"github.com/tidwall/gjson"
)

// TestMetrics checks the gateway's Prometheus metrics after traffic on each replica.
func TestMetrics(t *testing.T) {
	u := env.newUser(t, "metrics")
	for _, r := range []*harness.Replica{env.A, env.B} {
		t.Run(r.Config.Name, func(t *testing.T) {
			callLLM(t, u.Gateway(r), apiChat, "openai/gpt-e2e-a", "metrics "+nonce("m"), false, nil).requireOK(t)
			callLLM(t, u.Gateway(r), apiMessages, "anthropic/claude-e2e-a", "metrics "+nonce("m"), true, nil).requireOK(t)
			admin := env.admin.On(r.BaseURL)
			var body string
			harness.Eventually(t, 30*time.Second, "gateway metrics on "+r.Config.Name, func() error {
				resp := admin.Do(t, http.MethodGet, "/api/llm-gateway/metrics", nil)
				if resp.Status != http.StatusOK {
					return fmt.Errorf("metrics: %s", resp)
				}
				body = string(resp.Body)
				for _, want := range []string{"obot_llm_gateway_", `provider="openai"`, `provider="anthropic"`} {
					if !strings.Contains(body, want) {
						return fmt.Errorf("metrics lack %s", want)
					}
				}
				return nil
			})
			if strings.Contains(body, "bifrost_") {
				t.Errorf("metrics expose the engine's name")
			}
			var series []string
			for line := range strings.SplitSeq(body, "\n") {
				if strings.HasPrefix(line, "obot_llm_gateway_") {
					name, _, _ := strings.Cut(line, "{")
					series = append(series, name)
				}
			}
			if len(series) == 0 {
				t.Fatalf("no obot_llm_gateway_ series:\n%s", truncate(body))
			}
			t.Logf("%d obot_llm_gateway_ samples, e.g. %s", len(series), series[0])
		})
	}
}

// TestUsageAndAudit checks that gateway requests appear in Obot's LLM audit log and token
// usage, attributed to the caller and the gateway model.
func TestUsageAndAudit(t *testing.T) {
	u := env.newUser(t, "audit")
	n := nonce("audit")
	callLLM(t, u.Gateway(env.A), apiChat, "openai/gpt-e2e-a", "audit "+n+" @@usage:123:45@@", false, nil).requireOK(t)
	callLLM(t, u.Gateway(env.B), apiMessages, "anthropic/claude-e2e-a", "audit "+n+" @@usage:7:3@@", true, nil).requireOK(t)

	t.Run("LLM audit log", func(t *testing.T) {
		var items []gjson.Result
		harness.Eventually(t, 60*time.Second, "audit log entries for the user", func() error {
			resp := env.admin.Do(t, http.MethodGet, "/api/llm-audit-logs?user_id="+url.QueryEscape(u.ID)+"&limit=50", nil)
			if resp.Status != http.StatusOK {
				return fmt.Errorf("audit logs: %s", resp)
			}
			items = gjson.GetBytes(resp.Body, "items").Array()
			if len(items) < 2 {
				return fmt.Errorf("%d entries", len(items))
			}
			return nil
		})
		seen := map[string]gjson.Result{}
		for _, item := range items {
			seen[item.Get("targetModel").String()] = item
		}
		chat, ok := seen["openai/gpt-e2e-a"]
		if !ok {
			t.Fatalf("no audit entry for openai/gpt-e2e-a: %v", items)
		}
		if chat.Get("inputTokens").Int() != 123 || chat.Get("outputTokens").Int() != 45 || chat.Get("modelProvider").String() != "llm-gateway-model-provider" ||
			!strings.Contains(chat.Get("requestPath").String(), "/api/llm-proxy/v1/chat/completions") || chat.Get("responseStatus").Int() != 200 {
			t.Fatalf("unexpected audit entry: %s", chat.Raw)
		}
		msgs, ok := seen["anthropic/claude-e2e-a"]
		if !ok || msgs.Get("inputTokens").Int() != 7 || msgs.Get("outputTokens").Int() != 3 {
			t.Fatalf("unexpected streamed audit entry: %s", msgs.Raw)
		}
		// Auditors see the request as the client sent it.
		id := chat.Get("id").String()
		detail := newAuditor(t).Session.Do(t, http.MethodGet, "/api/llm-audit-logs/detail/"+id, nil)
		if detail.Status != http.StatusOK || !strings.Contains(string(detail.Body), n) {
			t.Fatalf("audit detail: %s", detail)
		}
	})

	t.Run("token usage", func(t *testing.T) {
		harness.Eventually(t, 60*time.Second, "token usage for the user", func() error {
			resp := env.admin.Do(t, http.MethodGet, "/api/users/"+u.ID+"/token-usage", nil)
			if resp.Status != http.StatusOK {
				return fmt.Errorf("usage: %s", resp)
			}
			var in, out int64
			models := map[string]bool{}
			for _, item := range gjson.GetBytes(resp.Body, "items").Array() {
				in += item.Get("inputTokens").Int()
				out += item.Get("outputTokens").Int()
				models[item.Get("model").String()] = true
			}
			if in != 130 || out != 48 {
				return fmt.Errorf("usage %d in / %d out, want 130 / 48: %s", in, out, resp.Body)
			}
			return nil
		})
	})
}
