package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/toru/easy-profile-app/internal/adapters/alsa"
	"github.com/toru/easy-profile-app/internal/adapters/easyeffects"
	"github.com/toru/easy-profile-app/internal/adapters/pipewire"
	"github.com/toru/easy-profile-app/internal/domain"
)

func setupTestDaemon(t *testing.T) (*Daemon, *pipewire.FakePipeWire, *alsa.FakeAlsa, *easyeffects.FakeEasyEffects) {
	t.Helper()
	ctrl := domain.NewAudioController(nil)
	fakePW := pipewire.NewFakePipeWire()
	fakeAlsa := alsa.NewFakeAlsa()
	fakeEE := easyeffects.NewFakeEasyEffects(
		"AKG K52 - Uso diario aprovado",
		"AKG K52 - Rock e Metal",
		"SP166 - Uso diario aprovado",
	)

	d := NewDaemon(ctrl, fakePW, fakeAlsa, fakeEE)
	t.Cleanup(d.Close)
	if err := d.Observe(); err != nil {
		t.Fatal(err)
	}
	return d, fakePW, fakeAlsa, fakeEE
}

func TestDaemon_InitHardware(t *testing.T) {
	d, fakePW, fakeAlsa, fakeEE := setupTestDaemon(t)
	ctx := context.Background()

	if err := d.InitHardware(ctx); err != nil {
		t.Fatalf("InitHardware failed: %v", err)
	}

	if !fakeAlsa.AutoMuteDisabled[AlsaCard] {
		t.Errorf("expected Auto-Mute disabled")
	}
	if !fakePW.MonitorChannelVolumes[VirtualSinkNode] {
		t.Errorf("expected monitor.channel-volumes = true")
	}
	if fakeAlsa.ControlVolumes["PCH:Headphone"] != 100 {
		t.Errorf("expected headphone unmuted at 100%%, got %d", fakeAlsa.ControlVolumes["PCH:Headphone"])
	}
	if !fakeAlsa.ControlMutes["PCH:Front"] {
		t.Errorf("expected front muted")
	}
	if fakeEE.ActivePreset != "AKG K52 - Uso diario aprovado" {
		t.Errorf("expected base preset loaded, got %s", fakeEE.ActivePreset)
	}
}

func TestDaemon_GetStatus(t *testing.T) {
	d, _, _, _ := setupTestDaemon(t)
	raw, err := d.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	var status DaemonStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatalf("JSON parse failed: %v", err)
	}

	if status.ActiveDestination.ID != "akg" {
		t.Errorf("expected akg destination, got %s", status.ActiveDestination.ID)
	}
	if status.OutputVolume != 95 {
		t.Errorf("expected volume 95, got %d", status.OutputVolume)
	}
	if status.OutputMuted {
		t.Errorf("expected output unmuted")
	}
	if status.MicLevel != 100 {
		t.Errorf("expected mic 100, got %d", status.MicLevel)
	}
	if len(status.AvailablePresets) != 2 {
		t.Errorf("expected 2 AKG compatible presets, got %d", len(status.AvailablePresets))
	}
}

func TestDaemon_VolumeAndMuteControls(t *testing.T) {
	d, fakePW, _, _ := setupTestDaemon(t)

	// Volume step +2%
	newVol, err := d.SetVolume(2)
	if err != nil || newVol != 97 {
		t.Fatalf("SetVolume failed: %v, newVol=%d", err, newVol)
	}
	if fakePW.NodeVolumes[VirtualSinkNode] <= 0 {
		t.Errorf("PipeWire virtual sink volume not updated")
	}

	// Output Mute toggle
	muted, err := d.ToggleOutputMute()
	if err != nil || !muted {
		t.Fatalf("ToggleOutputMute failed: %v, muted=%t", err, muted)
	}
	if !fakePW.NodeMutes[VirtualSinkNode] {
		t.Errorf("PipeWire virtual sink mute not updated")
	}

	// Dual-Mute toggle on Mic
	micMuted, err := d.ToggleMicMute()
	if err != nil || !micMuted {
		t.Fatalf("ToggleMicMute failed: %v, micMuted=%t", err, micMuted)
	}
	if !fakePW.NodeMutes[VirtualSourceNode] || !fakePW.NodeMutes[PhysicalMicPrefix] {
		t.Errorf("PipeWire Dual-Mute failed to update both virtual and physical sources")
	}
}

