package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/gateway/server/dispatcher"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/guardrails"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	"github.com/obot-platform/obot/pkg/messagepolicy"
	"github.com/obot-platform/obot/pkg/modelaccesspolicy"
	"github.com/obot-platform/obot/pkg/principal"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kuser "k8s.io/apiserver/pkg/authentication/user"
)

const (
	tokenUsageTimePeriod = 24 * time.Hour

	internalRequestTypeHeader = "X-Nanobot-Internal-Request-Type"
	threadTitleRequestType    = "nanobot.summary.thread_title"
)

var (
	openAIBaseURL    = "https://api.openai.com/v1"
	anthropicBaseURL = "https://api.anthropic.com/v1"
)

type responseModifier struct {
	user                kuser.Info
	model               string
	modelProvider       string
	routeDialect        llmtypes.Dialect
	projectID, threadID string
	client              *client.Client
	b                   *bufio.Reader
	c                   io.Closer
	stream              bool
	leftover            []byte
	// Non-streaming JSON is accumulated while forwarded, then parsed once at EOF or Close.
	nonStreamResponse bytes.Buffer
	nonStreamParsed   bool

	// Token usage and model access gating
	tokenUsageTracker *threadSafeTokenUsageTracker
	// gateway tracks an LLM gateway request: the budgets its usage counts against, which
	// are those of the model that served it. It is nil for other backends.
	gateway   gatewayRequest
	modelID   string
	mapHelper *modelaccesspolicy.Helper

	// Input policy violation: replacement text to send back via response header.
	inputPolicyReplacement string

	// Output (tool-call) policy evaluation fields.
	messagePolicyHelper *messagepolicy.Helper
	outputPolicies      []messagepolicy.ApplicablePolicy
	conversationHistory []messagepolicy.ConversationMessage
	pipeReader          *io.PipeReader // set when output policies are active; Read() reads from this
	audit               *llmAuditRecorder
}

type preparedLLMProxyRequest struct {
	body              []byte
	model             string
	tokenUsageTracker *threadSafeTokenUsageTracker
}

type llmProviderProxyBackend interface {
	modelProviderName() string
	upstreamURL(req *http.Request, credEnv map[string]string) (url.URL, llmtypes.Dialect, error)
	transport(provider v1.ModelProvider, credEnv map[string]string) (http.RoundTripper, error)
}

// credentialOptionalBackend is implemented by backends whose upstream credentials are not
// stored as an Obot model provider credential.
type credentialOptionalBackend interface {
	requiresCredential() bool
}

// trackerDialectBackend is implemented by backends that choose the usage tracker from the
// route's dialect rather than the model's.
type trackerDialectBackend interface {
	trackerDialect(routeDialect llmtypes.Dialect) llmtypes.Dialect
}

// modelResolvingBackend is implemented by backends with an additional way to resolve a
// model reference that the standard lookup did not find. A nil model means no match.
type modelResolvingBackend interface {
	resolveModel(l *llmProviderProxy, reference string) (*v1.Model, error)
}

// extraModelAuthorizingBackend is implemented by backends whose requests can name models
// besides the primary one (such as fallbacks), each of which must be authorized.
type extraModelAuthorizingBackend interface {
	authorizeExtraModels(l *llmProviderProxy, req api.Context, body []byte) ([]byte, error)
}

// gatewayRequest is what the LLM gateway backend tracks about one request. The gateway's
// routing rules and fallbacks choose the model that serves it, and its traffic is governed by
// budgets, which apply to that model, instead of the daily token quota.
type gatewayRequest interface {
	// release ends the request once its response has been delivered.
	release()
	// budgetIDs are the budgets a response served by model ("provider/model") counts against.
	budgetIDs(model string) []string
	// budgetExceeded is the budget that refused the last attempt, if one did.
	budgetExceeded() *client.LLMGatewayBudgetExceeded
}

// requestDecoratingBackend is implemented by the LLM gateway backend, which adds headers for
// the upstream and checks each model the gateway tries for the request.
type requestDecoratingBackend interface {
	decorateRequest(l *llmProviderProxy, req api.Context, model *v1.Model) (gatewayRequest, error)
}

type llmProviderProxy struct {
	dailyUserInputTokenLimit  int
	dailyUserOutputTokenLimit int
	backend                   llmProviderProxyBackend
	modelProvider             *v1.ModelProvider
	mapHelper                 *modelaccesspolicy.Helper
	messagePolicyHelper       *messagepolicy.Helper
	lock                      sync.RWMutex
}

func init() {
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		openAIBaseURL = base
	}
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		anthropicBaseURL = base
	}
}

func envVarForModelProvider(modelProvider v1.ModelProvider) (string, error) {
	for _, envVar := range modelProvider.Spec.RequiredConfigurationParameters {
		if strings.HasSuffix(envVar.Name, "_MODEL_PROVIDER_API_KEY") {
			return envVar.Name, nil
		}
	}

	return "", fmt.Errorf("model provider %q does not have an API key", modelProvider.Name)
}

// extractModelFromBody extracts the model name from an arbitrary JSON payload or body,
// checking top-level "model" first, then nested paths such as "message.model" and "response.model"
// used by different providers or event/response shapes.
func extractModelFromBody(body []byte) string {
	if model := gjson.GetBytes(body, "model").String(); model != "" {
		return model
	}
	if model := gjson.GetBytes(body, "message.model").String(); model != "" {
		return model
	}
	return gjson.GetBytes(body, "response.model").String()
}

func rewriteModelInBody(body []byte, model string) ([]byte, error) {
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	if _, ok := bodyMap["model"]; ok {
		bodyMap["model"] = model
	}
	return json.Marshal(bodyMap)
}

// copyBody returns a copy of the bytes in a request or response body.
// If the copy was successful the body is restored to its original state before returning so that
// it can be reused.
// The returned byte slice is safe to modify without affecting the restored body.
func copyBody(body *io.ReadCloser) ([]byte, error) {
	b, err := io.ReadAll(*body)
	(*body).Close()

	if err != nil {
		// b can be partial results on error, don't restore the body
		return nil, err
	}

	// Read was successful, restore the body with a copy.
	*body = io.NopCloser(bytes.NewReader(slices.Clone(b)))
	return b, nil
}

