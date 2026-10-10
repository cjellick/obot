package guardrails

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/system"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// MCPRuleStore is where MigrateMCPRules finds the rules it converts. *gateway.Client
// implements it.
type MCPRuleStore interface {
	ListGuardrailRules(ctx context.Context) ([]gatewaytypes.GuardrailRule, error)
	DeleteGuardrailRule(ctx context.Context, id string) error
}

// MCPRuleFilterName is the name of the guardrail filter an MCP guardrail rule is converted
// into. It is derived from the rule's ID, so replicas converting the same rule create the
// same filter.
func MCPRuleFilterName(ruleID string) string {
	sum := sha256.Sum256([]byte(ruleID))
	return system.MCPWebhookValidationPrefix + "guardrail" + hex.EncodeToString(sum[:])[:16]
}

// MCPRuleFilter converts an MCP guardrail rule into a guardrail filter that applies to tool
// calls on every server in the default catalog.
func MCPRuleFilter(rule gatewaytypes.GuardrailRule, namespace string) *v1.MCPWebhookValidation {
	direction := types.MCPGuardrailDirectionBoth
	switch rule.ApplyTo {
	case gatewaytypes.GuardrailApplyInput:
		direction = types.MCPGuardrailDirectionRequest
	case gatewaytypes.GuardrailApplyOutput:
		direction = types.MCPGuardrailDirectionResponse
	}
	var subjects []types.MCPGuardrailFilterSubject
	for _, s := range rule.Subjects {
		if s.Type == "selector" {
			// Rules treated a selector subject as everyone.
			s.Type = types.MCPGuardrailSubjectEveryone
		}
		subjects = append(subjects, types.MCPGuardrailFilterSubject{Type: s.Type, ID: s.ID})
	}
	samplingRate := rule.SamplingRate
	if samplingRate < 1 || samplingRate > 100 {
		samplingRate = 100
	}
	return &v1.MCPWebhookValidation{
		Name:      MCPRuleFilterName(rule.ID),
		Namespace: namespace,
		Spec: v1.MCPWebhookValidationSpec{
			Manifest: types.MCPWebhookValidationManifest{
				Name:            rule.Name,
				Resources:       []types.Resource{{Type: types.ResourceTypeMcpCatalog, ID: system.DefaultCatalog}},
				Selectors:       types.MCPSelectors{{Method: MCPMethodToolsCall}},
				AllowedToMutate: rule.Action == gatewaytypes.GuardrailActionRedact,
				Disabled:        !rule.Enabled,
				Guardrail: &types.MCPGuardrailFilterConfig{
					DetectorIDs:  append([]string(nil), rule.ProfileIDs...),
					Action:       rule.Action,
					Direction:    direction,
					FailClosed:   rule.FailClosed,
					SamplingRate: samplingRate,
					Subjects:     subjects,
					Condition:    rule.Condition,
					BlockMessage: rule.BlockMessage,
				},
			},
		},
	}
}

// MigrateMCPRules converts the guardrail rules that target MCP traffic into guardrail
// filters, then deletes the rules. It is idempotent and safe to run on several replicas at
// once: each rule's filter has a fixed name, and a filter that already exists is kept.
func MigrateMCPRules(ctx context.Context, store MCPRuleStore, client kclient.Client, namespace string) (int, error) {
	rules, err := store.ListGuardrailRules(ctx)
	if err != nil {
		return 0, err
	}
	var converted int
	for _, rule := range rules {
		if rule.Target != gatewaytypes.GuardrailTargetMCP {
			continue
		}
		if err := client.Create(ctx, MCPRuleFilter(rule, namespace)); err != nil && !apierrors.IsAlreadyExists(err) {
			return converted, fmt.Errorf("failed to create a guardrail filter for rule %q: %w", rule.Name, err)
		}
		if err := store.DeleteGuardrailRule(ctx, rule.ID); err != nil {
			return converted, fmt.Errorf("failed to delete guardrail rule %q after converting it: %w", rule.Name, err)
		}
		converted++
	}
	return converted, nil
}