func TestDaemon_PanicMuteAndReset(t *testing.T) {
	d, fakePW, _, fakeEE := setupTestDaemon(t)

	// Enter panic
	panicActive, err := d.TogglePanic()
	if err != nil || !panicActive {
		t.Fatalf("TogglePanic failed: %v", err)
	}
	if !fakePW.NodeMutes[VirtualSinkNode] || !fakePW.NodeMutes[VirtualSourceNode] || !fakePW.NodeMutes[PhysicalMicPrefix] {
		t.Errorf("Panic did not mute all targets")
	}

	// Reset base
	if err := d.ResetDefaults(); err != nil {
		t.Fatalf("ResetDefaults failed: %v", err)
	}
	if fakeEE.ActivePreset != "AKG K52 - Uso diario aprovado" {
		t.Errorf("expected daily base preset after reset, got %s", fakeEE.ActivePreset)
	}
}

func TestDaemon_SwitchDestination(t *testing.T) {
	d, fakePW, fakeAlsa, fakeEE := setupTestDaemon(t)

	// Switch to SP166
	if err := d.SelectDestination("SP166"); err != nil {
		t.Fatalf("SelectDestination SP166 failed: %v", err)
	}

	if fakeAlsa.ControlVolumes["PCH:Front"] != 100 {
		t.Errorf("expected SP166 at 100%% ceiling, got %d", fakeAlsa.ControlVolumes["PCH:Front"])
	}
	if !fakeAlsa.ControlMutes["PCH:Headphone"] {
		t.Errorf("expected headphone muted on ALSA")
	}
	if fakePW.SinkPorts[PhysicalSinkNode] != "analog-output-lineout" {
		t.Errorf("expected analog-output-lineout sink port")
	}
	if fakeEE.ActivePreset != "SP166 - Uso diario aprovado" {
		t.Errorf("expected SP166 preset, got %s", fakeEE.ActivePreset)
	}
}

func TestDaemon_MicMonitoring(t *testing.T) {
	d, fakePW, _, _ := setupTestDaemon(t)

	// Initially disabled
	if d.ctrl.MicMonitoring() || fakePW.MicMonitoring {
		t.Errorf("expected mic monitoring to be disabled initially")
	}

	// Toggle on
	active, dbusErr := d.ToggleMicMonitoring()
	if dbusErr != nil || !active {
		t.Fatalf("ToggleMicMonitoring failed: %v", dbusErr)
	}
	if !fakePW.MicMonitoring || !d.ctrl.MicMonitoring() {
		t.Errorf("expected mic monitoring to be enabled on fake and controller")
	}

	// Toggle off
	active, dbusErr = d.ToggleMicMonitoring()
	if dbusErr != nil || active {
		t.Fatalf("ToggleMicMonitoring (off) failed: %v", dbusErr)
	}
	if fakePW.MicMonitoring || d.ctrl.MicMonitoring() {
		t.Errorf("expected mic monitoring to be disabled after second toggle")
	}
}

func TestDaemon_PanicRestoreOnDaemonRestart(t *testing.T) {
	d, _, _, _ := setupTestDaemon(t)
	// Enter panic
	active, err := d.TogglePanic()
	if err != nil || !active {
		t.Fatalf("failed to enter panic: %v", err)
	}

	// Create new daemon with the same controller (simulating daemon restart with panic active)
	d2 := NewDaemon(d.ctrl, d.pw, d.alsa, d.ee)
	// Toggle panic to exit panic
	active, err = d2.TogglePanic()
	if err != nil {
		t.Fatalf("failed to exit panic on restarted daemon: %v", err)
	}
	if active {
		t.Errorf("expected panic to be inactive after exit toggle")
	}
}
