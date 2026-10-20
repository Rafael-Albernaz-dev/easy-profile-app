package main

import (
	"context"
	"testing"
	"time"
)

func TestReconcileBatchConsumesEarlierBurstPreservesLaterEvents(t *testing.T) {
	events := make(chan struct{}, 1)
	events <- struct{}{} // The initial wakeup has already been consumed by the caller.
	if !waitForReconcileBatch(context.Background(), events, time.Millisecond) || len(events) != 0 {
		t.Fatal("settling window left a redundant reconciliation queued")
	}
	events <- struct{}{} // Changes during reconciliation must still trigger the next pass.
	if len(events) != 1 {
		t.Fatal("new event lost")
	}
}

func TestReconcileBatchCancellationDoesNotDrainSafetyEvent(t *testing.T) {
	events := make(chan struct{}, 1)
	events <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForReconcileBatch(ctx, events, time.Hour) || len(events) != 1 {
		t.Fatal("cancellation drained pending work")
	}
}
