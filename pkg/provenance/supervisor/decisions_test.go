package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/provenance"
)

// fakeDecisions is a DecisionClient test double: it records the last call and
// answers each question with the function's answer.
type fakeDecisions struct {
	answer    func(q schemas.DecisionQuestion) schemas.DecisionAnswer
	err       error
	model     string
	calls     int
	lastModel string
	lastInput string
	lastQs    []schemas.DecisionQuestion
}

func (f *fakeDecisions) Decide(_ context.Context, model, input string, questions []schemas.DecisionQuestion) (*schemas.BifrostDecisionResponse, string, error) {
	f.calls++
	f.lastModel, f.lastInput, f.lastQs = model, input, questions
	if f.err != nil {
		return nil, "", f.err
	}
	resp := &schemas.BifrostDecisionResponse{Model: f.model}
	for _, q := range questions {
		a := f.answer(q)
		a.Name = q.Name
		resp.Answers = append(resp.Answers, a)
	}
	return resp, f.model, nil
}

func choiceAnswer(choice string, probs map[string]float64) schemas.DecisionAnswer {
	a := schemas.DecisionAnswer{Type: schemas.DecisionTypeChoice, Choice: &schemas.DecisionScalar{Str: schemas.Ptr(choice)}}
	for k, p := range probs {
		a.Probabilities = append(a.Probabilities, schemas.DecisionProbability{Value: schemas.DecisionScalar{Str: schemas.Ptr(k)}, Probability: p})
	}
	return a
}

func predicateAnswers(probs map[string]float64) func(schemas.DecisionQuestion) schemas.DecisionAnswer {
	return func(q schemas.DecisionQuestion) schemas.DecisionAnswer {
		p, ok := probs[*q.Name]
		if !ok {
			return schemas.DecisionAnswer{Type: schemas.DecisionTypePredicate, Probability: schemas.Ptr(0.01)}
		}
		if p < 0 {
			return schemas.DecisionAnswer{Type: schemas.DecisionTypeRefusal}
		}
		return schemas.DecisionAnswer{Type: schemas.DecisionTypePredicate, Probability: schemas.Ptr(p)}
	}
}

func decisionSupervisor(t *testing.T, dc DecisionClient, decider provenance.Decider, writer TextModel, settings provenance.Settings, opts ...Option) *Supervisor {
	t.Helper()
	opts = append([]Option{WithDecisions(dc), WithClock(testClock(t))}, opts...)
	s := NewWith(decider, writer, opts...)
	s.settings = func(context.Context) provenance.Settings { return settings }
	return s
}

