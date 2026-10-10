package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
)

const (
	defaultLLMGatewayBudgetID = "daily-token-limit"
)

// LLMGatewayBudgetExceeded describes the first limit a request would exceed.
type LLMGatewayBudgetExceeded struct {
	Budget  types.LLMGatewayBudget
	Limit   types.LLMGatewayBudgetLimit
	Used    float64
	ResetAt time.Time
}

type budgetUsageTotals struct {
	InputTokens  float64
	OutputTokens float64
	TotalTokens  float64
	Spend        float64
	Requests     float64
}

// LLMGatewayBudgetUsageEntry is the usage of one user (or of everyone, for a shared
// budget) in each limit's current window, in the same order as the budget's limits.
type LLMGatewayBudgetUsageEntry struct {
	UserID string    `json:"userID,omitempty"`
	Used   []float64 `json:"used"`
}

func (e *LLMGatewayBudgetExceeded) Error() string {
	what := metricLabel(e.Limit.Metric)
	scope := "shared"
	if e.Budget.PerUser {
		scope = "your"
	}
	msg := fmt.Sprintf("%s LLM budget %q is exhausted: %s of %s %s used in the current window", scope, e.Budget.Name, formatMetric(e.Limit.Metric, e.Used), formatMetric(e.Limit.Metric, e.Limit.Amount), what)
	if !e.ResetAt.IsZero() {
		msg += fmt.Sprintf("; more becomes available by %s", e.ResetAt.UTC().Format(time.RFC3339))
	}
	return msg
}

func metricLabel(metric string) string {
	switch metric {
	case types.LLMGatewayBudgetMetricSpend:
		return "spend"
	case types.LLMGatewayBudgetMetricInputTokens:
		return "input tokens"
	case types.LLMGatewayBudgetMetricOutputTokens:
		return "output tokens"
	case types.LLMGatewayBudgetMetricRequests:
		return "requests"
	default:
		return "tokens"
	}
}

func formatMetric(metric string, v float64) string {
	if metric == types.LLMGatewayBudgetMetricSpend {
		return fmt.Sprintf("$%.2f", v)
	}
	return strconv.FormatInt(int64(v), 10)
}

// ParseLLMGatewayBudgetWindow returns the start of a limit's current window and, for
// calendar windows, when it next resets. Rolling windows report a zero reset time.
func ParseLLMGatewayBudgetWindow(limit types.LLMGatewayBudgetLimit, now time.Time) (start, reset time.Time, _ error) {
	w := strings.TrimSpace(limit.Window)
	if len(w) < 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid window %q", limit.Window)
	}
	n, err := strconv.Atoi(w[:len(w)-1])
	if err != nil || n <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid window %q", limit.Window)
	}
	unit := w[len(w)-1]
	if limit.Calendar {
		if n != 1 {
			return time.Time{}, time.Time{}, fmt.Errorf("calendar windows must be a single unit, got %q", limit.Window)
		}
		now = now.UTC()
		switch unit {
		case 'm':
			start = now.Truncate(time.Minute)
			return start, start.Add(time.Minute), nil
		case 'h':
			start = now.Truncate(time.Hour)
			return start, start.Add(time.Hour), nil
		case 'd':
			start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			return start, start.AddDate(0, 0, 1), nil
		case 'w':
			day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			offset := (int(day.Weekday()) + 6) % 7 // weeks start on Monday
			start = day.AddDate(0, 0, -offset)
			return start, start.AddDate(0, 0, 7), nil
		case 'M':
			start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
			return start, start.AddDate(0, 1, 0), nil
		}
		return time.Time{}, time.Time{}, fmt.Errorf("invalid window unit in %q", limit.Window)
	}
	switch unit {
	case 'm':
		return now.Add(-time.Duration(n) * time.Minute), time.Time{}, nil
	case 'h':
		return now.Add(-time.Duration(n) * time.Hour), time.Time{}, nil
	case 'd':
		return now.AddDate(0, 0, -n), time.Time{}, nil
	case 'w':
		return now.AddDate(0, 0, -7*n), time.Time{}, nil
	case 'M':
		return now.AddDate(0, -n, 0), time.Time{}, nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("invalid window unit in %q", limit.Window)
}

