package client

import (
	"context"
	"errors"
	"time"

	"github.com/obot-platform/obot/pkg/gateway/types"
	"gorm.io/gorm"
)

const (
	aiConversationCredentialContext = "ai-conversation"
	aiConversationTitleKey          = "title"
	aiConversationMessagesKey       = "messages"
)

var (
	// ErrAIConversationNotFound is returned for a conversation that doesn't exist or isn't
	// the caller's.
	ErrAIConversationNotFound = errors.New("conversation not found")
)

// AIConversationContent is what a conversation holds: its title and its messages as JSON.
type AIConversationContent struct {
	Title    string
	Messages string
}

// AIConversationSummary lists a conversation without its messages.
type AIConversationSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SaveAIConversation creates or replaces the user's conversation.
func (c *Client) SaveAIConversation(ctx context.Context, userID, id string, content AIConversationContent) error {
	credential := types.Credential{
		Context: aiConversationCredentialContext,
		Name:    id,
		Secrets: map[string]string{aiConversationTitleKey: content.Title, aiConversationMessagesKey: content.Messages},
	}
	if err := c.encryptCredential(ctx, &credential); err != nil {
		return err
	}
	now := time.Now().UTC()
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing types.AIConversation
		err := tx.Where("id = ?", id).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&types.AIConversation{
				ID: id, UserID: userID, CreatedAt: now, UpdatedAt: now,
				Payload: credential.Secrets, Encrypted: credential.Encrypted,
			}).Error
		case err != nil:
			return err
		case existing.UserID != userID:
			return ErrAIConversationNotFound
		}
		// A struct, not a map, so the payload goes through its JSON serializer; Select so a
		// false Encrypted is written too.
		return tx.Model(&existing).Select("payload", "encrypted", "updated_at").Updates(types.AIConversation{
			Payload:   credential.Secrets,
			Encrypted: credential.Encrypted,
			UpdatedAt: now,
		}).Error
	})
}

// GetAIConversation returns the user's conversation.
func (c *Client) GetAIConversation(ctx context.Context, userID, id string) (AIConversationSummary, AIConversationContent, error) {
	var row types.AIConversation
	if err := c.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AIConversationSummary{}, AIConversationContent{}, ErrAIConversationNotFound
		}
		return AIConversationSummary{}, AIConversationContent{}, err
	}
	content, err := c.revealAIConversation(ctx, row)
	if err != nil {
		return AIConversationSummary{}, AIConversationContent{}, err
	}
	return summarizeAIConversation(row, content), content, nil
}

// ListAIConversations lists the user's conversations, most recent first.
func (c *Client) ListAIConversations(ctx context.Context, userID string, limit int) ([]AIConversationSummary, error) {
	var rows []types.AIConversation
	if err := c.db.WithContext(ctx).Where("user_id = ?", userID).Order("updated_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]AIConversationSummary, 0, len(rows))
	for _, row := range rows {
		content, err := c.revealAIConversation(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, summarizeAIConversation(row, content))
	}
	return out, nil
}

// DeleteAIConversation deletes the user's conversation.
func (c *Client) DeleteAIConversation(ctx context.Context, userID, id string) error {
	result := c.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&types.AIConversation{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAIConversationNotFound
	}
	return nil
}

func (c *Client) revealAIConversation(ctx context.Context, row types.AIConversation) (AIConversationContent, error) {
	credential := types.Credential{Context: aiConversationCredentialContext, Name: row.ID, Secrets: row.Payload, Encrypted: row.Encrypted}
	if err := c.decryptCredential(ctx, &credential); err != nil {
		return AIConversationContent{}, err
	}
	return AIConversationContent{
		Title:    credential.Secrets[aiConversationTitleKey],
		Messages: credential.Secrets[aiConversationMessagesKey],
	}, nil
}

func summarizeAIConversation(row types.AIConversation, content AIConversationContent) AIConversationSummary {
	return AIConversationSummary{ID: row.ID, Title: content.Title, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
