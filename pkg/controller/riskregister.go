package controller

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/obot-platform/obot/pkg/riskregister/collect"
)

const (
	riskRegisterTriggerScheduled = "scheduled"
)

// runRiskRegisterCollection collects for the AI risk register on a fixed interval. It runs on
// the leader only, so replicas don't collect at the same time.
func (c *Controller) runRiskRegisterCollection(ctx context.Context) {
	ticker := time.NewTicker(c.services.RiskRegisterInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.services.RiskRegister.Run(ctx, riskRegisterTriggerScheduled); err != nil && !errors.Is(err, collect.ErrRunInProgress) {
				slog.Error("scheduled risk register collection failed", "error", err)
			}
		}
	}
}
