package guardrails

import (
	"context"
	"testing"

	apitypes "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/gateway/types"
)

func TestMigrateMessagePolicies(t *testing.T) {
	store := &fakeStore{}
	policies := []LegacyPolicy{
		{
			ID: "mp1",
			Manifest: apitypes.MessagePolicyManifest{
				DisplayName: "No travel",
				Definition:  "Do not book travel.",
				Direction:   apitypes.PolicyDirectionUserMessage,
				Subjects:    []apitypes.Subject{{Type: apitypes.SubjectTypeGroup, ID: "sales"}},
			},
		},
		{
			ID: "mp2",
			Manifest: apitypes.MessagePolicyManifest{
				DisplayName: "No deletes",
				Definition:  "Never delete files.",
				Direction:   apitypes.PolicyDirectionToolCalls,
				Subjects:    []apitypes.Subject{{Type: apitypes.SubjectTypeSelector, ID: "*"}},
			},
		},
		{
			ID:       "mp3",
			Manifest: apitypes.MessagePolicyManifest{DisplayName: "Unassigned", Definition: "x", Direction: apitypes.PolicyDirectionBoth},
		},
	}
	if err := MigrateMessagePolicies(context.Background(), store, policies); err != nil {
		t.Fatal(err)
	}
	// Running again (after an interrupted run) creates nothing new.
	if err := MigrateMessagePolicies(context.Background(), store, policies); err != nil {
		t.Fatal(err)
	}
	if len(store.rules) != 3 || len(store.profiles) != 3 {
		t.Fatalf("expected 3 rules and profiles, got %d and %d", len(store.rules), len(store.profiles))
	}
	byID := map[string]types.GuardrailRule{}
	for _, r := range store.rules {
		byID[r.ID] = r
	}
	if r := byID["mp-mp1"]; r.ApplyTo != types.GuardrailApplyInput || r.Action != types.GuardrailActionBlock || !r.Enabled || r.Subjects[0].Type != "group" || r.Subjects[0].ID != "sales" {
		t.Fatalf("unexpected rule %+v", r)
	}
	if r := byID["mp-mp2"]; r.ApplyTo != types.GuardrailApplyOutput || r.Subjects[0].Type != types.LLMGatewayBudgetSubjectEveryone {
		t.Fatalf("unexpected rule %+v", r)
	}
	if r := byID["mp-mp3"]; r.Enabled || r.ApplyTo != types.GuardrailApplyBoth {
		t.Fatalf("a policy without subjects applied to no one and must stay inactive: %+v", r)
	}

	// The legacy LLM proxy reads the converted policies.
	e := New(context.Background(), store, nil)
	sales := Identity{UserID: "u1", Groups: []string{"sales"}}
	got, err := e.JudgePolicies(context.Background(), sales, apitypes.PolicyDirectionUserMessage)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Manifest.Definition != "Do not book travel." || got[0].ID != "mp-mp1/mp-mp1" {
		t.Fatalf("unexpected user-message policies %+v", got)
	}
	got, _ = e.JudgePolicies(context.Background(), Identity{UserID: "u2"}, apitypes.PolicyDirectionToolCalls)
	if len(got) != 1 || got[0].Manifest.DisplayName != "No deletes" {
		t.Fatalf("unexpected tool-call policies %+v", got)
	}
	if got, _ := e.JudgePolicies(context.Background(), Identity{UserID: "u2"}, apitypes.PolicyDirectionUserMessage); len(got) != 0 {
		t.Fatalf("policies must follow their subjects, got %+v", got)
	}
}
