package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/api"
	gatewayclient "github.com/obot-platform/obot/pkg/gateway/client"
	gtypes "github.com/obot-platform/obot/pkg/gateway/types"
	"github.com/obot-platform/obot/pkg/riskregister/collect"
	"k8s.io/apiserver/pkg/authentication/user"
)

func newRiskRegisterContext(t *testing.T, c *gatewayclient.Client, method, path string, body any, pathValues map[string]string) (api.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var payload []byte
	if body != nil {
		payload = mustMarshal(t, body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	for name, value := range pathValues {
		req.SetPathValue(name, value)
	}
	rec := httptest.NewRecorder()
	return api.Context{
		ResponseWriter: rec,
		Request:        req,
		GatewayClient:  c,
		User:           &user.DefaultInfo{UID: "7", Name: "ciso@example.com", Groups: types.RoleAdmin.Groups()},
	}, rec
}

func seedRisk(t *testing.T, c *gatewayclient.Client) uint {
	t.Helper()
	_, err := c.SyncRisks(t.Context(), time.Now().UTC(), []gtypes.Risk{{
		Fingerprint: "unapproved-mcp-server:sentry:abc",
		Type:        "unapproved-mcp-server",
		Segment:     string(types.RiskSegmentCodingAgent),
		Severity:    string(types.RiskSeverityHigh),
		Title:       "Unapproved MCP server on 7 devices",
		Sources:     []string{"sentry"},
	}}, []string{"sentry"})
	if err != nil {
		t.Fatal(err)
	}
	risks, err := c.ListRisks(t.Context(), gatewayclient.RiskListOptions{})
	if err != nil || len(risks) != 1 {
		t.Fatalf("seeded risks = %+v, %v", risks, err)
	}
	return risks[0].ID
}

func TestRiskRegisterDecide(t *testing.T) {
	tests := []struct {
		name       string
		body       types.RiskDecisionRequest
		wantStatus types.RiskStatus
		wantErr    int
	}{
		{
			name:    "accepting needs a reason",
			body:    types.RiskDecisionRequest{Action: types.RiskDecisionAccept},
			wantErr: http.StatusBadRequest,
		},
		{
			name:    "treating needs the policy",
			body:    types.RiskDecisionRequest{Action: types.RiskDecisionTreat, Note: "done"},
			wantErr: http.StatusBadRequest,
		},
		{
			name:    "unknown action",
			body:    types.RiskDecisionRequest{Action: "escalate", Note: "x"},
			wantErr: http.StatusBadRequest,
		},
		{
			name:       "accept with a reason",
			body:       types.RiskDecisionRequest{Action: types.RiskDecisionAccept, Note: "Used by the data team; reviewed."},
			wantStatus: types.RiskStatusAccepted,
		},
		{
			name:       "treat with a policy",
			body:       types.RiskDecisionRequest{Action: types.RiskDecisionTreat, TreatmentRef: "mdm-configuration/3"},
			wantStatus: types.RiskStatusTreated,
		},
		{
			name:       "assign keeps the status",
			body:       types.RiskDecisionRequest{Action: types.RiskDecisionAssign, Owner: "sam@example.com"},
			wantStatus: types.RiskStatusOpen,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newEnforcementTestGatewayClient(t)
			id := seedRisk(t, c)
			ctx, rec := newRiskRegisterContext(t, c, http.MethodPost, "/api/risk-register/risks/1/decisions", tt.body, map[string]string{"risk_id": "1"})

			err := NewRiskRegisterHandler(nil).Decide(ctx)
			if tt.wantErr != 0 {
				var httpErr *types.ErrHTTP
				if !errors.As(err, &httpErr) || httpErr.Code != tt.wantErr {
					t.Fatalf("err = %v, want HTTP %d", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var risk types.Risk
			if err := json.Unmarshal(rec.Body.Bytes(), &risk); err != nil {
				t.Fatal(err)
			}
			if risk.ID != id || risk.Status != tt.wantStatus || len(risk.Decisions) != 1 || risk.Decisions[0].Username != "ciso@example.com" || risk.Decisions[0].UserID != "7" {
				t.Fatalf("risk = %+v", risk)
			}
			if tt.body.Action == types.RiskDecisionTreat && (risk.TreatedAt == nil || risk.TreatmentRef != tt.body.TreatmentRef) {
				t.Fatalf("treated risk = %+v", risk)
			}
			if tt.body.Action == types.RiskDecisionAssign && risk.Owner != tt.body.Owner {
				t.Fatalf("owner = %q", risk.Owner)
			}
		})
	}
}

func TestRiskRegisterSources(t *testing.T) {
	c := newEnforcementTestGatewayClient(t)
	h := NewRiskRegisterHandler(nil)

	// Built-in sources can't be configured, and unknown fields are dropped.
	ctx, _ := newRiskRegisterContext(t, c, http.MethodPut, "/api/risk-register/sources/sentry", types.RiskSourceRequest{Enabled: false}, map[string]string{"kind": "sentry"})
	if err := h.UpdateSource(ctx); err == nil {
		t.Fatal("expected an error configuring a built-in source")
	}

	// An Okta address must be a bare https origin, and can't point inside the network.
	for _, orgURL := range []string{"http://example.okta.com", "https://example.okta.com/api/v1", "https://127.0.0.1"} {
		ctx, _ = newRiskRegisterContext(t, c, http.MethodPut, "/api/risk-register/sources/okta", types.RiskSourceRequest{
			Enabled: true,
			Config:  map[string]string{collect.ConfigOktaOrgURL: orgURL},
		}, map[string]string{"kind": "okta"})
		if err := h.UpdateSource(ctx); err == nil {
			t.Errorf("orgURL %q was accepted", orgURL)
		}
	}

	ctx, rec := newRiskRegisterContext(t, c, http.MethodPut, "/api/risk-register/sources/claude-compliance", types.RiskSourceRequest{
		Enabled: true,
		Config:  map[string]string{"unexpected": "value"},
		Secrets: map[string]string{collect.SecretComplianceAPIKey: "sk-ant-api01-secret", "other": "x"},
	}, map[string]string{"kind": "claude-compliance"})
	if err := h.UpdateSource(ctx); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sk-ant-api01-secret")) {
		t.Fatalf("the secret was returned: %s", rec.Body.String())
	}
	var saved types.RiskSource
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Enabled || len(saved.Config) != 0 || len(saved.ConfiguredSecrets) != 1 || saved.ConfiguredSecrets[0] != collect.SecretComplianceAPIKey {
		t.Fatalf("saved source = %+v", saved)
	}

	ctx, rec = newRiskRegisterContext(t, c, http.MethodGet, "/api/risk-register/sources", nil, nil)
	if err := h.ListSources(ctx); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("sk-ant-api01-secret")) {
		t.Fatalf("the secret was listed: %s", rec.Body.String())
	}
	var list types.RiskSourceList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != len(riskSourceOrder) || list.Items[0].Kind != types.RiskSourceObot || !list.Items[0].Enabled || !list.Items[1].Enabled {
		t.Fatalf("sources = %+v", list.Items)
	}
}

func TestRiskRegisterProfileIsEmptyBeforeARun(t *testing.T) {
	c := newEnforcementTestGatewayClient(t)
	ctx, rec := newRiskRegisterContext(t, c, http.MethodGet, "/api/risk-register/profile", nil, nil)
	if err := NewRiskRegisterHandler(nil).GetProfile(ctx); err != nil {
		t.Fatal(err)
	}
	var profile types.RiskUsageProfile
	if err := json.Unmarshal(rec.Body.Bytes(), &profile); err != nil || profile.Segments == nil || len(profile.Segments) != 0 {
		t.Fatalf("profile = %s (%v)", rec.Body.String(), err)
	}
}
