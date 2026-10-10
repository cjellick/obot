package guardrails

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	gateway "github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/messagepolicy"
)

const (
	// cacheTTL bounds how long rule and profile changes take to apply on other replicas.
	cacheTTL = 5 * time.Second
)

var (
	_ Store = (*gateway.Client)(nil)
)

// Identity is who a request is made for.
type Identity struct {
	UserID string
	Groups []string
}

// Request is content to evaluate.
type Request struct {
	Target   string
	Stage    string
	Identity Identity
	// Vars are the target's condition variables (model, provider, headers, mcp_tool, ...).
	Vars     map[string]any
	Segments []Segment
	History  []Segment
	// Model and Resource are recorded on events: the model, or the MCP server and tool.
	Model    string
	Resource string
	// Delivered reports that the content has already reached the client, as with a stream
	// that couldn't be held. Findings that would block or redact then end the response and
	// are recorded as errors, since the content can no longer be withheld.
	Delivered bool
}

// Decision is the combined outcome of every applicable rule.
type Decision struct {
	Blocked bool
	// Message explains a block to the caller.
	Message string
	// Segments is the request's segments after redaction.
	Segments []Segment
	// Redacted reports whether any segment changed.
	Redacted bool
}

// Plan summarizes the rules that apply to a request before it is evaluated, which callers
// use to decide how much of a streamed response to hold back.
type Plan struct {
	rules []*compiledRule
	// broken holds fail-closed rules whose condition could not be evaluated. They block.
	broken []*compiledRule
}

type compiledProfile struct {
	profile types.GuardrailProfile
	checker Checker
	redacts bool
	err     error
}

type compiledRule struct {
	rule     types.GuardrailRule
	profiles []*compiledProfile
	// allRedact reports whether every profile can locate findings, so redaction never has
	// to fall back to blocking.
	allRedact bool
}

// Store is where rules, profiles and events live. *gateway.Client implements it.
type Store interface {
	ListGuardrailRules(ctx context.Context) ([]types.GuardrailRule, error)
	ListGuardrailProfiles(ctx context.Context) ([]types.GuardrailProfile, error)
	GuardrailProfileSecrets(ctx context.Context, id string) (map[string]string, error)
	InsertGuardrailEvents(ctx context.Context, events []types.GuardrailEvent) error
}

// Engine evaluates guardrail rules.
type Engine struct {
	client Store
	deps   Deps

	lock     sync.Mutex
	loadedAt time.Time
	rules    []*compiledRule
	profiles map[string]*compiledProfile

	events chan types.GuardrailEvent

	// conditionErrors throttles events for rules whose condition fails at runtime, which
	// would otherwise record one per request.
	conditionErrorsLock sync.Mutex
	conditionErrors     map[string]time.Time
}

// Empty reports whether no rule applies.
func (p Plan) Empty() bool { return len(p.rules) == 0 && len(p.broken) == 0 }

// CanBlock reports whether any rule may block, which requires holding the whole response.
func (p Plan) CanBlock() bool {
	return len(p.broken) > 0 || slices.ContainsFunc(p.rules, func(r *compiledRule) bool {
		return r.rule.Action == types.GuardrailActionBlock || (r.rule.Action == types.GuardrailActionRedact && !r.allRedact) || r.rule.FailClosed
	})
}

// Modifies reports whether any rule may change or stop the response.
func (p Plan) Modifies() bool {
	return len(p.broken) > 0 || slices.ContainsFunc(p.rules, func(r *compiledRule) bool { return r.rule.Action != types.GuardrailActionLog || r.rule.FailClosed })
}

// New creates an engine. judge may be nil, in which case LLM-judge profiles fail.
func New(ctx context.Context, client Store, judge *messagepolicy.Helper) *Engine {
	e := &Engine{
		client: client,
		deps: Deps{
			Judge:      judge,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		},
		events: make(chan types.GuardrailEvent, 1000),
	}
	go e.writeEvents(ctx)
	return e
}

// Invalidate makes the next evaluation reload rules and profiles.
func (e *Engine) Invalidate() {
	e.lock.Lock()
	e.loadedAt = time.Time{}
	e.lock.Unlock()
}

func (e *Engine) load(ctx context.Context) ([]*compiledRule, error) {
	rules, _, err := e.loadAll(ctx)
	return rules, err
}

