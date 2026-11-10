package llmcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"
)

// decisionEmulationPlugin decides, for every attempt of a decision request, whether the
// attempt's provider serves the model's decisions natively (OpenAI's POST /v1/decisions for
// gpt-6-luna) or whether core must emulate the decision through the model's Responses API
// with a forced function call. Bifrost's compat plugin makes the same choice; Obot does not
// run that plugin, so the gateway marks decision attempts itself.
//
// Core clears the mark between attempts, so a native primary can fall back to an emulated
// model and the other way around.
type decisionEmulationPlugin struct{}

func (*decisionEmulationPlugin) GetName() string { return "obot-decision-emulation" }
func (*decisionEmulationPlugin) Cleanup() error  { return nil }

func (*decisionEmulationPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}

func (*decisionEmulationPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if req == nil || req.DecisionRequest == nil || ctx == nil {
		return req, nil, nil
	}
	if !NativeDecisions(ctx, req.DecisionRequest.Provider, req.DecisionRequest.Model) {
		ctx.SetValue(schemas.BifrostContextKeyChangeRequestType, schemas.ResponsesRequest)
	} else {
		ctx.ClearValue(schemas.BifrostContextKeyChangeRequestType)
	}
	return req, nil, nil
}

func (*decisionEmulationPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, err, nil
}

// NativeDecisions reports whether provider serves model's decisions on its own decisions
// endpoint. It follows Bifrost's compat plugin: the model catalog's supports_decisions row
// when there is one, otherwise the decision model families providers are known to serve.
func NativeDecisions(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string) bool {
	if provider == schemas.Typesafe {
		return true
	}
	canonical := model
	if ctx != nil {
		canonical = schemas.ResolveCanonicalModel(ctx, model)
	}
	fallback := (provider != schemas.OpenRouter && schemas.DefaultSupportsDecisions(canonical)) || schemas.IsTypesafeModel(canonical) ||
		(provider == schemas.Cloudflare && schemas.IsClefModel(canonical)) ||
		(provider == schemas.Perplexity && schemas.IsPerplexityDeciderModel(canonical)) ||
		(provider == schemas.Ollama && schemas.IsOllamaDecisionModel(canonical))
	return schemas.ResolveModelCaps(provider, canonical).SupportsDecisions(fallback)
}

// ProviderConfigured reports whether the gateway has a provider with at least one key.
func (e *Engine) ProviderConfigured(provider schemas.ModelProvider) bool {
	cfg := e.account.config.Load()
	if cfg == nil {
		return false
	}
	if _, ok := cfg.Providers[provider]; !ok {
		return false
	}
	return len(cfg.Keys[provider]) > 0
}

// Decision is for trusted internal callers: it asks model the questions about input through
// Bifrost's decision API, natively where the provider serves decisions and emulated through
// Responses elsewhere. It shares HTTP model normalization and the configured routing and
// plugin chain. The response's usage carries the gateway's price for it when the catalog
// knows the model and the provider reported none.
func (e *Engine) Decision(parent context.Context, model string, input schemas.DecisionInput, questions []schemas.DecisionQuestion) (resp *schemas.BifrostDecisionResponse, err error) {
	// Policy workers have no HTTP recovery. Contain synchronous hook failures here.
	defer func() {
		if recover() != nil {
			resp = nil
			err = errors.New("LLM gateway decision failed")
		}
	}()
	ctx, cancel := e.inferenceContext(parent)
	defer cancel()
	ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, map[string]string{"content-type": "application/json"})
	provider, normalized, _ := e.normalizeRequestModels(ctx, model, nil)
	if provider == "" {
		return nil, fmt.Errorf("model %q must be named as provider/model", model)
	}
	resp, engineErr := e.client.DecisionRequest(ctx, &schemas.BifrostDecisionRequest{
		Provider:  provider,
		Model:     normalized,
		Input:     input,
		Questions: questions,
	})
	if engineErr != nil {
		_, _, message := errorParts(engineErr)
		return nil, errors.New(message)
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	priceDecision(ctx, resp)
	return resp, nil
}

// priceDecision fills the response usage's cost from the model catalog, so callers outside
// the plugin chain can record spend.
func priceDecision(ctx *schemas.BifrostContext, resp *schemas.BifrostDecisionResponse) {
	if resp == nil || resp.Usage == nil || resp.Usage.Cost != nil {
		return
	}
	if cost := ctx.CalculateCostBreakdown(&schemas.BifrostResponse{DecisionResponse: resp}); cost != nil {
		resp.Usage.Cost = cost
	}
}

// serveDecision serves OpenAI's Decisions API at POST /v1/decisions for any configured
// model: natively for models whose provider serves decisions, emulated through Responses
// for the rest.
func (e *Engine) serveDecision(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, e.MaxRequestBodyBytes()))
	if err != nil {
		writeBodyReadError(w, dialectResponses, err)
		return
	}

	ctx, cancel := e.requestContext(r)
	defer cancel()
	var in openai.OpenAIDecisionRequest
	if err := json.Unmarshal(body, &in); err != nil {
		writeError(w, dialectResponses, http.StatusBadRequest, "invalid_request_error", "invalid request body: "+err.Error())
		return
	}
	req, err := in.ToBifrostDecisionRequest()
	if err != nil {
		writeError(w, dialectResponses, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	// The gateway, not OpenAI's route default, decides which provider a model name means.
	req.Provider, req.Model, req.Fallbacks = e.normalizeRequestModels(ctx, in.Model, in.Fallbacks)
	if req.Provider == "" {
		writeError(w, dialectResponses, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("model %q must be named as provider/model", req.Model))
		return
	}
	// Extensions are not forwarded; an emulated decision cannot honor them anyway.
	req.ExtraParams = nil

	resp, engineErr := e.client.DecisionRequest(ctx, req)
	if engineErr != nil {
		observeError(ctx, engineErr)
		writeEngineError(w, dialectResponses, engineErr)
		return
	}
	observeResponse(ctx, resp.ExtraFields)
	setServed(w.Header(), resp.ExtraFields)
	writeJSON(w, dialectResponses, http.StatusOK, openai.ToOpenAIDecisionResponse(resp))
}
