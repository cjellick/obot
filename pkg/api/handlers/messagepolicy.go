package handlers

import (
	"fmt"
	"net/http"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
)

var (
	errMessagePoliciesMoved = types.NewErrHTTP(http.StatusGone, "Message policies are now managed as guardrails: create an LLM Judge detector under AI Resources > Guardrails and a rule under AI Resources > Models > Guardrails, or use /api/llm-gateway/guardrails. Existing message policies were converted automatically.")
)

// MessagePolicyHandler serves the MessagePolicy resources left from before message policies
// became guardrails. They are read-only and no longer enforced.
type MessagePolicyHandler struct{}

func NewMessagePolicyHandler() *MessagePolicyHandler {
	return nil
}

// List returns all message policies.
func (*MessagePolicyHandler) List(req api.Context) error {
	var list v1.MessagePolicyList
	if err := req.List(&list); err != nil {
		return fmt.Errorf("failed to list message policies: %w", err)
	}

	items := make([]types.MessagePolicy, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, convertMessagePolicy(item))
	}

	return req.Write(types.MessagePolicyList{
		Items: items,
	})
}

// Get returns a specific message policy by ID.
func (*MessagePolicyHandler) Get(req api.Context) error {
	policyID := req.PathValue("id")

	var policy v1.MessagePolicy
	if err := req.Get(&policy, policyID); err != nil {
		return fmt.Errorf("failed to get message policy: %w", err)
	}

	return req.Write(convertMessagePolicy(policy))
}

// Create is no longer supported: message policies are guardrails now.
func (*MessagePolicyHandler) Create(api.Context) error {
	return errMessagePoliciesMoved
}

// Update is no longer supported: message policies are guardrails now.
func (*MessagePolicyHandler) Update(api.Context) error {
	return errMessagePoliciesMoved
}

// Delete deletes a message policy.
func (*MessagePolicyHandler) Delete(req api.Context) error {
	policyID := req.PathValue("id")

	return req.Delete(&v1.MessagePolicy{
		Name:      policyID,
		Namespace: req.Namespace(),
	})
}

func convertMessagePolicy(policy v1.MessagePolicy) types.MessagePolicy {
	return types.MessagePolicy{
		Metadata:              MetadataFrom(&policy),
		MessagePolicyManifest: policy.Spec.Manifest,
	}
}
