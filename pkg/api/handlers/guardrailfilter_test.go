package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/mcp"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"gorm.io/gorm"
)

type fakeGuardrailProfiles map[string]bool

func (f fakeGuardrailProfiles) GetGuardrailProfile(_ context.Context, id string) (*gatewaytypes.GuardrailProfile, error) {
	if !f[id] {
		return nil, gorm.ErrRecordNotFound
	}
	return &gatewaytypes.GuardrailProfile{ID: id}, nil
}

func TestGuardrailFilterIsAnImplementationSource(t *testing.T) {
	guardrail := &types.MCPGuardrailFilterConfig{DetectorIDs: []string{"secrets"}, Action: types.MCPGuardrailActionBlock}
	tests := []struct {
		name     string
		manifest types.MCPWebhookValidationManifest
		wantErr  bool
	}{
		{
			name:     "guardrail only",
			manifest: types.MCPWebhookValidationManifest{Guardrail: guardrail},
		},
		{
			name:     "guardrail and url are mutually exclusive",
			manifest: types.MCPWebhookValidationManifest{Guardrail: guardrail, URL: "https://example.com/webhook"},
			wantErr:  true,
		},
		{
			name:     "guardrail and catalog entry are mutually exclusive",
			manifest: types.MCPWebhookValidationManifest{Guardrail: guardrail, SystemMCPServerCatalogEntryID: "system-mcpcatentry1"},
			wantErr:  true,
		},
		{
			name: "guardrail and server manifest are mutually exclusive",
			manifest: types.MCPWebhookValidationManifest{
				Guardrail:               guardrail,
				ToolName:                "validate",
				SystemMCPServerManifest: &types.SystemMCPServerManifest{Name: "validator"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateManifest(t.Context(), &tt.manifest, mcp.ValidationOptions{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateManifest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateGuardrailFilter(t *testing.T) {
	profiles := fakeGuardrailProfiles{"secrets": true, "pii": true}
	tests := []struct {
		name    string
		config  types.MCPGuardrailFilterConfig
		secret  string
		wantErr string
	}{
		{
			name:   "valid",
			config: types.MCPGuardrailFilterConfig{DetectorIDs: []string{"secrets", "pii"}, Action: types.MCPGuardrailActionRedact, Condition: ` mcp_tool == "send" `},
		},
		{
			name:    "needs a detector",
			config:  types.MCPGuardrailFilterConfig{Action: types.MCPGuardrailActionBlock},
			wantErr: "at least one detector",
		},
		{
			name:    "detectors must exist",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"gone"}, Action: types.MCPGuardrailActionBlock},
			wantErr: "does not exist",
		},
		{
			name:    "detectors are listed once",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii", "pii"}, Action: types.MCPGuardrailActionBlock},
			wantErr: "more than once",
		},
		{
			name:    "action is required",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}},
			wantErr: "action",
		},
		{
			name:    "direction must be known",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}, Action: types.MCPGuardrailActionLog, Direction: "sideways"},
			wantErr: "direction",
		},
		{
			name:    "sampling rate is a percentage",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}, Action: types.MCPGuardrailActionLog, SamplingRate: 101},
			wantErr: "samplingRate",
		},
		{
			name:    "condition must compile",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}, Action: types.MCPGuardrailActionLog, Condition: `model == "x"`},
			wantErr: "condition",
		},
		{
			name:    "condition must be boolean",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}, Action: types.MCPGuardrailActionLog, Condition: `mcp_tool`},
			wantErr: "condition",
		},
		{
			name: "user subjects need an id",
			config: types.MCPGuardrailFilterConfig{
				DetectorIDs: []string{"pii"},
				Action:      types.MCPGuardrailActionLog,
				Subjects:    []types.MCPGuardrailFilterSubject{{Type: types.MCPGuardrailSubjectUser}},
			},
			wantErr: "need an id",
		},
		{
			name: "subject types are known",
			config: types.MCPGuardrailFilterConfig{
				DetectorIDs: []string{"pii"},
				Action:      types.MCPGuardrailActionLog,
				Subjects:    []types.MCPGuardrailFilterSubject{{Type: "selector", ID: "*"}},
			},
			wantErr: "subject type",
		},
		{
			name:    "no secret",
			config:  types.MCPGuardrailFilterConfig{DetectorIDs: []string{"pii"}, Action: types.MCPGuardrailActionLog},
			secret:  "shh",
			wantErr: "secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			manifest := types.MCPWebhookValidationManifest{Guardrail: &config, Secret: tt.secret, ToolName: "ignored"}
			err := validateGuardrailFilter(t.Context(), profiles, &manifest)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	// Defaults are filled in, and allowedToMutate follows the action.
	manifest := types.MCPWebhookValidationManifest{
		ToolName: "ignored",
		Guardrail: &types.MCPGuardrailFilterConfig{
			DetectorIDs: []string{"pii"},
			Action:      types.MCPGuardrailActionRedact,
			Condition:   "  true ",
		},
	}
	if err := validateGuardrailFilter(t.Context(), profiles, &manifest); err != nil {
		t.Fatal(err)
	}
	if g := manifest.Guardrail; g.Direction != types.MCPGuardrailDirectionBoth || g.SamplingRate != 100 || g.Condition != "true" || !manifest.AllowedToMutate || manifest.ToolName != "" {
		t.Fatalf("defaults not applied: %#v %#v", manifest, *g)
	}
	manifest.Guardrail.Action = types.MCPGuardrailActionBlock
	if err := validateGuardrailFilter(t.Context(), profiles, &manifest); err != nil || manifest.AllowedToMutate {
		t.Fatalf("a blocking filter must not be allowed to mutate: %v", err)
	}
}

func TestRejectGuardrailFilterServerOperations(t *testing.T) {
	var filter v1.MCPWebhookValidation
	filter.Name = "mwv1test"
	if err := rejectGuardrailFilter(filter, "logs"); err != nil {
		t.Fatalf("server-backed filters are allowed: %v", err)
	}
	filter.Spec.Manifest.Guardrail = &types.MCPGuardrailFilterConfig{}
	err := rejectGuardrailFilter(filter, "logs")
	var httpErr *types.ErrHTTP
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("expected a 400, got %v", err)
	}
}
