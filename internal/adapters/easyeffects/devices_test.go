package easyeffects

import (
	"context"
	"github.com/toru/easy-profile-app/internal/adapters"
	"testing"
)

func TestDeviceRepairPlanCoversBothGuardsWithoutRedundantWrites(t *testing.T) {
	observed := adapters.DSPDevices{Input: "old-mic.3", Output: "wrong-card", UseDefaultInput: true, UseDefaultOutput: true}
	changes := deviceChanges(observed, "HDA-Intel", "KA-1333.4")
	if len(changes) != 4 {
		t.Fatalf("incomplete guard replacement: %+v", changes)
	}
	keys := map[string]bool{}
	for _, change := range changes {
		keys[change.key] = true
	}
	for _, key := range []string{"input-device", "output-device", "use-default-input-device", "use-default-output-device"} {
		if !keys[key] {
			t.Fatal("missing", key)
		}
	}
	if len(deviceChanges(adapters.DSPDevices{Input: "KA-1333.4", Output: "HDA-Intel"}, "HDA-Intel", "KA-1333.4")) != 0 {
		t.Fatal("correct graph would feed watcher loop")
	}
	real := NewRealEasyEffects(t.TempDir(), 0)
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	t.Setenv("PATH", t.TempDir())
	if _, err := real.ReadDevices(context.Background()); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
	if err := real.EnsureDevices(context.Background(), "out", "in"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
}
