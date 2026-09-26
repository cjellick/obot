package client

import (
	"errors"
	"testing"
	"time"

	"github.com/obot-platform/obot/pkg/gateway/types"
)

func TestParseLLMGatewayBudgetWindow(t *testing.T) {
	now := time.Date(2026, 9, 24, 15, 30, 0, 0, time.UTC) // a Thursday

	tests := []struct {
		name      string
		limit     types.LLMGatewayBudgetLimit
		wantStart time.Time
		wantReset time.Time
		wantErr   bool
	}{
		{
			name:      "rolling day",
			limit:     types.LLMGatewayBudgetLimit{Window: "1d"},
			wantStart: now.AddDate(0, 0, -1),
		},
		{
			name:      "rolling six hours",
			limit:     types.LLMGatewayBudgetLimit{Window: "6h"},
			wantStart: now.Add(-6 * time.Hour),
		},
		{
			name:      "calendar day",
			limit:     types.LLMGatewayBudgetLimit{Window: "1d", Calendar: true},
			wantStart: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
			wantReset: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "calendar week starts monday",
			limit:     types.LLMGatewayBudgetLimit{Window: "1w", Calendar: true},
			wantStart: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
			wantReset: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "calendar month",
			limit:     types.LLMGatewayBudgetLimit{Window: "1M", Calendar: true},
			wantStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			wantReset: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "multi-unit calendar windows are rejected",
			limit:   types.LLMGatewayBudgetLimit{Window: "2d", Calendar: true},
			wantErr: true,
		},
		{
			name:    "unknown unit",
			limit:   types.LLMGatewayBudgetLimit{Window: "1y"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, reset, err := ParseLLMGatewayBudgetWindow(tt.limit, now)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !start.Equal(tt.wantStart) || !reset.Equal(tt.wantReset) {
				t.Fatalf("got start %v reset %v, want start %v reset %v", start, reset, tt.wantStart, tt.wantReset)
			}
		})
	}
}

