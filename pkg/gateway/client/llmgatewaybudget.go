package client

import (
	"context"
	"slices"

	"github.com/obot-platform/obot/pkg/gateway/types"
)

// ListLLMGatewayBudgetBindings returns every budget binding, optionally limited to one
// virtual key.
func (c *Client) ListLLMGatewayBudgetBindings(ctx context.Context, virtualKeyID string) ([]types.LLMGatewayBudgetBinding, error) {
	var bindings []types.LLMGatewayBudgetBinding
	q := c.db.WithContext(ctx).Order("id")
	if virtualKeyID != "" {
		q = q.Where("virtual_key_id = ?", virtualKeyID)
	}
	return bindings, q.Find(&bindings).Error
}

// ReplaceLLMGatewayBudgetBindings sets the complete list of subjects bound to a virtual key.
func (c *Client) ReplaceLLMGatewayBudgetBindings(ctx context.Context, virtualKeyID string, bindings []types.LLMGatewayBudgetBinding) error {
	tx := c.db.WithContext(ctx).Begin()
	defer tx.Rollback()

	if err := tx.Where("virtual_key_id = ?", virtualKeyID).Delete(&types.LLMGatewayBudgetBinding{}).Error; err != nil {
		return err
	}
	for i := range bindings {
		bindings[i].ID = 0
		bindings[i].VirtualKeyID = virtualKeyID
		if bindings[i].SubjectType == types.LLMGatewayBindingSubjectEveryone {
			bindings[i].SubjectID = ""
		}
	}
	if len(bindings) > 0 {
		if err := tx.Create(&bindings).Error; err != nil {
			return err
		}
	}
	return tx.Commit().Error
}

// DeleteLLMGatewayBudgetBindings removes every binding for a virtual key.
func (c *Client) DeleteLLMGatewayBudgetBindings(ctx context.Context, virtualKeyID string) error {
	return c.db.WithContext(ctx).Where("virtual_key_id = ?", virtualKeyID).Delete(&types.LLMGatewayBudgetBinding{}).Error
}

// LLMGatewayVirtualKeyForUser picks the virtual key that governs a caller: a binding for
// the user wins over one for any of their groups, which wins over an everyone binding.
// Among equally specific bindings the virtual key with the lowest ID wins, which stays
// stable as bindings are edited. An empty result means no binding applies.
func (c *Client) LLMGatewayVirtualKeyForUser(ctx context.Context, userID string, groups []string) (string, error) {
	var bindings []types.LLMGatewayBudgetBinding
	if err := c.db.WithContext(ctx).Order("id").Find(&bindings).Error; err != nil {
		return "", err
	}

	best, bestRank := "", 0
	for _, b := range bindings {
		rank := 0
		switch b.SubjectType {
		case types.LLMGatewayBindingSubjectUser:
			if b.SubjectID == userID {
				rank = 3
			}
		case types.LLMGatewayBindingSubjectGroup:
			if slices.Contains(groups, b.SubjectID) {
				rank = 2
			}
		case types.LLMGatewayBindingSubjectEveryone:
			rank = 1
		}
		if rank > bestRank || (rank == bestRank && rank > 0 && b.VirtualKeyID < best) {
			best, bestRank = b.VirtualKeyID, rank
		}
	}
	return best, nil
}
