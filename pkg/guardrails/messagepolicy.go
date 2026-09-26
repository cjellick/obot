package guardrails

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/messagepolicy"
)

const (
	// legacyPolicySeparator joins a rule and profile ID into the policy ID the legacy LLM
	// proxy reports violations with.
	legacyPolicySeparator = "/"
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
// messages check model input, and policies on tool calls check model output.
func MigrateMessagePolicies(ctx context.Context, store MigrationStore, policies []LegacyPolicy) error {
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
			Enabled:      len(m.Subjects) > 0,
			Target:       gatewaytypes.GuardrailTargetLLM,
			ApplyTo:      applyTo,
			Action:       gatewaytypes.GuardrailActionBlock,
			SamplingRate: 100,
			InputScope:   gatewaytypes.GuardrailScopeLatest,
			ProfileIDs:   []string{profile.ID},
			Subjects:     subjects,
		}
		if err := store.CreateGuardrailRule(ctx, rule); err != nil {
			return fmt.Errorf("failed to convert message policy %s: %w", p.ID, err)
		}
	}
	return nil
}

// JudgePolicies returns the LLM judge policies that apply to a caller, as message policies,
// for the legacy LLM proxy that still evaluates policies itself. Only enabled blocking rules
// count, and rules with conditions are skipped when the condition can't be decided without
// a model.
func (e *Engine) JudgePolicies(ctx context.Context, identity Identity, direction types.PolicyDirection) ([]messagepolicy.ApplicablePolicy, error) {
	rules, err := e.load(ctx)
	if err != nil {
		return nil, err
	}
	stage := StageInput
	if direction == types.PolicyDirectionToolCalls {
		stage = StageOutput
	}
	var out []messagepolicy.ApplicablePolicy
	for _, r := range rules {
		if r.rule.Target != gatewaytypes.GuardrailTargetLLM || r.rule.Action != gatewaytypes.GuardrailActionBlock ||
			!stageMatches(r.rule.ApplyTo, stage) || !subjectsMatch(r.rule.Subjects, identity) {
			continue
		}
		if ok, err := evalCondition(r.rule.Target, r.rule.Condition, identity, map[string]any{}); err != nil || !ok {
			continue
		}
		for _, p := range r.profiles {
			if p.profile.Type != JudgeType || p.err != nil {
				continue
			}
			judge, ok := p.checker.(*judgeChecker)
			if !ok || judge.definition == "" {
				continue
			}
			out = append(out, messagepolicy.ApplicablePolicy{
				ID: r.rule.ID + legacyPolicySeparator + p.profile.ID,
				Manifest: types.MessagePolicyManifest{
					DisplayName: r.rule.Name,
					Definition:  judge.definition,
					Direction:   direction,
				},
			})
		}
	}
	return out, nil
}

// LegacyViolationEvent is the guardrail event for a violation found by the legacy LLM proxy.
func LegacyViolationEvent(policyID, ruleName, userID, direction, explanation string) gatewaytypes.GuardrailEvent {
	ruleID, profileID, _ := strings.Cut(policyID, legacyPolicySeparator)
	stage := StageInput
	if direction == string(types.PolicyDirectionToolCalls) {
		stage = StageOutput
	}
	findings, _ := json.Marshal([]Finding{{Category: "policy", Message: explanation}})
	return gatewaytypes.GuardrailEvent{
		CreatedAt:   time.Now(),
		RuleID:      ruleID,
		RuleName:    ruleName,
		ProfileID:   profileID,
		ProfileType: JudgeType,
		Target:      gatewaytypes.GuardrailTargetLLM,
		Stage:       stage,
		Outcome:     "blocked",
		UserID:      userID,
		Findings:    findings,
	}
}
