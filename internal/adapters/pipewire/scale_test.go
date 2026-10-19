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
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
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

func TestMalformedMuteReadbackCannotAuthorizeUnmute(t *testing.T) {
	for _, bad := range []string{"Volume:", "Volume: garbage", "Volume: NaN [MUTED]", "Volume: +Inf", "Volume: -1", "Volume: 0.5 UNKNOWN", "Volume: 0.5 [MUTED] extra"} {
		if _, _, err := parseVolumeReadback([]byte(bad)); err == nil {
			t.Fatalf("invalid mute readback accepted: %q", bad)
		}
	}
	for _, valid := range []struct {
		raw   string
		muted bool
	}{{"Volume: 0.95", false}, {"Volume: 0.00 [MUTED]", true}, {"Volume: 1.05 [MUTED]", true}} {
		if _, muted, err := parseVolumeReadback([]byte(valid.raw)); err != nil || muted != valid.muted {
			t.Fatal(valid.raw, muted, err)
		}
	}
}

func TestVolumeReadbackCannotHideOnePercentStepOrPhysicalCeilingViolation(t *testing.T) {
	for _, percent := range []int{1, 50, 95, 97, 100} {
		cubic := float64(percent) / 100
		linear := cubic * cubic * cubic
		if !volumeReadbackMatches(cubic, linear) {
			t.Fatal("exact readback rejected", percent)
		}
		if volumeReadbackMatches(cubic-0.01, linear) || volumeReadbackMatches(cubic+0.01, linear) {
			t.Fatal("whole step mismatch accepted", percent)
		}
	}
}
