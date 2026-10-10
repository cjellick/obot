package client

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
)

const (
	// guardrailSecretContext is the credential context holding guardrail profile secrets.
	guardrailSecretContext = "guardrail-profile"
)

// GuardrailEventFilter narrows ListGuardrailEvents.
type GuardrailEventFilter struct {
	RuleID  string
	Outcome string
	UserID  string
	Target  string
	Since   time.Time
	Until   time.Time
	Limit   int
	Offset  int
}

func (c *Client) ListGuardrailRules(ctx context.Context) ([]types.GuardrailRule, error) {
	var rules []types.GuardrailRule
	return rules, c.db.WithContext(ctx).Order("created_at").Find(&rules).Error
}

func (c *Client) GetGuardrailRule(ctx context.Context, id string) (*types.GuardrailRule, error) {
	var rule types.GuardrailRule
	if err := c.db.WithContext(ctx).Where("id = ?", id).First(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

func (c *Client) CreateGuardrailRule(ctx context.Context, rule *types.GuardrailRule) error {
	if rule.ID == "" {
		rule.ID = uuid.NewString()
	}
	return c.db.WithContext(ctx).Create(rule).Error
}

func (c *Client) UpdateGuardrailRule(ctx context.Context, rule *types.GuardrailRule) error {
	return c.db.WithContext(ctx).Model(rule).
		Select("name", "description", "enabled", "target", "apply_to", "action", "condition", "sampling_rate",
			"input_scope", "fail_closed", "block_message", "profile_ids", "subjects").
		Updates(rule).Error
}

func (c *Client) DeleteGuardrailRule(ctx context.Context, id string) error {
	return c.db.WithContext(ctx).Where("id = ?", id).Delete(&types.GuardrailRule{}).Error
}

func (c *Client) ListGuardrailProfiles(ctx context.Context) ([]types.GuardrailProfile, error) {
	var profiles []types.GuardrailProfile
	return profiles, c.db.WithContext(ctx).Order("created_at").Find(&profiles).Error
}

func (c *Client) GetGuardrailProfile(ctx context.Context, id string) (*types.GuardrailProfile, error) {
	var profile types.GuardrailProfile
	if err := c.db.WithContext(ctx).Where("id = ?", id).First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

// CreateGuardrailProfile saves a profile and its secrets.
func (c *Client) CreateGuardrailProfile(ctx context.Context, profile *types.GuardrailProfile, secrets map[string]string) error {
	if profile.ID == "" {
		profile.ID = uuid.NewString()
	}
	if err := c.db.WithContext(ctx).Create(profile).Error; err != nil {
		return err
	}
	return c.SetGuardrailProfileSecrets(ctx, profile.ID, secrets)
}

func (c *Client) UpdateGuardrailProfile(ctx context.Context, profile *types.GuardrailProfile) error {
	return c.db.WithContext(ctx).Model(profile).Select("name", "description", "config").Updates(profile).Error
}

func (c *Client) DeleteGuardrailProfile(ctx context.Context, id string) error {
	if _, err := c.DeleteCredential(ctx, guardrailSecretContext, id); err != nil {
		return err
	}
	return c.db.WithContext(ctx).Where("id = ?", id).Delete(&types.GuardrailProfile{}).Error
}

// GuardrailProfileSecrets returns a profile's secrets, or an empty map if it has none.
func (c *Client) GuardrailProfileSecrets(ctx context.Context, id string) (map[string]string, error) {
	cred, err := c.RevealCredential(ctx, []string{guardrailSecretContext}, id)
	if errors.As(err, &CredentialNotFoundError{}) {
		return map[string]string{}, nil
	} else if err != nil {
		return nil, err
	}
	if cred.Secrets == nil {
		return map[string]string{}, nil
	}
	return cred.Secrets, nil
}

// SetGuardrailProfileSecrets replaces a profile's secrets. An empty map removes them.
func (c *Client) SetGuardrailProfileSecrets(ctx context.Context, id string, secrets map[string]string) error {
	if len(secrets) == 0 {
		_, err := c.DeleteCredential(ctx, guardrailSecretContext, id)
		return err
	}
	return c.UpsertCredential(ctx, types.Credential{
		Context: guardrailSecretContext,
		Name:    id,
		Secrets: secrets,
	})
}

func (c *Client) InsertGuardrailEvents(ctx context.Context, events []types.GuardrailEvent) error {
	if len(events) == 0 {
		return nil
	}
	return c.db.WithContext(ctx).Create(&events).Error
}

func (c *Client) ListGuardrailEvents(ctx context.Context, f GuardrailEventFilter) ([]types.GuardrailEvent, int64, error) {
	q := c.db.WithContext(ctx).Model(&types.GuardrailEvent{})
	if f.RuleID != "" {
		q = q.Where("rule_id = ?", f.RuleID)
	}
	if f.Outcome != "" {
		q = q.Where("outcome = ?", f.Outcome)
	}
	if f.UserID != "" {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Target != "" {
		q = q.Where("target = ?", f.Target)
	}
	if !f.Since.IsZero() {
		q = q.Where("created_at >= ?", f.Since)
	}
	if !f.Until.IsZero() {
		q = q.Where("created_at <= ?", f.Until)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var events []types.GuardrailEvent
	err := q.Order("created_at DESC").Limit(limit).Offset(f.Offset).Find(&events).Error
	return events, total, err
}

// deleteOldGuardrailEvents removes events older than the LLM audit log retention.
func (c *Client) deleteOldGuardrailEvents(ctx context.Context, now time.Time, retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}
	return c.db.WithContext(ctx).Where("created_at < ?", now.AddDate(0, 0, -retentionDays)).Delete(&types.GuardrailEvent{}).Error
}

// ErrGuardrailNotFound reports whether err is a missing rule or profile.
func ErrGuardrailNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