func (r *responseModifier) modifyResponse(resp *http.Response) error {
	if isModelsListRequest(resp.Request) {
		// Which authority applies is decided the same way the inference path
		// decides it, or the list contradicts the requests it describes: a
		// hosted agent evaluated against its owner's policies is told it may
		// use nothing while every call it makes succeeds. A client that picks
		// its model by listing -- Claude Code does -- then refuses to run at
		// all against models it is in fact authorized for.
		var (
			allowedTargetModels map[string]bool
			allowAllModels      bool
			err                 error
		)
		if agentModels, isAgent := principal.AuthorizedModelIDs(r.user); isAgent {
			allowedTargetModels, allowAllModels, err = r.mapHelper.GetAgentAllowedTargetModels(agentModels, r.modelProvider, string(r.routeDialect))
		} else {
			allowedTargetModels, allowAllModels, err = r.mapHelper.GetUserAllowedTargetModels(r.user, r.modelProvider, string(r.routeDialect))
		}
		if err != nil {
			return fmt.Errorf("failed to determine accessible models: %w", err)
		}
		if r.modelProvider == system.LLMGatewayModelProvider {
			// The gateway's Anthropic-format model list uses bare model IDs, which the
			// gateway also accepts in requests, so allow the bare form of each target too.
			for target := range maps.Clone(allowedTargetModels) {
				if _, bare, ok := strings.Cut(target, "/"); ok {
					allowedTargetModels[bare] = true
				}
			}
		}
		if err := filterModelListResponse(resp, allowedTargetModels, allowAllModels); err != nil {
			return err
		}
		r.audit.recordResponse(resp)
		wrapAuditOnlyResponse(resp, r.audit, r.client)
		return nil
	}

	if r.inputPolicyReplacement != "" {
		resp.Header.Set("X-Obot-Message-Policy-Replacement", r.inputPolicyReplacement)
	}
	r.rewriteBudgetExceeded(resp)
	r.applyServedModel(resp)
	r.audit.recordResponse(resp)

	if !shouldWrapLLMResponse(resp) && !r.shouldWrapGatewayResponse(resp) {
		wrapAuditOnlyResponse(resp, r.audit, r.client)
		return nil
	}

	r.c = resp.Body
	r.b = bufio.NewReader(resp.Body)
	r.stream = strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	resp.Body = r

	// When tool-call output policies are active, launch a goroutine that streams text
	// through immediately while buffering tool call chunks for policy evaluation.
	if len(r.outputPolicies) > 0 {
		pr, pw := io.Pipe()
		r.pipeReader = pr
		go r.streamAndEvaluateToolCalls(resp.Request.Context(), pw)
	}

	return nil
}

// applyServedModel costs and records a gateway response by the model that served it. The
// gateway's routing rules and fallbacks can serve a request with another model than the
// requested one, which may cost more or less.
func (r *responseModifier) applyServedModel(resp *http.Response) {
	// A refused request names the model it was routed to, but wasn't served by it.
	if r.modelProvider != system.LLMGatewayModelProvider || resp.StatusCode < 200 || resp.StatusCode > 299 {
		return
	}
	m := servedModelChange(resp.Header, r.model, func(served string) (*v1.Model, error) {
		return r.mapHelper.ResolveTargetModel(system.LLMGatewayModelProvider, served)
	})
	if m == nil {
		return
	}
	r.model = m.Spec.Manifest.TargetModel
	r.modelID = m.Name
	r.tokenUsageTracker.setCost(m.GetCost())
	r.audit.setModel(r.modelProvider, m.Name, m.Spec.Manifest.TargetModel)
}

// servedModelChange returns the model that served a gateway response when it isn't the
// requested one, and nil when it is or can't be told.
func servedModelChange(header http.Header, requested string, resolve func(string) (*v1.Model, error)) *v1.Model {
	provider, model := header.Get(headerServedProvider), header.Get(headerServedModel)
	if provider == "" || model == "" {
		return nil
	}
	served := provider + "/" + model
	if served == requested {
		return nil
	}
	m, err := resolve(served)
	if err != nil || m == nil {
		slog.Warn("LLM gateway response was served by an unknown model; recording it against the requested model", "requested", requested, "served", served, "error", err)
		return nil
	}
	return m
}

// budgetExceededType is the error type of a request refused because a budget is exhausted.
const budgetExceededType = "budget_exceeded"

func shouldWrapLLMResponse(resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	switch resp.Request.URL.Path {
	case "/v1/messages", "/anthropic/v1/messages", "/v1/responses", "/openai/v1/responses":
		return true
	default:
		return false
	}
}

// shouldWrapGatewayResponse reports whether an LLM gateway response carries usage that
// Obot must meter. The gateway also serves Chat Completions and embeddings, which the
// per-provider routes pass through unmetered.
func (r *responseModifier) shouldWrapGatewayResponse(resp *http.Response) bool {
	if r.modelProvider != system.LLMGatewayModelProvider || resp.StatusCode != http.StatusOK {
		return false
	}
	switch resp.Request.URL.Path {
	case "/v1/chat/completions", "/v1/embeddings":
		return true
	default:
		return false
	}
}

// filterModelListResponse rewrites a provider models-list response, dropping any
// model whose id is not in allowedTargetModels (the set the user may use). Both
// the Anthropic and OpenAI list endpoints return a top-level
// {"data": [{"id": ...}, ...]} envelope, so the same filtering applies to both
// passthroughs. Surviving entries are kept as their exact upstream bytes — no
// map[string]any round-trip that would reorder object keys or reformat numbers.
// The body is copied up front and restored, so any failure (or an unrecognized
// payload) just forwards the original response untouched.
func filterModelListResponse(resp *http.Response, allowedTargetModels map[string]bool, allowAllModels bool) error {
	if resp.StatusCode != http.StatusOK || allowAllModels {
		return nil
	}

	// copyBody restores resp.Body, so every early return below forwards the
	// original response unchanged; we only swap in the filtered body once it's
	// fully built.
	body, err := copyBody(&resp.Body)
	if err != nil {
		return err
	}

	// Only rewrite a recognized {"data": [...]} list envelope.
	data := gjson.GetBytes(body, "data")
	if !data.IsArray() {
		return nil
	}

	// Keep each allowed entry exactly as the provider encoded it (its raw bytes).
	var kept []string
	data.ForEach(func(_, entry gjson.Result) bool {
		if allowedTargetModels[entry.Get("id").String()] {
			kept = append(kept, entry.Raw)
		}
		return true
	})

	out, err := sjson.SetRawBytes(body, "data", []byte("["+strings.Join(kept, ",")+"]"))
	if err != nil {
		return nil
	}
	out, err = rewriteAnthropicListCursors(out)
	if err != nil {
		return nil
	}

	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Header.Del("Content-Encoding")

	return nil
}

