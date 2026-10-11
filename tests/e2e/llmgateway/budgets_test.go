//go:build e2e

package llmgateway

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/tests/e2e/harness"
)

type budgetLimit struct {
	Metric   string  `json:"metric"`
	Amount   float64 `json:"amount"`
	Window   string  `json:"window"`
	Calendar bool    `json:"calendar,omitempty"`
}

type budgetSubject struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type budget struct {
	ID              string          `json:"id,omitempty"`
	Name            string          `json:"name"`
	Enabled         bool            `json:"enabled"`
	PerUser         bool            `json:"perUser"`
	ExemptAdmins    bool            `json:"exemptAdmins"`
	UserTokenLimits bool            `json:"userTokenLimits,omitempty"`
	Limits          []budgetLimit   `json:"limits"`
	Models          []string        `json:"models,omitempty"`
	Subjects        []budgetSubject `json:"subjects"`
}

// createBudget creates a budget through the admin API and deletes it when the test ends.
func createBudget(t *testing.T, b budget) budget {
	t.Helper()
	b.Name = b.Name + " " + harness.RandomSuffix()
	b.Enabled = true
	var created budget
	env.admin.Post(t, "/api/llm-gateway/budgets", b, &created)
	if created.ID == "" {
		t.Fatalf("budget created without an ID")
	}
	t.Cleanup(func() {
		env.admin.Do(t, http.MethodDelete, "/api/llm-gateway/budgets/"+created.ID, nil)
	})
	return created
}

func users(us ...*testUser) []budgetSubject {
	out := make([]budgetSubject, 0, len(us))
	for _, u := range us {
		out = append(out, budgetSubject{Type: "user", ID: u.ID})
	}
	return out
}

// expectAllowed makes a request that must succeed.
func expectAllowed(t *testing.T, u *testUser, r *harness.Replica, model, why string) {
	t.Helper()
	res := callLLM(t, u.Gateway(r), apiChat, model, "budget "+nonce("b"), false, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("%s: want 200 on %s, got %d: %s", why, r.Config.Name, res.Status, truncate(res.Body))
	}
}

// expectExhausted makes a request that must be refused by budget b.
func expectExhausted(t *testing.T, u *testUser, r *harness.Replica, model string, b budget, why string) *llmResult {
	t.Helper()
	n := nonce("over")
	res := callLLM(t, u.Gateway(r), apiChat, model, "budget "+n, false, nil)
	if res.Status != http.StatusTooManyRequests {
		t.Fatalf("%s: want 429 on %s, got %d: %s", why, r.Config.Name, res.Status, truncate(res.Body))
	}
	if !strings.Contains(res.Body, b.Name) {
		t.Fatalf("%s: the 429 doesn't name budget %q: %s", why, b.Name, res.Body)
	}
	if len(env.mock.InferenceRequests(n)) != 0 {
		t.Fatalf("%s: a request refused by a budget reached the provider", why)
	}
	return res
}