// ValidateLLMGatewayBudget checks a budget before it is saved.
func ValidateLLMGatewayBudget(b *types.LLMGatewayBudget) error {
	if strings.TrimSpace(b.Name) == "" {
		return errors.New("name is required")
	}
	if len(b.Limits) == 0 {
		return errors.New("at least one limit is required")
	}
	for _, l := range b.Limits {
		switch l.Metric {
		case types.LLMGatewayBudgetMetricSpend, types.LLMGatewayBudgetMetricTokens, types.LLMGatewayBudgetMetricInputTokens,
			types.LLMGatewayBudgetMetricOutputTokens, types.LLMGatewayBudgetMetricRequests:
		default:
			return fmt.Errorf("invalid metric %q", l.Metric)
		}
		if l.Amount < 0 {
			return errors.New("limit amounts must not be negative")
		}
		if _, _, err := ParseLLMGatewayBudgetWindow(l, time.Now()); err != nil {
			return err
		}
	}
	for _, s := range b.Subjects {
		switch s.SubjectType {
		case types.LLMGatewayBudgetSubjectUser, types.LLMGatewayBudgetSubjectGroup:
			if s.SubjectID == "" {
				return fmt.Errorf("%s subjects need an ID", s.SubjectType)
			}
		case types.LLMGatewayBudgetSubjectEveryone:
		default:
			return fmt.Errorf("invalid subject type %q", s.SubjectType)
		}
	}
	return nil
}

func (c *Client) ListLLMGatewayBudgets(ctx context.Context) ([]types.LLMGatewayBudget, error) {
	var budgets []types.LLMGatewayBudget
	return budgets, c.db.WithContext(ctx).Preload("Subjects").Order("created_at").Find(&budgets).Error
}

func (c *Client) GetLLMGatewayBudget(ctx context.Context, id string) (*types.LLMGatewayBudget, error) {
	var budget types.LLMGatewayBudget
	if err := c.db.WithContext(ctx).Preload("Subjects").Where("id = ?", id).First(&budget).Error; err != nil {
		return nil, err
	}
	return &budget, nil
}

func (c *Client) CreateLLMGatewayBudget(ctx context.Context, b *types.LLMGatewayBudget) error {
	if err := ValidateLLMGatewayBudget(b); err != nil {
		return err
	}
	b.ID = uuid.NewString()
	return c.createLLMGatewayBudget(ctx, b)
}

func (c *Client) createLLMGatewayBudget(ctx context.Context, b *types.LLMGatewayBudget) error {
	for i := range b.Subjects {
		normalizeSubject(&b.Subjects[i])
	}
	return c.db.WithContext(ctx).Create(b).Error
}

func (c *Client) UpdateLLMGatewayBudget(ctx context.Context, b *types.LLMGatewayBudget) error {
	if err := ValidateLLMGatewayBudget(b); err != nil {
		return err
	}
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		subjects := b.Subjects
		if err := tx.Model(b).Select("name", "description", "enabled", "per_user", "exempt_admins", "user_token_limits", "limits", "models").Updates(b).Error; err != nil {
			return err
		}
		if err := tx.Where("budget_id = ?", b.ID).Delete(&types.LLMGatewayBudgetSubject{}).Error; err != nil {
			return err
		}
		for i := range subjects {
			subjects[i].ID = 0
			subjects[i].BudgetID = b.ID
			normalizeSubject(&subjects[i])
		}
		if len(subjects) > 0 {
			if err := tx.Create(&subjects).Error; err != nil {
				return err
			}
		}
		b.Subjects = subjects
		return nil
	})
}

func (c *Client) DeleteLLMGatewayBudget(ctx context.Context, id string) error {
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("budget_id = ?", id).Delete(&types.LLMGatewayBudgetUsage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("budget_id = ?", id).Delete(&types.LLMGatewayBudgetSubject{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&types.LLMGatewayBudget{}).Error
	})
}

func normalizeSubject(s *types.LLMGatewayBudgetSubject) {
	if s.SubjectType == types.LLMGatewayBudgetSubjectEveryone {
		s.SubjectID = ""
	}
}

// LLMGatewayBudgetsForUser returns the enabled budgets that apply to a user's request for a
// model. Every applicable budget is enforced; a request must fit within all of them.
func (c *Client) LLMGatewayBudgetsForUser(ctx context.Context, userID string, groups []string, isAdmin bool, modelID string) ([]types.LLMGatewayBudget, error) {
	var budgets []types.LLMGatewayBudget
	if err := c.db.WithContext(ctx).Preload("Subjects").Where("enabled = ?", true).Find(&budgets).Error; err != nil {
		return nil, err
	}

	var result []types.LLMGatewayBudget
	for _, b := range budgets {
		if isAdmin && b.ExemptAdmins {
			continue
		}
		if len(b.Models) > 0 && !slices.Contains(b.Models, modelID) {
			continue
		}
		if slices.ContainsFunc(b.Subjects, func(s types.LLMGatewayBudgetSubject) bool {
			switch s.SubjectType {
			case types.LLMGatewayBudgetSubjectEveryone:
				return true
			case types.LLMGatewayBudgetSubjectUser:
				return s.SubjectID == userID
			case types.LLMGatewayBudgetSubjectGroup:
				return slices.Contains(groups, s.SubjectID)
			}
			return false
		}) {
			result = append(result, b)
		}
	}
	return result, nil
}