// loadAll returns the enabled rules and every profile, by ID.
func (e *Engine) loadAll(ctx context.Context) ([]*compiledRule, map[string]*compiledProfile, error) {
	e.lock.Lock()
	defer e.lock.Unlock()
	if time.Since(e.loadedAt) < cacheTTL {
		return e.rules, e.profiles, nil
	}

	rules, err := e.client.ListGuardrailRules(ctx)
	if err != nil {
		return nil, nil, err
	}
	profiles, err := e.client.ListGuardrailProfiles(ctx)
	if err != nil {
		return nil, nil, err
	}

	byID := map[string]*compiledProfile{}
	for _, p := range profiles {
		cp := &compiledProfile{profile: p}
		info, ok := Provider(p.Type)
		cp.redacts = ok && info.Redacts
		var config map[string]any
		if len(p.Config) > 0 {
			if err := json.Unmarshal(p.Config, &config); err != nil {
				cp.err = fmt.Errorf("invalid configuration: %w", err)
			}
		}
		if cp.err == nil {
			secrets, err := e.client.GuardrailProfileSecrets(ctx, p.ID)
			if err != nil {
				cp.err = err
			} else if cp.checker, err = newChecker(p.Type, config, secrets, e.deps); err != nil {
				cp.err = err
			}
		}
		byID[p.ID] = cp
	}

	compiled := make([]*compiledRule, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		cr := &compiledRule{rule: r, allRedact: true}
		for _, id := range r.ProfileIDs {
			if p, ok := byID[id]; ok {
				cr.profiles = append(cr.profiles, p)
				cr.allRedact = cr.allRedact && p.redacts
			}
		}
		if len(cr.profiles) > 0 {
			compiled = append(compiled, cr)
		}
	}
	e.rules = compiled
	e.profiles = byID
	e.loadedAt = time.Now()
	return compiled, byID, nil
}

func stageMatches(applyTo, stage string) bool {
	return applyTo == types.GuardrailApplyBoth || applyTo == stage
}

func subjectsMatch(subjects []types.GuardrailRuleSubject, identity Identity) bool {
	if len(subjects) == 0 {
		return true
	}
	return slices.ContainsFunc(subjects, func(s types.GuardrailRuleSubject) bool {
		switch s.Type {
		case types.LLMGatewayBudgetSubjectEveryone, "selector":
			return true
		case types.LLMGatewayBudgetSubjectUser:
			return s.ID == identity.UserID
		case types.LLMGatewayBudgetSubjectGroup:
			return slices.Contains(identity.Groups, s.ID)
		}
		return false
	})
}

// Plan returns the rules that apply to a request's target, stage, caller and variables.
// Sampling is decided here, once per request and stage.
func (e *Engine) Plan(ctx context.Context, target, stage string, identity Identity, vars map[string]any) (Plan, error) {
	rules, err := e.load(ctx)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	for _, r := range rules {
		e.addToPlan(&plan, r, target, stage, identity, vars)
	}
	return plan, nil
}

// addToPlan adds a rule to a plan when it applies to the target, stage, caller and
// variables, and is sampled.
func (e *Engine) addToPlan(plan *Plan, r *compiledRule, target, stage string, identity Identity, vars map[string]any) {
	if r.rule.Target != target || !stageMatches(r.rule.ApplyTo, stage) || !subjectsMatch(r.rule.Subjects, identity) {
		return
	}
	ok, err := evalCondition(target, r.rule.Condition, identity, vars)
	if err != nil {
		// A condition can fail at runtime, e.g. headers["x"] when the header is absent.
		// Fail-closed rules block; others are skipped. Either way the failure is recorded.
		e.conditionFailed(r, target, stage, identity, err)
		if r.rule.FailClosed {
			plan.broken = append(plan.broken, r)
		}
		return
	}
	if !ok {
		return
	}
	if rate := r.rule.SamplingRate; rate > 0 && rate < 100 && rand.IntN(100) >= rate {
		return
	}
	plan.rules = append(plan.rules, r)
}

