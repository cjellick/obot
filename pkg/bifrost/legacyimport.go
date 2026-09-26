package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	gateway "github.com/obot-platform/obot/pkg/gateway/client"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// LegacyImportResult reports what ImportLegacyProviders did for each legacy provider.
type LegacyImportResult struct {
	ModelProvider   string `json:"modelProvider"`
	GatewayProvider string `json:"gatewayProvider,omitempty"`
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
}

// ImportLegacyModelProviders copies the credentials of configured legacy model providers
// into the gateway, so an existing installation can switch over without re-entering keys.
// Providers the gateway already has are left alone.
func (engine *Engine) ImportLegacyModelProviders(ctx context.Context, c kclient.Client, gatewayClient *gateway.Client) ([]LegacyImportResult, error) {
	var providers v1.ModelProviderList
	if err := c.List(ctx, &providers, kclient.InNamespace(system.DefaultNamespace)); err != nil {
		return nil, err
	}

	var existing struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := engine.Do(ctx, http.MethodGet, "/api/providers", nil, &existing); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, p := range existing.Providers {
		have[p.Name] = true
	}

	var results []LegacyImportResult
	for _, mp := range providers.Items {
		if mp.Name == system.BifrostModelProvider || mp.Name == system.ModelProxyModelProvider {
			continue
		}
		plan, ok := legacyImportPlans[mp.Name]
		if !ok {
			continue
		}
		result := LegacyImportResult{ModelProvider: mp.Name, GatewayProvider: plan.provider}

		cred, err := gatewayClient.RevealCredential(ctx, []string{mp.Name, system.GenericModelProviderCredentialContext}, mp.Name)
		if errors.As(err, &gateway.CredentialNotFoundError{}) || (err == nil && len(cred.Secrets) == 0) {
			result.Status = "skipped"
			result.Message = "not configured"
			results = append(results, result)
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to read credential for %s: %w", mp.Name, err)
		}

		if have[plan.provider] {
			result.Status = "skipped"
			result.Message = "a gateway provider with this name already exists"
			results = append(results, result)
			continue
		}

		providerBody, key := plan.build(cred.Secrets)
		providerBody["provider"] = plan.provider
		if err := engine.Do(ctx, http.MethodPost, "/api/providers", providerBody, nil); err != nil {
			result.Status = "error"
			result.Message = err.Error()
			results = append(results, result)
			continue
		}
		if key != nil {
			key["name"] = "imported-" + mp.Name
			if _, ok := key["models"]; !ok {
				key["models"] = []string{"*"}
			}
			key["weight"] = 1
			if err := engine.Do(ctx, http.MethodPost, "/api/providers/"+url.PathEscape(plan.provider)+"/keys", key, nil); err != nil {
				result.Status = "error"
				result.Message = "provider created, but adding its key failed: " + err.Error()
				results = append(results, result)
				continue
			}
		}
		have[plan.provider] = true
		result.Status = "imported"
		results = append(results, result)
	}
	return results, nil
}

type legacyImportPlan struct {
	provider string
	build    func(secrets map[string]string) (provider map[string]any, key map[string]any)
}

func secretWithSuffix(secrets map[string]string, suffix string) string {
	for k, v := range secrets {
		if strings.HasSuffix(k, suffix) {
			return v
		}
	}
	return ""
}

var legacyImportPlans = map[string]legacyImportPlan{
	system.OpenAIModelProvider: {
		provider: "openai",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			return map[string]any{}, map[string]any{"value": secretWithSuffix(s, "_MODEL_PROVIDER_API_KEY")}
		},
	},
	system.AnthropicModelProvider: {
		provider: "anthropic",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			return map[string]any{}, map[string]any{"value": secretWithSuffix(s, "_MODEL_PROVIDER_API_KEY")}
		},
	},
	system.GenericResponsesModelProvider: {
		provider: "generic-responses",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			return map[string]any{
				"custom_provider_config": map[string]any{"base_provider_type": "openai"},
				"network_config":         map[string]any{"base_url": secretWithSuffix(s, "_MODEL_PROVIDER_BASE_URL"), "default_request_timeout_in_seconds": 300},
			}, map[string]any{
				"value": secretWithSuffix(s, "_MODEL_PROVIDER_API_KEY"),
			}
		},
	},
	system.AmazonBedrockModelProvider: {
		provider: "bedrock",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			region := secretWithSuffix(s, "_MODEL_PROVIDER_REGION")
			if region == "" {
				region = "us-east-1"
			}
			cfg := map[string]any{
				"access_key": secretWithSuffix(s, "_MODEL_PROVIDER_ACCESS_KEY_ID"),
				"secret_key": secretWithSuffix(s, "_MODEL_PROVIDER_SECRET_ACCESS_KEY"),
				"region":     region,
			}
			if token := secretWithSuffix(s, "_MODEL_PROVIDER_SESSION_TOKEN"); token != "" {
				cfg["session_token"] = token
			}
			return map[string]any{}, map[string]any{"value": "", "bedrock_key_config": cfg}
		},
	},
	system.AmazonBedrockAPIKeyModelProvider: {
		provider: "bedrock",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			region := secretWithSuffix(s, "_MODEL_PROVIDER_REGION")
			if region == "" {
				region = "us-east-1"
			}
			return map[string]any{}, map[string]any{
				"value":              secretWithSuffix(s, "_MODEL_PROVIDER_API_KEY"),
				"bedrock_key_config": map[string]any{"region": region},
			}
		},
	},
	system.AzureModelProvider: {
		provider: "azure",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			return map[string]any{}, map[string]any{
				"value":            secretWithSuffix(s, "_MODEL_PROVIDER_API_KEY"),
				"azure_key_config": map[string]any{"endpoint": secretWithSuffix(s, "_MODEL_PROVIDER_ENDPOINT")},
			}
		},
	},
	system.AzureEntraModelProvider: {
		provider: "azure",
		build: func(s map[string]string) (map[string]any, map[string]any) {
			return map[string]any{}, map[string]any{
				"value": "",
				"azure_key_config": map[string]any{
					"endpoint":      secretWithSuffix(s, "_MODEL_PROVIDER_ENDPOINT"),
					"client_id":     secretWithSuffix(s, "_MODEL_PROVIDER_CLIENT_ID"),
					"client_secret": secretWithSuffix(s, "_MODEL_PROVIDER_CLIENT_SECRET"),
					"tenant_id":     secretWithSuffix(s, "_MODEL_PROVIDER_TENANT_ID"),
				},
			}
		},
	},
}
