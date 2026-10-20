package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/adapters/easyeffects"
)

type countedEffects struct {
	*easyeffects.FakeEasyEffects
	devices, presets int
}

func (f *countedEffects) ReadDevices(ctx context.Context) (adapters.DSPDevices, error) {
	f.devices++
	return f.FakeEasyEffects.ReadDevices(ctx)
}
func (f *countedEffects) GetActivePreset(ctx context.Context) (string, error) {
	f.presets++
	return f.FakeEasyEffects.GetActivePreset(ctx)
}

func TestHealthyReconcileInspectsOnceWithoutHardwareWrites(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	counted := &countedEffects{FakeEasyEffects: ee}
	d.ee = counted
	before := pw.ObservationReads
	for range 3 {
		if err := d.Reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	if pw.ObservationReads-before != 3 || counted.devices != 3 || counted.presets != 3 {
		t.Fatalf("repeated reads: graph=%d devices=%d presets=%d", pw.ObservationReads-before, counted.devices, counted.presets)
	}
	if len(pw.Calls) != 0 || len(ee.Calls) != 0 {
		t.Fatalf("healthy reconciliation changed hardware: %v %v", pw.Calls, ee.Calls)
	}
}

func TestReconcileStillReadsAfterRepairAndPreservesMicMute(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	pw.MonitorChannelVolumes[VirtualSinkNode] = false
	before := pw.ObservationReads
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if pw.ObservationReads-before < 2 || !statusOf(t, d).MonitorVolumesConfirmed || !pw.NodeMutes[PhysicalMicPrefix] || !pw.NodeMutes[VirtualSourceNode] {
		t.Fatal("repair lacked fresh confirmation or unmuted mic")
	}
	ee.ActivePreset = "AKG K52 - Rock e Metal"
	before = pw.ObservationReads
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if pw.ObservationReads-before < 2 || ee.ActivePreset != d.ctrl.ActiveOutputPreset() {
		t.Fatal("autoload override was not repaired and confirmed")
	}
}

func TestHealthyReconcileDoesNotHidePresetReadFailure(t *testing.T) {
	d, _, _, ee := setupTestDaemon(t)
	ee.GetActiveErr = errors.New("unreadable plugin chain")
	if err := d.Reconcile(); err == nil || statusOf(t, d).ProtectionState == "confirmed" {
		t.Fatal("missing preset readback falsely confirmed")
	}
}