// rewriteAnthropicListCursors repoints first_id/last_id at the (already filtered)
// data array so the Anthropic pagination cursors never reference a model the user
// can't see (the SDK paginates off last_id). OpenAI lists carry no such fields
// and are returned unchanged. On an empty page the cursors are removed rather
// than nulled, except last_id is kept when more pages remain so the client can
// still advance.
func rewriteAnthropicListCursors(body []byte) ([]byte, error) {
	hasFirst := gjson.GetBytes(body, "first_id").Exists()
	hasLast := gjson.GetBytes(body, "last_id").Exists()
	if !hasFirst && !hasLast {
		return body, nil
	}

	var err error
	if ids := gjson.GetBytes(body, "data.#.id").Array(); len(ids) > 0 {
		if hasFirst {
			if body, err = sjson.SetBytes(body, "first_id", ids[0].String()); err != nil {
				return nil, err
			}
		}
		if hasLast {
			if body, err = sjson.SetBytes(body, "last_id", ids[len(ids)-1].String()); err != nil {
				return nil, err
			}
		}
		return body, nil
	}

	// Empty page.
	if hasFirst {
		if body, err = sjson.DeleteBytes(body, "first_id"); err != nil {
			return nil, err
		}
	}
	if hasLast && !gjson.GetBytes(body, "has_more").Bool() {
		if body, err = sjson.DeleteBytes(body, "last_id"); err != nil {
			return nil, err
		}
	}
	return body, nil
}

func (r *responseModifier) Read(p []byte) (int, error) {
	// When output policies are active, the goroutine handles everything via the pipe.
	if r.pipeReader != nil {
		n, err := r.pipeReader.Read(p)
		if n > 0 {
			r.audit.captureResponseChunk(p[:n])
		}
		return n, err
	}

	if !r.stream {
		n, err := r.b.Read(p)
		if n > 0 {
			_, _ = r.nonStreamResponse.Write(p[:n])
			r.audit.captureResponseChunk(p[:n])
		}
		if errors.Is(err, io.EOF) {
			r.parseNonStreamTokenUsage()
		}
		return n, err
	}

	if len(r.leftover) > 0 {
		n := copy(p, r.leftover)
		r.leftover = r.leftover[n:]
		r.audit.captureResponseChunk(p[:n])
		return n, nil
	}

	line, err := r.b.ReadBytes('\n')
	if len(line) > 0 && errors.Is(err, io.EOF) {
		// Don't send an EOF until we read everything.
		err = nil
	}
	if err != nil {
		n := copy(p, line)
		r.audit.captureResponseChunk(p[:n])
		return n, err
	}

	var prefix []byte
	if r.stream {
		prefix = []byte("data: ")
		rest, ok := bytes.CutPrefix(line, prefix)
		if !ok {
			// This isn't a data line, so send it through.
			n := copy(p, line)
			r.audit.captureResponseChunk(p[:n])
			return n, nil
		}
		line = rest
	}

	r.tokenUsageTracker.addTokenUsage(line)

	n := copy(p, prefix)
	if n < len(prefix) {
		// We couldn't copy the entire prefix, so save the leftover for the next read and return.
		r.leftover = append(prefix[n:], line...)
		r.audit.captureResponseChunk(p[:n])
		return n, nil
	}

	n += copy(p[n:], line)

	// If we didn't copy the entire line, then save the leftover for the next read.
	r.leftover = line[n-len(prefix):]

	r.audit.captureResponseChunk(p[:n])
	return n, nil
}

func (r *responseModifier) parseNonStreamTokenUsage() {
	if r.stream || r.pipeReader != nil || r.nonStreamParsed {
		return
	}
	r.tokenUsageTracker.addTokenUsage(r.nonStreamResponse.Bytes())
	r.nonStreamParsed = true
}

// streamAndEvaluateToolCalls reads the upstream response, streams text through immediately,
// buffers tool call chunks, and evaluates policies only against tool calls.
// For non-streaming responses, it buffers the entire response and evaluates inline.
func (r *responseModifier) streamAndEvaluateToolCalls(ctx context.Context, pw *io.PipeWriter) {
	defer pw.Close()

	if r.stream {
		r.streamAndEvaluateToolCallsSSE(ctx, pw)
	} else {
		r.streamAndEvaluateToolCallsJSON(ctx, pw)
	}
}

