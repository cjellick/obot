//go:build e2e

package llmgateway

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/obot-platform/obot/tests/e2e/harness"
)

// TestResponseCache enables the gateway's response cache (backed by Redis) and checks that
// repeated identical requests are served from it, per user, and that clients can opt out.
func TestResponseCache(t *testing.T) {
	cachePlugin := map[string]any{
		"name":    "semantic_cache",
		"enabled": true,
		"config": map[string]any{
			"ttl":                            "5m",
			"cache_by_model":                 true,
			"cache_by_provider":              true,
			"conversation_history_threshold": 3,
			"vector_store_namespace":         "ObotE2ECache" + harness.RandomSuffix(),
			"default_cache_key":              "",
			"dimension":                      1,
		},
	}
	resp := env.admin.Do(t, http.MethodPost, "/api/llm-gateway/admin/plugins", cachePlugin)
	if resp.Status == http.StatusConflict {
		env.admin.Put(t, "/api/llm-gateway/admin/plugins/semantic_cache", cachePlugin, nil)
	} else if resp.Status >= 300 {
		t.Fatalf("enable the cache plugin: %s", resp)
	}
	t.Cleanup(func() {
		cachePlugin["enabled"] = false
		env.admin.Do(t, http.MethodPut, "/api/llm-gateway/admin/plugins/semantic_cache", cachePlugin)
	})

	u1, u2 := env.newUser(t, "cache1"), env.newUser(t, "cache2")
	model := "openai/gpt-e2e-a"

	// cachedCall sends the same request until one is answered without reaching the
	// provider, since the cache is written after the first response.
	upstreamCount := func(n string) int { return len(env.mock.InferenceRequests(n)) }
	waitForCacheHit := func(u *testUser, prompt, n string) *llmResult {
		t.Helper()
		var res *llmResult
		harness.Eventually(t, 30*time.Second, "a cache hit", func() error {
			before := upstreamCount(n)
			res = callLLM(t, u.Gateway(env.A), apiChat, model, prompt, false, nil).requireOK(t)
			if after := upstreamCount(n); after != before {
				return fmt.Errorf("the request reached the provider (%d upstream requests)", after)
			}
			return nil
		})
		return res
	}

	t.Run("repeated request is served from the cache", func(t *testing.T) {
		n := nonce("cache")
		prompt := "cache me " + n
		first := callLLM(t, u1.Gateway(env.A), apiChat, model, prompt, false, nil).requireOK(t)
		hit := waitForCacheHit(u1, prompt, n)
		if hit.Text != first.Text {
			t.Fatalf("cached reply %q differs from the original %q", hit.Text, first.Text)
		}
		before := upstreamCount(n)
		for range 3 {
			callLLM(t, u1.Gateway(env.A), apiChat, model, prompt, false, nil).requireOK(t)
		}
		if after := upstreamCount(n); after != before {
			t.Fatalf("repeated requests reached the provider: %d -> %d", before, after)
		}
		if before != 1 {
			t.Logf("the provider saw %d requests before the cache was written", before)
		}

		t.Run("another user is not served the cached response", func(t *testing.T) {
			before := upstreamCount(n)
			callLLM(t, u2.Gateway(env.A), apiChat, model, prompt, false, nil).requireOK(t)
			if after := upstreamCount(n); after != before+1 {
				t.Fatalf("another user's identical request didn't reach the provider: %d -> %d", before, after)
			}
		})
	})

	t.Run("no-store requests are not cached", func(t *testing.T) {
		n := nonce("nostore")
		prompt := "do not store " + n
		noStore := map[string]string{"X-Obot-Cache-No-Store": "true"}
		callLLM(t, u1.Gateway(env.A), apiChat, model, prompt, false, nil, noStore).requireOK(t)
		// Give a cache write time to land, if it (wrongly) happened, then repeat without
		// the header: the provider must be called again.
		time.Sleep(2 * time.Second)
		callLLM(t, u1.Gateway(env.A), apiChat, model, prompt, false, nil).requireOK(t)
		if got := upstreamCount(n); got != 2 {
			t.Fatalf("want 2 upstream requests after a no-store request and a repeat, got %d", got)
		}
		// The repeat was stored, so the next one is a hit.
		waitForCacheHit(u1, prompt, n)
	})
}
