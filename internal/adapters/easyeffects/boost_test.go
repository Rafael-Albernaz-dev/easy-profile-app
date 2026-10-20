package easyeffects

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"
)

func TestPresetOverlayWaitsForRequestedNativePresetAndChain(t *testing.T) {
	base := []string{"equalizer#0", "compressor#0"}
	if presetOverlayReady("Jazz", "Rock", base, base) {
		t.Fatal("overlay attached before preset identity changed")
	}
	if presetOverlayReady("Jazz", "Jazz", []string{"equalizer#0"}, base) {
		t.Fatal("overlay attached to incomplete chain")
	}
	if !presetOverlayReady("Jazz", "Jazz", base, base) {
		t.Fatal("loaded native preset not accepted")
	}
	if !presetOverlayReady("Jazz", "Jazz", append(slices.Clone(base), boostPlugin), base) {
		t.Fatal("confirmed overlay not accepted")
	}
}

func TestBoostLimiterOverlayPreservesPresetAndRemovesOnlyOwnTail(t *testing.T) {
	base := []string{"equalizer#0", "limiter#0"}
	chain := slices.Clone(base)
	values := map[string]string{}
	writes := 0
	read := func() (map[string]string, error) { return values, nil }
	write := func(k, v string) error { writes++; values[k] = v; return nil }
	set := func(p []string) error { chain = slices.Clone(p); return nil }
	if err := configureBoost(true, 100, chain, base, read, write, set); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(chain, []string{"equalizer#0", "limiter#0", boostPlugin}) || !slices.Equal(base, []string{"equalizer#0", "limiter#0"}) || values["input-gain"] != "0.000000" {
		t.Fatal("preset altered or unlock raised gain", chain, base, values)
	}
	writes = 0
	if err := configureBoost(true, 100, chain, base, read, write, set); err != nil || writes != 0 {
		t.Fatal("healthy overlay rewritten", err, writes)
	}
	if err := configureBoost(true, 112, chain, base, read, write, set); err != nil {
		t.Fatal(err)
	}
	if validateBoostSettings(values, 112) != nil || values["input-gain"] == "0.000000" {
		t.Fatal("extra gain missing")
	}
	gain, err := strconv.ParseFloat(values["input-gain"], 64)
	if err != nil || math.Abs(gain-60*math.Log10(1.12)) > 0.000001 {
		t.Fatal("wrong cubic-scale boost gain", gain, err)
	}
	if err := configureBoost(false, 100, chain, base, read, write, set); err != nil || !slices.Equal(chain, base) || values["input-gain"] != "0.0" {
		t.Fatal("disable did not restore original chain", err, chain, values)
	}
}

func TestBoostRejectsUnknownChainAndUnconfirmedLimiter(t *testing.T) {
	want := errors.New("settings failed")
	base := []string{"equalizer#0"}
	for _, fault := range []string{"chain", "read", "write", "readback", "detach"} {
		chain := slices.Clone(base)
		if fault == "chain" {
			chain = []string{"other#0"}
		}
		if fault == "detach" {
			chain = append(chain, boostPlugin)
		}
		values := map[string]string{}
		err := configureBoost(fault != "detach", 112, chain, base, func() (map[string]string, error) {
			if fault == "read" {
				return nil, want
			}
			return values, nil
		}, func(k, v string) error {
			if fault == "write" || fault == "detach" {
				return want
			}
			if fault != "readback" {
				values[k] = v
			}
			return nil
		}, func([]string) error { return nil })
		if err == nil {
			t.Fatal("failed overlay accepted", fault)
		}
	}
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	t.Setenv("PATH", t.TempDir())
	if err := NewRealEasyEffects("", 0).SetOutputBoost(context.Background(), true, 112); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal("host escape", err)
	}
}
