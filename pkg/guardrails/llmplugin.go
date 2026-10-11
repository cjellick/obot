package guardrails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	bifrostcore "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/gateway/types"
)

const (
	// HeaderRequestToken carries a reference to the caller's identity from Obot's LLM proxy to
	// the guardrail plugins running inside the gateway. It is set by Obot, never by callers.
	HeaderRequestToken = "x-obot-guardrails-token"

	// InputPluginName and OutputPluginName are the gateway plugins. The input plugin runs
	// first, so content is redacted before any other plugin (including request logging)
	// sees it; the output plugin runs last, which makes its response hook run first.
	InputPluginName  = "obot-guardrails-input"
	OutputPluginName = "obot-guardrails-output"

	guardrailErrorType = "guardrail_intervention"
)

var (
	requestBindings sync.Map // token -> requestBinding

	outputStateKey = schemas.BifrostContextKey("obot-guardrails-output")

	_ schemas.LLMPlugin = (*InputPlugin)(nil)
	_ schemas.LLMPlugin = (*OutputPlugin)(nil)
)

// outputState carries what the output plugin needs from the request to the response.
type outputState struct {
	plan    Plan
	request Request
	stream  *streamState
}

// InputPlugin applies input rules to model requests.
type InputPlugin struct {
	engine *Engine
}

// OutputPlugin applies output rules to model responses, including streamed ones.
type OutputPlugin struct {
	engine *Engine
}

// AttemptCheck reports whether a request may be served by a model, named "provider/model":
// whether the caller may use it and has budget left for it. An *AttemptError refuses the
// attempt with its status, error type and message; any other error is an internal failure,
// reported to the caller as a 500 without its details.
type AttemptCheck func(ctx context.Context, model string) error

// AttemptError refuses an attempt, such as with 429 budget_exceeded.
type AttemptError struct {
	Status  int
	Type    string
	Message string
}

func (e *AttemptError) Error() string { return e.Message }

// requestBinding is what Obot knows about a request it sent to the gateway.
type requestBinding struct {
	identity     Identity
	attemptCheck AttemptCheck
}

// BindRequest registers the identity of a request that is about to be sent to the gateway
// and returns the token to send in HeaderRequestToken. attemptCheck, when set, is checked
// for every model the gateway tries: the gateway's routing rules and fallbacks can serve a
// request with a model other than the one the caller asked for. Call release once the
// response has been fully delivered.
func BindRequest(identity Identity, attemptCheck AttemptCheck) (token string, release func()) {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	token = hex.EncodeToString(buf)
	requestBindings.Store(token, requestBinding{identity: identity, attemptCheck: attemptCheck})
	return token, func() { requestBindings.Delete(token) }
}

// Plugins returns the gateway plugins that enforce guardrails.
func (e *Engine) Plugins() (*InputPlugin, *OutputPlugin) {
	return &InputPlugin{engine: e}, &OutputPlugin{engine: e}
}

func requestHeaders(ctx *schemas.BifrostContext) map[string]string {
	headers, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	return headers
}

// bindingFromContext finds what Obot bound to the request. Requests Obot didn't bind, such
// as model listing or the LLM judge's own calls, aren't guarded.
func bindingFromContext(ctx *schemas.BifrostContext) (requestBinding, bool) {
	token := ""
	for k, v := range requestHeaders(ctx) {
		if strings.EqualFold(k, HeaderRequestToken) {
			token = v
		}
	}
	if token == "" {
		return requestBinding{}, false
	}
	binding, ok := requestBindings.Load(token)
	if !ok {
		return requestBinding{}, false
	}
	return binding.(requestBinding), true
}

// identityFromContext finds the identity Obot bound to the request, and the request headers
// rules may see.
func identityFromContext(ctx *schemas.BifrostContext) (Identity, map[string]string, bool) {
	binding, ok := bindingFromContext(ctx)
	if !ok {
		return Identity{}, nil, false
	}
	headers := requestHeaders(ctx)
	visible := map[string]string{}
	for k, v := range headers {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-bf-") || lower == HeaderRequestToken || lower == "authorization" || lower == "cookie" || lower == "x-api-key" {
			continue
		}
		visible[lower] = v
	}
	return binding.identity, visible, true
}