// Evaluate checks a request against a plan's rules. Rules run in order; a block stops
// evaluation, and each redaction is applied before the next rule sees the content.
func (e *Engine) Evaluate(ctx context.Context, plan Plan, req Request) Decision {
	d := Decision{Segments: req.Segments}
	if len(plan.broken) > 0 {
		d.Blocked = true
		d.Message = fmt.Sprintf("Request blocked: the condition of the %q guardrail could not be evaluated.", plan.broken[0].rule.Name)
		return d
	}
	for _, r := range plan.rules {
		segments := d.Segments
		offset := 0
		// Input scope only applies to conversations; an MCP tool call has no earlier turns.
		if req.Target == types.GuardrailTargetLLM && req.Stage == StageInput && r.rule.InputScope == types.GuardrailScopeLatest && r.rule.Action != types.GuardrailActionRedact {
			offset = latestStart(segments)
			segments = segments[offset:]
		}
		if len(segments) == 0 {
			continue
		}

		for _, p := range r.profiles {
			if p.err != nil {
				if e.fail(ctx, r, p, req, p.err, &d) {
					return d
				}
				continue
			}
			result, err := p.checker.Check(ctx, CheckInput{
				Target:   req.Target,
				Stage:    req.Stage,
				Segments: segments,
				History:  append(slices.Clip(req.History), d.Segments[:offset]...),
			})
			if err != nil {
				if e.fail(ctx, r, p, req, err, &d) {
					return d
				}
				continue
			}
			if len(result.Findings) == 0 && len(result.Replacements) == 0 {
				continue
			}

			outcome := "logged"
			switch r.rule.Action {
			case types.GuardrailActionBlock:
				outcome = "blocked"
			case types.GuardrailActionRedact:
				redacted, ok := redactResult(segments, result)
				if !ok {
					// The checker can't say where the problem is, so it can't be removed.
					outcome = "blocked"
					break
				}
				if req.Delivered {
					outcome = "blocked"
					break
				}
				outcome = "redacted"
				d.Segments = append(slices.Clip(d.Segments[:offset]), redacted...)
				segments = redacted
				d.Redacted = true
			}
			if outcome == "blocked" && req.Delivered {
				// Too late to withhold anything: end the response and say so.
				e.record(r, p, req, "error", result.Findings, "the content was already delivered because the response could not be held")
				d.Blocked = true
				d.Message = blockMessage(r, result.Findings)
				return d
			}
			e.record(r, p, req, outcome, result.Findings, "")
			if outcome == "blocked" {
				d.Blocked = true
				d.Message = blockMessage(r, result.Findings)
				return d
			}
		}
	}
	return d
}

// conditionFailed records a rule whose condition failed to evaluate, at most once a minute
// per rule.
func (e *Engine) conditionFailed(r *compiledRule, target, stage string, identity Identity, err error) {
	e.conditionErrorsLock.Lock()
	last, seen := e.conditionErrors[r.rule.ID]
	if seen && time.Since(last) < time.Minute {
		e.conditionErrorsLock.Unlock()
		return
	}
	if e.conditionErrors == nil {
		e.conditionErrors = map[string]time.Time{}
	}
	e.conditionErrors[r.rule.ID] = time.Now()
	e.conditionErrorsLock.Unlock()

	slog.Warn("guardrail rule condition failed to evaluate", "rule", r.rule.Name, "failClosed", r.rule.FailClosed, "error", err)
	e.enqueue(types.GuardrailEvent{
		RuleID:   r.rule.ID,
		RuleName: r.rule.Name,
		Target:   target,
		Stage:    stage,
		Outcome:  "error",
		UserID:   identity.UserID,
		Error:    "condition failed to evaluate: " + err.Error(),
	})
}

// fail handles a profile that couldn't be evaluated. It reports whether the request is
// blocked.
func (e *Engine) fail(ctx context.Context, r *compiledRule, p *compiledProfile, req Request, err error, d *Decision) bool {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	slog.Warn("guardrail profile failed", "rule", r.rule.Name, "profile", p.profile.Name, "error", err)
	e.record(r, p, req, "error", nil, err.Error())
	if !r.rule.FailClosed {
		return false
	}
	d.Blocked = true
	d.Message = fmt.Sprintf("Request blocked: the %q guardrail could not be evaluated.", r.rule.Name)
	return true
}