func TestLLMGatewayBudgets(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	perUser := &types.LLMGatewayBudget{
		Name:     "Everyone",
		Enabled:  true,
		PerUser:  true,
		Limits:   []types.LLMGatewayBudgetLimit{{Metric: types.LLMGatewayBudgetMetricRequests, Amount: 2, Window: "1d"}},
		Subjects: []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectEveryone, SubjectID: "ignored"}},
	}
	shared := &types.LLMGatewayBudget{
		Name:     "Engineering",
		Enabled:  true,
		Limits:   []types.LLMGatewayBudgetLimit{{Metric: types.LLMGatewayBudgetMetricSpend, Amount: 1, Window: "1M", Calendar: true}},
		Subjects: []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectGroup, SubjectID: "eng"}},
	}
	opusOnly := &types.LLMGatewayBudget{
		Name:         "Opus",
		Enabled:      true,
		PerUser:      true,
		ExemptAdmins: true,
		Limits:       []types.LLMGatewayBudgetLimit{{Metric: types.LLMGatewayBudgetMetricTokens, Amount: 10, Window: "1h"}},
		Models:       []string{"m1-opus"},
		Subjects:     []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectUser, SubjectID: "alice"}},
	}
	disabled := &types.LLMGatewayBudget{
		Name:     "Disabled",
		Enabled:  false,
		Limits:   []types.LLMGatewayBudgetLimit{{Metric: types.LLMGatewayBudgetMetricRequests, Amount: 0, Window: "1d"}},
		Subjects: []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectEveryone}},
	}
	for _, b := range []*types.LLMGatewayBudget{perUser, shared, opusOnly, disabled} {
		if err := c.CreateLLMGatewayBudget(ctx, b); err != nil {
			t.Fatalf("create %s: %v", b.Name, err)
		}
	}

	names := func(bs []types.LLMGatewayBudget) []string {
		var out []string
		for _, b := range bs {
			out = append(out, b.Name)
		}
		return out
	}

	got, err := c.LLMGatewayBudgetsForUser(ctx, "alice", []string{"eng"}, false, "m1-opus")
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); len(n) != 3 {
		t.Fatalf("alice on opus: got %v, want Everyone, Engineering and Opus", n)
	}
	got, _ = c.LLMGatewayBudgetsForUser(ctx, "alice", []string{"eng"}, true, "m1-opus")
	if n := names(got); len(n) != 2 {
		t.Fatalf("admin alice should be exempt from Opus: got %v", n)
	}
	got, _ = c.LLMGatewayBudgetsForUser(ctx, "bob", nil, false, "m1-gpt")
	if n := names(got); len(n) != 1 || n[0] != "Everyone" {
		t.Fatalf("bob: got %v, want only Everyone", n)
	}

	// Per-user budgets count each user separately; shared budgets count everyone.
	usage := types.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5, TotalSpend: 0.6}
	if err := c.RecordLLMGatewayBudgetUsage(ctx, []string{perUser.ID, shared.ID}, "alice", "m1-gpt", usage); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckLLMGatewayBudgets(ctx, []types.LLMGatewayBudget{*perUser, *shared}, "alice"); err != nil {
		t.Fatalf("alice should still be within her budgets: %v", err)
	}
	if err := c.RecordLLMGatewayBudgetUsage(ctx, []string{perUser.ID, shared.ID}, "carol", "m1-gpt", usage); err != nil {
		t.Fatal(err)
	}

	var exceeded *LLMGatewayBudgetExceeded
	err = c.CheckLLMGatewayBudgets(ctx, []types.LLMGatewayBudget{*shared}, "alice")
	if !errors.As(err, &exceeded) || exceeded.Budget.Name != "Engineering" || exceeded.ResetAt.IsZero() {
		t.Fatalf("expected the shared spend budget to be exhausted, got %v", err)
	}
	if err := c.CheckLLMGatewayBudgets(ctx, []types.LLMGatewayBudget{*perUser}, "alice"); err != nil {
		t.Fatalf("carol's usage must not count against alice's own budget: %v", err)
	}

	if err := c.RecordLLMGatewayBudgetUsage(ctx, []string{perUser.ID}, "alice", "m1-gpt", usage); err != nil {
		t.Fatal(err)
	}
	err = c.CheckLLMGatewayBudgets(ctx, []types.LLMGatewayBudget{*perUser}, "alice")
	if !errors.As(err, &exceeded) || exceeded.Limit.Metric != types.LLMGatewayBudgetMetricRequests || exceeded.ResetAt.IsZero() {
		t.Fatalf("expected alice's request limit to be reached with a reset time, got %v", err)
	}

	report, err := c.LLMGatewayBudgetUsageReport(ctx, perUser)
	if err != nil {
		t.Fatal(err)
	}
	byUser := map[string]float64{}
	for _, e := range report {
		byUser[e.UserID] = e.Used[0]
	}
	if byUser["alice"] != 2 || byUser["carol"] != 1 {
		t.Fatalf("unexpected per-user report %v", byUser)
	}
	report, _ = c.LLMGatewayBudgetUsageReport(ctx, shared)
	if len(report) != 1 || report[0].Used[0] < 1.19 || report[0].Used[0] > 1.21 {
		t.Fatalf("unexpected shared report %+v", report)
	}

	// Updating replaces subjects; deleting removes usage too.
	perUser.Subjects = []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectUser, SubjectID: "dave"}}
	if err := c.UpdateLLMGatewayBudget(ctx, perUser); err != nil {
		t.Fatal(err)
	}
	got, _ = c.LLMGatewayBudgetsForUser(ctx, "bob", nil, false, "m1-gpt")
	if len(got) != 0 {
		t.Fatalf("bob should no longer have budgets, got %v", names(got))
	}
	if err := c.DeleteLLMGatewayBudget(ctx, perUser.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetLLMGatewayBudget(ctx, perUser.ID); err == nil {
		t.Fatal("expected the budget to be gone")
	}
}

func TestEnsureDefaultLLMGatewayBudget(t *testing.T) {
	c := newTestClient(t)
	if err := c.EnsureDefaultLLMGatewayBudget(t.Context(), 1000, -1); err != nil {
		t.Fatal(err)
	}
	budgets, err := c.ListLLMGatewayBudgets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 1 || len(budgets[0].Limits) != 1 || budgets[0].Limits[0].Metric != types.LLMGatewayBudgetMetricInputTokens ||
		!budgets[0].PerUser || !budgets[0].ExemptAdmins || !budgets[0].Enabled {
		t.Fatalf("unexpected default budget %+v", budgets)
	}
}
