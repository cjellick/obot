package messagepolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/aiusage"
)

type decisionGateway struct {
	usageEngine
	openai    bool
	err       error
	lastModel string
}

func (g *decisionGateway) ProviderConfigured(provider string) bool {
	return g.openai && provider == "openai"
}

func (g *decisionGateway) Decision(_ context.Context, model string, _ schemas.DecisionInput, questions []schemas.DecisionQuestion) (*schemas.BifrostDecisionResponse, error) {
	g.lastModel = model
	if g.err != nil {
		return nil, g.err
	}
	resp := &schemas.BifrostDecisionResponse{Model: model, Usage: &schemas.BifrostLLMUsage{PromptTokens: 40, CompletionTokens: 2}}
	for range questions {
		resp.Answers = append(resp.Answers, schemas.DecisionAnswer{Type: schemas.DecisionTypePredicate, Probability: schemas.Ptr(0.5)})
	}
	return resp, nil
}

func TestDecideUsesGPT6LunaWhenOpenAIIsConfigured(t *testing.T) {
	gw := &decisionGateway{openai: true}
	recorder := &capturedCalls{}
	h := NewHelper(nil, gw)
	h.SetUsageRecorder(recorder)
	ctx := aiusage.WithFeature(t.Context(), aiusage.FeatureContentTagger)
	_, model, err := h.Decide(ctx, "", "evidence", []schemas.DecisionQuestion{{Type: schemas.DecisionTypePredicate}})
	if err != nil {
		t.Fatal(err)
	}
	if model != DecisionModelOpenAI || gw.lastModel != DecisionModelOpenAI {
		t.Fatalf("model = %q, sent %q", model, gw.lastModel)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].Feature != aiusage.FeatureContentTagger || recorder.calls[0].Usage.PromptTokens != 40 {
		t.Fatalf("recorded %+v", recorder.calls)
	}
}

func TestDecideNamedModelAndFailure(t *testing.T) {
	gw := &decisionGateway{err: errors.New("boom")}
	recorder := &capturedCalls{}
	h := NewHelper(nil, gw)
	h.SetUsageRecorder(recorder)
	if _, _, err := h.Decide(t.Context(), "anthropic/claude-haiku-4-5", "x", []schemas.DecisionQuestion{{Type: schemas.DecisionTypePredicate}}); err == nil {
		t.Fatal("expected an error")
	}
	if gw.lastModel != "anthropic/claude-haiku-4-5" {
		t.Fatalf("sent %q", gw.lastModel)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].Err == nil {
		t.Fatalf("a failed call is recorded too: %+v", recorder.calls)
	}
}

func TestDecideUnsupported(t *testing.T) {
	h := NewHelper(nil, usageEngine{})
	if _, _, err := h.Decide(t.Context(), "", "x", nil); !errors.Is(err, ErrDecisionsUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