// attemptError refuses an attempt. Fallbacks stay allowed, so the gateway goes on to the
// next model, which is checked the same way.
func attemptError(err error) *schemas.BifrostError {
	status, errType, message := http.StatusInternalServerError, "api_error", "failed to check whether the request may use this model"
	if refusal := (*AttemptError)(nil); errors.As(err, &refusal) {
		status, errType, message = refusal.Status, refusal.Type, refusal.Message
	} else {
		slog.Error("failed to check an LLM gateway attempt", "error", err)
	}
	allowFallbacks := true
	return &schemas.BifrostError{
		Type:           &errType,
		StatusCode:     &status,
		AllowFallbacks: &allowFallbacks,
		Error:          &schemas.ErrorField{Type: &errType, Message: message},
	}
}

// checkAttempt refuses an attempt on a model the request may not be served by. It runs for
// every attempt, after the gateway's routing rules chose the model and for each fallback.
func checkAttempt(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) *schemas.LLMPluginShortCircuit {
	binding, ok := bindingFromContext(ctx)
	if !ok || binding.attemptCheck == nil {
		return nil
	}
	if _, name, _ := req.GetRequestFields(); name == "" {
		// Not a model call, such as listing a provider's models.
		return nil
	}
	model, _ := requestFields(req)
	if err := binding.attemptCheck(ctx, model); err != nil {
		return &schemas.LLMPluginShortCircuit{Error: attemptError(err)}
	}
	return nil
}

func conditionVarsForRequest(req *schemas.BifrostRequest, headers map[string]string) (map[string]any, string) {
	model, provider := requestFields(req)
	params := map[string]any{}
	if req.ChatRequest != nil && req.ChatRequest.Params != nil {
		if req.ChatRequest.Params.MaxCompletionTokens != nil {
			params["max_tokens"] = int64(*req.ChatRequest.Params.MaxCompletionTokens)
		}
	}
	return map[string]any{"model": model, "provider": provider, "headers": headers, "params": params}, model
}

func guardrailError(message string) *schemas.BifrostError {
	status := http.StatusBadRequest
	errType := guardrailErrorType
	allowFallbacks := false
	return &schemas.BifrostError{
		Type:           &errType,
		StatusCode:     &status,
		AllowFallbacks: &allowFallbacks,
		Error:          &schemas.ErrorField{Type: &errType, Message: message},
	}
}

func (p *InputPlugin) GetName() string { return InputPluginName }

func (p *InputPlugin) Cleanup() error { return nil }

func (p *InputPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}

func (p *InputPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	// The input plugin runs first, so no other plugin or provider sees an attempt the request
	// may not be served by.
	if denied := checkAttempt(ctx, req); denied != nil {
		return req, denied, nil
	}
	identity, headers, ok := identityFromContext(ctx)
	if !ok {
		return req, nil, nil
	}
	vars, model := conditionVarsForRequest(req, headers)
	plan, err := p.engine.Plan(ctx, types.GuardrailTargetLLM, StageInput, identity, vars)
	if err != nil {
		slog.Error("failed to load guardrail rules; blocking the request", "error", err)
		return req, &schemas.LLMPluginShortCircuit{Error: guardrailError("Request blocked: guardrails could not be evaluated.")}, nil
	}
	if plan.Empty() {
		return req, nil, nil
	}
	disableRawPassthrough(ctx)
	refs := requestText(req)
	d := p.engine.Evaluate(ctx, plan, Request{
		Target: types.GuardrailTargetLLM, Stage: StageInput, Identity: identity, Vars: vars,
		Segments: refs.segments(), Model: model,
	})
	if d.Blocked {
		return req, &schemas.LLMPluginShortCircuit{Error: guardrailError(d.Message)}, nil
	}
	if d.Redacted {
		refs.apply(d.Segments)
	}
	return req, nil, nil
}

func (p *InputPlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}

func (p *OutputPlugin) GetName() string { return OutputPluginName }

func (p *OutputPlugin) Cleanup() error { return nil }

