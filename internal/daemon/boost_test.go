package daemon

import (
	"errors"
	"testing"
)

func TestBoostUnlockPresetRoutesResetAndMicrophoneSafety(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	if _, err := d.SetVolumeAbsolute(100); err != nil {
		t.Fatal(err)
	}
	if active, err := d.ToggleOutputBoost(); err != nil || !active {
		t.Fatal(active, err)
	}
	if s := statusOf(t, d); s.OutputVolume != 100 || s.OutputMaxPercent != 150 || !s.OutputBoost {
		t.Fatal("unlock changed level", s)
	}
	if n, err := d.SetVolumeAbsolute(150); err != nil || n != 150 {
		t.Fatal(n, err)
	}
	if pw.NodeVolumes[VirtualSinkNode] != 1 || ee.BoostPercent != 150 {
		t.Fatal("gain misplaced", pw.NodeVolumes, ee.BoostPercent)
	}
	if n, err := d.SetMicVolumeAbsolute(150); err != nil || n != 100 {
		t.Fatal("mic exceeded ceiling", n, err)
	}
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	if err := d.SelectPreset("AKG K52 - Rock e Metal"); err != nil {
		t.Fatal(err)
	}
	if !ee.BoostEnabled || statusOf(t, d).OutputVolume != 150 {
		t.Fatal("preset cleared Boost")
	}
	if err := d.SelectDestination("sp166"); err != nil {
		t.Fatal(err)
	}
	if ee.BoostPercent != 95 || statusOf(t, d).OutputVolume != 95 {
		t.Fatal("route inherited extra gain", ee.BoostPercent, statusOf(t, d))
	}
	if err := d.SelectDestination("akg"); err != nil {
		t.Fatal(err)
	}
	if ee.BoostPercent != 150 || statusOf(t, d).OutputVolume != 150 {
		t.Fatal("route lost Boost level")
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("panic lost mic mute")
	}
	if _, err := d.SetOutputBoost(false); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(t, d); s.OutputVolume != 100 || s.OutputBoost || ee.BoostEnabled {
		t.Fatal("disable failed", s)
	}
	if _, err := d.SetOutputBoost(true); err != nil {
		t.Fatal(err)
	}
	if err := d.ResetDefaults(); err != nil {
		t.Fatal(err)
	}
	if s := statusOf(t, d); s.OutputBoost || s.OutputVolume != 95 || !s.MicPrivacyMuted {
		t.Fatal("reset violated contract", s)
	}
	for _, value := range pw.NodeVolumes {
		if value > 1 {
			t.Fatal("physical/virtual gain exceeded normal cap")
		}
	}
}

func TestBoostFailureLeavesOutputMutedAndObserveModeCannotMutate(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	ee.BoostErr = errors.New("limiter unavailable")
	if _, err := d.SetOutputBoost(true); err == nil || d.ctrl.OutputBoost() || !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("unprotected boost accepted")
	}
	ee.BoostErr = nil
	d.SetObserveOnly(true)
	before := len(pw.Calls)
	if _, err := d.ToggleOutputBoost(); err == nil || len(pw.Calls) != before {
		t.Fatal("observe mode mutated boost")
	}
}

func TestBoostLevelFailureMutesOutputButObserveOnlyNeverWrites(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.SetOutputBoost(true); err != nil {
		t.Fatal(err)
	}
	pw.SetVolumeErr = errors.New("volume readback failed after extra gain")
	if _, err := d.SetVolumeAbsolute(90); err == nil || !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("gain failure did not mute physical output")
	}
	d.SetObserveOnly(true)
	before := len(pw.Calls)
	if _, err := d.SetVolumeAbsolute(150); err == nil || len(pw.Calls) != before {
		t.Fatal("observe-only fallback wrote hardware")
	}
}

func TestBoostStatusCannotConfirmSettingsWithoutNativeDSP(t *testing.T) {
	d, _, _, ee := setupTestDaemon(t)
	if _, err := d.SetOutputBoost(true); err != nil {
		t.Fatal(err)
	}
	ee.ValidateDSPErr = errors.New("native limiter disconnected")
	if err := d.Observe(); err == nil {
		t.Fatal("native DSP loss accepted")
	}
	s := statusOf(t, d)
	if s.OutputBoostConfirmed || s.DSPBound || s.LastError == "" {
		t.Fatal("settings incorrectly confirmed DSP", s)
	}
}
