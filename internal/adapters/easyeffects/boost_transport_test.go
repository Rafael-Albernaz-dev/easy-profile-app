package easyeffects

import (
	"context"
	"errors"
	"github.com/toru/easy-profile-app/internal/inspect"
	"testing"
	"time"
)

func TestBoostNeedsConnectedDSPRatherThanInstalledPlugin(t *testing.T) {
	g := &inspect.Graph{Nodes: map[int]*inspect.NodeInfo{1: {Name: "easyeffects_sink", MediaClass: "Audio/Sink"}, 2: {Name: "ee_soe_equalizer"}, 3: {Name: "ee_soe_limiter"}, 4: {Name: "physical", MediaClass: "Audio/Sink"}}}
	chain := []string{"equalizer#1", "limiter#99"}
	if outputDSPReady(g, chain) {
		t.Fatal("unlinked nodes accepted")
	}
	g.Links = []inspect.LinkInfo{{OutNode: 1, InNode: 2, State: "active"}, {OutNode: 2, InNode: 3, State: "active"}, {OutNode: 3, InNode: 4, State: "active"}}
	if !outputDSPReady(g, chain) {
		t.Fatal("valid DSP rejected")
	}
	if outputDSPReady(g, chain[:1]) {
		t.Fatal("stale limiter accepted after OFF")
	}
	g.Links[1].State = "error"
	if outputDSPReady(g, chain) {
		t.Fatal("broken DSP accepted")
	}
}
func TestBoostConfirmationWaitsAndHonorsCancellation(t *testing.T) {
	n := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitBoostConfirmation(ctx, func() error {
		n++
		if n < 3 {
			return errors.New("loading")
		}
		return nil
	}); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	cancel()
	if err := waitBoostConfirmation(ctx, func() error { t.Fatal("read after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx2, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	if err := waitBoostConfirmation(ctx2, func() error { return errors.New("stale chain") }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
