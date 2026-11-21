package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// riskSourceSecretContext is the credential context that holds risk source secrets,
	// keyed by source kind.
	riskSourceSecretContext = "risk-register-source"
)

// RiskListOptions filters the risk register. Empty slices match everything.
type RiskListOptions struct {
	Status  []string
	Segment []string
	Type    []string
}

// RiskSyncResult says what a sync changed.
type RiskSyncResult struct {
	Found int
	New   int
}

// ListRisks returns register entries, most severe and most recently seen first.
func (c *Client) ListRisks(ctx context.Context, opts RiskListOptions) ([]types.Risk, error) {
	db := c.db.WithContext(ctx).Model(&types.Risk{})
	if len(opts.Status) > 0 {
		db = db.Where("status IN ?", opts.Status)
	}
	if len(opts.Segment) > 0 {
		db = db.Where("segment IN ?", opts.Segment)
	}
	if len(opts.Type) > 0 {
		db = db.Where("type IN ?", opts.Type)
	}
	var risks []types.Risk
	if err := db.Order("CASE severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END").Order("last_seen_at DESC").Order("id").Find(&risks).Error; err != nil {
		return nil, err
	}
	return risks, nil
}

// GetRisk returns one register entry with its decisions, oldest first.
func (c *Client) GetRisk(ctx context.Context, id uint) (*types.Risk, []types.RiskDecision, error) {
	var risk types.Risk
	if err := c.db.WithContext(ctx).First(&risk, id).Error; err != nil {
		return nil, nil, err
	}
	var decisions []types.RiskDecision
	if err := c.db.WithContext(ctx).Where("risk_id = ?", id).Order("id").Find(&decisions).Error; err != nil {
		return nil, nil, err
	}
	return &risk, decisions, nil
}

// SyncRisks brings the register in line with one collection run. A finding that is new
// becomes an open entry. One that is already there keeps its status, owner and decisions and
// has its description refreshed. An entry that wasn't found this time is marked unobserved,
// but only when every source it came from ran successfully, so a failed source never clears
// its own entries. Treated entries get a dated proof record either way.
func (c *Client) SyncRisks(ctx context.Context, now time.Time, found []types.Risk, okSources []string) (RiskSyncResult, error) {
	result := RiskSyncResult{Found: len(found)}
	err := c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []types.Risk
		if err := tx.Find(&existing).Error; err != nil {
			return err
		}
		byFingerprint := make(map[string]*types.Risk, len(existing))
		for i := range existing {
			byFingerprint[existing[i].Fingerprint] = &existing[i]
		}

		seen := make(map[string]bool, len(found))
		for _, finding := range found {
			seen[finding.Fingerprint] = true
			risk, ok := byFingerprint[finding.Fingerprint]
			if !ok {
				finding.ID = 0
				finding.Status = string(types2.RiskStatusOpen)
				finding.FirstSeenAt, finding.LastSeenAt, finding.Observed = now, now, true
				if err := tx.Create(&finding).Error; err != nil {
					return err
				}
				result.New++
				continue
			}

			risk.Type, risk.Segment, risk.Severity = finding.Type, finding.Segment, finding.Severity
			risk.Title, risk.Summary = finding.Title, finding.Summary
			risk.Sources, risk.Evidence, risk.Metrics = finding.Sources, finding.Evidence, finding.Metrics
			risk.Treatment, risk.Frameworks = finding.Treatment, finding.Frameworks
			if finding.Analysis != "" {
				risk.Analysis = finding.Analysis
			}
			risk.LastSeenAt, risk.Observed = now, true
			if risk.Status == string(types2.RiskStatusTreated) {
				risk.Proof = &types2.RiskEvidenceStatus{
					CheckedAt:     *types2.NewTime(now),
					StillObserved: true,
					Metrics:       finding.Metrics,
					Note:          "The condition was still found after the policy was recorded.",
				}
			}
			if err := tx.Save(risk).Error; err != nil {
				return err
			}
		}

		for i := range existing {
			risk := &existing[i]
			if seen[risk.Fingerprint] || !containsAll(okSources, risk.Sources) {
				continue
			}
			changed := risk.Observed
			risk.Observed = false
			if risk.Status == string(types2.RiskStatusTreated) {
				changed = true
				risk.Proof = &types2.RiskEvidenceStatus{
					CheckedAt: *types2.NewTime(now),
					Note:      "The condition was not found in this collection.",
				}
			}
			if changed {
				if err := tx.Save(risk).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	return result, err
}

// DecideRisk records a decision on a risk and applies its effect in one transaction. It
// returns the updated risk.
func (c *Client) DecideRisk(ctx context.Context, id uint, now time.Time, userID, username string, req types2.RiskDecisionRequest) (*types.Risk, error) {
	var risk types.Risk
	err := c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&risk, id).Error; err != nil {
			return err
		}
		from := risk.Status
		switch req.Action {
		case types2.RiskDecisionAccept:
			risk.Status = string(types2.RiskStatusAccepted)
		case types2.RiskDecisionDismiss:
			risk.Status = string(types2.RiskStatusDismissed)
		case types2.RiskDecisionReopen:
			risk.Status = string(types2.RiskStatusOpen)
			risk.TreatedAt, risk.TreatmentRef, risk.Proof = nil, "", nil
		case types2.RiskDecisionTreat:
			risk.Status = string(types2.RiskStatusTreated)
			risk.TreatedAt, risk.TreatmentRef = &now, req.TreatmentRef
			risk.Proof = nil
		case types2.RiskDecisionAssign:
			risk.Owner = req.Owner
		case types2.RiskDecisionComment:
		default:
			return fmt.Errorf("unknown risk decision %q", req.Action)
		}
		if err := tx.Save(&risk).Error; err != nil {
			return err
		}
		return tx.Create(&types.RiskDecision{
			CreatedAt:  now,
			RiskID:     risk.ID,
			UserID:     userID,
			Username:   username,
			Action:     string(req.Action),
			Note:       req.Note,
			FromStatus: from,
			ToStatus:   risk.Status,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &risk, nil
}

// OpenRiskCountsBySegment counts open register entries that are still observed, per segment.
func (c *Client) OpenRiskCountsBySegment(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		Segment string
		Count   int
	}
	if err := c.db.WithContext(ctx).Model(&types.Risk{}).
		Select("segment, COUNT(*) AS count").
		Where("status = ? AND observed = ?", string(types2.RiskStatusOpen), true).
		Group("segment").Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.Segment] = row.Count
	}
	return counts, nil
}