func (t budgetUsageTotals) value(metric string) float64 {
	switch metric {
	case types.LLMGatewayBudgetMetricSpend:
		return t.Spend
	case types.LLMGatewayBudgetMetricInputTokens:
		return t.InputTokens
	case types.LLMGatewayBudgetMetricOutputTokens:
		return t.OutputTokens
	case types.LLMGatewayBudgetMetricRequests:
		return t.Requests
	default:
		return t.TotalTokens
	}
}

func (c *Client) budgetUsageSince(ctx context.Context, budgetID, userID string, since time.Time) (budgetUsageTotals, error) {
	var totals budgetUsageTotals
	q := c.db.WithContext(ctx).Model(&types.LLMGatewayBudgetUsage{}).
		Select("COALESCE(SUM(input_tokens), 0) AS input_tokens, COALESCE(SUM(output_tokens), 0) AS output_tokens, "+
			"COALESCE(SUM(total_tokens), 0) AS total_tokens, COALESCE(SUM(spend), 0) AS spend, COUNT(*) AS requests").
		Where("budget_id = ? AND created_at >= ?", budgetID, since)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	return totals, q.Scan(&totals).Error
}

// CheckLLMGatewayBudgets reports the first limit among the given budgets that the user has
// already reached. Usage is counted per user for per-user budgets and across everyone for
// shared ones.
func (c *Client) CheckLLMGatewayBudgets(ctx context.Context, budgets []types.LLMGatewayBudget, userID string) error {
	now := time.Now()
	var user *types.User
	for _, b := range budgets {
		scopeUser := ""
		if b.PerUser {
			scopeUser = userID
		}
		limits := b.Limits
		if b.PerUser && b.UserTokenLimits {
			if user == nil {
				if user, _ = c.UserByID(ctx, userID); user == nil {
					user = &types.User{}
				}
			}
			limits = withUserTokenLimits(limits, user)
		}
		for _, l := range limits {
			start, reset, err := ParseLLMGatewayBudgetWindow(l, now)
			if err != nil {
				return fmt.Errorf("budget %q has an invalid limit: %w", b.Name, err)
			}
			totals, err := c.budgetUsageSince(ctx, b.ID, scopeUser, start)
			if err != nil {
				return err
			}
			if used := totals.value(l.Metric); used >= l.Amount {
				if reset.IsZero() {
					reset, err = c.rollingWindowReset(ctx, b.ID, scopeUser, start, l)
					if err != nil {
						return err
					}
				}
				return &LLMGatewayBudgetExceeded{Budget: b, Limit: l, Used: used, ResetAt: reset}
			}
		}
	}
	return nil
}

// withUserTokenLimits applies a user's personal daily token limits to a budget's input and
// output token limits, as the daily token quota did.
func withUserTokenLimits(limits []types.LLMGatewayBudgetLimit, user *types.User) []types.LLMGatewayBudgetLimit {
	out := make([]types.LLMGatewayBudgetLimit, 0, len(limits))
	for _, l := range limits {
		personal := 0
		switch l.Metric {
		case types.LLMGatewayBudgetMetricInputTokens:
			personal = user.DailyInputTokensLimit
		case types.LLMGatewayBudgetMetricOutputTokens:
			personal = user.DailyOutputTokensLimit
		}
		switch {
		case personal < 0:
			continue
		case personal > 0:
			l.Amount = float64(personal)
		}
		out = append(out, l)
	}
	return out
}

// rollingWindowReset estimates when a rolling window frees up: when the oldest usage in it
// ages out.
func (c *Client) rollingWindowReset(ctx context.Context, budgetID, userID string, start time.Time, l types.LLMGatewayBudgetLimit) (time.Time, error) {
	var oldest types.LLMGatewayBudgetUsage
	q := c.db.WithContext(ctx).Where("budget_id = ? AND created_at >= ?", budgetID, start)
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if err := q.Order("created_at").Limit(1).Find(&oldest).Error; err != nil || oldest.ID == 0 {
		return time.Time{}, err
	}
	now := time.Now()
	windowStart, _, _ := ParseLLMGatewayBudgetWindow(l, now)
	return oldest.CreatedAt.Add(now.Sub(windowStart)), nil
}

