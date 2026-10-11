package client

import (
	"context"

	"gorm.io/gorm"
)

// llmGatewayLockID is the Postgres advisory lock that serializes changes Obot itself makes
// to the LLM gateway's shared configuration across replicas.
const llmGatewayLockID int64 = 0x6f626f744c4c4d67 // "obotLLMg"

// WithLLMGatewayLock runs fn while holding a lock every Obot replica shares, so replicas that
// start together don't make the same gateway changes at once, each from its own copy of the
// configuration. With SQLite there is a single replica, and fn just runs.
func (c *Client) WithLLMGatewayLock(ctx context.Context, fn func() error) error {
	db := c.db.WithContext(ctx)
	if db.Name() != "postgres" {
		return fn()
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", llmGatewayLockID).Error; err != nil {
			return err
		}
		return fn()
	})
}