func TestBudgets(t *testing.T) {
	replicas := []*harness.Replica{env.A, env.B}

	t.Run("per-user request limit is exact across replicas", func(t *testing.T) {
		u, other := env.newUser(t, "budget-req"), env.newUser(t, "budget-req-other")
		const n = 6
		b := createBudget(t, budget{
			Name:    "e2e requests",
			PerUser: true,
			Limits:  []budgetLimit{{Metric: "requests", Amount: n, Window: "1h"}},
			// The other user is assigned too: per-user budgets give each their own allowance.
			Subjects: users(u, other),
		})
		for i := range n {
			expectAllowed(t, u, replicas[i%2], "openai/gpt-e2e-a", fmt.Sprintf("request %d of %d", i+1, n))
		}
		res := expectExhausted(t, u, replicas[n%2], "openai/gpt-e2e-a", b, "request over the limit")
		if !strings.Contains(res.Body, "6 of 6") {
			t.Errorf("want the 429 to report 6 of 6 requests used: %s", res.Body)
		}
		if res.Header.Get("Retry-After") == "" {
			t.Errorf("the 429 has no Retry-After header")
		}
		// Clients such as Claude Code would otherwise retry it until they give up.
		if got := res.Header.Get("X-Should-Retry"); got != "false" {
			t.Errorf("the 429 has X-Should-Retry %q, want false", got)
		}
		expectExhausted(t, u, replicas[(n+1)%2], "openai/gpt-e2e-a", b, "request over the limit on the other replica")
		// Anthropic clients get the error in their format.
		msg := callLLM(t, u.Gateway(env.A), apiMessages, "anthropic/claude-e2e-a", "hi", false, nil)
		if msg.Status != http.StatusTooManyRequests || msg.ErrorMessage == "" || !strings.Contains(msg.ErrorMessage, b.Name) {
			t.Errorf("want an Anthropic-format 429 naming the budget, got %d: %s", msg.Status, msg.Body)
		}
		// The other user has their own allowance.
		expectAllowed(t, other, env.B, "openai/gpt-e2e-a", "another user's per-user allowance")

		var usage struct {
			Items []struct {
				UserID string    `json:"userID"`
				Used   []float64 `json:"used"`
			} `json:"items"`
		}
		env.admin.Get(t, "/api/llm-gateway/budgets/"+b.ID+"/usage", &usage)
		found := false
		for _, item := range usage.Items {
			if item.UserID == u.ID {
				found = true
				if len(item.Used) != 1 || item.Used[0] != n {
					t.Errorf("usage report for the user: %v, want [%d]", item.Used, n)
				}
			}
		}
		if !found {
			t.Errorf("usage report has no entry for the user: %+v", usage.Items)
		}
	})

	t.Run("shared budget across two users", func(t *testing.T) {
		u1, u2 := env.newUser(t, "budget-shared1"), env.newUser(t, "budget-shared2")
		b := createBudget(t, budget{
			Name:     "e2e shared",
			PerUser:  false,
			Limits:   []budgetLimit{{Metric: "requests", Amount: 4, Window: "1d"}},
			Subjects: users(u1, u2),
		})
		expectAllowed(t, u1, env.A, "openai/gpt-e2e-a", "u1 first")
		expectAllowed(t, u2, env.B, "openai/gpt-e2e-a", "u2 first")
		expectAllowed(t, u1, env.B, "anthropic/claude-e2e-a", "u1 second")
		expectAllowed(t, u2, env.A, "anthropic/claude-e2e-a", "u2 second")
		res := expectExhausted(t, u1, env.A, "openai/gpt-e2e-a", b, "u1 after the pool is used")
		if !strings.Contains(res.Body, "shared") {
			t.Errorf("want the 429 to say the budget is shared: %s", res.Body)
		}
		expectExhausted(t, u2, env.B, "openai/gpt-e2e-a", b, "u2 after the pool is used")
	})

	t.Run("token budget", func(t *testing.T) {
		u := env.newUser(t, "budget-tokens")
		// Each mock response reports 10 input and 5 output tokens.
		b := createBudget(t, budget{
			Name:     "e2e tokens",
			PerUser:  true,
			Limits:   []budgetLimit{{Metric: "tokens", Amount: 30, Window: "1d", Calendar: true}},
			Subjects: users(u),
		})
		expectAllowed(t, u, env.A, "openai/gpt-e2e-a", "0 of 30 tokens used")
		expectAllowed(t, u, env.B, "anthropic/claude-e2e-a", "15 of 30 tokens used")
		res := expectExhausted(t, u, env.A, "openai/gpt-e2e-a", b, "30 of 30 tokens used")
		if !strings.Contains(res.Body, "30 of 30") {
			t.Errorf("want the 429 to report 30 of 30 tokens: %s", res.Body)
		}
	})

	t.Run("output token budget counts streamed usage", func(t *testing.T) {
		u := env.newUser(t, "budget-output")
		b := createBudget(t, budget{
			Name:     "e2e output tokens",
			PerUser:  true,
			Limits:   []budgetLimit{{Metric: "output_tokens", Amount: 10, Window: "6h"}},
			Subjects: users(u),
		})
		for i, api := range []string{apiResponses, apiMessages} {
			callLLM(t, u.Gateway(replicas[i]), api, "openai/gpt-e2e-a", "stream "+nonce("s"), true, nil).requireOK(t)
		}
		expectExhausted(t, u, env.A, "openai/gpt-e2e-a", b, "10 of 10 output tokens used by streams")
	})

	t.Run("spend budget", func(t *testing.T) {
		u := env.newUser(t, "budget-spend")
		target := "anthropic/claude-e2e-b"
		id := env.modelID(t, target)
		// $0.10 per token: each request (10 in, 5 out) costs $1.50.
		var model types.Model
		env.admin.Get(t, "/api/models/"+id, &model)
		manifest := model.ModelManifest
		manifest.OverrideCost = &types.ModelCost{Input: 100000, Output: 100000}
		env.admin.Put(t, "/api/models/"+id, manifest, nil)
		t.Cleanup(func() {
			manifest.OverrideCost = nil
			env.admin.Put(t, "/api/models/"+id, manifest, nil)
		})
		for _, r := range replicas {
			harness.Eventually(t, 30*time.Second, "override cost on "+r.Config.Name, func() error {
				var m types.Model
				env.admin.On(r.BaseURL).Get(t, "/api/models/"+id, &m)
				if m.OverrideCost == nil || m.OverrideCost.Input != 100000 {
					return fmt.Errorf("override cost %+v", m.OverrideCost)
				}
				return nil
			})
		}

		b := createBudget(t, budget{
			Name:     "e2e spend",
			PerUser:  true,
			Limits:   []budgetLimit{{Metric: "spend", Amount: 3, Window: "1M", Calendar: true}},
			Subjects: users(u),
		})
		expectAllowed(t, u, env.A, target, "$0 of $3 spent")
		expectAllowed(t, u, env.B, target, "$1.50 of $3 spent")
		res := expectExhausted(t, u, env.A, target, b, "$3 of $3 spent")
		if !strings.Contains(res.Body, "$3.00 of $3.00") {
			t.Errorf("want the 429 to report $3.00 of $3.00: %s", res.Body)
		}
		var usage struct {
			Items []struct {
				Used []float64 `json:"used"`
			} `json:"items"`
		}
		env.admin.Get(t, "/api/llm-gateway/budgets/"+b.ID+"/usage", &usage)
		if len(usage.Items) != 1 || len(usage.Items[0].Used) != 1 || math.Abs(usage.Items[0].Used[0]-3) > 1e-9 {
			t.Errorf("spend usage report: %+v, want $3", usage.Items)
		}
	})

	t.Run("admin exemption", func(t *testing.T) {
		u := env.newUser(t, "budget-exempt")
		b := createBudget(t, budget{
			Name:         "e2e exempt admins",
			PerUser:      true,
			ExemptAdmins: true,
			Limits:       []budgetLimit{{Metric: "requests", Amount: 1, Window: "1h"}},
			Subjects:     users(env.adminUser, u),
		})
		for i := range 3 {
			expectAllowed(t, env.adminUser, replicas[i%2], "openai/gpt-e2e-a", "an exempt admin")
		}
		expectAllowed(t, u, env.A, "openai/gpt-e2e-a", "a user's first request")
		expectExhausted(t, u, env.B, "openai/gpt-e2e-a", b, "a user's second request")

		// Without the exemption, the same admin is limited.
		nb := createBudget(t, budget{
			Name:     "e2e admins not exempt",
			PerUser:  true,
			Limits:   []budgetLimit{{Metric: "requests", Amount: 1, Window: "1h"}},
			Models:   []string{env.modelID(t, "openai/gpt-e2e-b")},
			Subjects: users(env.adminUser),
		})
		expectAllowed(t, env.adminUser, env.A, "openai/gpt-e2e-b", "an admin's first request")
		expectExhausted(t, env.adminUser, env.B, "openai/gpt-e2e-b", nb, "an admin who isn't exempt")
	})

	t.Run("model-scoped budget", func(t *testing.T) {
		u := env.newUser(t, "budget-model")
		b := createBudget(t, budget{
			Name:     "e2e model scoped",
			PerUser:  true,
			Limits:   []budgetLimit{{Metric: "requests", Amount: 1, Window: "1h"}},
			Models:   []string{env.modelID(t, "anthropic/claude-e2e-a")},
			Subjects: users(u),
		})
		expectAllowed(t, u, env.A, "anthropic/claude-e2e-a", "first request to the budgeted model")
		expectExhausted(t, u, env.B, "anthropic/claude-e2e-a", b, "second request to the budgeted model")
		// A bare model ID resolves to the same model and is limited too.
		expectExhausted(t, u, env.A, "claude-e2e-a", b, "the budgeted model by its bare ID")
		for i := range 3 {
			expectAllowed(t, u, replicas[i%2], "openai/gpt-e2e-a", "another model")
		}
	})

	t.Run("userTokenLimits uses personal daily limits", func(t *testing.T) {
		limited, unlimited, plain := env.newUser(t, "budget-personal"), env.newUser(t, "budget-personal-off"), env.newUser(t, "budget-personal-none")
		// Obot has no API for personal token limits; they are set on the user record.
		for id, limit := range map[string]int{limited.ID: 15, unlimited.ID: -1} {
			if _, err := env.cluster.PG.Exec(t.Context(), env.cluster.DB, fmt.Sprintf("update users set daily_input_tokens_limit = %d where id = %s", limit, id)); err != nil {
				t.Fatalf("set personal limit: %v", err)
			}
		}
		b := createBudget(t, budget{
			Name:            "e2e personal limits",
			PerUser:         true,
			UserTokenLimits: true,
			Limits:          []budgetLimit{{Metric: "input_tokens", Amount: 25, Window: "1d", Calendar: true}},
			Subjects:        users(limited, unlimited, plain),
		})
		// 15 personal input tokens: two requests of 10.
		expectAllowed(t, limited, env.A, "openai/gpt-e2e-a", "0 of 15 personal input tokens")
		expectAllowed(t, limited, env.B, "openai/gpt-e2e-a", "10 of 15 personal input tokens")
		// The migrated "Daily token limit" budget also uses personal limits and applies to
		// everyone, so either budget may report it.
		res := callLLM(t, limited.Gateway(env.A), apiChat, "openai/gpt-e2e-a", "over "+nonce("p"), false, nil)
		if res.Status != http.StatusTooManyRequests || !strings.Contains(res.Body, "20 of 15 input tokens") ||
			!strings.Contains(res.Body, b.Name) && !strings.Contains(res.Body, "Daily token limit") {
			t.Fatalf("want a 429 for 20 of 15 personal input tokens, got %d: %s", res.Status, res.Body)
		}
		// A negative personal limit removes the budget's limit.
		for i := range 4 {
			expectAllowed(t, unlimited, replicas[i%2], "openai/gpt-e2e-a", "a user with no personal limit")
		}
		// Without a personal limit, the budget's own 25 applies: three requests of 10.
		for i := range 3 {
			expectAllowed(t, plain, replicas[i%2], "openai/gpt-e2e-a", "the budget's own limit")
		}
		expectExhausted(t, plain, env.B, "openai/gpt-e2e-a", b, "30 of 25 input tokens")
	})

	t.Run("migrated daily token limit budget exists", func(t *testing.T) {
		var list struct {
			Items []budget `json:"items"`
		}
		env.admin.Get(t, "/api/llm-gateway/budgets", &list)
		for _, b := range list.Items {
			if b.ID == "daily-token-limit" {
				if !b.UserTokenLimits || !b.ExemptAdmins || !b.PerUser {
					t.Fatalf("default budget: %+v", b)
				}
				return
			}
		}
		t.Fatalf("no default daily token limit budget in %+v", list.Items)
	})
}
