//go:build e2e

package llmgateway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/obot-platform/obot/tests/e2e/harness"
	"github.com/tidwall/gjson"
)

// guardrailRule is the rule API's shape.
type guardrailRule struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name"`
	Enabled      bool            `json:"enabled"`
	Target       string          `json:"target,omitempty"`
	ApplyTo      string          `json:"applyTo"`
	Action       string          `json:"action"`
	Condition    string          `json:"condition,omitempty"`
	FailClosed   bool            `json:"failClosed,omitempty"`
	BlockMessage string          `json:"blockMessage,omitempty"`
	InputScope   string          `json:"inputScope,omitempty"`
	ProfileIDs   []string        `json:"profileIDs"`
	Subjects     []budgetSubject `json:"subjects"`
}

type guardrailEvent struct {
	ID          int             `json:"id"`
	RuleID      string          `json:"ruleID"`
	RuleName    string          `json:"ruleName"`
	ProfileType string          `json:"profileType"`
	Target      string          `json:"target"`
	Stage       string          `json:"stage"`
	Outcome     string          `json:"outcome"`
	UserID      string          `json:"userID"`
	Model       string          `json:"model"`
	Resource    string          `json:"resource"`
	Findings    json.RawMessage `json:"findings"`
	Error       string          `json:"error"`
}

// githubToken returns a random, well-formed GitHub token for the secrets detector.
func githubToken() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var sb strings.Builder
	sb.WriteString("ghp_")
	for range 36 {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		sb.WriteByte(alphabet[n.Int64()])
	}
	return sb.String()
}

// createProfile creates a guardrail detector and deletes it when the test ends.
func createProfile(t *testing.T, typ string, config map[string]any) string {
	t.Helper()
	var p struct {
		ID string `json:"id"`
	}
	env.admin.Post(t, "/api/llm-gateway/guardrails/profiles", map[string]any{
		"name":   "e2e " + typ + " " + harness.RandomSuffix(),
		"type":   typ,
		"config": config,
	}, &p)
	if p.ID == "" {
		t.Fatal("profile created without an ID")
	}
	t.Cleanup(func() {
		env.admin.Do(t, http.MethodDelete, "/api/llm-gateway/guardrails/profiles/"+p.ID, nil)
	})
	return p.ID
}

// createRule creates an LLM guardrail rule and deletes it when the test ends. Cleanups run
// in reverse order, so the rule goes before the profiles it uses.
func createRule(t *testing.T, r guardrailRule) guardrailRule {
	t.Helper()
	r.Name = r.Name + " " + harness.RandomSuffix()
	r.Enabled = true
	var created guardrailRule
	env.admin.Post(t, "/api/llm-gateway/guardrails/rules", r, &created)
	if created.ID == "" {
		t.Fatal("rule created without an ID")
	}
	t.Cleanup(func() {
		env.admin.Do(t, http.MethodDelete, "/api/llm-gateway/guardrails/rules/"+created.ID, nil)
	})
	return created
}

// waitForEvents polls the events API until filter returns at least min events.
func waitForEvents(t *testing.T, query url.Values, atLeast int) ([]guardrailEvent, string) {
	t.Helper()
	var (
		events []guardrailEvent
		raw    string
	)
	harness.Eventually(t, 30*time.Second, fmt.Sprintf("%d guardrail events for %s", atLeast, query.Encode()), func() error {
		resp := env.admin.Do(t, http.MethodGet, "/api/llm-gateway/guardrails/events?"+query.Encode(), nil)
		if resp.Status != http.StatusOK {
			return fmt.Errorf("events: %s", resp)
		}
		var list struct {
			Items []guardrailEvent `json:"items"`
		}
		resp.JSON(t, &list)
		events, raw = list.Items, string(resp.Body)
		if len(events) < atLeast {
			return fmt.Errorf("got %d events", len(events))
		}
		return nil
	})
	return events, raw
}

// requireGuardrailBlock checks a blocked call: HTTP 400 with a guardrail_intervention error,
// or, for a stream that had already started, an error event of that type and no content.
func requireGuardrailBlock(t *testing.T, res *llmResult, secret string) {
	t.Helper()
	if strings.Contains(res.Body, secret) {
		t.Fatalf("a blocked response contains the secret: %s", truncate(res.Body))
	}
	if res.Status == http.StatusBadRequest {
		if !strings.Contains(res.Body, "guardrail_intervention") {
			t.Fatalf("want a guardrail_intervention error, got: %s", truncate(res.Body))
		}
		return
	}
	if res.Status == http.StatusOK && strings.Contains(res.Body, "guardrail_intervention") && res.Text == "" && res.ToolArgs == "" {
		return
	}
	t.Fatalf("want HTTP 400 guardrail_intervention, got %d (text %q): %s", res.Status, res.Text, truncate(res.Body))
}