// streamAndEvaluateToolCallsSSE handles streaming (SSE) responses.
// Text delta chunks are forwarded immediately; once the first tool_call chunk appears,
// all remaining lines are buffered until the stream ends. After policy evaluation,
// the buffered lines (including tool calls) are forwarded unmodified, and a violation
// marker is injected if a policy was violated. The downstream client (nanobot) detects
// this marker and returns error tool_results instead of executing the tools.
func (r *responseModifier) streamAndEvaluateToolCallsSSE(ctx context.Context, pw *io.PipeWriter) {
	var (
		buffered             [][]byte // all lines from the first tool_call onward, in original order
		toolCalls            []messagepolicy.ToolCallInfo
		seenToolCalls        bool
		anthropicBlockToTool map[int]int // maps Anthropic content block index → toolCalls slice index
		responsesItemToTool  map[int]int // maps Responses API output_index → toolCalls slice index
	)

	for {
		line, err := r.b.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}

		// Once we see the first tool_call chunk, buffer everything remaining
		// so we can evaluate policies before deciding what to send.
		if seenToolCalls {
			buffered = append(buffered, slices.Clone(line))

			rest, isData := bytes.CutPrefix(line, []byte("data: "))
			if isData {
				r.tokenUsageTracker.addTokenUsage(rest)

				// Anthropic format: content_block_start/content_block_delta
				accumulateAnthropicToolCallInfo(rest, &toolCalls, anthropicBlockToTool)
				// OpenAI Responses API format: response.output_item.added / response.function_call_arguments.delta
				accumulateResponsesAPIToolCallInfo(rest, &toolCalls, responsesItemToTool)
			}

			if err != nil {
				break
			}
			continue
		}

		rest, isData := bytes.CutPrefix(line, []byte("data: "))
		if !isData {
			_, _ = pw.Write(line)
			if err != nil {
				break
			}
			continue
		}

		r.tokenUsageTracker.addTokenUsage(rest)

		if isAnthropicToolCallEvent(rest) {
			// Anthropic-format tool calls (content_block_start with type "tool_use").
			seenToolCalls = true
			anthropicBlockToTool = make(map[int]int)
			buffered = append(buffered, slices.Clone(line))
			accumulateAnthropicToolCallInfo(rest, &toolCalls, anthropicBlockToTool)
		} else if isResponsesAPIToolCallEvent(rest) {
			// OpenAI Responses API tool calls (response.output_item.added with function_call).
			seenToolCalls = true
			responsesItemToTool = make(map[int]int)
			buffered = append(buffered, slices.Clone(line))
			accumulateResponsesAPIToolCallInfo(rest, &toolCalls, responsesItemToTool)
		} else {
			_, _ = pw.Write(line)
		}

		if err != nil {
			break
		}
	}

	if len(toolCalls) == 0 {
		for _, line := range buffered {
			_, _ = pw.Write(line)
		}
		return
	}

	// Evaluate policies against tool calls.
	targetMessage := buildToolCallTargetMessage(toolCalls)
	slog.Info("evaluating tool calls against policies", "toolCalls", len(toolCalls), "policies", len(r.outputPolicies))
	violations := r.messagePolicyHelper.EvaluateMessage(ctx, r.outputPolicies, r.conversationHistory, targetMessage, types2.PolicyDirectionToolCalls)

	if len(violations) == 0 {
		for _, line := range buffered {
			_, _ = pw.Write(line)
		}
		return
	}

	// Log each violation.
	blockedContent, _ := json.Marshal(toolCalls)
	for _, v := range violations {
		logViolation(context.Background(), r.client, v, r.user.GetUID(), string(types2.PolicyDirectionToolCalls), blockedContent, r.projectID, r.threadID)
	}

	// Violation — forward tool calls (to keep conversation history valid)
	// and inject a violation marker that nanobot can detect.
	var explanations []string
	for _, v := range violations {
		explanations = append(explanations, v.Explanation)
	}
	notification := fmt.Sprintf(
		"This tool call was blocked due to a policy violation. Please inform the user that you cannot complete their requested action. Explanation: %s",
		strings.Join(explanations, "\n"),
	)
	violationJSON, _ := json.Marshal(map[string]string{
		"obot_tool_call_policy_violation": notification,
	})
	violationLine := fmt.Sprintf("data: %s\n\n", violationJSON)

	// Flush buffered lines, injecting the violation marker before the stream terminator.
	// OpenAI Responses API ends with response.completed; Anthropic ends with content_block_stop.
	injected := false
	for _, line := range buffered {
		if !injected {
			rest, isData := bytes.CutPrefix(line, []byte("data: "))
			if isData {
				trimmed := bytes.TrimSpace(rest)
				eventType := gjson.GetBytes(trimmed, "type").String()
				isResponsesCompleted := eventType == "response.completed"
				isAnthropicStop := eventType == "content_block_stop"
				if isResponsesCompleted || isAnthropicStop {
					_, _ = pw.Write([]byte(violationLine))
					injected = true
				}
			}
		}
		_, _ = pw.Write(line)
	}
	if !injected {
		_, _ = pw.Write([]byte(violationLine))
	}
}

// streamAndEvaluateToolCallsJSON handles non-streaming (single JSON) responses.
func (r *responseModifier) streamAndEvaluateToolCallsJSON(ctx context.Context, pw *io.PipeWriter) {
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r.b)
	body := buf.Bytes()

	r.tokenUsageTracker.addTokenUsage(body)

	// Anthropic format: content array with type "tool_use"
	anthropicContent := gjson.GetBytes(body, "content")
	// OpenAI Responses API format: output array with type "function_call"
	responsesOutput := gjson.GetBytes(body, "output")

	var toolCalls []messagepolicy.ToolCallInfo
	if responsesOutput.Exists() {
		responsesOutput.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "function_call" {
				toolCalls = append(toolCalls, messagepolicy.ToolCallInfo{
					Name:      item.Get("name").String(),
					Arguments: item.Get("arguments").String(),
				})
			}
			return true
		})
	} else if anthropicContent.Exists() {
		anthropicContent.ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").String() == "tool_use" {
				input := block.Get("input").Raw
				if input == "" {
					input = "{}"
				}
				toolCalls = append(toolCalls, messagepolicy.ToolCallInfo{
					Name:      block.Get("name").String(),
					Arguments: input,
				})
			}
			return true
		})
	}

	if len(toolCalls) == 0 {
		_, _ = pw.Write(body)
		return
	}

	targetMessage := buildToolCallTargetMessage(toolCalls)
	violations := r.messagePolicyHelper.EvaluateMessage(ctx, r.outputPolicies, r.conversationHistory, targetMessage, types2.PolicyDirectionToolCalls)

	if len(violations) == 0 {
		_, _ = pw.Write(body)
		return
	}

	// Log each violation.
	blockedContent, _ := json.Marshal(toolCalls)
	for _, v := range violations {
		logViolation(context.Background(), r.client, v, r.user.GetUID(), string(types2.PolicyDirectionToolCalls), blockedContent, r.projectID, r.threadID)
	}

	// Violation — keep tool calls but add violation marker to the JSON.
	var explanations []string
	for _, v := range violations {
		explanations = append(explanations, v.Explanation)
	}
	notification := fmt.Sprintf(
		"This tool call was blocked due to a policy violation. Please inform the user that you cannot complete their requested action. Explanation: %s",
		strings.Join(explanations, "\n"),
	)

	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		_, _ = pw.Write(body)
		return
	}
	bodyMap["obot_tool_call_policy_violation"] = notification
	modified, err := json.Marshal(bodyMap)
	if err != nil {
		_, _ = pw.Write(body)
		return
	}
	_, _ = pw.Write(append(modified, '\n'))
}

// isAnthropicToolCallEvent checks if an SSE data payload is an Anthropic tool-call-related event
// (content_block_start with type "tool_use", or content_block_delta with type "input_json_delta").
func isAnthropicToolCallEvent(data []byte) bool {
	eventType := gjson.GetBytes(data, "type").String()
	return (eventType == "content_block_start" && gjson.GetBytes(data, "content_block.type").String() == "tool_use") ||
		(eventType == "content_block_delta" && gjson.GetBytes(data, "delta.type").String() == "input_json_delta")
}