// RecordLLMGatewayBudgetUsage counts one request's usage against each of the budgets it was
// admitted under.
func (c *Client) RecordLLMGatewayBudgetUsage(ctx context.Context, budgetIDs []string, userID, model string, usage types.TokenUsage) error {
	if len(budgetIDs) == 0 {
		return nil
	}
	rows := make([]types.LLMGatewayBudgetUsage, 0, len(budgetIDs))
	now := time.Now()
	for _, id := range budgetIDs {
		rows = append(rows, types.LLMGatewayBudgetUsage{
			BudgetID:     id,
			UserID:       userID,
			CreatedAt:    now,
			Model:        model,
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			TotalTokens:  usage.TotalTokens,
			Spend:        usage.TotalSpend,
		})
	}
	return c.db.WithContext(ctx).Create(&rows).Error
}

// LLMGatewayBudgetUsageReport returns current usage for a budget: one entry per user who
// has usage in the longest window for per-user budgets, or a single total for shared ones.
func (c *Client) LLMGatewayBudgetUsageReport(ctx context.Context, b *types.LLMGatewayBudget) ([]LLMGatewayBudgetUsageEntry, error) {
	now := time.Now()
	starts := make([]time.Time, len(b.Limits))
	earliest := now
	for i, l := range b.Limits {
		start, _, err := ParseLLMGatewayBudgetWindow(l, now)
		if err != nil {
			return nil, err
		}
		starts[i] = start
		if start.Before(earliest) {
			earliest = start
		}
	}

	if !b.PerUser {
		entry := LLMGatewayBudgetUsageEntry{Used: make([]float64, len(b.Limits))}
		for i, l := range b.Limits {
			totals, err := c.budgetUsageSince(ctx, b.ID, "", starts[i])
			if err != nil {
				return nil, err
			}
			entry.Used[i] = totals.value(l.Metric)
		}
		return []LLMGatewayBudgetUsageEntry{entry}, nil
	}

	var userIDs []string
	if err := c.db.WithContext(ctx).Model(&types.LLMGatewayBudgetUsage{}).
		Where("budget_id = ? AND created_at >= ?", b.ID, earliest).
		Distinct("user_id").Pluck("user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	entries := make([]LLMGatewayBudgetUsageEntry, 0, len(userIDs))
	for _, userID := range userIDs {
		entry := LLMGatewayBudgetUsageEntry{UserID: userID, Used: make([]float64, len(b.Limits))}
		for i, l := range b.Limits {
			totals, err := c.budgetUsageSince(ctx, b.ID, userID, starts[i])
			if err != nil {
				return nil, err
			}
			entry.Used[i] = totals.value(l.Metric)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// deleteOldLLMGatewayBudgetUsage removes usage older than any window can reach.
func (c *Client) deleteOldLLMGatewayBudgetUsage(ctx context.Context, now time.Time) error {
	return c.db.WithContext(ctx).Where("created_at < ?", now.AddDate(-1, -1, 0)).Delete(&types.LLMGatewayBudgetUsage{}).Error
}

// EnsureDefaultLLMGatewayBudget carries the server's daily token limits over to the LLM
// gateway as an editable per-user budget. Negative limits are disabled and produce no limit.
func (c *Client) EnsureDefaultLLMGatewayBudget(ctx context.Context, dailyInputTokens, dailyOutputTokens int) error {
	var limits []types.LLMGatewayBudgetLimit
	if dailyInputTokens >= 0 {
		limits = append(limits, types.LLMGatewayBudgetLimit{Metric: types.LLMGatewayBudgetMetricInputTokens, Amount: float64(dailyInputTokens), Window: "1d"})
	}
	if dailyOutputTokens >= 0 {
		limits = append(limits, types.LLMGatewayBudgetLimit{Metric: types.LLMGatewayBudgetMetricOutputTokens, Amount: float64(dailyOutputTokens), Window: "1d"})
	}
	if len(limits) == 0 {
		return nil
	}
	// A fixed ID makes this safe to run on several replicas at once: only one budget can be
	// created, and the others find it.
	if _, err := c.GetLLMGatewayBudget(ctx, defaultLLMGatewayBudgetID); err == nil {
		return nil
	}
	err := c.createLLMGatewayBudget(ctx, &types.LLMGatewayBudget{
		ID:              defaultLLMGatewayBudgetID,
		Name:            "Daily token limit",
		Description:     "Carried over from the server's daily user token limits. Users' personal daily token limits still apply in its place.",
		Enabled:         true,
		PerUser:         true,
		ExemptAdmins:    true,
		UserTokenLimits: true,
		Limits:          limits,
		Subjects:        []types.LLMGatewayBudgetSubject{{SubjectType: types.LLMGatewayBudgetSubjectEveryone}},
	})
	if err != nil {
		if _, getErr := c.GetLLMGatewayBudget(ctx, defaultLLMGatewayBudgetID); getErr == nil {
			// Another replica created it first.
			return nil
		}
	}
	return err
}
