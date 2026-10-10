package guardrails

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/storage/scheme"
	"github.com/obot-platform/obot/pkg/system"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type migrationRules struct {
	lock  sync.Mutex
	rules []gatewaytypes.GuardrailRule
}

func (m *migrationRules) ListGuardrailRules(context.Context) ([]gatewaytypes.GuardrailRule, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	return slices.Clone(m.rules), nil
}

func (m *migrationRules) DeleteGuardrailRule(_ context.Context, id string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.rules = slices.DeleteFunc(m.rules, func(r gatewaytypes.GuardrailRule) bool { return r.ID == id })
	return nil
}

func kclientKey(name string) kclient.ObjectKey {
	return kclient.ObjectKey{Namespace: system.DefaultNamespace, Name: name}
}

func TestMigrateMCPRules(t *testing.T) {
	llmRule := gatewaytypes.GuardrailRule{ID: "llm", Name: "LLM", Target: gatewaytypes.GuardrailTargetLLM, Enabled: true}
	store := &migrationRules{rules: []gatewaytypes.GuardrailRule{
		llmRule,
		{
			ID:           "rule-1",
			Name:         "No secrets",
			Enabled:      true,
			Target:       gatewaytypes.GuardrailTargetMCP,
			ApplyTo:      gatewaytypes.GuardrailApplyInput,
			Action:       gatewaytypes.GuardrailActionRedact,
			Condition:    `mcp_tool == "send"`,
			SamplingRate: 50,
			FailClosed:   true,
			BlockMessage: "nope",
			ProfileIDs:   []string{"secrets", "pii"},
			Subjects:     []gatewaytypes.GuardrailRuleSubject{{Type: "group", ID: "eng"}, {Type: "selector", ID: "*"}},
		},
		{
			ID:      "rule-2",
			Name:    "Disabled",
			Enabled: false,
			Target:  gatewaytypes.GuardrailTargetMCP,
			ApplyTo: gatewaytypes.GuardrailApplyOutput,
			Action:  gatewaytypes.GuardrailActionBlock,
		},
	}}
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	converted, err := MigrateMCPRules(t.Context(), store, client, system.DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if converted != 2 {
		t.Fatalf("converted %d rules, want 2", converted)
	}
	if rules, _ := store.ListGuardrailRules(t.Context()); len(rules) != 1 || rules[0].ID != "llm" {
		t.Fatalf("only the LLM rule should remain: %#v", rules)
	}

	var filter v1.MCPWebhookValidation
	if err := client.Get(t.Context(), kclientKey(MCPRuleFilterName("rule-1")), &filter); err != nil {
		t.Fatal(err)
	}
	want := types.MCPWebhookValidationManifest{
		Name:            "No secrets",
		Resources:       []types.Resource{{Type: types.ResourceTypeMcpCatalog, ID: "default"}},
		Selectors:       types.MCPSelectors{{Method: "tools/call"}},
		AllowedToMutate: true,
		Guardrail: &types.MCPGuardrailFilterConfig{
			DetectorIDs:  []string{"secrets", "pii"},
			Action:       types.MCPGuardrailActionRedact,
			Direction:    types.MCPGuardrailDirectionRequest,
			FailClosed:   true,
			SamplingRate: 50,
			Subjects: []types.MCPGuardrailFilterSubject{
				{Type: types.MCPGuardrailSubjectGroup, ID: "eng"},
				{Type: types.MCPGuardrailSubjectEveryone, ID: "*"},
			},
			Condition:    `mcp_tool == "send"`,
			BlockMessage: "nope",
		},
	}
	if !reflect.DeepEqual(filter.Spec.Manifest, want) {
		t.Fatalf("manifest = %#v\nwant %#v", filter.Spec.Manifest, want)
	}

	if err := client.Get(t.Context(), kclientKey(MCPRuleFilterName("rule-2")), &filter); err != nil {
		t.Fatal(err)
	}
	if g := filter.Spec.Manifest.Guardrail; !filter.Spec.Manifest.Disabled || g.Direction != types.MCPGuardrailDirectionResponse || g.SamplingRate != 100 || filter.Spec.Manifest.AllowedToMutate {
		t.Fatalf("unexpected disabled filter: %#v %#v", filter.Spec.Manifest, *g)
	}

	// Running again, or on another replica that saw the rules before they were deleted,
	// keeps the existing filters and converts nothing twice.
	store.rules = append(store.rules, gatewaytypes.GuardrailRule{ID: "rule-1", Name: "renamed", Target: gatewaytypes.GuardrailTargetMCP})
	if _, err := MigrateMCPRules(t.Context(), store, client, system.DefaultNamespace); err != nil {
		t.Fatal(err)
	}
	var filters v1.MCPWebhookValidationList
	if err := client.List(t.Context(), &filters); err != nil {
		t.Fatal(err)
	}
	if len(filters.Items) != 2 {
		t.Fatalf("expected two filters, got %d", len(filters.Items))
	}
	if err := client.Get(t.Context(), kclientKey(MCPRuleFilterName("rule-1")), &filter); err != nil || filter.Spec.Manifest.Name != "No secrets" {
		t.Fatalf("an existing filter must be kept: %v %q", err, filter.Spec.Manifest.Name)
	}
	if rules, _ := store.ListGuardrailRules(t.Context()); !reflect.DeepEqual(rules, []gatewaytypes.GuardrailRule{llmRule}) {
		t.Fatalf("converted rules must be deleted: %#v", rules)
	}
}
