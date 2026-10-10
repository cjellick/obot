package guardrails

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/obot-platform/obot/pkg/gateway/types"
)

type fakeStore struct {
	lock     sync.Mutex
	rules    []types.GuardrailRule
	profiles []types.GuardrailProfile
	secrets  map[string]map[string]string
	events   []types.GuardrailEvent
	// loadErr makes listing rules and profiles fail, like an unreachable database.
	loadErr error
}

// misplacedChecker reports a finding whose span doesn't fit the text, like a service whose
// offsets were misread.
type misplacedChecker struct{}

// failingChecker always errors, to exercise fail-open and fail-closed behavior.
type failingChecker struct{}

// flaggingChecker flags everything without saying where, like a content classifier.
type flaggingChecker struct{}

func init() {
	Register(ProviderInfo{Type: "test-failing"}, func(map[string]any, map[string]string, Deps) (Checker, error) {
		return failingChecker{}, nil
	})
	Register(ProviderInfo{Type: "test-misplaced", Redacts: true}, func(map[string]any, map[string]string, Deps) (Checker, error) {
		return misplacedChecker{}, nil
	})
	Register(ProviderInfo{Type: "test-flagging"}, func(map[string]any, map[string]string, Deps) (Checker, error) {
		return flaggingChecker{}, nil
	})
}

func (f *fakeStore) ListGuardrailRules(context.Context) ([]types.GuardrailRule, error) {
	return f.rules, f.loadErr
}

func (f *fakeStore) ListGuardrailProfiles(context.Context) ([]types.GuardrailProfile, error) {
	return f.profiles, f.loadErr
}

func (f *fakeStore) GuardrailProfileSecrets(_ context.Context, id string) (map[string]string, error) {
	return f.secrets[id], nil
}

func (f *fakeStore) InsertGuardrailEvents(_ context.Context, events []types.GuardrailEvent) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.events = append(f.events, events...)
	return nil
}

func (f *fakeStore) outcomes() []string {
	f.lock.Lock()
	defer f.lock.Unlock()
	var out []string
	for _, e := range f.events {
		out = append(out, e.Outcome)
	}
	return out
}

func profile(id, typ string, config map[string]any) types.GuardrailProfile {
	raw, _ := json.Marshal(config)
	return types.GuardrailProfile{ID: id, Name: id, Type: typ, Config: raw}
}

func rule(name, action string, profiles ...string) types.GuardrailRule {
	return types.GuardrailRule{
		ID: name, Name: name, Enabled: true, Target: types.GuardrailTargetLLM,
		ApplyTo: types.GuardrailApplyBoth, Action: action, SamplingRate: 100, ProfileIDs: profiles,
	}
}

func (failingChecker) Check(context.Context, CheckInput) (Result, error) {
	return Result{}, errors.New("service unavailable")
}

func (misplacedChecker) Check(_ context.Context, in CheckInput) (Result, error) {
	end := len(in.Segments[0].Text) + 10
	return Result{Findings: []Finding{{Category: "pii", Spans: []Span{{Segment: 0, Start: 2, End: end, Label: "PII"}}}}}, nil
}

func (flaggingChecker) Check(context.Context, CheckInput) (Result, error) {
	return Result{Findings: []Finding{{Category: "harmful", Message: "unsafe content"}}}, nil
}

func evaluate(t *testing.T, store *fakeStore, identity Identity, vars map[string]any, segments ...Segment) Decision {
	t.Helper()
	e := New(t.Context(), store, nil)
	plan, err := e.Plan(t.Context(), types.GuardrailTargetLLM, StageInput, identity, vars)
	if err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(t.Context(), plan, Request{Target: types.GuardrailTargetLLM, Stage: StageInput, Identity: identity, Vars: vars, Segments: segments})
	e.Flush()
	return d
}

func TestRedactsSecretsAndPII(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{
			profile("secrets", "secrets", nil),
			profile("pii", "regex", map[string]any{"piiTemplate": []any{"EMAIL", "CREDIT_CARD"}}),
		},
		rules: []types.GuardrailRule{rule("redact", types.GuardrailActionRedact, "secrets", "pii")},
	}
	d := evaluate(t, store, Identity{UserID: "u1"}, nil,
		Segment{Role: RoleUser, Text: "my key is AKIAIOSFODNN7EXAMPLE and email bob@example.com"},
		Segment{Role: RoleAssistant, Text: "card 4111 1111 1111 1111, not 1234 5678 9012 3456"},
	)
	if d.Blocked || !d.Redacted {
		t.Fatalf("expected redaction, got %+v", d)
	}
	if got := d.Segments[0].Text; got != "my key is [REDACTED:AWS_ACCESS_KEY_ID] and email [REDACTED:EMAIL]" {
		t.Fatalf("unexpected redaction %q", got)
	}
	if got := d.Segments[1].Text; got != "card [REDACTED:CREDIT_CARD], not 1234 5678 9012 3456" {
		t.Fatalf("a Luhn-invalid number must not be redacted: %q", got)
	}
	if o := store.outcomes(); len(o) != 2 || o[0] != "redacted" {
		t.Fatalf("unexpected events %v", o)
	}
}

