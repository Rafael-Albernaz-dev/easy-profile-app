package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/toru/easy-profile-app/internal/domain"
)

func TestStatusReadableDuringBlockedHardware(t *testing.T) {
	d, _, _, _ := setupTestDaemon(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_ = d.execute(false, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	read := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		for range 100 {
			_, _ = d.GetStatus()
		}
		read <- time.Since(start) / 100
	}()
	var elapsed time.Duration
	var blocked bool
	select {
	case elapsed = <-read:
	case <-time.After(5 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-finished
	if blocked {
		<-read
		t.Fatal("status blocked behind hardware worker (>5ms)")
	}
	t.Logf("GetStatus during blocked hardware: %s/op", elapsed)
	if elapsed >= 5*time.Millisecond {
		t.Fatal("status read budget exceeded")
	}
}

func TestSameSessionRestartRestoresPerPortVolumesAndPreset(t *testing.T) {
	cfg := DefaultConfig()
	store := domain.NewSessionStore(filepath.Join(t.TempDir(), "session.json"))
	ctrl, err := cfg.ControllerWithSession(store, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	d, pw, al, ee := setupTestDaemon(t)
	d.ctrl = ctrl
	tracker := NewMemorySessionTracker("session-1")
	d.SetSessionTracker(tracker)
	if err := d.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetVolumeAbsolute(20); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetMicVolumeAbsolute(30); err != nil {
		t.Fatal(err)
	}
	if err := d.SelectPreset("AKG K52 - Rock e Metal"); err != nil {
		t.Fatal(err)
	}
	if err := d.SelectDestination("sp166"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetVolumeAbsolute(40); err != nil {
		t.Fatal(err)
	}
	if err := d.SelectDestination("akg"); err != nil {
		t.Fatal(err)
	}
	restored, err := cfg.ControllerWithSession(store, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDaemon(restored, pw, al, ee)
	t.Cleanup(restarted.Close)
	restarted.SetSessionTracker(tracker)
	if err := restarted.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := statusOf(t, restarted)
	if s.OutputVolume != 20 || s.MicLevel != 30 || s.ActivePreset != "AKG K52 - Rock e Metal" {
		t.Fatalf("lost session: %+v", s)
	}
	if err := restarted.SelectDestination("sp166"); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, restarted).OutputVolume; got != 40 {
		t.Fatalf("lost other port: %d", got)
	}
	fresh, err := cfg.ControllerWithSession(store, "session-2")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.OutputVirtualVolume().Percent() != 95 {
		t.Fatal("previous login volume leaked into new session")
	}
}

type failedLoginCommit struct{}

func (failedLoginCommit) ShouldApplyLoginPolicy() (bool, error) { return true, nil }
func (failedLoginCommit) MarkLoginPolicyApplied() error         { return errors.New("marker write denied") }

func TestInvalidLoginMarkerAndFailedCommitNeverUnmute(t *testing.T) {
	for _, content := range []string{"{", "{}", `{"session_id":"session-1","policy_done":false}`} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "login_session.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			tracker := NewFileSessionTracker(path, "session-1")
			if should, err := tracker.ShouldApplyLoginPolicy(); should || err == nil {
				t.Fatal("invalid marker authorized login")
			}
			d, pw, _, _ := setupTestDaemon(t)
			pw.NodeMutes[VirtualSourceNode] = true
			pw.NodeMutes[PhysicalMicPrefix] = true
			d.SetSessionTracker(tracker)
			if err := d.InitHardware(context.Background()); err == nil {
				t.Fatal("invalid marker ignored")
			}
			if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
				t.Fatal("microphone was unmuted")
			}
			if len(pw.Calls) != 0 {
				t.Fatal("invalid marker allowed hardware writes")
			}
		})
	}
	d, pw, _, _ := setupTestDaemon(t)
	pw.NodeMutes[VirtualSourceNode] = true
	pw.NodeMutes[PhysicalMicPrefix] = true
	d.SetSessionTracker(failedLoginCommit{})
	if err := d.InitHardware(context.Background()); err == nil {
		t.Fatal("failed marker commit ignored")
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] || len(pw.Calls) != 0 {
		t.Fatal("failed commit allowed hardware changes")
	}
}

func TestReconcileRestoresGraphAndMonitorGainWithoutUnmutingMic(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	if err := d.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	pw.MonitorChannelVolumes[VirtualSinkNode] = false
	pw.MonitorChannelVolumes[VirtualSourceNode] = false
	pw.DefaultSink = PhysicalSinkNode
	pw.ConfiguredSink = PhysicalSinkNode
	pw.DefaultSource = pw.PhysicalMicrophone
	pw.ConfiguredSource = pw.PhysicalMicrophone
	pw.PhysicalMicrophone = "alsa_input.usb-MU900_KA-1333_20190805V001-00.iec958-stereo.4"
	ee.Devices.Input = "stale-device"
	ee.Devices.Output = "wrong-output"
	ee.Devices.UseDefaultInput = true
	ee.Devices.UseDefaultOutput = true
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	s := statusOf(t, d)
	if !s.DSPBound || !s.MonitorVolumesConfirmed || s.ProtectionState != "confirmed" {
		t.Fatalf("false protection: %+v", s)
	}
	if !pw.MonitorChannelVolumes[VirtualSinkNode] || !pw.MonitorChannelVolumes[VirtualSourceNode] || pw.DefaultSink != VirtualSinkNode || pw.DefaultSource != VirtualSourceNode || pw.ConfiguredSource != VirtualSourceNode || ee.Devices.Input != pw.PhysicalMicrophone || ee.Devices.Output != PhysicalSinkNode || ee.Devices.UseDefaultInput || ee.Devices.UseDefaultOutput {
		t.Fatal("DSP graph not repaired")
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("repair unmuted microphone")
	}
}

func TestFailedGraphRepairStaysDegradedAndPhysicallyMuted(t *testing.T) {
	d, pw, _, ee := setupTestDaemon(t)
	pw.MonitorChannelVolumes[VirtualSinkNode] = false
	ee.EnsureDevicesErr = errors.New("device binding failed")
	if err := d.Reconcile(); err == nil {
		t.Fatal("repair error ignored")
	}
	if !pw.NodeMutes[PhysicalSinkNode] || statusOf(t, d).ProtectionState == "confirmed" {
		t.Fatal("failed repair falsely safe")
	}
}

func TestObserveDoesNotConsumeLoginPolicyOrRepairHardware(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	tracker := NewMemorySessionTracker("session-1")
	pw.MonitorChannelVolumes[VirtualSinkNode] = false
	d.SetSessionTracker(tracker)
	d.SetObserveOnly(true)
	if err := d.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if len(pw.Calls) != 0 || pw.MonitorChannelVolumes[VirtualSinkNode] {
		t.Fatal("observe repaired hardware")
	}
	if should, _ := tracker.ShouldApplyLoginPolicy(); !should {
		t.Fatal("observe consumed login policy")
	}
	if statusOf(t, d).ProtectionState == "confirmed" {
		t.Fatal("observe ignored disabled monitor gain")
	}
}

func TestMissingSessionSnapshotCannotApplyDefaultsOnRestart(t *testing.T) {
	store := domain.NewSessionStore(filepath.Join(t.TempDir(), "session.json"))
	ctrl, err := DefaultConfig().ControllerWithSession(store, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	d, pw, _, _ := setupTestDaemon(t)
	d.ctrl = ctrl
	tracker := NewMemorySessionTracker("session-1")
	_ = tracker.MarkLoginPolicyApplied()
	d.SetSessionTracker(tracker)
	if err := d.InitHardware(context.Background()); err == nil {
		t.Fatal("missing snapshot allowed defaults")
	}
	if len(pw.Calls) != 0 {
		t.Fatal("missing snapshot allowed hardware writes")
	}
	if _, err := os.Stat(store.FilePath()); !os.IsNotExist(err) {
		t.Fatal("failure fabricated a valid snapshot")
	}
}

func TestProcessLockExcludesSecondDaemonBeforeHardware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "easyprofile.lock")
	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireProcessLock(path)
	if err == nil {
		_ = second.Close()
		t.Fatal("second daemon acquired lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Close()
}

func TestDelayedDestinationVolumeCannotChangeAnotherRoute(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if err := d.SelectDestination("sp166"); err != nil {
		t.Fatal(err)
	}
	before := pw.NodeVolumes[VirtualSinkNode]
	if _, err := d.SetVolumeForDestination("akg", 52); err == nil {
		t.Fatal("stale volume request accepted")
	}
	if pw.NodeVolumes[VirtualSinkNode] != before {
		t.Fatal("AKG request changed SP166 volume")
	}
	if result, err := d.SetVolumeForDestination("sp166", 21); err != nil || result != 21 {
		t.Fatal(result, err)
	}
}

func TestMissingLoginMarkerWithExistingSessionNeverAuthorizesUnmute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	ctrl, err := DefaultConfig().ControllerWithSession(domain.NewSessionStore(path), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetOutputVolume(domain.NewPercentageLevel(20))
	restored, err := DefaultConfig().ControllerWithSession(domain.NewSessionStore(path), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	d, pw, _, _ := setupTestDaemon(t)
	d.ctrl = restored
	pw.NodeMutes[VirtualSourceNode] = true
	pw.NodeMutes[PhysicalMicPrefix] = true
	d.SetSessionTracker(NewMemorySessionTracker("session-1"))
	if err := d.InitHardware(context.Background()); err == nil {
		t.Fatal("inconsistent marker authorized unmute")
	}
	if len(pw.Calls) != 0 || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("inconsistent marker changed audio")
	}
}

func TestParentCancellationStopsInitialization(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	blocking := &blockedRoute{FakePipeWire: pw, entered: make(chan struct{})}
	d.pw = blocking
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- d.InitHardware(ctx) }()
	<-blocking.entered
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled initialization succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation ignored")
	}
	if !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("cancelled initialization released hardware mute")
	}
}

