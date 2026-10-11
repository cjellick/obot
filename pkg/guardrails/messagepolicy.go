package guardrails

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
)

// LegacyPolicy is a message policy to convert into a guardrail.
type LegacyPolicy struct {
	ID       string
	Manifest types.MessagePolicyManifest
}

// MigrationStore creates the guardrails message policies become.
type MigrationStore interface {
	GetGuardrailRule(ctx context.Context, id string) (*gatewaytypes.GuardrailRule, error)
	GetGuardrailProfile(ctx context.Context, id string) (*gatewaytypes.GuardrailProfile, error)
	CreateGuardrailProfile(ctx context.Context, profile *gatewaytypes.GuardrailProfile, secrets map[string]string) error
	CreateGuardrailRule(ctx context.Context, rule *gatewaytypes.GuardrailRule) error
}

// MigrateMessagePolicies converts message policies into guardrails: each becomes an LLM
// judge profile with its definition and a blocking rule with its subjects. Policies on user
// messages check model input, and policies on tool calls check model output. The rules fail
// closed, as message policies did.
//
// enforced reports whether message policies were being enforced (the message policy feature
// was enabled). If not, the rules are created disabled, so policies that never applied don't
// start blocking on upgrade.
func MigrateMessagePolicies(ctx context.Context, store MigrationStore, policies []LegacyPolicy, enforced bool) error {
	for _, p := range policies {
		// Skip policies converted by an earlier, interrupted run.
		if existing, err := store.GetGuardrailRule(ctx, "mp-"+p.ID); err == nil && existing != nil {
			continue
		}
		m := p.Manifest
		config, err := json.Marshal(map[string]any{"definition": m.Definition})
		if err != nil {
			return err
		}
		profile := &gatewaytypes.GuardrailProfile{
			ID:          "mp-" + p.ID,
			Name:        m.DisplayName,
			Description: "Converted from a message policy.",
			Type:        JudgeType,
			Config:      config,
		}
		// The profile may exist from an earlier, interrupted run.
		if existing, err := store.GetGuardrailProfile(ctx, profile.ID); err != nil || existing == nil {
			if err := store.CreateGuardrailProfile(ctx, profile, nil); err != nil {
				return fmt.Errorf("failed to convert message policy %s: %w", p.ID, err)
			}
		}

		applyTo := gatewaytypes.GuardrailApplyBoth
		switch m.Direction {
		case types.PolicyDirectionUserMessage:
			applyTo = gatewaytypes.GuardrailApplyInput
		case types.PolicyDirectionToolCalls:
			applyTo = gatewaytypes.GuardrailApplyOutput
		}
		var subjects []gatewaytypes.GuardrailRuleSubject
		for _, s := range m.Subjects {
			switch s.Type {
			case types.SubjectTypeSelector:
				subjects = append(subjects, gatewaytypes.GuardrailRuleSubject{Type: gatewaytypes.LLMGatewayBudgetSubjectEveryone})
			case types.SubjectTypeUser:
				subjects = append(subjects, gatewaytypes.GuardrailRuleSubject{Type: gatewaytypes.LLMGatewayBudgetSubjectUser, ID: s.ID})
			case types.SubjectTypeGroup:
				subjects = append(subjects, gatewaytypes.GuardrailRuleSubject{Type: gatewaytypes.LLMGatewayBudgetSubjectGroup, ID: s.ID})
			}
		}
		if len(subjects) == 0 {
			// A message policy without subjects applied to no one; keep it, but disabled.
			subjects = []gatewaytypes.GuardrailRuleSubject{{Type: gatewaytypes.LLMGatewayBudgetSubjectEveryone}}
		}
		rule := &gatewaytypes.GuardrailRule{
			ID:           "mp-" + p.ID,
			Name:         m.DisplayName,
			Description:  "Converted from a message policy.",
			Enabled:      enforced && len(m.Subjects) > 0,
			Target:       gatewaytypes.GuardrailTargetLLM,
			ApplyTo:      applyTo,
			Action:       gatewaytypes.GuardrailActionBlock,
			SamplingRate: 100,
			InputScope:   gatewaytypes.GuardrailScopeLatest,
			// Message policies blocked when the judge couldn't decide, for example because
			// its model was unavailable; keep that, rather than letting traffic through.
			FailClosed: true,
			ProfileIDs: []string{profile.ID},
			Subjects:   subjects,
		}
		if err := store.CreateGuardrailRule(ctx, rule); err != nil {
			return fmt.Errorf("failed to convert message policy %s: %w", p.ID, err)
		}
	}
	return nil
}
