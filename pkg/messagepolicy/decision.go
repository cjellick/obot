package messagepolicy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/apiclient/types"
)

const (
	// DecisionModelOpenAI is the model bounded decisions use when the LLM gateway has an
	// OpenAI provider: OpenAI's native decisions model.
	DecisionModelOpenAI = "openai/gpt-6-luna"
)

// ErrDecisionsUnsupported is returned when the LLM gateway cannot make decision calls.
var ErrDecisionsUnsupported = errors.New("the LLM gateway does not support decisions")

// decisionEngine is implemented by gateways that serve Bifrost's decision API.
type decisionEngine interface {
	Decision(context.Context, string, schemas.DecisionInput, []schemas.DecisionQuestion) (*schemas.BifrostDecisionResponse, error)
}

// providerEngine is implemented by gateways that report their configured providers.
type providerEngine interface {
	ProviderConfigured(provider string) bool
}

// Decide asks typed questions (predicates, choices, scores) about input through the LLM
// gateway's decision API and returns the response and the gateway model that answered.
//
// model is resolved as Complete documents, except that "" means the decision default:
// DecisionModelOpenAI when the gateway has an OpenAI provider, which serves decisions
// natively, and otherwise the llm-mini alias's model, for which the gateway emulates the
// decision. The call is recorded in usage and the LLM audit log like every other call.
func (h *Helper) Decide(ctx context.Context, model, input string, questions []schemas.DecisionQuestion) (*schemas.BifrostDecisionResponse, string, error) {
	engine, ok := h.llmGateway.(decisionEngine)
	if !ok {
		return nil, "", ErrDecisionsUnsupported
	}
	resolved, err := h.resolveDecisionModel(ctx, model)
	if err != nil {
		return nil, "", err
	}
	start := time.Now()
	resp, err := engine.Decision(ctx, resolved.targetModel, schemas.DecisionInput{Text: &input}, questions)
	var usage *schemas.BifrostLLMUsage
	if resp != nil {
		usage = resp.Usage
	}
	h.record(ctx, resolved, usage, start, err)
	if err != nil {
		return nil, resolved.targetModel, fmt.Errorf("decision call failed: %w", err)
	}
	if resp == nil || len(resp.Answers) != len(questions) {
		return nil, resolved.targetModel, errors.New("decision call returned the wrong number of answers")
	}
	return resp, resolved.targetModel, nil
}

// DecisionModel returns the gateway model Decide uses for model "".
func (h *Helper) DecisionModel(ctx context.Context) string {
	resolved, err := h.resolveDecisionModel(ctx, "")
	if err != nil {
		return ""
	}
	return resolved.targetModel
}

func (h *Helper) resolveDecisionModel(ctx context.Context, model string) (*resolvedModel, error) {
	if model != "" {
		return h.resolveNamedModel(ctx, model)
	}
	if p, ok := h.llmGateway.(providerEngine); ok && p.ProviderConfigured(string(schemas.OpenAI)) {
		return &resolvedModel{targetModel: DecisionModelOpenAI}, nil
	}
	return h.resolveModelByAlias(ctx, types.DefaultModelAliasTypeLLMMini)
}
