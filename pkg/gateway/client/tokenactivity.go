package client

import (
	"context"
	"time"

	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
)

func (c *Client) InsertTokenUsage(ctx context.Context, activity *types.RunTokenActivity) error {
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if activity.ID == 0 {
			return tx.Create(activity).Error
		}
		return tx.Updates(activity).Error
	})
}

func (c *Client) TokenUsageForUser(ctx context.Context, userID string, start, end time.Time) ([]types.RunTokenActivity, error) {
	var activities []types.RunTokenActivity
	return activities, c.db.WithContext(ctx).Where("user_id = ?", userID).Where("created_at >= ? AND created_at <= ?", start, end).Order("created_at DESC").Find(&activities).Error
}

func (c *Client) TotalTokenUsageForUser(ctx context.Context, userID string, start, end time.Time) (types.RunTokenActivity, error) {
	activity, err := c.tokenUsageByUser(ctx, userID, start, end)
	if err != nil || len(activity) == 0 {
		return types.RunTokenActivity{
			UserID: userID,
		}, err
	}

	return activity[0], nil
}

func (c *Client) TokenUsageByUser(ctx context.Context, start, end time.Time) ([]types.RunTokenActivity, error) {
	return c.tokenUsageByUser(ctx, "", start, end)
}

// TokenUsageSeriesInRange returns all individual token usage records in the time range for all users.
// Results are ordered by created_at descending.
// The range is [start, end] inclusive so that the requested end time is the last moment included.
func (c *Client) TokenUsageSeriesInRange(ctx context.Context, start, end time.Time) ([]types.RunTokenActivity, error) {
	var activities []types.RunTokenActivity
	err := c.db.WithContext(ctx).Where("created_at >= ? AND created_at <= ?", start, end).
		Where("user_id IS NOT NULL").
		Order("created_at DESC").
		Find(&activities).Error
	return activities, err
}

func (c *Client) tokenUsageByUser(ctx context.Context, userID string, start, end time.Time) ([]types.RunTokenActivity, error) {
	var activities []types.RunTokenActivity
	db := c.db.WithContext(ctx).Model(new(types.RunTokenActivity)).
		Select("user_id, "+
			"SUM(input_tokens) as input_tokens, "+
			"SUM(cache_read_tokens) as cache_read_tokens, "+
			"SUM(cache_write_tokens) as cache_write_tokens, "+
			"SUM(output_tokens) as output_tokens, "+
			"SUM(thinking_tokens) as thinking_tokens, "+
			"SUM(total_tokens) as total_tokens, "+
			"SUM(input_spend) as input_spend, "+
			"SUM(cache_read_spend) as cache_read_spend, "+
			"SUM(cache_write_spend) as cache_write_spend, "+
			"SUM(output_spend) as output_spend, "+
			"SUM(total_spend) as total_spend").
		Where("created_at >= ? AND created_at <= ?", start, end)
	if userID != "" {
		db = db.Where("user_id = ?", userID)
	} else {
		db = db.Where("user_id IS NOT NULL")
	}
	return activities, db.Group("user_id").Scan(&activities).Error
}
