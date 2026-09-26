package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	openai "github.com/obot-platform/chat-completion-client"
	"github.com/obot-platform/obot/apiclient/types"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
)

// Metadata keys set on listed models. The provider back-population controller reads
// "displayName", "usage" and "dialect"; the remaining keys carry Bifrost pricing so the
// caller can derive ModelInfo costs.
const (
	MetadataProvider        = "bifrostProvider"
	MetadataInputCost       = "bifrostInputCostPerToken"
	MetadataOutputCost      = "bifrostOutputCostPerToken"
	MetadataCacheReadCost   = "bifrostCacheReadCostPerToken"
	MetadataCacheWriteCost  = "bifrostCacheWriteCostPerToken"
	MetadataContextLength   = "bifrostContextLength"
	MetadataMaxOutputTokens = "bifrostMaxOutputTokens"
)

// ModelProviderResource is the ModelProvider resource that represents the embedded
// gateway. Unlike registry providers it has no daemon and no credential: providers and
// keys are configured in Bifrost itself.
func ModelProviderResource() *v1.ModelProvider {
	return &v1.ModelProvider{
		Namespace: system.DefaultNamespace,
		Name:      system.BifrostModelProvider,
		Spec: v1.ModelProviderSpec{
			ModelProviderManifest: types.ModelProviderManifest{
				CommonProviderMetadata: types.CommonProviderMetadata{
					Name:        "LLM Gateway",
					Description: "Obot's built-in LLM gateway, powered by Bifrost. Route to 20+ providers with load balancing, fallbacks, budgets, rate limits, caching and analytics.",
					Icon:        "/admin/assets/bifrost_icon.svg",
					Link:        "https://github.com/maximhq/bifrost",
				},
			},
		},
	}
}

type listedModel struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	NormalizedName string `json:"normalized_name"`
	ContextLength  int    `json:"context_length"`
	MaxOutput      int    `json:"max_output_tokens"`
	OwnedBy        string `json:"owned_by"`
	Architecture   *struct {
		Modality         string   `json:"modality"`
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing map[string]string `json:"pricing"`
}

// ListModels returns every model Bifrost can currently route to, in the shape the
// provider back-population controller expects.
func (e *Engine) ListModels(ctx context.Context) (*openai.ModelsList, error) {
	var resp struct {
		Data []listedModel `json:"data"`
	}
	if err := e.Do(ctx, http.MethodGet, "/v1/models", nil, &resp); err != nil {
		return nil, fmt.Errorf("failed to list models from LLM gateway: %w", err)
	}

	result := &openai.ModelsList{Models: make([]openai.Model, 0, len(resp.Data))}
	seen := make(map[string]struct{}, len(resp.Data))
	for _, m := range resp.Data {
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}

		provider, modelID, ok := strings.Cut(m.ID, "/")
		if !ok {
			continue
		}

		displayName := m.NormalizedName
		if displayName == "" {
			displayName = modelID
		}

		metadata := map[string]string{
			"displayName":    fmt.Sprintf("%s (%s)", displayName, provider),
			"usage":          string(modelUsage(modelID, m)),
			"dialect":        string(DialectForModel(provider, modelID)),
			MetadataProvider: provider,
		}
		if m.ContextLength > 0 {
			metadata[MetadataContextLength] = strconv.Itoa(m.ContextLength)
		}
		if m.MaxOutput > 0 {
			metadata[MetadataMaxOutputTokens] = strconv.Itoa(m.MaxOutput)
		}
		for key, meta := range map[string]string{
			"prompt":            MetadataInputCost,
			"completion":        MetadataOutputCost,
			"input_cache_read":  MetadataCacheReadCost,
			"input_cache_write": MetadataCacheWriteCost,
		} {
			if v := m.Pricing[key]; v != "" {
				metadata[meta] = v
			}
		}

		result.Models = append(result.Models, openai.Model{
			ID:       m.ID,
			Object:   "model",
			OwnedBy:  m.OwnedBy,
			Metadata: metadata,
		})
	}
	return result, nil
}

// DialectForModel picks the API dialect Obot's agents should use for a model. Bifrost
// translates every dialect to every provider, so this only selects the most faithful one:
// Anthropic models speak Messages natively, everything else speaks Responses.
func DialectForModel(provider, modelID string) llmtypes.Dialect {
	if provider == "anthropic" || strings.Contains(strings.ToLower(modelID), "claude") {
		return llmtypes.DialectAnthropicMessages
	}
	return llmtypes.DialectOpenAIResponses
}

func modelUsage(modelID string, m listedModel) types.ModelUsage {
	id := strings.ToLower(modelID)
	if m.Architecture != nil {
		for _, out := range m.Architecture.OutputModalities {
			switch out {
			case "embeddings", "embedding":
				return types.ModelUsageEmbedding
			case "image":
				return types.ModelUsageImage
			}
		}
	}
	switch {
	case strings.Contains(id, "embed"):
		return types.ModelUsageEmbedding
	case strings.Contains(id, "dall-e"), strings.Contains(id, "gpt-image"), strings.Contains(id, "imagen"):
		return types.ModelUsageImage
	case strings.Contains(id, "tts"), strings.Contains(id, "whisper"), strings.Contains(id, "transcribe"),
		strings.Contains(id, "moderation"), strings.Contains(id, "rerank"):
		return types.ModelUsageOther
	}
	return types.ModelUsageLLM
}

// CostFromMetadata converts Bifrost per-token pricing into Obot's per-million-token cost.
func CostFromMetadata(metadata map[string]string) (types.ModelCost, bool) {
	perMillion := func(key string) float64 {
		v, err := strconv.ParseFloat(metadata[key], 64)
		if err != nil {
			return 0
		}
		return v * 1_000_000
	}
	cost := types.ModelCost{
		TokenUsageCost: types.TokenUsageCost{
			Input:      perMillion(MetadataInputCost),
			Output:     perMillion(MetadataOutputCost),
			CacheRead:  perMillion(MetadataCacheReadCost),
			CacheWrite: perMillion(MetadataCacheWriteCost),
		},
	}
	return cost, cost.Input > 0 || cost.Output > 0
}

// HasUpstreamProviders reports whether Bifrost has at least one usable provider.
func (e *Engine) HasUpstreamProviders(ctx context.Context) bool {
	if !e.Ready() {
		return false
	}
	var resp struct {
		Providers []struct {
			Name                 string `json:"name"`
			CustomProviderConfig *struct {
				IsKeyLess bool `json:"is_key_less"`
			} `json:"custom_provider_config"`
		} `json:"providers"`
	}
	if err := e.Do(ctx, http.MethodGet, "/api/providers", nil, &resp); err != nil {
		return false
	}
	for _, p := range resp.Providers {
		if p.CustomProviderConfig != nil && p.CustomProviderConfig.IsKeyLess {
			return true
		}
		var keys struct {
			Keys []json.RawMessage `json:"keys"`
		}
		if err := e.Do(ctx, http.MethodGet, "/api/providers/"+url.PathEscape(p.Name)+"/keys", nil, &keys); err == nil && len(keys.Keys) > 0 {
			return true
		}
		// Keyless built-in providers such as Ollama and vLLM are usable without keys.
		if p.Name == "ollama" || p.Name == "vllm" || p.Name == "sgl" {
			return true
		}
	}
	return false
}