func redactResult(segments []Segment, result Result) ([]Segment, bool) {
	out := slices.Clone(segments)
	for i, text := range result.Replacements {
		if i >= 0 && i < len(out) {
			out[i].Text = text
		}
	}
	for i := range result.Replacements {
		if i < 0 || i >= len(out) {
			return nil, false
		}
	}
	var spans []Span
	for _, f := range result.Findings {
		if len(f.Spans) == 0 {
			if len(result.Replacements) > 0 {
				continue
			}
			return nil, false
		}
		for _, s := range f.Spans {
			// A span that doesn't fit the text would redact nothing; block instead.
			if s.Segment < 0 || s.Segment >= len(out) || s.Start < 0 || s.End <= s.Start || s.End > len(out[s.Segment].Text) {
				return nil, false
			}
		}
		spans = append(spans, f.Spans...)
	}
	return ApplySpans(out, spans), true
}

// latestStart returns the index of the first segment after the last assistant message or
// tool call, i.e. what the caller added since the model last responded.
func latestStart(segments []Segment) int {
	for i, s := range slices.Backward(segments) {
		if s.Role == RoleAssistant || s.Role == RoleToolCall {
			return i + 1
		}
	}
	return 0
}

func blockMessage(r *compiledRule, findings []Finding) string {
	if r.rule.BlockMessage != "" {
		return r.rule.BlockMessage
	}
	var reasons []string
	for _, f := range findings {
		reason := cmp.Or(f.Explanation, f.Message, f.Category)
		if reason != "" && !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	msg := fmt.Sprintf("Blocked by the %q guardrail", r.rule.Name)
	if len(reasons) > 0 {
		msg += ": " + strings.Join(reasons, "; ")
	}
	// Reasons, such as a judge's explanation, may already end in punctuation.
	if strings.HasSuffix(msg, ".") || strings.HasSuffix(msg, "!") || strings.HasSuffix(msg, "?") {
		return msg
	}
	return msg + "."
}

func (e *Engine) record(r *compiledRule, p *compiledProfile, req Request, outcome string, findings []Finding, errMsg string) {
	var findingsJSON json.RawMessage
	if len(findings) > 0 {
		findingsJSON, _ = json.Marshal(findings)
	}
	event := types.GuardrailEvent{
		CreatedAt:   time.Now(),
		RuleID:      r.rule.ID,
		RuleName:    r.rule.Name,
		ProfileID:   p.profile.ID,
		ProfileName: p.profile.Name,
		ProfileType: p.profile.Type,
		Target:      req.Target,
		Stage:       req.Stage,
		Outcome:     outcome,
		UserID:      req.Identity.UserID,
		Model:       req.Model,
		Resource:    req.Resource,
		Findings:    findingsJSON,
		Error:       errMsg,
	}
	e.enqueue(event)
}

func (e *Engine) enqueue(event types.GuardrailEvent) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	select {
	case e.events <- event:
	default:
		slog.Warn("dropping guardrail event: the event buffer is full", "rule", event.RuleName)
	}
}

func (e *Engine) writeEvents(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var batch []types.GuardrailEvent
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := e.client.InsertGuardrailEvents(context.Background(), batch); err != nil {
			slog.Warn("failed to save guardrail events", "count", len(batch), "error", err)
		}
		batch = nil
	}
	for {
		select {
		case ev := <-e.events:
			batch = append(batch, ev)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

// Flush waits briefly for buffered events to be written. It is used by tests.
func (e *Engine) Flush() {
	deadline := time.Now().Add(3 * time.Second)
	for len(e.events) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)
}

// TestProfile runs a profile's checker against sample text without recording events, so
// admins can try a profile before relying on it.
func (e *Engine) TestProfile(ctx context.Context, profile types.GuardrailProfile, secrets map[string]string, target, stage, text string) (Result, []Segment, error) {
	var config map[string]any
	if len(profile.Config) > 0 {
		if err := json.Unmarshal(profile.Config, &config); err != nil {
			return Result{}, nil, fmt.Errorf("invalid configuration: %w", err)
		}
	}
	checker, err := newChecker(profile.Type, config, secrets, e.deps)
	if err != nil {
		return Result{}, nil, err
	}
	role := RoleUser
	switch {
	case target == types.GuardrailTargetMCP && stage == StageOutput:
		role = RoleToolResult
	case target == types.GuardrailTargetMCP:
		role = RoleToolCall
	case stage == StageOutput:
		role = RoleAssistant
	}
	segments := []Segment{{Role: role, Text: text, Name: "test"}}
	result, err := checker.Check(ctx, CheckInput{Target: target, Stage: stage, Segments: segments})
	if err != nil {
		return Result{}, nil, err
	}
	redacted, _ := redactResult(segments, result)
	return result, redacted, nil
}
