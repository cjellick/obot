package types

import (
	"time"
)

// AIConversation is one Obot AI chat, owned by the user who had it. Its title and messages
// are content, so they are kept in Payload, encrypted with the credential store's
// encryption when it is configured.
type AIConversation struct {
	ID        string            `json:"id" gorm:"primaryKey"`
	UserID    string            `json:"userID" gorm:"index;not null"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt" gorm:"index"`
	Payload   map[string]string `json:"-" gorm:"serializer:json"`
	Encrypted bool              `json:"-"`
}