// ListRiskSources returns the configured sources.
func (c *Client) ListRiskSources(ctx context.Context) ([]types.RiskSource, error) {
	var sources []types.RiskSource
	if err := c.db.WithContext(ctx).Order("kind").Find(&sources).Error; err != nil {
		return nil, err
	}
	return sources, nil
}

// SaveRiskSource stores a source's configuration and merges secrets into what is already
// stored: an empty value keeps the saved one.
func (c *Client) SaveRiskSource(ctx context.Context, source types.RiskSource, secrets map[string]string) error {
	merged, err := c.RiskSourceSecrets(ctx, source.Kind)
	if err != nil {
		return err
	}
	for name, value := range secrets {
		if value != "" {
			merged[name] = value
		}
	}
	if len(merged) > 0 {
		if err := c.UpsertCredential(ctx, types.Credential{Context: riskSourceSecretContext, Name: source.Kind, Secrets: merged}); err != nil {
			return err
		}
	}
	return c.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"updated_at", "enabled", "config"}),
	}).Create(&source).Error
}

// RiskSourceSecrets returns a source's secrets, or an empty map if it has none.
func (c *Client) RiskSourceSecrets(ctx context.Context, kind string) (map[string]string, error) {
	cred, err := c.RevealCredential(ctx, []string{riskSourceSecretContext}, kind)
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

// SetRiskSourceStatus records how a source did in the latest run.
func (c *Client) SetRiskSourceStatus(ctx context.Context, status types2.RiskSourceStatus) error {
	return c.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_status"}),
	}).Create(&types.RiskSource{Kind: string(status.Kind), LastStatus: &status}).Error
}

// CreateRiskRun records that a collection run started.
func (c *Client) CreateRiskRun(ctx context.Context, run *types.RiskCollectionRun) error {
	return c.db.WithContext(ctx).Create(run).Error
}

// UpdateRiskRun saves a collection run's outcome.
func (c *Client) UpdateRiskRun(ctx context.Context, run *types.RiskCollectionRun) error {
	return c.db.WithContext(ctx).Save(run).Error
}

// LatestRiskRun returns the most recent finished collection run, or nil if there is none.
func (c *Client) LatestRiskRun(ctx context.Context) (*types.RiskCollectionRun, error) {
	var run types.RiskCollectionRun
	err := c.db.WithContext(ctx).Where("finished_at IS NOT NULL").Order("id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListRiskRuns returns the most recent collection runs, newest first.
func (c *Client) ListRiskRuns(ctx context.Context, limit int) ([]types.RiskCollectionRun, error) {
	var runs []types.RiskCollectionRun
	if err := c.db.WithContext(ctx).Order("id DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// ErrRiskNotFound reports whether err is a missing register entry.
func ErrRiskNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// containsAll reports whether every element of want is in have. An empty want is never
// contained, so an entry with no recorded source is left alone.
func containsAll(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