func TestUnreadableMuteDuringReconcileRequestsFailSafeMute(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.pw = &failingMic{FakePipeWire: pw, readFails: true}
	if err := d.Reconcile(); err == nil {
		t.Fatal("unreadable mute accepted")
	}
	if !pw.NodeMutes[PhysicalSinkNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("unknown mute did not fail safe")
	}
}

func TestCorruptAudioSnapshotAndFailedPersistenceAreReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := domain.NewSessionStore(path)
	for _, bad := range []string{"{", `{"version":1,"session_id":"session-1","active_destination":"akg","mic_level_pct":100}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := DefaultConfig().ControllerWithSession(store, "session-1"); err == nil {
			t.Fatal("invalid audio state accepted")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctrl, err := DefaultConfig().ControllerWithSession(store, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	// Force Rename to fail without permissions assumptions or touching anything outside t.TempDir.
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	d, _, _, _ := setupTestDaemon(t)
	d.ctrl = ctrl
	if _, err := d.SetVolumeAbsolute(20); err == nil {
		t.Fatal("state write failure reported success")
	}
	s := statusOf(t, d)
	if s.LastError == "" || s.ProtectionState == "confirmed" {
		t.Fatal("persistence error missing from status")
	}
	if err := d.Observe(); err == nil {
		t.Fatal("observation erased unresolved persistence error")
	}
}
