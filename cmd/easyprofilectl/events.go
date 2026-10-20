package main

import (
	"context"
	"time"
)

// The event channel has capacity one. Consume the pending wakeup at the end of
// the bounded settling window, before a fresh observation, rather than running
// another full reconciliation for an event already covered by that observation.
// Events arriving during reconciliation remain queued for the next pass.
func waitForReconcileBatch(ctx context.Context, events <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-events:
	default:
	}
	return true
}