func TestBlockAndConditions(t *testing.T) {
	block := rule("no-secrets-on-gpt", types.GuardrailActionBlock, "secrets")
	block.Condition = `model.startsWith("openai/") && !("security" in user.groups)`
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{block},
	}
	secret := Segment{Role: RoleUser, Text: "token ghp_abcdefghijklmnopqrstuvwxyz0123456789"}

	d := evaluate(t, store, Identity{UserID: "u1"}, map[string]any{"model": "openai/gpt-4.1"}, secret)
	if !d.Blocked || !strings.Contains(d.Message, "GITHUB_TOKEN") {
		t.Fatalf("expected a block naming the finding, got %+v", d)
	}
	if d := evaluate(t, store, Identity{UserID: "u1"}, map[string]any{"model": "anthropic/claude"}, secret); d.Blocked {
		t.Fatal("the condition should exclude other providers")
	}
	if d := evaluate(t, store, Identity{UserID: "u1", Groups: []string{"security"}}, map[string]any{"model": "openai/gpt-4.1"}, secret); d.Blocked {
		t.Fatal("the condition should exclude the security group")
	}
}

func TestSubjectsScopeAndFailureModes(t *testing.T) {
	scoped := rule("alice-only", types.GuardrailActionBlock, "flag")
	scoped.Subjects = []types.GuardrailRuleSubject{{Type: "user", ID: "alice"}}
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("flag", "test-flagging", nil)},
		rules:    []types.GuardrailRule{scoped},
	}
	msg := Segment{Role: RoleUser, Text: "hello"}
	if d := evaluate(t, store, Identity{UserID: "bob"}, nil, msg); d.Blocked {
		t.Fatal("rule must only apply to its subjects")
	}
	if d := evaluate(t, store, Identity{UserID: "alice"}, nil, msg); !d.Blocked {
		t.Fatal("rule must apply to alice")
	}

	// A redacting rule whose checker can't locate findings blocks instead.
	store.rules = []types.GuardrailRule{rule("redact-unlocatable", types.GuardrailActionRedact, "flag")}
	if d := evaluate(t, store, Identity{UserID: "bob"}, nil, msg); !d.Blocked {
		t.Fatal("unlocatable findings must block a redacting rule")
	}

	// Checker errors fail open unless the rule fails closed.
	store.profiles = []types.GuardrailProfile{profile("down", "test-failing", nil)}
	open := rule("open", types.GuardrailActionBlock, "down")
	store.rules = []types.GuardrailRule{open}
	if d := evaluate(t, store, Identity{UserID: "bob"}, nil, msg); d.Blocked {
		t.Fatal("a failing profile must not block a fail-open rule")
	}
	closed := open
	closed.FailClosed = true
	store.rules = []types.GuardrailRule{closed}
	if d := evaluate(t, store, Identity{UserID: "bob"}, nil, msg); !d.Blocked {
		t.Fatal("a failing profile must block a fail-closed rule")
	}
}

func TestLatestScope(t *testing.T) {
	r := rule("latest", types.GuardrailActionBlock, "secrets")
	r.InputScope = types.GuardrailScopeLatest
	store := &fakeStore{profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)}, rules: []types.GuardrailRule{r}}
	old := Segment{Role: RoleUser, Text: "AKIAIOSFODNN7EXAMPLE"}
	if d := evaluate(t, store, Identity{}, nil, old, Segment{Role: RoleAssistant, Text: "ok"}, Segment{Role: RoleUser, Text: "thanks"}); d.Blocked {
		t.Fatal("latest scope must ignore messages before the model's last reply")
	}
}

func TestCompileCondition(t *testing.T) {
	if _, err := CompileCondition(types.GuardrailTargetLLM, `mcp_tool == "x"`); err == nil {
		t.Fatal("LLM rules must not see MCP variables")
	}
	if _, err := CompileCondition(types.GuardrailTargetMCP, `mcp_tool == "delete_repo" && user.id != ""`); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileCondition(types.GuardrailTargetLLM, `model`); err == nil {
		t.Fatal("non-boolean conditions must be rejected")
	}
}

