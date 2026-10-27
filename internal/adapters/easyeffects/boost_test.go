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
	values := boostParameters(100)
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
	if err := configureBoost(true, 150, chain, base, read, write, set); err != nil {
		t.Fatal(err)
	}
	if validateBoostSettings(values, 150) != nil || values["input-gain"] == "0.000000" {
		t.Fatal("extra gain missing")
	}
	gain, err := strconv.ParseFloat(values["input-gain"], 64)
	if err != nil || math.Abs(gain-60*math.Log10(1.50)) > 0.000001 {
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
		values := boostParameters(100)
		values["release"] = "5.0"
		err := configureBoost(fault != "detach", 150, chain, base, func() (map[string]string, error) {
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
	if err := NewRealEasyEffects("", 0).SetOutputBoost(context.Background(), true, 150); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal("host escape", err)
	}
}

func TestBoostParameterValidationAgainstSchema(t *testing.T) {
	for pct := 100; pct <= 150; pct++ {
		params := boostParameters(pct)
		if err := validateBoostParameters(params); err != nil {
			t.Fatalf("valid parameters for %d%% failed validation: %v", pct, err)
		}
	}

	// Schema specifies release range [0.25, 20.0]. The old value 50.0 must be rejected.
	badRelease := boostParameters(150)
	badRelease["release"] = "50.0"
	if err := validateBoostParameters(badRelease); err == nil {
		t.Fatal("release=50.0 must be rejected by schema validator")
	}

	tooLowRelease := boostParameters(150)
	tooLowRelease["release"] = "0.1"
	if err := validateBoostParameters(tooLowRelease); err == nil {
		t.Fatal("release=0.1 must be rejected by schema validator")
	}

	badEnum := boostParameters(150)
	badEnum["mode"] = "'Invalid Mode'"
	if err := validateBoostParameters(badEnum); err == nil {
		t.Fatal("invalid mode enum must be rejected")
	}

	badGain := boostParameters(150)
	badGain["input-gain"] = "40.0"
	if err := validateBoostParameters(badGain); err == nil {
		t.Fatal("input-gain=40.0 outside [-36, 36] must be rejected")
	}
}

func TestBoostConfigureRollsBackOnPartialFailure(t *testing.T) {
	base := []string{"equalizer#0", "compressor#0"}
	initialValues := boostParameters(100)
	for k, v := range map[string]string{"bypass": "true", "input-gain": "0.0", "release": "5.0", "mode": "'Herm Thin'", "attack": "5.0", "threshold": "-10.0"} {
		initialValues[k] = v
	}

	// Case 1: write fails on a specific key
	chain := slices.Clone(base)
	values := make(map[string]string)
	for k, v := range initialValues {
		values[k] = v
	}
	writeCount := 0
	read := func() (map[string]string, error) { return values, nil }
	write := func(k, v string) error {
		writeCount++
		if writeCount == 4 {
			return errors.New("simulated dconf write failure")
		}
		values[k] = v
		return nil
	}
	set := func(p []string) error { chain = slices.Clone(p); return nil }

	err := configureBoost(true, 150, chain, base, read, write, set)
	if err == nil {
		t.Fatal("expected configureBoost to fail on partial write")
	}
	// Verify rollback: input-gain must be 0.0, chain must remain base
	if values["input-gain"] != "0.0" {
		t.Fatalf("rollback did not reset input-gain to 0.0: got %s", values["input-gain"])
	}
	if !slices.Equal(chain, base) {
		t.Fatalf("rollback did not preserve base chain: got %v", chain)
	}

	// Case 2: setChain fails
	chain = slices.Clone(base)
	values = make(map[string]string)
	for k, v := range initialValues {
		values[k] = v
	}
	write = func(k, v string) error {
		values[k] = v
		return nil
	}
	set = func(p []string) error {
		if slices.Contains(p, boostPlugin) {
			return errors.New("simulated chain attach failure")
		}
		chain = slices.Clone(p)
		return nil
	}

	err = configureBoost(true, 150, chain, base, read, write, set)
	if err == nil {
		t.Fatal("expected configureBoost to fail on setChain failure")
	}
	if values["input-gain"] != "0.0" {
		t.Fatalf("rollback did not reset input-gain to 0.0 on setChain failure: got %s", values["input-gain"])
	}
}

func TestBoostInstalledSchemaValidation(t *testing.T) {
	params := boostParameters(150)
	// Validate against the installed schema file if present on host
	if err := validateInstalledLimiterSchema(installedLimiterSchemaPath, params); err != nil {
		t.Fatalf("boost parameters failed against installed schema: %v", err)
	}

	badParams := boostParameters(150)
	badParams["release"] = "50.0"
	if err := validateInstalledLimiterSchema(installedLimiterSchemaPath, badParams); err == nil {
		t.Fatal("installed schema validation must reject release=50.0")
	}
}

func TestBoostRollbackNeverRestoresPositiveGainAndReportsCleanupFailure(t *testing.T) {
	old := boostParameters(150)
	values := map[string]string{}
	for k, v := range old {
		values[k] = v
	}
	values["release"] = "5.0"
	failure := errors.New("partial write")
	cleanupFailure := errors.New("cleanup failed")
	calls := 0
	err := configureBoost(true, 140, []string{"equalizer#0", boostPlugin}, []string{"equalizer#0"}, func() (map[string]string, error) { return values, nil }, func(k, v string) error {
		calls++
		values[k] = v
		if k == "release" {
			return failure
		}
		return nil
	}, func([]string) error { return nil }, func(modified map[string]string, base []string) error {
		return rollbackBoostSettings(modified, base, func(k, v string) error {
			values[k] = v
			if k == "release" {
				return cleanupFailure
			}
			return nil
		}, func([]string) error { return nil })
	})
	if !errors.Is(err, failure) || !errors.Is(err, cleanupFailure) || values["input-gain"] != "0.0" || calls == 0 {
		t.Fatal("unsafe/incomplete rollback", err, values)
	}
}
