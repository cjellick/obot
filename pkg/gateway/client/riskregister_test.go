package client

import (
	"testing"
	"time"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/gateway/types"
)

func TestSyncRisksKeepsDecisionsAndTracksProof(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	day1 := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

	server := types.Risk{
		Fingerprint: "unapproved-mcp-server:sentry:abc",
		Type:        "unapproved-mcp-server",
		Segment:     string(types2.RiskSegmentCodingAgent),
		Severity:    string(types2.RiskSeverityHigh),
		Title:       "Unapproved MCP server on 7 devices",
		Sources:     []string{"sentry"},
		Metrics:     map[string]int{"devices": 7},
	}
	keys := types.Risk{
		Fingerprint: "long-lived-provider-keys:anthropic",
		Type:        "long-lived-provider-keys",
		Segment:     string(types2.RiskSegmentAPIWorkload),
		Severity:    string(types2.RiskSeverityMedium),
		Title:       "2 Anthropic API keys older than 90 days",
		Sources:     []string{"anthropic-admin"},
	}

	result, err := c.SyncRisks(ctx, day1, []types.Risk{server, keys}, []string{"sentry", "anthropic-admin"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Found != 2 || result.New != 2 {
		t.Fatalf("first sync = %+v, want 2 found, 2 new", result)
	}

	risks, err := c.ListRisks(ctx, RiskListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(risks) != 2 || risks[0].Fingerprint != server.Fingerprint || risks[0].Status != string(types2.RiskStatusOpen) || !risks[0].Observed {
		t.Fatalf("after first sync: %+v", risks)
	}
	serverID := risks[0].ID

	// A person records the policy that treats the server risk.
	treated, err := c.DecideRisk(ctx, serverID, day1.Add(time.Hour), "42", "ciso@example.com", types2.RiskDecisionRequest{
		Action:       types2.RiskDecisionTreat,
		Note:         "Enforcement turned on for the engineering device configuration.",
		TreatmentRef: "mdm-configuration/3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if treated.Status != string(types2.RiskStatusTreated) || treated.TreatedAt == nil || treated.TreatmentRef != "mdm-configuration/3" {
		t.Fatalf("after treat: %+v", treated)
	}

	// The next run still finds the server on fewer devices. The decision stays, the
	// description refreshes, and the entry gets a proof record.
	day2 := day1.AddDate(0, 0, 1)
	server.Title = "Unapproved MCP server on 2 devices"
	server.Metrics = map[string]int{"devices": 2}
	result, err = c.SyncRisks(ctx, day2, []types.Risk{server, keys}, []string{"sentry", "anthropic-admin"})
	if err != nil {
		t.Fatal(err)
	}
	if result.New != 0 {
		t.Fatalf("second sync created %d entries, want 0", result.New)
	}
	risk, decisions, err := c.GetRisk(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if risk.Status != string(types2.RiskStatusTreated) || risk.Title != server.Title || !risk.FirstSeenAt.Equal(day1) || !risk.LastSeenAt.Equal(day2) {
		t.Fatalf("after second sync: %+v", risk)
	}
	if risk.Proof == nil || !risk.Proof.StillObserved || risk.Proof.Metrics["devices"] != 2 {
		t.Fatalf("proof after second sync: %+v", risk.Proof)
	}
	if len(decisions) != 1 || decisions[0].Username != "ciso@example.com" || decisions[0].FromStatus != string(types2.RiskStatusOpen) || decisions[0].ToStatus != string(types2.RiskStatusTreated) {
		t.Fatalf("decisions: %+v", decisions)
	}

	// The third run no longer finds the server, and the Anthropic source failed. The
	// server entry is marked unobserved with proof. The keys entry is left alone, because
	// its source didn't report.
	day3 := day2.AddDate(0, 0, 1)
	if _, err := c.SyncRisks(ctx, day3, nil, []string{"sentry"}); err != nil {
		t.Fatal(err)
	}
	risk, _, err = c.GetRisk(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if risk.Observed || risk.Proof == nil || risk.Proof.StillObserved || !risk.Proof.CheckedAt.GetTime().Equal(day3) {
		t.Fatalf("server after third sync: observed=%v proof=%+v", risk.Observed, risk.Proof)
	}
	open, err := c.ListRisks(ctx, RiskListOptions{Status: []string{string(types2.RiskStatusOpen)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Fingerprint != keys.Fingerprint || !open[0].Observed {
		t.Fatalf("open entries after third sync: %+v", open)
	}

	counts, err := c.OpenRiskCountsBySegment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[string(types2.RiskSegmentAPIWorkload)] != 1 || counts[string(types2.RiskSegmentCodingAgent)] != 0 {
		t.Fatalf("open counts: %+v", counts)
	}
}

func TestRiskSourceSecretsAreMergedAndKept(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	source := types.RiskSource{Kind: string(types2.RiskSourceOkta), Enabled: true, Config: map[string]string{"orgURL": "https://example.okta.com"}}
	if err := c.SaveRiskSource(ctx, source, map[string]string{"apiToken": "first"}); err != nil {
		t.Fatal(err)
	}
	// Saving again with an empty secret keeps the stored value.
	source.Config["orgURL"] = "https://example-2.okta.com"
	if err := c.SaveRiskSource(ctx, source, map[string]string{"apiToken": ""}); err != nil {
		t.Fatal(err)
	}
	secrets, err := c.RiskSourceSecrets(ctx, source.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["apiToken"] != "first" {
		t.Fatalf("secrets = %v, want apiToken kept", secrets)
	}

	// Recording a run's status doesn't disturb the configuration.
	if err := c.SetRiskSourceStatus(ctx, types2.RiskSourceStatus{Kind: types2.RiskSourceOkta, OK: true, Counts: map[string]int{"apps": 3}}); err != nil {
		t.Fatal(err)
	}
	sources, err := c.ListRiskSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || !sources[0].Enabled || sources[0].Config["orgURL"] != "https://example-2.okta.com" || sources[0].LastStatus == nil || sources[0].LastStatus.Counts["apps"] != 3 {
		t.Fatalf("sources = %+v", sources)
	}
}