func TestGuardrails(t *testing.T) {
	secrets := createProfile(t, "secrets", map[string]any{"generic": false})
	pii := createProfile(t, "regex", map[string]any{"piiTemplate": []string{"EMAIL"}})

	t.Run("input redaction", func(t *testing.T) {
		u := env.newUser(t, "gr-input")
		rule := createRule(t, guardrailRule{Name: "e2e input redact", ApplyTo: "input", Action: "redact",
			ProfileIDs: []string{secrets, pii}, Subjects: users(u)})
		for _, api := range allAPIs {
			for _, model := range []string{"openai/gpt-e2e-a", "anthropic/claude-e2e-a"} {
				secret, email := githubToken(), "jane."+harness.RandomSuffix()+"@example.org"
				n := nonce("in")
				callLLM(t, u.Gateway(env.A), api, model, fmt.Sprintf("%s my token is %s and mail %s", n, secret, email), false, nil).requireOK(t)
				reqs := env.mock.InferenceRequests(n)
				if len(reqs) != 1 {
					t.Fatalf("%s %s: want 1 upstream request, got %d", api, model, len(reqs))
				}
				body := reqs[0].Body
				if strings.Contains(body, secret) || strings.Contains(body, email) {
					t.Fatalf("%s %s: the provider received unredacted input: %s", api, model, body)
				}
				if !strings.Contains(body, "[REDACTED:GITHUB_TOKEN]") || !strings.Contains(body, "[REDACTED:EMAIL]") {
					t.Fatalf("%s %s: want redaction labels upstream: %s", api, model, body)
				}
			}
		}
		events, raw := waitForEvents(t, url.Values{"ruleID": {rule.ID}, "outcome": {"redacted"}}, 6)
		for _, e := range events {
			if e.UserID != u.ID || e.Stage != "input" || e.Target != "llm" || e.Model == "" {
				t.Fatalf("unexpected event %+v", e)
			}
		}
		if strings.Contains(raw, "ghp_") || strings.Contains(raw, "@example.org") {
			t.Fatalf("guardrail events contain the offending content: %s", raw)
		}
	})

	t.Run("output redaction", func(t *testing.T) {
		u := env.newUser(t, "gr-output")
		createRule(t, guardrailRule{Name: "e2e output redact", ApplyTo: "output", Action: "redact",
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		for _, api := range allAPIs {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t", api, stream), func(t *testing.T) {
					secret := githubToken()
					n := nonce("out")
					reply := "here it is: " + secret + " done " + n
					// Five-byte chunks split the secret across many stream events.
					res := callLLM(t, u.Gateway(env.A), api, "openai/gpt-e2e-a", n+" "+harness.Reply(reply)+" @@chunk:5@@", stream, nil).requireOK(t)
					if strings.Contains(res.Body, secret) || strings.Contains(res.Text, secret) {
						t.Fatalf("the client received the secret: %s", truncate(res.Body))
					}
					want := "here it is: [REDACTED:GITHUB_TOKEN] done " + n
					if res.Text != want {
						t.Fatalf("want redacted text %q, got %q", want, res.Text)
					}
				})
			}
		}
		t.Run("tool call arguments", func(t *testing.T) {
			for _, api := range allAPIs {
				for _, stream := range []bool{false, true} {
					secret := githubToken()
					n := nonce("tool")
					res := callLLM(t, u.Gateway(env.A), api, "anthropic/claude-e2e-a",
						n+" "+harness.ToolCall("deploy", map[string]any{"token": secret, "note": n})+" @@chunk:7@@", stream, map[string]any{
							"tools": toolsFor(api),
						}).requireOK(t)
					if strings.Contains(res.Body, secret) {
						t.Fatalf("%s stream=%t: the client received the secret in a tool call: %s", api, stream, truncate(res.Body))
					}
					if !strings.Contains(res.ToolArgs, "REDACTED") {
						t.Fatalf("%s stream=%t: want redacted tool call arguments, got %q: %s", api, stream, res.ToolArgs, truncate(res.Body))
					}
				}
			}
		})
	})

	t.Run("block", func(t *testing.T) {
		u := env.newUser(t, "gr-block")
		message := "Blocked by the e2e policy " + harness.RandomSuffix()
		createRule(t, guardrailRule{Name: "e2e block", ApplyTo: "both", Action: "block", BlockMessage: message,
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		for _, api := range allAPIs {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("input/%s/stream=%t", api, stream), func(t *testing.T) {
					secret := githubToken()
					n := nonce("blockin")
					res := callLLM(t, u.Gateway(env.A), api, "openai/gpt-e2e-a", n+" "+secret, stream, nil)
					requireGuardrailBlock(t, res, secret)
					if res.Status != http.StatusBadRequest {
						t.Fatalf("a blocked request must get HTTP 400, got %d", res.Status)
					}
					if !strings.Contains(res.Body, message) {
						t.Fatalf("want the custom block message: %s", res.Body)
					}
					if len(env.mock.InferenceRequests(n)) != 0 {
						t.Fatal("a blocked request reached the provider")
					}
				})
				t.Run(fmt.Sprintf("output/%s/stream=%t", api, stream), func(t *testing.T) {
					secret := githubToken()
					n := nonce("blockout")
					res := callLLM(t, u.Gateway(env.A), api, "anthropic/claude-e2e-a", n+" "+harness.Reply("leaking "+secret+" now")+" @@chunk:5@@", stream, nil)
					requireGuardrailBlock(t, res, secret)
					if !strings.Contains(res.Body, message) {
						t.Fatalf("want the custom block message: %s", truncate(res.Body))
					}
				})
			}
		}
	})

	t.Run("log only", func(t *testing.T) {
		u := env.newUser(t, "gr-log")
		rule := createRule(t, guardrailRule{Name: "e2e log", ApplyTo: "both", Action: "log",
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		secret := githubToken()
		n := nonce("log")
		res := callLLM(t, u.Gateway(env.A), apiChat, "openai/gpt-e2e-a", n+" "+secret+" "+harness.Reply("echo "+secret), true, nil).requireOK(t)
		if res.Text != "echo "+secret {
			t.Fatalf("a log-only rule changed the response: %q", res.Text)
		}
		if reqs := env.mock.InferenceRequests(n); len(reqs) != 1 || !strings.Contains(reqs[0].Body, secret) {
			t.Fatal("a log-only rule changed the request")
		}
		events, raw := waitForEvents(t, url.Values{"ruleID": {rule.ID}}, 2)
		for _, e := range events {
			if e.Outcome != "logged" {
				t.Fatalf("want logged events, got %+v", e)
			}
		}
		if strings.Contains(raw, secret) {
			t.Fatalf("events contain the secret: %s", raw)
		}
	})

	t.Run("subjects", func(t *testing.T) {
		member, target, bystander := env.newUser(t, "gr-member"), env.newUser(t, "gr-target"), env.newUser(t, "gr-bystander")
		group := "e2e-group-" + harness.RandomSuffix()
		// Local auth has no groups; add one as an auth provider would.
		if _, err := env.cluster.PG.Exec(t.Context(), env.cluster.DB, fmt.Sprintf(
			"insert into groups (id, auth_provider_name, auth_provider_namespace, name) values ('%s', 'local-auth-provider', 'default', '%s');"+
				"insert into group_memberships (user_id, group_id, created_at) values (%s, '%s', now())",
			group, group, member.ID, group)); err != nil {
			t.Fatalf("add group: %v", err)
		}
		createRule(t, guardrailRule{Name: "e2e group rule", ApplyTo: "input", Action: "redact",
			ProfileIDs: []string{secrets}, Subjects: []budgetSubject{{Type: "group", ID: group}}})
		createRule(t, guardrailRule{Name: "e2e user rule", ApplyTo: "input", Action: "redact",
			ProfileIDs: []string{secrets}, Subjects: users(target)})
		for _, tc := range []struct {
			who      *testUser
			redacted bool
		}{
			{who: member, redacted: true},
			{who: target, redacted: true},
			{who: bystander, redacted: false},
		} {
			secret := githubToken()
			n := nonce("subj")
			callLLM(t, tc.who.Gateway(env.A), apiChat, "openai/gpt-e2e-a", n+" "+secret, false, nil).requireOK(t)
			reqs := env.mock.InferenceRequests(n)
			if len(reqs) != 1 {
				t.Fatalf("%s: want 1 upstream request", tc.who.Email)
			}
			if got := !strings.Contains(reqs[0].Body, secret); got != tc.redacted {
				t.Fatalf("%s: redacted=%t, want %t", tc.who.Email, got, tc.redacted)
			}
		}
	})

	t.Run("condition", func(t *testing.T) {
		u := env.newUser(t, "gr-cond")
		createRule(t, guardrailRule{Name: "e2e condition", ApplyTo: "input", Action: "redact",
			Condition:  `model == "openai/gpt-e2e-b"`,
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		for model, redacted := range map[string]bool{"openai/gpt-e2e-b": true, "gpt-e2e-b": true, "openai/gpt-e2e-a": false} {
			secret := githubToken()
			n := nonce("cond")
			callLLM(t, u.Gateway(env.A), apiChat, model, n+" "+secret, false, nil).requireOK(t)
			reqs := env.mock.InferenceRequests(n)
			if len(reqs) != 1 {
				t.Fatalf("%s: want 1 upstream request", model)
			}
			if got := !strings.Contains(reqs[0].Body, secret); got != redacted {
				t.Fatalf("%s: redacted=%t, want %t", model, got, redacted)
			}
		}

		// A condition that fails at runtime (a missing header) skips the rule, records an
		// error event, and blocks when the rule fails closed.
		open := createRule(t, guardrailRule{Name: "e2e condition error", ApplyTo: "input", Action: "redact",
			Condition: `headers["x-e2e-missing"] == "1"`, ProfileIDs: []string{secrets}, Subjects: users(u)})
		secret := githubToken()
		n := nonce("conderr")
		callLLM(t, u.Gateway(env.A), apiChat, "openai/gpt-e2e-a", n+" "+secret, false, nil).requireOK(t)
		if reqs := env.mock.InferenceRequests(n); len(reqs) != 1 || !strings.Contains(reqs[0].Body, secret) {
			t.Fatal("a rule whose condition failed was applied")
		}
		waitForEvents(t, url.Values{"ruleID": {open.ID}, "outcome": {"error"}}, 1)

		u2 := env.newUser(t, "gr-cond-closed")
		createRule(t, guardrailRule{Name: "e2e condition error closed", ApplyTo: "input", Action: "redact", FailClosed: true,
			Condition: `headers["x-e2e-missing"] == "1"`, ProfileIDs: []string{secrets}, Subjects: users(u2)})
		requireGuardrailBlock(t, callLLM(t, u2.Gateway(env.A), apiChat, "openai/gpt-e2e-a", "hi "+nonce("c"), false, nil), "ghp_")
	})

	t.Run("fail closed with an erroring detector", func(t *testing.T) {
		dead := createProfile(t, "presidio", map[string]any{"analyzerURL": "http://127.0.0.1:1"})
		closedUser, openUser := env.newUser(t, "gr-closed"), env.newUser(t, "gr-open")
		createRule(t, guardrailRule{Name: "e2e fail closed", ApplyTo: "input", Action: "redact", FailClosed: true,
			ProfileIDs: []string{dead}, Subjects: users(closedUser)})
		openRule := createRule(t, guardrailRule{Name: "e2e fail open", ApplyTo: "input", Action: "redact",
			ProfileIDs: []string{dead}, Subjects: users(openUser)})

		n := nonce("closed")
		res := callLLM(t, closedUser.Gateway(env.A), apiChat, "openai/gpt-e2e-a", "hello "+n, false, nil)
		requireGuardrailBlock(t, res, "never")
		if res.Status != http.StatusBadRequest || len(env.mock.InferenceRequests(n)) != 0 {
			t.Fatalf("a fail-closed rule with a broken detector let the request through: %d", res.Status)
		}
		n = nonce("open")
		callLLM(t, openUser.Gateway(env.A), apiChat, "openai/gpt-e2e-a", "hello "+n, false, nil).requireOK(t)
		if len(env.mock.InferenceRequests(n)) != 1 {
			t.Fatal("a fail-open rule with a broken detector blocked the request")
		}
		events, _ := waitForEvents(t, url.Values{"ruleID": {openRule.ID}, "outcome": {"error"}}, 1)
		if events[0].Error == "" {
			t.Fatalf("an error event has no error: %+v", events[0])
		}
	})

	t.Run("gateway request log never contains the secret", func(t *testing.T) {
		u := env.newUser(t, "gr-reqlog")
		createRule(t, guardrailRule{Name: "e2e reqlog", ApplyTo: "both", Action: "redact",
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		inSecret, outSecret := githubToken(), githubToken()
		var ns []string
		for _, stream := range []bool{false, true} {
			n := nonce("reqlog")
			ns = append(ns, n)
			callLLM(t, u.Gateway(env.A), apiChat, "openai/gpt-e2e-a",
				n+" "+inSecret+" "+harness.Reply("out "+outSecret)+" @@chunk:5@@", stream, nil).requireOK(t)
		}
		for _, n := range ns {
			var logs struct {
				Logs []map[string]any `json:"logs"`
			}
			harness.Eventually(t, 30*time.Second, "the gateway request log for "+n, func() error {
				resp := env.admin.Do(t, http.MethodGet, "/api/llm-gateway/admin/logs?limit=20&content_search="+url.QueryEscape(n), nil)
				if resp.Status != http.StatusOK {
					return fmt.Errorf("logs: %s", resp)
				}
				resp.JSON(t, &logs)
				if len(logs.Logs) == 0 {
					return fmt.Errorf("no log yet")
				}
				// Stream logs are completed asynchronously.
				if s, _ := logs.Logs[0]["status"].(string); s == "processing" {
					return fmt.Errorf("log still processing")
				}
				return nil
			})
			for _, l := range logs.Logs {
				id, _ := l["id"].(string)
				detail := env.admin.Do(t, http.MethodGet, "/api/llm-gateway/admin/logs/"+url.PathEscape(id), nil)
				for _, body := range []string{mustJSON(l), string(detail.Body)} {
					if strings.Contains(body, inSecret) || strings.Contains(body, outSecret) {
						t.Fatalf("the gateway request log contains a secret: %s", truncate(body))
					}
				}
				if !strings.Contains(string(detail.Body), "REDACTED") {
					t.Fatalf("want the request log to show the redacted content: %s", truncate(string(detail.Body)))
				}
			}
		}
	})

	t.Run("Claude Code passthrough is checked", func(t *testing.T) {
		u := env.newUser(t, "gr-claude")
		createRule(t, guardrailRule{Name: "e2e claude code", ApplyTo: "both", Action: "redact",
			ProfileIDs: []string{secrets}, Subjects: users(u)})
		inSecret, outSecret := githubToken(), githubToken()
		ua := map[string]string{"User-Agent": "claude-cli/2.0.0 (external, cli)", "anthropic-beta": "claude-code-20250219"}
		for _, stream := range []bool{false, true} {
			n := nonce("claude")
			res := callLLM(t, u.Gateway(env.A), apiMessages, "claude-e2e-a",
				n+" "+inSecret+" "+harness.Reply("out "+outSecret)+" @@chunk:5@@", stream, nil, ua).requireOK(t)
			reqs := env.mock.InferenceRequests(n)
			if len(reqs) != 1 {
				t.Fatalf("stream=%t: want 1 upstream request, got %d", stream, len(reqs))
			}
			if strings.Contains(reqs[0].Body, inSecret) || !strings.Contains(reqs[0].Body, "[REDACTED:GITHUB_TOKEN]") {
				t.Fatalf("stream=%t: the provider received unredacted Claude Code input: %s", stream, reqs[0].Body)
			}
			if strings.Contains(res.Body, outSecret) || res.Text != "out [REDACTED:GITHUB_TOKEN]" {
				t.Fatalf("stream=%t: Claude Code received unredacted output %q: %s", stream, res.Text, truncate(res.Body))
			}
		}
	})

	t.Run("events API filters", func(t *testing.T) {
		var list struct {
			Items []guardrailEvent `json:"items"`
			Total int              `json:"total"`
		}
		env.admin.Get(t, "/api/llm-gateway/guardrails/events?target=llm&outcome=blocked&limit=5", &list)
		if list.Total == 0 || len(list.Items) == 0 || len(list.Items) > 5 {
			t.Fatalf("want blocked LLM events, limited to 5: total %d, %d items", list.Total, len(list.Items))
		}
		for _, e := range list.Items {
			if e.Outcome != "blocked" || e.Target != "llm" {
				t.Fatalf("filter mismatch: %+v", e)
			}
		}
		if gjson.Valid(mustJSON(list)) && strings.Contains(mustJSON(list), "ghp_") {
			t.Fatal("events contain secrets")
		}
	})
}

// toolsFor returns a tool definition in the API format, so tool calls are expected.
func toolsFor(api string) []any {
	schema := map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string"}, "note": map[string]any{"type": "string"}}}
	switch api {
	case apiChat:
		return []any{map[string]any{"type": "function", "function": map[string]any{"name": "deploy", "parameters": schema}}}
	case apiResponses:
		return []any{map[string]any{"type": "function", "name": "deploy", "parameters": schema}}
	default:
		return []any{map[string]any{"name": "deploy", "input_schema": schema}}
	}
}
