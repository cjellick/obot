package mcpwebhookvalidation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/obot-platform/nah/pkg/router"
	"github.com/obot-platform/obot/apiclient/types"
	gatewaytypes "github.com/obot-platform/obot/pkg/gateway/types"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/obot-platform/obot/pkg/storage/scheme"
	"github.com/obot-platform/obot/pkg/system"
	"gorm.io/gorm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeProfiles struct {
	profiles map[string]gatewaytypes.GuardrailProfile
	secrets  map[string]map[string]string
}

type recordingResponse struct {
	router.Response
	retryAfter time.Duration
}

func (f fakeProfiles) GetGuardrailProfile(_ context.Context, id string) (*gatewaytypes.GuardrailProfile, error) {
	p, ok := f.profiles[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return &p, nil
}

func (f fakeProfiles) GuardrailProfileSecrets(_ context.Context, id string) (map[string]string, error) {
	return f.secrets[id], nil
}

func (r *recordingResponse) RetryAfter(d time.Duration) { r.retryAfter = d }

func TestGuardrailFilterHasNoSystemServer(t *testing.T) {
	profiles := fakeProfiles{
		profiles: map[string]gatewaytypes.GuardrailProfile{
			"secrets": {ID: "secrets", Type: "secrets", Config: json.RawMessage(`{}`)},
			// Lakera needs an API key, which isn't set.
			"lakera": {ID: "lakera", Type: "lakera", Config: json.RawMessage(`{}`)},
		},
	}

	tests := []struct {
		name           string
		detectorIDs    []string
		wantConfigured bool
	}{
		{
			name:           "detectors exist",
			detectorIDs:    []string{"secrets"},
			wantConfigured: true,
		},
		{
			name:        "a detector was deleted",
			detectorIDs: []string{"secrets", "gone"},
		},
		{
			name:        "a detector can't be used",
			detectorIDs: []string{"lakera"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &v1.MCPWebhookValidation{}
			filter.Name = "mwv1guardrail"
			filter.Namespace = system.DefaultNamespace
			filter.Spec.Manifest = types.MCPWebhookValidationManifest{
				Name:      "No secrets",
				Guardrail: &types.MCPGuardrailFilterConfig{DetectorIDs: tt.detectorIDs, Action: types.MCPGuardrailActionBlock},
			}
			// A server left from before the filter became a guardrail filter.
			leftover := &v1.SystemMCPServer{Name: system.SystemMCPServerPrefix + filter.Name, Namespace: filter.Namespace}
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(filter, leftover).Build()

			resp := &recordingResponse{}
			if err := (&Handler{profiles: profiles}).EnsureSystemServer(router.Request{
				Ctx:       t.Context(),
				Client:    client,
				Object:    filter,
				Namespace: filter.Namespace,
				Name:      filter.Name,
			}, resp); err != nil {
				t.Fatal(err)
			}

			if filter.Status.Configured != tt.wantConfigured {
				t.Fatalf("configured = %v, want %v", filter.Status.Configured, tt.wantConfigured)
			}
			if resp.retryAfter == 0 {
				t.Fatal("a guardrail filter's status should be refreshed")
			}
			var servers v1.SystemMCPServerList
			if err := client.List(t.Context(), &servers); err != nil {
				t.Fatal(err)
			}
			if len(servers.Items) != 0 {
				t.Fatalf("a guardrail filter must not have a system MCP server: %#v", servers.Items)
			}
			err := client.Get(t.Context(), router.Key(filter.Namespace, leftover.Name), &v1.SystemMCPServer{})
			if !apierrors.IsNotFound(err) {
				t.Fatalf("expected the leftover server to be deleted, got %v", err)
			}
		})
	}
}