func TestJudgeByDecision(t *testing.T) {
	tests := []struct {
		name       string
		answer     schemas.DecisionAnswer
		wantEffect provenance.Effect
		wantConf   float64
		wantIn     []string
	}{
		{
			name:       "deny with its probability",
			answer:     choiceAnswer("deny", map[string]float64{"deny": 0.9, "allow": 0.04, "require_approval": 0.06}),
			wantEffect: provenance.EffectDeny,
			wantConf:   0.9,
			wantIn:     []string{"I denied this", `"untrusted-egress"`, "deny with 90% probability"},
		},
		{
			name:       "require approval",
			answer:     choiceAnswer("require_approval", map[string]float64{"require_approval": 0.55, "allow": 0.45}),
			wantEffect: provenance.EffectRequireApproval,
			wantConf:   0.55,
			wantIn:     []string{"I held this for approval", "55%"},
		},
		{
			name: "confidence when there is no distribution",
			answer: schemas.DecisionAnswer{
				Type:       schemas.DecisionTypeChoice,
				Choice:     &schemas.DecisionScalar{Str: schemas.Ptr("Allow")},
				Confidence: schemas.Ptr(0.8),
			},
			wantEffect: provenance.EffectAllow,
			wantConf:   0.8,
			wantIn:     []string{"I allowed this", "80%"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &fakeDecisions{model: "openai/gpt-6-luna", answer: func(schemas.DecisionQuestion) schemas.DecisionAnswer { return tt.answer }}
			decider := &fakeDecider{}
			s := decisionSupervisor(t, dc, decider, nil, provenance.Settings{})
			out, err := s.Judge(context.Background(), provenance.JudgeInput{
				Policy: provenance.Policy{Name: "untrusted-egress", Guidance: "be careful with pastebins"},
				Action: provenance.Action{Tool: "http.post"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if out.Effect != tt.wantEffect || out.Confidence != tt.wantConf || out.Model != "openai/gpt-6-luna" {
				t.Fatalf("got %+v", out)
			}
			for _, s := range tt.wantIn {
				if !strings.Contains(out.Rationale, s) {
					t.Errorf("rationale %q does not contain %q", out.Rationale, s)
				}
			}
			if decider.calls != 0 {
				t.Errorf("the chat decider was called")
			}
			q := dc.lastQs[0]
			if q.Type != schemas.DecisionTypeChoice || len(q.Choices) != len(provenance.JudgeOptions) {
				t.Errorf("question = %+v", q)
			}
			if !strings.Contains(*q.Instructions.Text, "be careful with pastebins") {
				t.Errorf("guidance missing from instructions: %q", *q.Instructions.Text)
			}
			if !strings.Contains(dc.lastInput, "Policy that fired: untrusted-egress") {
				t.Errorf("input = %q", dc.lastInput)
			}
		})
	}
}

func TestJudgeDecisionFallback(t *testing.T) {
	tests := []struct {
		name        string
		dc          *fakeDecisions
		wantEffect  provenance.Effect
		wantChatted bool
	}{
		{
			name:        "call error falls back to chat",
			dc:          &fakeDecisions{err: errors.New("decisions not supported")},
			wantEffect:  provenance.EffectAllow,
			wantChatted: true,
		},
		{
			name: "refusal falls back to chat",
			dc: &fakeDecisions{answer: func(schemas.DecisionQuestion) schemas.DecisionAnswer {
				return schemas.DecisionAnswer{Type: schemas.DecisionTypeRefusal}
			}},
			wantEffect:  provenance.EffectAllow,
			wantChatted: true,
		},
		{
			name: "unknown option falls back to chat",
			dc: &fakeDecisions{answer: func(schemas.DecisionQuestion) schemas.DecisionAnswer {
				return choiceAnswer("shrug", nil)
			}},
			wantEffect:  provenance.EffectAllow,
			wantChatted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decider := &fakeDecider{answer: provenance.Answer{Option: "allow", Rationale: "chat", Confidence: 0.7}}
			s := decisionSupervisor(t, tt.dc, decider, nil, provenance.Settings{})
			out, err := s.Judge(context.Background(), provenance.JudgeInput{Policy: provenance.Policy{Name: "p"}})
			if err != nil {
				t.Fatal(err)
			}
			if out.Effect != tt.wantEffect || (decider.calls > 0) != tt.wantChatted {
				t.Fatalf("effect %s, chat calls %d", out.Effect, decider.calls)
			}
		})
	}
}

func TestJudgeDecisionNoFallbackPastBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	decider := &fakeDecider{answer: provenance.Answer{Option: "allow"}}
	s := decisionSupervisor(t, &fakeDecisions{err: context.DeadlineExceeded}, decider, nil, provenance.Settings{})
	if _, err := s.Judge(ctx, provenance.JudgeInput{}); err == nil {
		t.Fatal("expected an error, which the engine turns into require_approval")
	}
	if decider.calls != 0 {
		t.Fatal("fell back to the chat path after the budget ran out")
	}
}

func TestTriageByDecision(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
		answer    schemas.DecisionAnswer
		wantRec   string
		wantConf  float64
		wantIn    string
	}{
		{
			name:     "confident approve",
			answer:   choiceAnswer("approve", map[string]float64{"approve": 0.85, "deny": 0.1, "escalate": 0.05}),
			wantRec:  "approve",
			wantConf: 0.85,
			wantIn:   "I recommend approving",
		},
		{
			name:     "unsure approve escalates",
			answer:   choiceAnswer("approve", map[string]float64{"approve": 0.5, "deny": 0.4, "escalate": 0.1}),
			wantRec:  "escalate",
			wantConf: 0.5,
			wantIn:   "below the 60%",
		},
		{
			name:      "custom threshold",
			threshold: 0.9,
			answer:    choiceAnswer("deny", map[string]float64{"deny": 0.85}),
			wantRec:   "escalate",
			wantConf:  0.85,
			wantIn:    "below the 90%",
		},
		{
			name:     "escalate stays escalate",
			answer:   choiceAnswer("escalate", map[string]float64{"escalate": 0.3}),
			wantRec:  "escalate",
			wantConf: 0.3,
			wantIn:   "security reviewer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &fakeDecisions{model: "anthropic/claude-haiku", answer: func(schemas.DecisionQuestion) schemas.DecisionAnswer { return tt.answer }}
			var opts []Option
			if tt.threshold > 0 {
				opts = append(opts, WithTriageThreshold(tt.threshold))
			}
			s := decisionSupervisor(t, dc, &fakeDecider{}, nil, provenance.Settings{}, opts...)
			got, err := s.TriageApproval(context.Background(), provenance.Approval{PolicyName: "egress", ExpiresAt: time.Now()}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Recommendation != tt.wantRec || got.Confidence != tt.wantConf || !strings.Contains(got.Rationale, tt.wantIn) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestClassifyContentByDecision(t *testing.T) {
	probs := map[string]float64{
		"pii":         0.95,
		"secret":      0.69,
		"financial":   0.5,
		"source-code": -1, // refused
	}
	tests := []struct {
		name     string
		settings provenance.ContentTagging
		want     provenance.Labels
	}{
		{
			name: "default threshold",
			want: provenance.Labels{provenance.LabelPII},
		},
		{
			name:     "global threshold",
			settings: provenance.ContentTagging{Threshold: 0.6},
			want:     provenance.Labels{provenance.LabelPII, provenance.LabelSecret},
		},
		{
			name: "per-label threshold",
			settings: provenance.ContentTagging{
				LabelThresholds: map[string]float64{"financial": 0.4, "pii": 0.99},
			},
			want: provenance.Labels{provenance.LabelFinancial},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := &fakeDecisions{model: "m", answer: predicateAnswers(probs)}
			s := decisionSupervisor(t, dc, nil, nil, provenance.Settings{ContentTagging: tt.settings})
			out, err := s.ClassifyContent(context.Background(), provenance.ContentToClassify{Text: "Jane Doe, jane@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			if out.Labels.String() != tt.want.String() {
				t.Fatalf("labels = %s, want %s", out.Labels, tt.want)
			}
			if len(dc.lastQs) != len(provenance.ContentLabels) {
				t.Fatalf("asked %d questions", len(dc.lastQs))
			}
			for _, q := range dc.lastQs {
				if q.Type != schemas.DecisionTypePredicate {
					t.Errorf("question %s is %s", *q.Name, q.Type)
				}
			}
		})
	}
}

func TestClassifyContentUsesLabelDescriptions(t *testing.T) {
	dc := &fakeDecisions{answer: predicateAnswers(map[string]float64{"export-controlled": 0.9})}
	s := decisionSupervisor(t, dc, nil, nil, provenance.Settings{}, WithContentLabels(func(context.Context) []LabelDescription {
		return []LabelDescription{{Label: "export-controlled", Description: "technical data covered by ITAR"}}
	}))
	out, err := s.ClassifyContent(context.Background(), provenance.ContentToClassify{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dc.lastQs) != 1 || *dc.lastQs[0].Name != "export-controlled" || !strings.HasSuffix(*dc.lastQs[0].Instructions.Text, "technical data covered by ITAR") {
		t.Fatalf("questions = %+v", dc.lastQs)
	}
	if out.Labels.String() != "export-controlled" {
		t.Fatalf("labels = %s", out.Labels)
	}
}

func TestClassifyContentDecisionFallback(t *testing.T) {
	writer := &fakeTextModel{responses: []string{`{"labels": ["secret"], "rationale": "a key"}`}}
	s := decisionSupervisor(t, &fakeDecisions{err: errors.New("unsupported")}, nil, writer, provenance.Settings{})
	out, err := s.ClassifyContent(context.Background(), provenance.ContentToClassify{Text: "sk-123"})
	if err != nil {
		t.Fatal(err)
	}
	if writer.calls != 1 || out.Labels.String() != "secret" {
		t.Fatalf("chat calls %d, labels %s", writer.calls, out.Labels)
	}
}

func TestClassifyToolByDecision(t *testing.T) {
	dc := &fakeDecisions{answer: predicateAnswers(map[string]float64{
		"untrusted":       0.92,
		"writes-external": 0.61,
		"exec":            0.2,
	})}
	s := decisionSupervisor(t, dc, nil, nil, provenance.Settings{})
	labels, rationale, err := s.ClassifyTool(context.Background(), provenance.ToolDescriptor{Server: "web", Tool: "fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if labels.String() != (provenance.Labels{provenance.LabelUntrusted, provenance.CapWritesExternal}).String() {
		t.Fatalf("labels = %s", labels)
	}
	if !strings.Contains(rationale, "untrusted (92%)") {
		t.Fatalf("rationale = %q", rationale)
	}
	if len(dc.lastQs) != len(provenance.CapabilityLabels) {
		t.Fatalf("asked %d questions", len(dc.lastQs))
	}
}

func TestDecisionModelOverride(t *testing.T) {
	dc := &fakeDecisions{answer: func(schemas.DecisionQuestion) schemas.DecisionAnswer {
		return choiceAnswer("allow", map[string]float64{"allow": 1})
	}}
	s := decisionSupervisor(t, dc, &fakeDecider{}, nil, provenance.Settings{})
	s.decisionOverride = func(context.Context) string { return "llm" }
	if _, err := s.Judge(context.Background(), provenance.JudgeInput{}); err != nil {
		t.Fatal(err)
	}
	if dc.lastModel != "llm" {
		t.Fatalf("model = %q", dc.lastModel)
	}
}
