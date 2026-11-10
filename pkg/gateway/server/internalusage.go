package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/obot-platform/obot/pkg/aiusage"
	"github.com/obot-platform/obot/pkg/gateway/client"
	"github.com/obot-platform/obot/pkg/gateway/types"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
)

const (
	// internalRequestPathPrefix marks the LLM audit entries of Obot's own model calls, which
	// have no HTTP request: the path names the feature that made the call.
	internalRequestPathPrefix = "/obot/"
)

// InternalUsageRecorder records the model calls Obot makes itself, such as Obot AI's
// answers and the Supervisor's decisions, the way the LLM gateway records its requests: as
// token usage and spend against the user who triggered them, or Obot itself for background
// work, and in the LLM audit log. Request and response bodies are not kept.
type InternalUsageRecorder struct {
	client *client.Client
}

func NewInternalUsageRecorder(c *client.Client) *InternalUsageRecorder {
	return &InternalUsageRecorder{client: c}
}

// Record saves one call. Failures are logged; the call already happened.
func (r *InternalUsageRecorder) Record(ctx context.Context, call aiusage.Call) {
	if r == nil || r.client == nil {
		return
	}
	usage := internalTokenUsage(call)
	status := http.StatusOK
	if call.Err != nil {
		status = 0
	}
	outcome, message, responseStatus := llmOutcome(status, call.Err)
	provider, _, _ := strings.Cut(call.Model, "/")

	activity := &types.RunTokenActivity{
		Name:       internalRequestPathPrefix + call.Feature,
		UserID:     call.UserID,
		Model:      call.Model,
		Provider:   provider,
		Outcome:    outcome,
		DurationMs: call.Duration.Milliseconds(),
		Usage:      usage,
	}
	if err := r.client.InsertTokenUsage(ctx, activity); err != nil {
		slog.Warn("Failed to save token usage for an Obot model call", "feature", call.Feature, "error", err)
	}

	if !r.client.LLMAuditLogEnabled() {
		return
	}
	r.client.LogLLMAuditEntry(types.LLMAuditLog{
		ID:              uuid.New().String(),
		CreatedAt:       time.Now().Add(-call.Duration),
		Duration:        call.Duration.Milliseconds(),
		UserID:          call.UserID,
		ModelProvider:   provider,
		TargetModel:     call.Model,
		ServedProvider:  provider,
		ServedModel:     call.Model,
		RequestPath:     internalRequestPathPrefix + call.Feature,
		RequestMethod:   http.MethodPost,
		ResponseStatus:  responseStatus,
		Outcome:         outcome,
		Error:           message,
		InputTokens:     usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		CacheReadTokens: usage.CacheReadTokens,
		ReasoningTokens: usage.ThinkingTokens,
		Spend:           usage.TotalSpend,
		UserAgent:       "Obot",
	})
}

// internalTokenUsage normalizes a call's usage and prices it with its Model's costs.
func internalTokenUsage(call aiusage.Call) types.TokenUsage {
	if call.Usage == nil {
		return types.TokenUsage{}
	}
	var model v1.Model
	if call.ModelResource != nil {
		model = *call.ModelResource
	}
	tracker := newTokenUsageTrackerForDialect(model, llmtypes.DialectOpenAIChatCompletions)
	// Bifrost's usage, in the OpenAI chat completions shape the tracker reads.
	u := call.Usage
	cached, reasoning := 0, 0
	if u.PromptTokensDetails != nil {
		cached = u.PromptTokensDetails.CachedReadTokens
	}
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	line, err := json.Marshal(map[string]any{"usage": map[string]any{
		"prompt_tokens":             u.PromptTokens,
		"prompt_tokens_details":     map[string]any{"cached_tokens": cached},
		"completion_tokens":         u.CompletionTokens,
		"completion_tokens_details": map[string]any{"reasoning_tokens": reasoning},
	}})
	if err != nil || tracker == nil {
		return types.TokenUsage{}
	}
	tracker.addTokenUsage(line)
	return withCatalogCost(tracker.getTokenUsage(), u.Cost)
}

// withCatalogCost prices usage with the gateway's model catalog when the Model's own costs
// priced it at nothing: decision calls to a model that has no Model in Obot, such as
// openai/gpt-6-luna, are priced by the gateway, which reports the price on the usage.
func withCatalogCost(usage types.TokenUsage, cost *schemas.BifrostCost) types.TokenUsage {
	if cost == nil || usage.TotalSpend > 0 {
		return usage
	}
	usage.InputSpend, usage.OutputSpend = cost.InputCost, cost.OutputCost
	if d := cost.InputCostDetails; d != nil {
		usage.CacheReadSpend, usage.CacheWriteSpend = d.CachedReadCost, d.CachedWriteCost
	}
	usage.TotalSpend = cost.TotalCost
	if usage.TotalSpend == 0 {
		usage.TotalSpend = usage.InputSpend + usage.OutputSpend
	}
	return usage
}
