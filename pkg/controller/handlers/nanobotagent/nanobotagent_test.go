package nanobotagent

import (
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	storagescheme "github.com/obot-platform/obot/pkg/storage/scheme"
	"github.com/obot-platform/obot/pkg/system"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestChooseModelPrefersKnownNames(t *testing.T) {
	models := []v1.Model{
		{
			Name: "ollama-qwen3",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "other",
					TargetModel: "some-other-model",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
		{
			Name: "openai-gpt-5.4",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "openai-gpt-5.4",
					TargetModel: "openai/gpt-5.4",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
	}

	model, err := chooseModel(t.Context(), nil, "", models, types.DefaultModelAliasTypeLLM)
	if err != nil {
		t.Fatalf("expected model, got error: %v", err)
	}

	if model.Name != "openai-gpt-5.4" {
		t.Fatalf("expected openai-gpt-5.4, got %q", model.Name)
	}
}

func TestChooseModelFallsBackToFirstActiveModel(t *testing.T) {
	models := []v1.Model{
		{
			Name: "groq-llama-3.1-70b-versatile",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "model-a",
					TargetModel: "model-a",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
	}

	model, err := chooseModel(t.Context(), nil, "", models, types.DefaultModelAliasTypeLLM)
	if err != nil {
		t.Fatalf("expected model, got error: %v", err)
	}

	if model.Name != "groq-llama-3.1-70b-versatile" {
		t.Fatalf("expected groq-llama-3.1-70b-versatile, got %q", model.Name)
	}
}

func TestChooseModelPrefersSuggestedOrder(t *testing.T) {
	models := []v1.Model{
		{
			Name: "anthropic-claude-sonnet-4-6",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "anthropic-claude-sonnet-4-6",
					TargetModel: "anthropic/claude-sonnet-4-6",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
		{
			Name: "openai-gpt-5.4",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "openai-gpt-5.4",
					TargetModel: "openai/gpt-5.4",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
	}

	model, err := chooseModel(t.Context(), nil, "", models, types.DefaultModelAliasTypeLLM)
	if err != nil {
		t.Fatalf("expected model, got error: %v", err)
	}

	if model.Name != "openai-gpt-5.4" {
		t.Fatalf("expected openai-gpt-5.4, got %q", model.Name)
	}
}

func TestNanobotParseModelProviderRejectsUnsupportedProvider(t *testing.T) {
	h := &Handler{serverURL: "https://obot.example.com"}

	_, _, err := h.parseModelProvider(resolvedLLMModel{
		Name:            "my-model",
		ModelProvider:   "unknown-model-provider",
		ProviderDialect: llmtypes.DialectOpenResponses,
	})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestBuildNanobotProviderConfigYAMLSingleProvider(t *testing.T) {
	p := nanobotLLMProvider{
		Name:    "openai-model-provider",
		Dialect: llmtypes.DialectOpenAIResponses,
		APIKey:  "${OPENAI_MODEL_PROVIDER_API_KEY}",
		BaseURL: "https://obot.example.com/api/llm-proxy/openai/v1",
	}

	yaml, err := buildNanobotProviderConfigYAML(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg agentConfig
	if err := sigsyaml.Unmarshal([]byte(yaml), &cfg); err != nil {
		t.Fatalf("failed to parse output YAML: %v", err)
	}

	if len(cfg.LLMProviders) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.LLMProviders))
	}
	got := cfg.LLMProviders["openai-model-provider"]
	if string(got.Dialect) != string(llmtypes.DialectOpenAIResponses) {
		t.Errorf("dialect = %q, want OpenAIResponses", got.Dialect)
	}
	if got.BaseURL != p.BaseURL {
		t.Errorf("baseURL = %q, want %q", got.BaseURL, p.BaseURL)
	}
}

func TestBuildNanobotProviderConfigYAMLMultipleProviders(t *testing.T) {
	openai := nanobotLLMProvider{
		Name:    "openai-model-provider",
		Dialect: llmtypes.DialectOpenAIResponses,
		APIKey:  "${OPENAI_MODEL_PROVIDER_API_KEY}",
		BaseURL: "https://obot.example.com/api/llm-proxy/openai/v1",
	}
	anthropic := nanobotLLMProvider{
		Name:    "anthropic-model-provider",
		Dialect: llmtypes.DialectAnthropicMessages,
		APIKey:  "${ANTHROPIC_MODEL_PROVIDER_API_KEY}",
		BaseURL: "https://obot.example.com/api/llm-proxy/anthropic/v1",
	}

	yaml, err := buildNanobotProviderConfigYAML(openai, anthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg agentConfig
	if err := sigsyaml.Unmarshal([]byte(yaml), &cfg); err != nil {
		t.Fatalf("failed to parse output YAML: %v", err)
	}

	if len(cfg.LLMProviders) != 2 {
		t.Fatalf("expected 2 providers, got %d: %v", len(cfg.LLMProviders), cfg.LLMProviders)
	}
	if string(cfg.LLMProviders["openai-model-provider"].Dialect) != string(llmtypes.DialectOpenAIResponses) {
		t.Errorf("openai dialect = %q, want OpenAIResponses", cfg.LLMProviders["openai-model-provider"].Dialect)
	}
	if string(cfg.LLMProviders["anthropic-model-provider"].Dialect) != string(llmtypes.DialectAnthropicMessages) {
		t.Errorf("anthropic dialect = %q, want AnthropicMessages", cfg.LLMProviders["anthropic-model-provider"].Dialect)
	}
}

func TestBuildNanobotProviderConfigYAMLDeduplicates(t *testing.T) {
	p := nanobotLLMProvider{
		Name:    "openai-model-provider",
		Dialect: llmtypes.DialectOpenAIResponses,
		APIKey:  "${OPENAI_MODEL_PROVIDER_API_KEY}",
		BaseURL: "https://obot.example.com/api/llm-proxy/openai/v1",
	}

	yaml, err := buildNanobotProviderConfigYAML(p, p) // same provider twice
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg agentConfig
	if err := sigsyaml.Unmarshal([]byte(yaml), &cfg); err != nil {
		t.Fatalf("failed to parse output YAML: %v", err)
	}

	if len(cfg.LLMProviders) != 1 {
		t.Errorf("expected deduplication to 1 provider, got %d", len(cfg.LLMProviders))
	}
}

func TestResolveModelCarriesProviderAndDialect(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(storagescheme.Scheme).
		WithObjects(
			&v1.DefaultModelAlias{
				APIVersion: v1.SchemeGroupVersion.String(), Kind: "DefaultModelAlias",
				Name: "llm",
				Spec: v1.DefaultModelAliasSpec{
					Manifest: types.DefaultModelAliasManifest{Alias: "llm", Model: "groq-llama"},
				},
			},
			&v1.Model{
				APIVersion: v1.SchemeGroupVersion.String(), Kind: "Model",
				Name: "groq-llama",
				Spec: v1.ModelSpec{
					Manifest: types.ModelManifest{
						Name:          "groq-llama",
						TargetModel:   "llama-3.1-70b-versatile",
						ModelProvider: "groq-model-provider",
						Active:        true,
						Usage:         types.ModelUsageLLM,
						Dialect:       string(llmtypes.DialectOpenAIChatCompletions),
					},
				},
			},
		).Build()

	model, err := resolveModel(t.Context(), c, "", types.DefaultModelAliasTypeLLM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model.Name != "groq-llama" {
		t.Errorf("Name = %q, want groq-llama", model.Name)
	}
	if model.ModelProvider != "groq-model-provider" {
		t.Errorf("ModelProvider = %q, want groq-model-provider", model.ModelProvider)
	}
	if model.ProviderDialect != llmtypes.DialectOpenAIChatCompletions {
		t.Errorf("ProviderDialect = %q, want OpenAIChatCompletions", model.ProviderDialect)
	}
}

func TestChooseModelMiniFallsBackToResolvedLLM(t *testing.T) {
	client := fake.NewClientBuilder().
		WithScheme(storagescheme.Scheme).
		WithObjects(
			&v1.DefaultModelAlias{
				APIVersion: v1.SchemeGroupVersion.String(),
				Kind:       "DefaultModelAlias",
				Name:       "llm",
				Spec: v1.DefaultModelAliasSpec{
					Manifest: types.DefaultModelAliasManifest{
						Alias: "llm",
						Model: "openai-gpt-5.4",
					},
				},
			},
			&v1.Model{
				APIVersion: v1.SchemeGroupVersion.String(),
				Kind:       "Model",
				Name:       "openai-gpt-5.4",
				Spec: v1.ModelSpec{
					Manifest: types.ModelManifest{
						Name:        "gpt-5.4",
						TargetModel: "gpt-5.4",
						Active:      true,
						Usage:       types.ModelUsageLLM,
					},
				},
			},
		).
		Build()

	models := []v1.Model{
		{
			Name: "openai-gpt-5.4",
			Spec: v1.ModelSpec{
				Manifest: types.ModelManifest{
					Name:        "openai-gpt-5.4",
					TargetModel: "openai/gpt-5.4",
					Active:      true,
					Usage:       types.ModelUsageLLM,
				},
			},
		},
	}

	model, err := chooseModel(t.Context(), client, "", models, types.DefaultModelAliasTypeLLMMini)
	if err != nil {
		t.Fatalf("expected model, got error: %v", err)
	}

	if model.Name != "openai-gpt-5.4" {
		t.Fatalf("expected openai-gpt-5.4, got %q", model.Name)
	}
}

func TestNanobotParseModelProviderLLMGateway(t *testing.T) {
	h := &Handler{serverURL: "https://obot.example.com"}

	for _, dialect := range []llmtypes.Dialect{llmtypes.DialectAnthropicMessages, llmtypes.DialectOpenAIResponses} {
		p, qualifiedName, err := h.parseModelProvider(resolvedLLMModel{
			Name:            "m1-gateway-model",
			ModelProvider:   system.LLMGatewayModelProvider,
			ProviderDialect: dialect,
		})
		if err != nil {
			t.Fatalf("parseModelProvider: %v", err)
		}
		if p.BaseURL != "https://obot.example.com/api/llm-proxy/v1" {
			t.Errorf("baseURL = %q", p.BaseURL)
		}
		if p.Dialect != dialect {
			t.Errorf("dialect = %q, want %q", p.Dialect, dialect)
		}
		if qualifiedName != system.LLMGatewayModelProvider+"/m1-gateway-model" {
			t.Errorf("qualified name = %q", qualifiedName)
		}
	}
}

func TestLLMAndMiniShareTheGatewayProvider(t *testing.T) {
	h := &Handler{serverURL: "https://obot.example.com"}

	llmProvider, llmDefault, err := h.parseModelProvider(resolvedLLMModel{Name: "m1-sonnet", ModelProvider: system.LLMGatewayModelProvider, ProviderDialect: llmtypes.DialectAnthropicMessages})
	if err != nil {
		t.Fatal(err)
	}
	miniProvider, miniDefault, err := h.parseModelProvider(resolvedLLMModel{Name: "m1-mini", ModelProvider: system.LLMGatewayModelProvider, ProviderDialect: llmtypes.DialectOpenAIResponses})
	if err != nil {
		t.Fatal(err)
	}
	if llmDefault != system.LLMGatewayModelProvider+"/m1-sonnet" || miniDefault != system.LLMGatewayModelProvider+"/m1-mini" {
		t.Fatalf("defaults = %q, %q", llmDefault, miniDefault)
	}

	yaml, err := buildNanobotProviderConfigYAML(llmProvider, miniProvider)
	if err != nil {
		t.Fatal(err)
	}
	var cfg agentConfig
	if err := sigsyaml.Unmarshal([]byte(yaml), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.LLMProviders) != 1 {
		t.Fatalf("expected one gateway provider, got %d:\n%s", len(cfg.LLMProviders), yaml)
	}
}