// accumulateAnthropicToolCallInfo extracts tool call name/arguments from Anthropic SSE events.
// content_block_start events with type "tool_use" create new entries;
// content_block_delta events with type "input_json_delta" append partial arguments.
func accumulateAnthropicToolCallInfo(data []byte, toolCalls *[]messagepolicy.ToolCallInfo, blockToTool map[int]int) {
	eventType := gjson.GetBytes(data, "type").String()
	switch {
	case eventType == "content_block_start" && gjson.GetBytes(data, "content_block.type").String() == "tool_use":
		blockIdx := int(gjson.GetBytes(data, "index").Int())
		toolIdx := len(*toolCalls)
		blockToTool[blockIdx] = toolIdx
		*toolCalls = append(*toolCalls, messagepolicy.ToolCallInfo{
			Name: gjson.GetBytes(data, "content_block.name").String(),
		})
	case eventType == "content_block_delta" && gjson.GetBytes(data, "delta.type").String() == "input_json_delta":
		blockIdx := int(gjson.GetBytes(data, "index").Int())
		if toolIdx, ok := blockToTool[blockIdx]; ok && toolIdx < len(*toolCalls) {
			(*toolCalls)[toolIdx].Arguments += gjson.GetBytes(data, "delta.partial_json").String()
		}
	}
}

// isResponsesAPIToolCallEvent checks if an SSE data payload is an OpenAI Responses API tool-call-related event
// (response.output_item.added with type "function_call", or response.function_call_arguments.delta).
func isResponsesAPIToolCallEvent(data []byte) bool {
	eventType := gjson.GetBytes(data, "type").String()
	return (eventType == "response.output_item.added" && gjson.GetBytes(data, "item.type").String() == "function_call") ||
		eventType == "response.function_call_arguments.delta"
}

// accumulateResponsesAPIToolCallInfo extracts tool call name/arguments from OpenAI Responses API SSE events.
// response.output_item.added events with item.type "function_call" create new entries;
// response.function_call_arguments.delta events append partial arguments.
func accumulateResponsesAPIToolCallInfo(data []byte, toolCalls *[]messagepolicy.ToolCallInfo, itemToTool map[int]int) {
	if itemToTool == nil {
		return
	}
	eventType := gjson.GetBytes(data, "type").String()
	switch {
	case eventType == "response.output_item.added" && gjson.GetBytes(data, "item.type").String() == "function_call":
		outputIdx := int(gjson.GetBytes(data, "output_index").Int())
		toolIdx := len(*toolCalls)
		itemToTool[outputIdx] = toolIdx
		*toolCalls = append(*toolCalls, messagepolicy.ToolCallInfo{
			Name: gjson.GetBytes(data, "item.name").String(),
		})
	case eventType == "response.function_call_arguments.delta":
		outputIdx := int(gjson.GetBytes(data, "output_index").Int())
		if toolIdx, ok := itemToTool[outputIdx]; ok && toolIdx < len(*toolCalls) {
			(*toolCalls)[toolIdx].Arguments += gjson.GetBytes(data, "delta").String()
		}
	}
}

// buildToolCallTargetMessage formats tool calls into the target message string for the policy judge.
// logViolation persists a policy violation record. Failures are logged but non-fatal.
func logViolation(ctx context.Context, c *client.Client, v messagepolicy.MessagePolicyViolation, userID, direction string, blockedContent json.RawMessage, projectID, threadID string) {
	if c == nil {
		return
	}
	if err := c.LogMessagePolicyViolation(ctx, &types.MessagePolicyViolation{
		CreatedAt:            time.Now(),
		UserID:               userID,
		PolicyID:             v.PolicyID,
		PolicyName:           v.PolicyName,
		PolicyDefinition:     v.PolicyDefinition,
		Direction:            direction,
		ViolationExplanation: v.Explanation,
		BlockedContent:       blockedContent,
		ProjectID:            projectID,
		ThreadID:             threadID,
	}); err != nil {
		slog.Warn("failed to log policy violation", "policyID", v.PolicyID, "error", err)
	}
	// Message policies are guardrails now; record the violation with their events too.
	if err := c.InsertGuardrailEvents(ctx, []types.GuardrailEvent{
		guardrails.LegacyViolationEvent(v.PolicyID, v.PolicyName, userID, direction),
	}); err != nil {
		slog.Warn("failed to record guardrail event", "policyID", v.PolicyID, "error", err)
	}
}

func buildToolCallTargetMessage(toolCalls []messagepolicy.ToolCallInfo) string {
	var sb strings.Builder
	for i, tc := range toolCalls {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "[called tool %q with args: %s]", tc.Name, tc.Arguments)
	}
	return sb.String()
}

func (r *responseModifier) Close() error {
	r.parseNonStreamTokenUsage()
	if r.tokenUsageTracker != nil {
		usage := r.tokenUsageTracker.getTokenUsage()
		r.audit.setTokenUsage(usage)
		// Recorded against the owner for the same reason the limit is checked
		// against them: an agent has no user row of its own, and its usage
		// should still land on whoever created it.
		activity := newRunTokenActivity(r.user, r.model, usage)
		if err := r.client.InsertTokenUsage(context.Background(), activity); err != nil {
			slog.Warn("failed to save token usage for user", "userID", r.user.GetUID(), "error", err)
		}
		if r.gateway != nil {
			if err := r.client.RecordLLMGatewayBudgetUsage(context.Background(), r.gateway.budgetIDs(r.model), activity.UserID, r.modelID, usage); err != nil {
				slog.Warn("failed to record LLM gateway budget usage", "userID", activity.UserID, "error", err)
			}
		}
	}
	// ReverseProxy does not return body-copy errors to the handler, so Close is
	// the terminal point for proxied responses. The handler defer is a fallback.
	err := r.c.Close()
	r.audit.finish(r.client, err)
	return err
}

func newRunTokenActivity(user kuser.Info, model string, usage types.TokenUsage) *types.RunTokenActivity {
	activity := &types.RunTokenActivity{
		UserID: principal.ResourceOwnerID(user),
		Model:  model,
		Usage:  usage,
	}
	if attribution, ok := principal.APIKeyAttributionFromUser(user); ok {
		activity.APIKeyID = &attribution.ID
		activity.APIKeyName = attribution.Name
	}
	return activity
}

