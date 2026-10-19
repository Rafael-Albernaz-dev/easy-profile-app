package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters/alsa"
	"github.com/toru/easy-profile-app/internal/adapters/easyeffects"
	"github.com/toru/easy-profile-app/internal/adapters/pipewire"
	"github.com/toru/easy-profile-app/internal/domain"
)

func statusOf(t *testing.T, d *Daemon) DaemonStatus {
	t.Helper()
	raw, err := d.GetStatus()
	if err != nil {
		t.Fatal(err)
	}
	var status DaemonStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatal(err)
	}
	return status
}
func TestPanicRestoresIndependentObservedMicrophoneMutes(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	pw.NodeMutes[VirtualSourceNode] = false
	pw.NodeMutes[PhysicalMicPrefix] = true
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("panic collapsed divergent microphone snapshot")
	}
	if err := d.ResetDefaults(); err != nil {
		t.Fatal(err)
	}
	if pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("reset changed independent microphone mutes")
	}
}
func TestPanicManualOverrideAndVolumeScale(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.SetVolumeAbsolute(50); err != nil {
		t.Fatal(err)
	}
	if math.Abs(pw.NodeVolumes[VirtualSinkNode]-0.125) > 1e-9 {
		t.Fatalf("port must receive linear .125 for 50%%: %g", pw.NodeVolumes[VirtualSinkNode])
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	if pw.NodeMutes[VirtualSourceNode] || pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("manual override lost")
	}
}
func TestFailedVolumeDoesNotCommitAndFailedRouteStaysMuted(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	pw.SetVolumeErr = errors.New("volume failed")
	if _, err := d.SetVolumeAbsolute(30); err == nil {
		t.Fatal("missing volume error")
	}
	if got := statusOf(t, d); got.OutputVolume != 95 || got.LastError == "" {
		t.Fatalf("false success: %+v", got)
	}
	pw.SetVolumeErr = nil
	pw.SetPortErr = errors.New("route failed")
	if err := d.SelectDestination("sp166"); err == nil {
		t.Fatal("missing route error")
	}
	if !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("failed route unmuted hardware")
	}
}
func TestPresetFilteringTracksDestination(t *testing.T) {
	d, _, _, _ := setupTestDaemon(t)
	if err := d.SelectDestination("sp166"); err != nil {
		t.Fatal(err)
	}
	for _, p := range statusOf(t, d).AvailablePresets {
		if strings.HasPrefix(p, "AKG") {
			t.Fatalf("stale preset %s on speakers", p)
		}
	}
}

// blockedRoute ensures preemption is observable without sleeps or hardware processes.
type blockedRoute struct {
	*pipewire.FakePipeWire
	entered chan struct{}
	once    sync.Once
}

func (p *blockedRoute) SetSinkPort(ctx context.Context, sink, port string) error {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return ctx.Err()
}
func TestPanicCancelsRouteBeforeQueuedVolume(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	blocking := &blockedRoute{FakePipeWire: pw, entered: make(chan struct{})}
	d.pw = blocking
	routeDone := make(chan struct{})
	go func() { defer close(routeDone); _ = d.SelectDestination("sp166") }()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("route never started")
	}
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	<-routeDone
	if !pw.NodeMutes[PhysicalSinkNode] || !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("panic did not protect interrupted route")
	}
}
func TestObserveOnlyRefusesEveryControl(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.SetObserveOnly(true)
	initial := len(pw.Calls)
	if _, e := d.SetVolume(2); e == nil {
		t.Fatal("volume allowed")
	}
	if _, e := d.SetMicVolume(-2); e == nil {
		t.Fatal("mic allowed")
	}
	if _, e := d.ToggleOutputMute(); e == nil {
		t.Fatal("output toggle allowed")
	}
	if _, e := d.ToggleMicMute(); e == nil {
		t.Fatal("mic toggle allowed")
	}
	if _, e := d.TogglePanic(); e == nil {
		t.Fatal("panic allowed")
	}
	if e := d.SelectDestination("sp166"); e == nil {
		t.Fatal("route allowed")
	}
	if e := d.SelectPreset(domain.DefaultAKGDestination().BasePreset); e == nil {
		t.Fatal("preset allowed")
	}
	if e := d.ResetDefaults(); e == nil {
		t.Fatal("reset allowed")
	}
	if e := d.MigrateRawMicStream("Recorder"); e == nil {
		t.Fatal("migration allowed in observe-only")
	}
	if len(pw.Calls) != initial {
		t.Fatal("observe-only mutated fake hardware")
	}
}