func (f *fakeStore) GetGuardrailRule(_ context.Context, id string) (*types.GuardrailRule, error) {
	for _, r := range f.rules {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeStore) GetGuardrailProfile(_ context.Context, id string) (*types.GuardrailProfile, error) {
	for _, p := range f.profiles {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, errors.New("not found")
}

func (f *fakeStore) CreateGuardrailProfile(_ context.Context, p *types.GuardrailProfile, _ map[string]string) error {
	f.profiles = append(f.profiles, *p)
	return nil
}

func (f *fakeStore) CreateGuardrailRule(_ context.Context, r *types.GuardrailRule) error {
	f.rules = append(f.rules, *r)
	return nil
}

func TestRedactionThatCannotBeAppliedBlocks(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("p", "test-misplaced", nil)},
		rules:    []types.GuardrailRule{rule("redact", types.GuardrailActionRedact, "p")},
	}
	d := evaluate(t, store, Identity{UserID: "u1"}, nil, Segment{Role: RoleUser, Text: "hello"})
	if !d.Blocked || d.Redacted {
		t.Fatalf("a span outside the text must block, not claim a redaction: %+v", d)
	}
	if got := store.outcomes(); len(got) != 1 || got[0] != "blocked" {
		t.Fatalf("unexpected events %v", got)
	}
}

func TestConditionRuntimeErrors(t *testing.T) {
	broken := rule("needs-header", types.GuardrailActionBlock, "secrets")
	broken.Condition = `headers["x-team"] == "a"`
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{broken},
	}
	vars := map[string]any{"headers": map[string]string{}}

	// Fail-open: the rule is skipped, but the failure is recorded.
	d := evaluate(t, store, Identity{UserID: "u1"}, vars, Segment{Role: RoleUser, Text: "hello"})
	if d.Blocked {
		t.Fatalf("a fail-open rule must not block: %+v", d)
	}
	if got := store.outcomes(); len(got) != 1 || got[0] != "error" {
		t.Fatalf("expected an error event, got %v", got)
	}

	// Fail-closed: the request is blocked.
	store.rules[0].FailClosed = true
	if d := evaluate(t, store, Identity{UserID: "u1"}, vars, Segment{Role: RoleUser, Text: "hello"}); !d.Blocked || !strings.Contains(d.Message, "needs-header") {
		t.Fatalf("a fail-closed rule whose condition fails must block: %+v", d)
	}
}

func TestFindingsOnDeliveredContentAreErrors(t *testing.T) {
	store := &fakeStore{
		profiles: []types.GuardrailProfile{profile("secrets", "secrets", nil)},
		rules:    []types.GuardrailRule{rule("redact", types.GuardrailActionRedact, "secrets")},
	}
	e := New(t.Context(), store, nil)
	plan, err := e.Plan(t.Context(), types.GuardrailTargetLLM, StageOutput, Identity{UserID: "u1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(t.Context(), plan, Request{
		Target: types.GuardrailTargetLLM, Stage: StageOutput, Identity: Identity{UserID: "u1"}, Delivered: true,
		Segments: []Segment{{Role: RoleAssistant, Text: "key AKIAIOSFODNN7EXAMPLE"}},
	})
	e.Flush()
	if !d.Blocked || d.Redacted {
		t.Fatalf("content already delivered can't be redacted; the response must end: %+v", d)
	}
	if got := store.outcomes(); len(got) != 1 || got[0] != "error" {
		t.Fatalf("expected an error event rather than a redaction, got %v", got)
	}
}

func TestJudgeExplanationIsShownButNotStored(t *testing.T) {
	findings := []Finding{{Category: "policy", Message: "Violates the policy", Explanation: "Your message mentions AKIA..."}}
	r := &compiledRule{rule: types.GuardrailRule{Name: "judge"}}
	if msg := blockMessage(r, findings); !strings.Contains(msg, "Your message mentions") {
		t.Fatalf("the caller should see the explanation: %s", msg)
	}
	raw, _ := json.Marshal(findings)
	if strings.Contains(string(raw), "AKIA") {
		t.Fatalf("the explanation must not be stored: %s", raw)
	}
}

func TestBlockMessagePunctuation(t *testing.T) {
	r := &compiledRule{rule: types.GuardrailRule{Name: "No cooking"}}
	if got := blockMessage(r, []Finding{{Explanation: "Recipes aren't allowed here."}}); strings.HasSuffix(got, "..") {
		t.Fatalf("double period: %q", got)
	}
	if got := blockMessage(r, []Finding{{Category: "policy"}}); !strings.HasSuffix(got, "policy.") {
		t.Fatalf("missing period: %q", got)
	}
}