func wrapAuditOnlyResponse(resp *http.Response, audit *llmAuditRecorder, client *client.Client) {
	if audit == nil || resp == nil || resp.Body == nil {
		return
	}
	resp.Body = &llmAuditResponseBody{
		body:   resp.Body,
		audit:  audit,
		client: client,
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// llmRewriteRequest rewrites requests for the upstream model provider. Director
// made ReverseProxy append the client IP to X-Forwarded-For automatically, so
// that is replicated here. SetXForwarded is deliberately not used: it would
// additionally send X-Forwarded-Host and X-Forwarded-Proto, which Director never
// did and which would expose internal hostnames to upstream model providers.
func llmRewriteRequest(u url.URL) func(*httputil.ProxyRequest) {
	return func(r *httputil.ProxyRequest) {
		if clientIP, _, err := net.SplitHostPort(r.In.RemoteAddr); err == nil {
			if prior := r.In.Header.Values("X-Forwarded-For"); len(prior) > 0 {
				clientIP = strings.Join(prior, ", ") + ", " + clientIP
			}
			r.Out.Header.Set("X-Forwarded-For", clientIP)
		}

		urlCopy := u // avoid mutating the original url.URL across requests
		req := r.Out
		reqPath := req.PathValue("path")
		switch {
		case urlCopy.Path == "":
			// Upstream base has no path. Ensure exactly one /v1 prefix in the
			// final URL regardless of whether the client supplied it.
			if strings.HasPrefix(reqPath, "v1/") || reqPath == "v1" {
				urlCopy.Path = "/"
			} else {
				urlCopy.Path = "/v1"
			}
		case strings.HasSuffix(urlCopy.Path, "/v1"):
			// The upstream base already ends in /v1. Strip the same prefix from
			// the client path so we don't produce /v1/v1/...
			reqPath = strings.TrimPrefix(reqPath, "v1/")
			if reqPath == "v1" {
				reqPath = ""
			}
		}
		urlCopy.Path = path.Join(urlCopy.Path, reqPath)
		req.URL = &urlCopy
		req.Host = urlCopy.Host

		// Ensure the upstream transport can transparently decode compressed responses.
		// If we forward Accept-Encoding from the client, net/http transport will not
		// auto-decompress and token usage parsing can see compressed bytes.
		req.Header.Del("Accept-Encoding")
		req.Header.Del(internalRequestTypeHeader)
	}
}

// parseMessagesFromBody converts the raw messages array from the request body into
// ConversationMessages for policy evaluation. Returns the conversation history,
// the last user message content, and its index in the raw array (-1 if not found).
func parseMessagesFromBody(rawMessages []any) ([]messagepolicy.ConversationMessage, string, int) {
	var (
		history     []messagepolicy.ConversationMessage
		lastUserMsg string
		lastUserIdx = -1
	)

	for i, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		itemType, _ := msg["type"].(string)

		// OpenAI Responses API: top-level function_call items.
		if itemType == "function_call" {
			name, _ := msg["name"].(string)
			arguments, _ := msg["arguments"].(string)
			history = append(history, messagepolicy.ConversationMessage{
				Role: "assistant",
				ToolCalls: []messagepolicy.ToolCallInfo{
					{Name: name, Arguments: arguments},
				},
			})
			continue
		}

		// OpenAI Responses API: top-level function_call_output items.
		if itemType == "function_call_output" {
			callID, _ := msg["call_id"].(string)
			output, _ := msg["output"].(string)
			history = append(history, messagepolicy.ConversationMessage{
				Role:       "tool",
				Content:    output,
				ToolCallID: callID,
			})
			continue
		}

		role, _ := msg["role"].(string)
		content := extractContentString(msg["content"])

		cm := messagepolicy.ConversationMessage{
			Role:    role,
			Content: content,
		}

		history = append(history, cm)

		if role == "user" && content != "" {
			lastUserMsg = content
			lastUserIdx = i
		}
	}

	return history, lastUserMsg, lastUserIdx
}

// extractRawMessages extracts the messages array from a request body map.
// For Anthropic, this is the "messages" field.
// For OpenAI Responses API, this is the "input" field (when it's an array).
func extractRawMessages(bodyMap map[string]any) []any {
	if messages, ok := bodyMap["messages"].([]any); ok {
		return messages
	}
	if input, ok := bodyMap["input"].([]any); ok {
		return input
	}
	return nil
}

// extractContentString extracts a text string from a message content field,
// which can be either a plain string or an array of content parts.
func extractContentString(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		// Array of content parts — extract text parts.
		var parts []string
		for _, part := range c {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := partMap["type"].(string)
			switch partType {
			case "text", "input_text", "output_text":
				if text, ok := partMap["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func (s *Server) newLLMProviderProxy(u *url.URL, modelProviderName string) *llmProviderProxy {
	return &llmProviderProxy{
		dailyUserInputTokenLimit:  s.dailyUserInputTokenLimit,
		dailyUserOutputTokenLimit: s.dailyUserOutputTokenLimit,
		backend:                   apiKeyLLMProviderBackend{u: *u, providerName: modelProviderName},
		mapHelper:                 s.mapHelper,
		messagePolicyHelper:       s.messagePolicyHelper,
	}
}

func (l *llmProviderProxy) proxy(req api.Context) (retErr error) {
	l.lock.RLock()
	modelProvider := l.modelProvider
	l.lock.RUnlock()

	if modelProvider == nil {
		modelProvider = new(v1.ModelProvider)
		if err := req.Get(modelProvider, l.backend.modelProviderName()); err != nil {
			return fmt.Errorf("model provider %s not found: %w", l.backend.modelProviderName(), err)
		}

		l.lock.Lock()
		l.modelProvider = modelProvider
		l.lock.Unlock()
	}

	var credEnv map[string]string
	if b, ok := l.backend.(credentialOptionalBackend); !ok || b.requiresCredential() {
		var err error
		credEnv, err = dispatcher.CredentialEnvForModelProvider(req.Context(), req.GatewayClient, *modelProvider)
		if err != nil {
			if errors.As(err, &client.CredentialNotFoundError{}) {
				return types2.NewErrBadRequest("model provider %q is not configured; verify that the LLM gateway endpoint matches the configured provider", modelProvider.Name)
			}
			return fmt.Errorf("failed to get credential environment for model provider: %w", err)
		}
	}

	var audit *llmAuditRecorder
	if req.GatewayClient.LLMAuditLogEnabled() {
		audit = newLLMAuditRecorder(req.Request, req.User, defaultLLMAuditLogResponseCaptureLimit)
		defer func() {
			audit.finish(req.GatewayClient, retErr)
		}()
	}
	audit.setModel(l.backend.modelProviderName(), "", "")

	u, routeDialect, err := l.backend.upstreamURL(req.Request, credEnv)
	if err != nil {
		return err
	}

	body, err := copyBody(&req.Request.Body)
	if err != nil {
		return fmt.Errorf("failed to copy body: %w", err)
	}
	audit.setRequestBody(body)
	audit.setClientSessionID(routeDialect, req.Request.Header, body)
	audit.setReasoningEffort(l.backend.modelProviderName(), body)

	prepared := &preparedLLMProxyRequest{body: body}
	var resolvedModel *v1.Model

	targetModel := extractModelFromBody(body)
	if targetModel == "" {
		if _, gated := l.backend.(modelResolvingBackend); gated && !isModelsListRequest(req.Request) {
			// Backends that can reach any model authorize every request by the model it
			// names, so a request that names none cannot be authorized.
			return types2.NewErrBadRequest("a model is required")
		}
	}
	if targetModel != "" {
		audit.setModel(modelProvider.Name, "", targetModel)

		model, err := l.mapHelper.ResolveModelReference(req.Context(), req.Storage, modelProvider.Namespace, modelProvider.Name, targetModel)
		if resolver, ok := l.backend.(modelResolvingBackend); ok && apierrors.IsNotFound(err) {
			if fallback, fallbackErr := resolver.resolveModel(l, targetModel); fallbackErr == nil && fallback != nil {
				model, err = fallback, nil
			}
		}
		if apierrors.IsNotFound(err) {
			return types2.NewErrNotFound("model %q is not available through this endpoint", targetModel)
		}
		if err != nil {
			return fmt.Errorf("failed to get model: %w", err)
		}
		resolvedModel = model
		if model.Spec.Manifest.ModelProvider != modelProvider.Name {
			return types2.NewErrBadRequest("requested model does not match configured provider %q", targetModel)
		}
		prepared.model = model.Spec.Manifest.TargetModel
		audit.setModel(modelProvider.Name, model.Name, prepared.model)

		// A hosted agent's authority was fixed when its instance was created, so
		// it is limited to the models configured on it rather than re-evaluated
		// against access policies, which describe people and would not match a
		// principal that is not one.
		if agentModels, isAgent := principal.AuthorizedModelIDs(req.User); isAgent {
			if !modelAllowedForAgent(agentModels, model.Name) {
				return types2.NewErrForbidden("agent is not configured to use model %q", targetModel)
			}
		} else {
			hasAccess, err := l.mapHelper.UserHasAccessToModel(req.User, model.Name)
			if err != nil {
				return fmt.Errorf("failed to check user access to model %q: %w", model.Name, err)
			}
			if !hasAccess {
				return types2.NewErrForbidden("user does not have permission to use model %q", targetModel)
			}
		}

		if b, ok := l.backend.(trackerDialectBackend); ok {
			prepared.tokenUsageTracker = newTokenUsageTrackerForDialect(*model, b.trackerDialect(routeDialect))
		} else {
			prepared.tokenUsageTracker = newTokenUsageTracker(*model)
		}

		prepared.body, err = rewriteModelInBody(body, prepared.model)
		if err != nil {
			return fmt.Errorf("failed to rewrite model in request body: %w", err)
		}
		if authorizer, ok := l.backend.(extraModelAuthorizingBackend); ok {
			if prepared.body, err = authorizer.authorizeExtraModels(l, req, prepared.body); err != nil {
				return err
			}
		}
		audit.setRequestBody(prepared.body)
	}

	var (
		messagePolicyHelper    = l.messagePolicyHelper
		outputPolicies         []messagepolicy.ApplicablePolicy
		conversationHistory    []messagepolicy.ConversationMessage
		inputPolicyReplacement string
	)
	if shouldSkipMessagePolicyEnforcement(req.Request) {
		messagePolicyHelper = nil
	}
	if messagePolicyHelper != nil && req.User.GetUID() != "" {
		var bodyMap map[string]any
		if err := json.Unmarshal(prepared.body, &bodyMap); err == nil {
			outputPolicies, conversationHistory, inputPolicyReplacement, err = applyMessagePolicies(
				req.Context(), messagePolicyHelper, req.User, req.GatewayClient, bodyMap, "", "",
			)
			if err != nil {
				return err
			}
			if len(outputPolicies) > 0 && routeDialect == llmtypes.DialectOpenAIChatCompletions {
				// Tool-call policies are evaluated on Anthropic Messages and OpenAI Responses
				// streams only, so fail closed rather than let tool calls through unchecked.
				return types2.NewErrBadRequest("tool call message policies apply to you; use the Anthropic Messages (/v1/messages) or OpenAI Responses (/v1/responses) API")
			}
			if inputPolicyReplacement != "" {
				b, err := json.Marshal(bodyMap)
				if err != nil {
					return fmt.Errorf("failed to marshal modified body: %w", err)
				}
				audit.setPolicyModifiedRequestBody(b)
				prepared.body = b
			}
		}
	}
	req.Request.Body = io.NopCloser(bytes.NewReader(prepared.body))
	req.ContentLength = int64(len(prepared.body))

	// Daily token limits are a per-person quota, and a hosted agent is not a
	// person: its UID identifies an instance, so a user lookup would fail. Bill
	// the owner instead, so an agent still counts against whoever created it
	// rather than escaping accounting altogether.
	usageUserID := principal.ResourceOwnerID(req.User)

	decorator, isGateway := l.backend.(requestDecoratingBackend)
	if isGateway {
		// Budgets are checked for each model the gateway tries, when it tries it.
	} else if remainingUsage, err := req.GatewayClient.RemainingTokenUsageForUser(
		req.Context(),
		usageUserID,
		tokenUsageTimePeriod,
		l.dailyUserInputTokenLimit,
		l.dailyUserOutputTokenLimit,
	); err != nil {
		return err
	} else if remainingUsage.IsDepleted() {
		return types2.NewErrHTTP(http.StatusTooManyRequests, fmt.Sprintf("no tokens remaining (input tokens remaining: %d, output tokens remaining: %d)", remainingUsage.InputTokens, remainingUsage.OutputTokens))
	}

	var gateway gatewayRequest
	if isGateway {
		var err error
		if gateway, err = decorator.decorateRequest(l, req, resolvedModel); err != nil {
			return err
		}
		defer gateway.release()
	}

	transport, err := l.backend.transport(*modelProvider, credEnv)
	if err != nil {
		return err
	}

	modifier := &responseModifier{
		user:                   req.User,
		model:                  prepared.model,
		modelProvider:          modelProvider.Name,
		routeDialect:           routeDialect,
		client:                 req.GatewayClient,
		tokenUsageTracker:      prepared.tokenUsageTracker,
		gateway:                gateway,
		modelID:                modelName(resolvedModel),
		mapHelper:              l.mapHelper,
		inputPolicyReplacement: inputPolicyReplacement,
		messagePolicyHelper:    messagePolicyHelper,
		outputPolicies:         outputPolicies,
		conversationHistory:    conversationHistory,
		audit:                  audit,
	}

	var proxyErr error
	(&httputil.ReverseProxy{
		Rewrite:   llmRewriteRequest(u),
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			proxyErr = err
			audit.recordResponseStatus(http.StatusBadGateway)
			audit.finish(req.GatewayClient, err)
			slog.Warn("LLM provider proxy error", "error", err)
			http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
		ModifyResponse: modifier.modifyResponse,
	}).ServeHTTP(req.ResponseWriter, req.Request)
	if proxyErr != nil {
		return nil
	}

	return nil
}

// isModelsListRequest reports whether req targets the provider models-list
// endpoint (GET .../v1/models) on the passthrough routes.
func isModelsListRequest(req *http.Request) bool {
	if req.Method != http.MethodGet {
		return false
	}
	// The {path...} route value holds the client path, e.g. "v1/models".
	return strings.TrimPrefix(strings.Trim(req.PathValue("path"), "/"), "v1/") == "models"
}

// applyMessagePolicies evaluates input and output message policies against body, modifying
// it in-place if an input policy is violated (replacing the last user message with an LLM
// refusal). Returns the output policies to enforce on the response, the conversation history
// for output policy evaluation, the user-facing replacement text to surface via response
// header, and any error.
func applyMessagePolicies(
	ctx context.Context,
	helper *messagepolicy.Helper,
	userInfo kuser.Info,
	gatewayClient *client.Client,
	body map[string]any,
	projectID, threadID string,
) ([]messagepolicy.ApplicablePolicy, []messagepolicy.ConversationMessage, string, error) {
	inputPolicies, err := helper.GetApplicablePolicies(userInfo, types2.PolicyDirectionUserMessage)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get applicable input policies: %w", err)
	}

	var inputPolicyReplacement string
	if len(inputPolicies) > 0 {
		rawMessages := extractRawMessages(body)
		if len(rawMessages) > 0 {
			history, lastUserMsg, lastUserIdx := parseMessagesFromBody(rawMessages)
			// Only evaluate when the user message is last in the conversation. If there are
			// messages after it (assistant responses, tool results, etc.), this is a
			// tool-calling continuation and the user text has already been evaluated.
			if lastUserIdx == len(rawMessages)-1 {
				violations := helper.EvaluateMessage(ctx, inputPolicies, history, lastUserMsg, types2.PolicyDirectionUserMessage)
				if len(violations) > 0 {
					blockedContent, _ := json.Marshal(map[string]string{"message": lastUserMsg})
					var explanations []string
					for _, v := range violations {
						logViolation(ctx, gatewayClient, v, userInfo.GetUID(), string(types2.PolicyDirectionUserMessage), blockedContent, projectID, threadID)
						explanations = append(explanations, v.Explanation)
					}
					if msgMap, ok := rawMessages[lastUserIdx].(map[string]any); ok {
						msgMap["content"] = `Please respond to the user with exactly: "Sorry, I can't help with that."`
						inputPolicyReplacement = fmt.Sprintf("[policy-violation] %s", strings.Join(explanations, "\n"))
					}
				}
			}
		}
	}

	outputPolicies, err := helper.GetApplicablePolicies(userInfo, types2.PolicyDirectionToolCalls)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to get applicable output policies: %w", err)
	}

	var conversationHistory []messagepolicy.ConversationMessage
	if len(outputPolicies) > 0 {
		rawMessages := extractRawMessages(body)
		conversationHistory, _, _ = parseMessagesFromBody(rawMessages)
	}

	return outputPolicies, conversationHistory, inputPolicyReplacement, nil
}

func shouldSkipMessagePolicyEnforcement(req *http.Request) bool {
	if req == nil {
		return false
	}

	return req.Header.Get(internalRequestTypeHeader) == threadTitleRequestType
}

// modelAllowedForAgent matches a model against an agent's configured list.
//
// The match is literal. Alias references are expanded into concrete model IDs
// when the agent principal is built, so anything still carrying an "obot://"
// prefix here failed to resolve and correctly grants nothing.
//
// Only exact IDs and the "*" wildcard are understood. Alias references such as
// obot://llm and wildcard suffixes are not expanded here, so an agent
// configured solely with those cannot yet reach a model through this proxy.
// Denying is the safe direction while that resolution is missing.
func modelAllowedForAgent(configured []string, modelID string) bool {
	return slices.Contains(configured, "*") || slices.Contains(configured, modelID)
}

func modelName(model *v1.Model) string {
	if model == nil {
		return ""
	}
	return model.Name
}

// budgetExceededResponse is the response to a request refused because a budget is
// exhausted: a 429 in the client's API format, with X-Should-Retry: false so clients such
// as Claude Code don't retry it, and Retry-After when the budget's window resets.
func budgetExceededResponse(path string, exceeded *client.LLMGatewayBudgetExceeded, now time.Time) (http.Header, []byte) {
	var body any
	if strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens") {
		body = map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "rate_limit_error", "message": exceeded.Error()},
		}
	} else {
		body = map[string]any{
			"error": map[string]any{"message": exceeded.Error(), "type": budgetExceededType, "code": budgetExceededType},
		}
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-Should-Retry", "false")
	if !exceeded.ResetAt.IsZero() && exceeded.ResetAt.After(now) {
		header.Set("Retry-After", strconv.Itoa(int(math.Ceil(exceeded.ResetAt.Sub(now).Seconds()))))
	}
	data, _ := json.Marshal(body)
	return header, append(data, '\n')
}

// rewriteBudgetExceeded turns the gateway's refusal of a request whose budgets are exhausted
// into the budget error clients are given.
func (r *responseModifier) rewriteBudgetExceeded(resp *http.Response) {
	if r.gateway == nil || resp.StatusCode != http.StatusTooManyRequests {
		return
	}
	exceeded := r.gateway.budgetExceeded()
	if exceeded == nil {
		return
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || (gjson.GetBytes(body, "error.type").String() != budgetExceededType && gjson.GetBytes(body, "type").String() != budgetExceededType) {
		// A provider's own rate limit, after a budget refused an earlier attempt.
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	header, data := budgetExceededResponse(resp.Request.URL.Path, exceeded, time.Now())
	for k, v := range header {
		resp.Header[k] = v
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(data)))
	resp.ContentLength = int64(len(data))
	resp.Body = io.NopCloser(bytes.NewReader(data))
}
