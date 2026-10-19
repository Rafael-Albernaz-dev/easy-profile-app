package pipewire

import (
	"context"
	"math"
	"testing"
)

func TestWPCTLLinearToCubicBoundary(t *testing.T) {
	for _, tc := range []struct {
		linear   float64
		expected string
	}{{0, "0.000000000"}, {0.125, "0.500000000"}, {0.857375, "0.950000000"}, {0.912673, "0.970000000"}, {1, "1.000000000"}} {
		got, err := wpctlVolume(tc.linear)
		if err != nil || got != tc.expected {
			t.Fatalf("%g => %q %v; expected %q", tc.linear, got, err, tc.expected)
		}
	}
	for _, bad := range []float64{-1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := wpctlVolume(bad); err == nil {
			t.Fatalf("accepted invalid gain %g", bad)
		}
	}
}
func TestRealReadsAndMonitorCannotEscapeTestMode(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "")
	t.Setenv("PATH", t.TempDir())
	r := NewRealPipeWire(0)
	ctx := context.Background()
	if _, err := r.GetNodeMute(ctx, "mic"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
	if _, err := r.GetSinkPort(ctx, "sink"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
	if err := r.Watch(ctx, make(chan struct{}, 1)); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
}