func (p *OutputPlugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error {
	return nil
}

func (p *OutputPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	identity, headers, ok := identityFromContext(ctx)
	if !ok {
		return req, nil, nil
	}
	vars, model := conditionVarsForRequest(req, headers)
	plan, err := p.engine.Plan(ctx, types.GuardrailTargetLLM, StageOutput, identity, vars)
	if err != nil {
		slog.Error("failed to load guardrail rules; blocking the request", "error", err)
		return req, &schemas.LLMPluginShortCircuit{Error: guardrailError("Request blocked: guardrails could not be evaluated.")}, nil
	}
	if plan.Empty() {
		return req, nil, nil
	}
	disableRawPassthrough(ctx)
	state := &outputState{
		plan: plan,
		request: Request{
			Target: types.GuardrailTargetLLM, Stage: StageOutput, Identity: identity, Vars: vars,
			// The (already input-checked) request is context for output checks.
			History: requestText(req).segments(),
			Model:   model,
		},
	}
	if bifrostcore.IsStreamRequestType(req.RequestType) {
		state.stream = newStreamState()
		if plan.Modifies() {
			// Hold the stream until the complete response has been checked.
			ctx.PauseStream()
			state.stream.held = streamPaused(ctx)
			if !state.stream.held {
				slog.Warn("could not hold a streamed response for guardrail checks; findings can only end the stream")
			}
		}
	}
	ctx.SetValue(outputStateKey, state)
	return req, nil, nil
}

// disableRawPassthrough makes the gateway send the request it parsed, and return the response
// it parsed, instead of forwarding the client's raw request body and the provider's raw
// response. Guardrails check and rewrite the parsed forms, so raw passthrough would bypass
// them: the Anthropic integration forwards raw bodies for Claude Code, for example. This is
// the same switch the gateway makes when it falls back to a provider that can't take the raw
// body.
func disableRawPassthrough(ctx *schemas.BifrostContext) {
	ctx.SetValue(schemas.BifrostContextKeyUseRawRequestBody, false)
	ctx.ClearValue(schemas.BifrostContextKeyRawRequestBodyTextRewriter)
	ctx.ClearValue(schemas.BifrostContextKeyRawRequestBodyTextTransformer)
	ctx.ClearValue(schemas.BifrostContextKeyRawStreamTextCodec)
	ctx.ClearValue(schemas.BifrostContextKeyRawResponseTextTransformer)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawResponse, false)
	ctx.SetValue(schemas.BifrostContextKeyPassthroughOverridesPresent, false)
	ctx.ClearValue(schemas.BifrostContextKeyURLPath)
}

func streamPaused(ctx *schemas.BifrostContext) bool {
	tracer, traceID, err := bifrostcore.GetTracerFromContext(ctx)
	if err != nil || tracer == nil || traceID == "" {
		return false
	}
	return tracer.IsStreamPaused(traceID)
}

func (p *OutputPlugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	state, _ := ctx.Value(outputStateKey).(*outputState)
	if state == nil || resp == nil {
		if state != nil && state.stream != nil && state.stream.held && bifrostcore.IsFinalChunk(ctx) {
			ctx.ResumeStream()
		}
		return resp, bifrostErr, nil
	}
	if state.stream != nil {
		return p.postStream(ctx, state, resp, bifrostErr)
	}

	refs := responseText(resp)
	req := state.request
	req.Segments = refs.segments()
	d := p.engine.Evaluate(ctx, state.plan, req)
	if d.Blocked {
		return nil, guardrailError(d.Message), nil
	}
	if d.Redacted {
		refs.apply(d.Segments)
	}
	return resp, bifrostErr, nil
}

func (p *OutputPlugin) postStream(ctx *schemas.BifrostContext, state *outputState, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	st := state.stream
	st.observe(resp)
	final := bifrostcore.IsFinalChunk(ctx)
	if !final {
		if st.held {
			// Nothing reaches the client until the response is checked; downstream plugins
			// such as request logging only see content after it has been checked.
			return st.blank(resp), bifrostErr, nil
		}
		return resp, bifrostErr, nil
	}

	req := state.request
	req.Segments = st.segments()
	req.Delivered = !st.held
	d := p.engine.Evaluate(ctx, state.plan, req)
	switch {
	case d.Blocked:
		if st.held {
			if err := ctx.ClearPausedStreamBuffer(); err != nil {
				slog.Warn("failed to discard a blocked stream's buffered content", "error", err)
			}
		}
		ctx.EndStream(guardrailError(d.Message))
		return resp, bifrostErr, nil
	case !st.held:
		return resp, bifrostErr, nil
	}

	finalText := st.finalText(d)
	emitted := map[string]bool{}
	if err := ctx.TransformPausedStreamBuffer(func(chunks []*schemas.BifrostStreamChunk) (schemas.PausedStreamBufferTransformResult, error) {
		out := make([]*schemas.BifrostStreamChunk, len(chunks))
		for i, c := range chunks {
			out[i] = st.rewriteChunk(c, finalText, emitted)
		}
		return schemas.PausedStreamBufferTransformResult{Chunks: out, ReleaseCount: len(out)}, nil
	}); err != nil {
		// The client would otherwise receive the blanked content; end the stream instead.
		ctx.EndStream(guardrailError(fmt.Sprintf("Response could not be delivered after guardrail checks: %v", err)))
		return resp, bifrostErr, nil
	}
	resp = st.rewriteResponse(resp, finalText, emitted)
	ctx.ResumeStream()
	return resp, bifrostErr, nil
}
