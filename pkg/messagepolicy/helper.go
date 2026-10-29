package messagepolicy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/alias"
	llmtypes "github.com/obot-platform/obot/pkg/llm"
	"github.com/obot-platform/obot/pkg/llmengine"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Helper evaluates natural-language policies with an LLM judge: the LLM judge guardrail,
// which message policies became.
type Helper struct {
	client     kclient.Client
	llmGateway *llmengine.Engine
}

// ApplicablePolicy pairs a policy's ID with its manifest.
type ApplicablePolicy struct {
	ID       string
	Manifest types.MessagePolicyManifest
}

// NewHelper returns a judge that calls the default model aliases' models through the LLM
// gateway.
func NewHelper(client kclient.Client, llmGateway *llmengine.Engine) *Helper {
	return &Helper{client: client, llmGateway: llmGateway}
}

// Complete makes one chat completion through the LLM gateway and returns the model's text
// and the gateway model that answered. It is the primitive the provenance Supervisor builds
// on.
//
// model is a default model alias ("llm-mini", "llm"; "" means "llm-mini"), the name of a
// Model in storage, or a gateway model id ("openai/gpt-5-mini") sent through as is. When
// jsonSchema is set the output is constrained to it with response_format, which the gateway
// translates for every provider; a provider that refuses the request is asked once more
// without it, so callers must still parse the text defensively.
func (h *Helper) Complete(ctx context.Context, model, system, user string, jsonSchema map[string]any) (string, string, error) {
	resolved, err := h.resolveNamedModel(ctx, model)
	if err != nil {
		return "", "", err
	}
	text, err := h.complete(ctx, resolved, system, user, jsonSchema)
	return text, resolved.targetModel, err
}

func (h *Helper) complete(ctx context.Context, resolved *resolvedModel, system, user string, jsonSchema map[string]any) (string, error) {
	messages := []chatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	if jsonSchema == nil {
		return h.callLLM(ctx, resolved, messages)
	}
	text, status, err := h.postChatCompletion(ctx, resolved, chatCompletionRequest{
		Model:    resolved.targetModel,
		Messages: messages,
		Stream:   true,
		ResponseFormat: map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": jsonSchema,
			},
		},
	})
	if err != nil && status >= http.StatusBadRequest && status < http.StatusInternalServerError && ctx.Err() == nil {
		// The provider refused structured output; the prompt asks for JSON anyway.
		slog.Debug("Structured output refused, retrying without response_format", "model", resolved.targetModel, "status", status)
		return h.callLLM(ctx, resolved, messages)
	}
	return text, err
}

// resolveNamedModel resolves a model name as Complete documents it.
func (h *Helper) resolveNamedModel(ctx context.Context, model string) (*resolvedModel, error) {
	switch model {
	case "", string(types.DefaultModelAliasTypeLLMMini):
		return h.resolveModelByAlias(ctx, types.DefaultModelAliasTypeLLMMini)
	case string(types.DefaultModelAliasTypeLLM):
		return h.resolveModelByAlias(ctx, types.DefaultModelAliasTypeLLM)
	}
	if h.llmGateway == nil {
		return nil, errors.New("the LLM gateway is not available")
	}
	target := model
	if h.client != nil {
		var mdl v1.Model
		err := alias.Get(ctx, h.client, &mdl, system.DefaultNamespace, model)
		switch {
		case err == nil:
			if !mdl.Spec.Manifest.Active {
				return nil, fmt.Errorf("model %q is not active", mdl.Spec.Manifest.Name)
			}
			if mdl.Spec.Manifest.ModelProvider != system.LLMGatewayModelProvider {
				return nil, fmt.Errorf("model %q is not served by the LLM gateway", mdl.Name)
			}
			target = mdl.Spec.Manifest.TargetModel
		case !apierrors.IsNotFound(err):
			return nil, fmt.Errorf("failed to look up model %q: %w", model, err)
		}
	}
	u := h.llmGateway.BaseURL()
	u.Path = "/v1"
	return &resolvedModel{
		targetModel:  target,
		providerName: system.LLMGatewayModelProvider,
		providerURL:  u.String(),
		dialect:      string(llmtypes.DialectOpenAIChatCompletions),
		httpClient:   &http.Client{Transport: h.llmGateway.Transport()},
	}, nil
}
