package server

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/aiusage"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
)

func TestInternalTokenUsagePricesTheCall(t *testing.T) {
	model := v1.Model{Status: v1.ModelStatus{Cost: types2.ModelCost{TokenUsageCost: types2.TokenUsageCost{Input: 2, Output: 8, CacheRead: 0.5}}}}
	usage := internalTokenUsage(aiusage.Call{
		Model:         "openai/gpt-5.4",
		ModelResource: &model,
		Usage: &schemas.BifrostLLMUsage{
			PromptTokens:            1_000_000,
			PromptTokensDetails:     &schemas.ChatPromptTokensDetails{CachedReadTokens: 200_000},
			CompletionTokens:        100_000,
			CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{ReasoningTokens: 40_000},
		},
	})
	if usage.InputTokens != 1_000_000 || usage.CacheReadTokens != 200_000 || usage.OutputTokens != 100_000 || usage.ThinkingTokens != 40_000 {
		t.Fatalf("tokens: %+v", usage)
	}
	// 800k uncached input at $2/M, 200k cached at $0.50/M and 100k output at $8/M.
	if want := 1.6 + 0.1 + 0.8; math.Abs(usage.TotalSpend-want) > 1e-9 {
		t.Fatalf("spend %v, want %v", usage.TotalSpend, want)
	}

	if usage := internalTokenUsage(aiusage.Call{Model: "openai/gpt-5.4"}); usage.TotalTokens != 0 || usage.TotalSpend != 0 {
		t.Fatalf("a call without usage counts nothing: %+v", usage)
	}
}