func TestPanicAcceptedWhenNormalQueueFull(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	blocking := &blockedRoute{FakePipeWire: pw, entered: make(chan struct{})}
	d.pw = blocking
	routeDone := make(chan struct{})
	go func() { defer close(routeDone); _ = d.SelectDestination("sp166") }()
	<-blocking.entered
	d.qmu.Lock()
	for range 128 {
		d.queue = append(d.queue, &request{created: time.Now(), done: make(chan error, 1), fn: func(context.Context) error {
			if !d.ctrl.PanicState().Active {
				return errors.New("normal command overtook panic")
			}
			return nil
		}})
	}
	pending := append([]*request(nil), d.queue...)
	d.qmu.Unlock()
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	<-routeDone
	for _, r := range pending {
		if err := <-r.done; err != nil {
			t.Fatal(err)
		}
	}
}

type failingMic struct {
	*pipewire.FakePipeWire
	readFails bool
	muteFails bool
}

func (f *failingMic) GetNodeMute(ctx context.Context, node string) (bool, error) {
	if f.readFails && node == PhysicalMicPrefix {
		return false, errors.New("mic read failed")
	}
	return f.FakePipeWire.GetNodeMute(ctx, node)
}
func (f *failingMic) SetNodeMute(ctx context.Context, node string, value bool) error {
	if f.muteFails && node == PhysicalMicPrefix {
		return errors.New("physical mute failed")
	}
	return f.FakePipeWire.SetNodeMute(ctx, node, value)
}
func TestPanicAttemptsAllTargetsWithUnreadableSnapshot(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.pw = &failingMic{FakePipeWire: pw, readFails: true}
	if _, err := d.TogglePanic(); err == nil {
		t.Fatal("unreadable snapshot reported success")
	}
	if !pw.NodeMutes[PhysicalSinkNode] || !pw.NodeMutes[PhysicalMicPrefix] || !pw.NodeMutes[VirtualSourceNode] {
		t.Fatal("panic skipped targets after read error")
	}
	if _, err := d.TogglePanic(); err == nil {
		t.Fatal("unknown snapshot allowed restore")
	}
	if !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("unknown state was unmuted")
	}
}
func TestPartialMuteRemainsDegradedAfterObservation(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.pw = &failingMic{FakePipeWire: pw, muteFails: true}
	if _, err := d.ToggleMicMute(); err == nil {
		t.Fatal("partial mute reported success")
	}
	_ = d.Observe()
	status := statusOf(t, d)
	if status.MicPrivacyMuted || status.LastError == "" || status.ProtectionState == "confirmed" {
		t.Fatalf("false protection: %+v", status)
	}
}
func TestVolumeRequestsCoalesceWithoutLosingSteps(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	entered, release := make(chan struct{}), make(chan struct{})
	blocker := make(chan struct{})
	go func() {
		defer close(blocker)
		_ = d.execute(false, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	d.qmu.Lock()
	requests := []*request{}
	for range 10 {
		r := &request{level: &levelRequest{value: -2}, created: time.Now(), done: make(chan error, 1)}
		requests = append(requests, r)
		d.queue = append(d.queue, r)
	}
	d.qmu.Unlock()
	close(release)
	<-blocker
	for _, r := range requests {
		if err := <-r.done; err != nil {
			t.Fatal(err)
		}
	}
	if got := statusOf(t, d).OutputVolume; got != 75 {
		t.Fatalf("lost adjustments: %d", got)
	}
	count := 0
	for _, call := range pw.Calls {
		if call == "SetNodeVolume:"+VirtualSinkNode {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one hardware write for burst; got %d", count)
	}
}

type monitorOrder struct {
	*pipewire.FakePipeWire
	violation error
}

func (p *monitorOrder) EnsureMonitorChannelVolumes(ctx context.Context, node string) error {
	if !p.NodeMutes[PhysicalSinkNode] {
		p.violation = errors.New("monitor gain enabled before physical silence")
	}
	if level := p.NodeVolumes[node]; level < 0 || level > 1 {
		p.violation = errors.New("monitor enabled with unclamped virtual gain")
	}
	return p.FakePipeWire.EnsureMonitorChannelVolumes(ctx, node)
}
func TestInitializationProtectsHardwareBeforeEnablingVirtualGain(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	pw.NodeVolumes[VirtualSinkNode] = 1.05 * 1.05 * 1.05
	checked := &monitorOrder{FakePipeWire: pw}
	d.pw = checked
	if err := d.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	if checked.violation != nil {
		t.Fatal(checked.violation)
	}
	if pw.NodeVolumes[VirtualSinkNode] > 1 {
		t.Fatal("startup retained >100% virtual gain")
	}
}

func TestLoginPolicyVsDaemonRestartMutes(t *testing.T) {
	ctrl := domain.NewAudioController(nil)
	fakePW := pipewire.NewFakePipeWire()
	fakeAlsa := alsa.NewFakeAlsa()
	fakeEE := easyeffects.NewFakeEasyEffects("AKG K52 - Uso diario aprovado")

	// Hardware initially observed with mic muted and output muted
	fakePW.NodeMutes[VirtualSourceNode] = true
	fakePW.NodeMutes[PhysicalMicPrefix] = true
	fakePW.NodeMutes[VirtualSinkNode] = true

	st := NewMemorySessionTracker("session-login-1")
	d := NewDaemon(ctrl, fakePW, fakeAlsa, fakeEE)
	d.SetSessionTracker(st)
	d.SetStabilization(0)
	t.Cleanup(d.Close)

	ctx := context.Background()

	// 1. First run on fresh login: login policy MUST apply (unmute output & mic, 95% out, 100% mic)
	if err := d.InitHardware(ctx); err != nil {
		t.Fatalf("InitHardware failed on login: %v", err)
	}

	status := statusOf(t, d)
	if status.OutputMuted {
		t.Errorf("expected output unmuted on login")
	}
	if status.MicVirtualMuted || status.MicPhysicalMuted {
		t.Errorf("expected mic unmuted on login, got virt=%v phys=%v", status.MicVirtualMuted, status.MicPhysicalMuted)
	}
	if status.OutputVolume != 95 {
		t.Errorf("expected output volume 95%% on login, got %d", status.OutputVolume)
	}
	if status.MicLevel != 100 {
		t.Errorf("expected mic level 100%% on login, got %d", status.MicLevel)
	}

	// 2. User mutes mic for privacy
	if _, err := d.ToggleMicMute(); err != nil {
		t.Fatalf("ToggleMicMute failed: %v", err)
	}
	if !fakePW.NodeMutes[VirtualSourceNode] || !fakePW.NodeMutes[PhysicalMicPrefix] {
		t.Fatalf("expected hardware mic to be muted by user")
	}

	// 3. Daemon restarts within the SAME session (e.g. systemctl restart easyprofiled)
	ctrl2 := domain.NewAudioController(nil)
	d2 := NewDaemon(ctrl2, fakePW, fakeAlsa, fakeEE)
	d2.SetSessionTracker(st) // same session tracker, where login policy was already done!
	d2.SetStabilization(0)
	t.Cleanup(d2.Close)

	if err := d2.InitHardware(ctx); err != nil {
		t.Fatalf("InitHardware failed on restart: %v", err)
	}

	// It MUST preserve the user's observed privacy mutes, NOT force unmuting!
	status2 := statusOf(t, d2)
	if !status2.MicVirtualMuted || !status2.MicPhysicalMuted {
		t.Errorf("daemon restart within same session stomped user mic mute! virt=%v phys=%v",
			status2.MicVirtualMuted, status2.MicPhysicalMuted)
	}

	// 4. User logs out and logs in anew (new session ID)
	st.SetSession("session-login-2")
	ctrl3 := domain.NewAudioController(nil)
	d3 := NewDaemon(ctrl3, fakePW, fakeAlsa, fakeEE)
	d3.SetSessionTracker(st)
	d3.SetStabilization(0)
	t.Cleanup(d3.Close)

	if err := d3.InitHardware(ctx); err != nil {
		t.Fatalf("InitHardware failed on new login: %v", err)
	}

	// On NEW login, login policy runs and restores unmuted base daily state
	status3 := statusOf(t, d3)
	if status3.OutputMuted || status3.MicVirtualMuted || status3.MicPhysicalMuted {
		t.Errorf("new login failed to apply unmuted base state: outMuted=%v micVirt=%v micPhys=%v",
			status3.OutputMuted, status3.MicVirtualMuted, status3.MicPhysicalMuted)
	}
}
