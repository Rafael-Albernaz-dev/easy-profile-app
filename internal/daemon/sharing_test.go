package daemon

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestOutputSharingToggleAndIndependentMicMutes(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	if active, err := d.ToggleOutputSharing(); err != nil || !active {
		t.Fatalf("start: %v", err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("sharing unmuted microphone")
	}
	if active, err := d.ToggleOutputSharing(); err != nil || active {
		t.Fatalf("stop: %v", err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("stop changed microphone mutes")
	}
}

func TestOutputSharingPanicNeverResumesAndKeepsThreeMuteSnapshot(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	pw.NodeMutes[PhysicalMicPrefix] = true
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	if active, err := d.TogglePanic(); err != nil || !active {
		t.Fatal(err)
	}
	if pw.Sharing.Active {
		t.Fatal("panic left sharing alive")
	}
	if _, err := d.SetOutputSharing(true); err == nil {
		t.Fatal("sharing allowed during panic")
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if pw.Sharing.Active || pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("panic restoration lost independent states or resumed sharing")
	}
}

func TestOutputSharingStopFailureDoesNotPreventEmergencyMutes(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	pw.StopSharingErr = errors.New("previous device unavailable")
	if _, err := d.TogglePanic(); err == nil {
		t.Fatal("failure hidden")
	}
	if !pw.NodeMutes[VirtualSinkNode] || !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("restoration blocked emergency mutes")
	}
	raw, _ := d.GetStatus()
	var status DaemonStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatal(err)
	}
	if status.OutputSharing || !status.OutputSharingRestorePending || status.LastError == "" {
		t.Fatalf("failure hidden in status: %s", raw)
	}
	pw.StopSharingErr = nil
}

func TestOutputSharingMuteStopsWithoutUnmutingMic(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	if muted, err := d.ToggleMicMute(); err != nil || !muted {
		t.Fatal(err)
	}
	if pw.Sharing.Active || !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("mute failed to stop transmission")
	}
}

func TestOutputSharingObserveListenAndResetBoundaries(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.SetObserveOnly(true)
	if _, err := d.SetOutputSharing(true); err == nil || pw.Sharing.Active {
		t.Fatal("observe allowed sharing")
	}
	d.SetObserveOnly(false)
	if _, err := d.SetMicMonitoring(true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetOutputSharing(true); err == nil {
		t.Fatal("listen feedback allowed")
	}
	if _, err := d.SetMicMonitoring(false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetMicMonitoring(true); err == nil {
		t.Fatal("listen allowed while sharing")
	}
	if err := d.ResetDefaults(); err != nil || pw.Sharing.Active {
		t.Fatalf("reset: %v", err)
	}
}

func TestOutputSharingShutdownCleansTransport(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if pw.Sharing.Active {
		t.Fatal("shutdown left sharing alive")
	}
}

func TestOutputSharingMuteOverridesSnapshotDuringPendingRestore(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.SetOutputSharing(true); err != nil {
		t.Fatal(err)
	}
	pw.StopSharingErr = errors.New("restore unavailable")
	if _, err := d.TogglePanic(); err == nil {
		t.Fatal("restore failure hidden")
	}
	if _, err := d.ToggleMicMute(); err == nil {
		t.Fatal("pending failure hidden")
	}
	pw.StopSharingErr = nil
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("panic restore ignored explicit mute")
	}
}

func TestOutputSharingFailedReadbackStopsTransmission(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	pw.ReadSharingErr = errors.New("readback unavailable")
	if _, err := d.SetOutputSharing(true); err == nil {
		t.Fatal("readback failure hidden")
	}
	if pw.Sharing.Active {
		t.Fatal("failed activation left transmission alive")
	}
	pw.ReadSharingErr = nil
}
