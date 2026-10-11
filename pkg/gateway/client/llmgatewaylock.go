package client

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// llmGatewayLockID is the Postgres advisory lock that serializes changes Obot itself makes
// to the LLM gateway's shared configuration across replicas.
const llmGatewayLockID int64 = 0x6f626f744c4c4d67 // "obotLLMg"

// llmGatewayLockTimeout bounds how long a replica holds, or waits for, the LLM gateway lock.
const llmGatewayLockTimeout = 5 * time.Minute

// WithLLMGatewayLock runs fn while holding a lock every Obot replica shares, so replicas that
// start together don't make the same gateway changes at once, each from its own copy of the
// configuration. With SQLite there is a single replica, and fn just runs.
//
// The lock lasts as long as a transaction, which is rolled back when its context ends. It
// doesn't use ctx, so a caller that goes away (an administrator's request) can't release the
// lock while fn is still changing the gateway; a timeout bounds it instead.
func (c *Client) WithLLMGatewayLock(ctx context.Context, fn func() error) error {
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), llmGatewayLockTimeout)
	defer cancel()
	db := c.db.WithContext(lockCtx)
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
