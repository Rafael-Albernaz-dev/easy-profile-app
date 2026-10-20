package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/domain"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DBusInterface     = "org.easyprofile.Daemon"
	DBusPath          = "/org/easyprofile/Daemon"
	DBusService       = DBusInterface
	VirtualSinkNode   = "easyeffects_sink"
	VirtualSourceNode = "easyeffects_source"
	PhysicalSinkNode  = "alsa_output.pci-0000_00_1f.3.analog-stereo"
	PhysicalMicPrefix = "alsa_input.usb-MU900_KA-1333"
	AlsaCard          = "PCH"
)

// DaemonStatus represents the complete serialized state broadcast to clients.
type DaemonStatus struct {
	DSPBound                    bool            `json:"dsp_bound"`
	MonitorVolumesConfirmed     bool            `json:"monitor_volumes_confirmed"`
	PhysicalMicrophone          string          `json:"physical_microphone"`
	ObservedDestination         string          `json:"observed_destination"`
	ObservedPreset              string          `json:"observed_preset"`
	LastError                   string          `json:"last_error"`
	ProtectionState             string          `json:"protection_state"`
	ObservedOutputMute          *bool           `json:"observed_output_mute"`
	ObservedMicVirtualMute      *bool           `json:"observed_mic_virtual_mute"`
	ObservedMicPhysicalMute     *bool           `json:"observed_mic_physical_mute"`
	ObserveOnly                 bool            `json:"observe_only"`
	ActiveDestination           DestinationInfo `json:"active_destination"`
	OutputVolume                int             `json:"output_volume"`
	OutputMuted                 bool            `json:"output_muted"`
	MicLevel                    int             `json:"mic_level"`
	MicVirtualMuted             bool            `json:"mic_virtual_muted"`
	MicPhysicalMuted            bool            `json:"mic_physical_muted"`
	MicPrivacyMuted             bool            `json:"mic_privacy_muted"`
	PanicActive                 bool            `json:"panic_active"`
	ActivePreset                string          `json:"active_preset"`
	AvailablePresets            []string        `json:"available_presets"`
	RawMicStreams               []string        `json:"raw_mic_streams"`
	MicMonitoring               bool            `json:"mic_monitoring"`
	OutputSharing               bool            `json:"output_sharing"`
	OutputSharingRestorePending bool            `json:"output_sharing_restore_pending"`
}

type DestinationInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	PortName    string `json:"port_name"`
	CeilingPct  int    `json:"ceiling_pct"`
}

type levelRequest struct {
	value         int
	destination   string
	absolute, mic bool
	result        int
}
type request struct {
	level   *levelRequest
	created time.Time
	panic   bool
	fn      func(context.Context) error
	done    chan error
}

// Daemon has one hardware writer. Panic cancels an in-flight command and runs
// ahead of queued normal work. Every interrupted route remains physically muted.
type Daemon struct {
	hardware           Config
	activePanic        bool
	panicSnapshotKnown bool
	presets            []string
	mu                 sync.Mutex
	ctrl               *domain.AudioController
	pw                 adapters.PipeWirePort
	alsa               adapters.AlsaPort
	ee                 adapters.EasyEffectsPort
	conn               *dbus.Conn
	status             DaemonStatus
	statusJSON         atomic.Value // immutable string; GetStatus never takes the hardware lock
	observeOnly        bool
	stabilization      time.Duration
	sessionTracker     SessionTracker
	qmu                sync.Mutex
	wake               *sync.Cond
	queue              []*request
	activeCancel       context.CancelFunc
	stopped            bool
	done               chan struct{}
}

func NewDaemon(ctrl *domain.AudioController, pw adapters.PipeWirePort, alsa adapters.AlsaPort, ee adapters.EasyEffectsPort) *Daemon {
	d := &Daemon{hardware: DefaultConfig(), ctrl: ctrl, pw: pw, alsa: alsa, ee: ee, done: make(chan struct{})}
	if ctrl != nil && ctrl.PanicState().Active {
		d.panicSnapshotKnown = true
	}
	d.wake = sync.NewCond(&d.qmu)
	d.updateStatus(nil)
	go d.worker()
	return d
}
func (d *Daemon) SetObserveOnly(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.observeOnly = v
	d.updateStatus(nil)
}
func (d *Daemon) SetSessionTracker(st SessionTracker) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessionTracker = st
}
func (d *Daemon) SetStabilization(v time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stabilization = v
}
func (d *Daemon) Close() {
	d.qmu.Lock()
	d.stopped = true
	if d.activeCancel != nil {
		d.activeCancel()
	}
	d.wake.Broadcast()
	d.qmu.Unlock()
	<-d.done
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.observeOnly && (d.ctrl.OutputSharing() || d.status.OutputSharingRestorePending) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		err := d.stopSharing(ctx)
		d.updateStatus(err)
	}
}
func (d *Daemon) worker() {
	defer close(d.done)
	for {
		d.qmu.Lock()
		for len(d.queue) == 0 && !d.stopped {
			d.wake.Wait()
		}
		if d.stopped {
			for _, r := range d.queue {
				r.done <- errors.New("daemon closed")
			}
			d.queue = nil
			d.qmu.Unlock()
			return
		}
		i := 0
		for j, r := range d.queue {
			if r.panic {
				i = j
				break
			}
		}
		r := d.queue[i]
		d.queue = append(d.queue[:i], d.queue[i+1:]...)
		group := []*request{r}
		// Only adjacent requests for the same channel can be merged; never cross a toggle/route/panic barrier.
		if r.level != nil && !r.panic {
			for len(d.queue) > 0 {
				next := d.queue[0]
				if next.level == nil || next.panic || next.level.mic != r.level.mic || next.level.destination != r.level.destination {
					break
				}
				group = append(group, next)
				d.queue = d.queue[1:]
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		d.activeCancel = cancel
		d.activePanic = r.panic
		d.qmu.Unlock()
		d.mu.Lock()
		var err error
		if !r.panic && time.Since(r.created) > 18*time.Second {
			err = errors.New("queued command expired")
		} else if r.level != nil {
			current := d.ctrl.OutputVirtualVolume().Percent()
			if r.level.mic {
				current = d.ctrl.Microphone().PostDSPLevel.Percent()
			}
			for _, member := range group {
				level := member.level
				if level.absolute {
					current = domain.NewPercentageLevel(level.value).Percent()
				} else {
					current = domain.NewPercentageLevel(current + level.value).Percent()
				}
			}
			result := 0
			if r.level.destination != "" && r.level.destination != string(d.ctrl.ActiveDestination().ID) {
				err = errors.New("stale volume destination; request discarded")
			} else {
				result, err = d.applyVolume(ctx, current, r.level.mic)
			}
			for _, member := range group {
				member.level.result = result
			}
		} else {
			err = r.fn(ctx)
		}
		err = errors.Join(err, d.ctrl.PersistenceError())
		d.updateStatus(err)
		d.emit()
		d.mu.Unlock()
		cancel()
		d.qmu.Lock()
		d.activeCancel = nil
		d.qmu.Unlock()
		for _, member := range group {
			member.done <- err
		}
	}
}
func (d *Daemon) execute(priority bool, fn func(context.Context) error) *dbus.Error {
	r := &request{panic: priority, fn: fn, done: make(chan error, 1), created: time.Now()}
	return d.submit(r)
}
func (d *Daemon) submit(r *request) *dbus.Error {
	priority := r.panic
	d.qmu.Lock()
	if d.stopped || (!priority && len(d.queue) >= 128) {
		d.qmu.Unlock()
		return dbus.MakeFailedError(errors.New("daemon stopped or command queue full"))
	}
	d.queue = append(d.queue, r)
	if priority && !d.activePanic && d.activeCancel != nil {
		d.activeCancel()
	}
	d.wake.Signal()
	d.qmu.Unlock()
	if err := <-r.done; err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}
func (d *Daemon) SetDBusConn(conn *dbus.Conn) { d.mu.Lock(); defer d.mu.Unlock(); d.conn = conn }
func (d *Daemon) Export(conn *dbus.Conn) error {
	d.SetDBusConn(conn)
	// Export only the public API: lifecycle/configuration methods must never be callable over IPC.
	methods := map[string]interface{}{
		"GetStatus": d.GetStatus, "SetVolume": d.SetVolume, "SetVolumeAbsolute": d.SetVolumeAbsolute,
		"SetVolumeForDestination": d.SetVolumeForDestination,
		"SetMicVolume":            d.SetMicVolume, "SetMicVolumeAbsolute": d.SetMicVolumeAbsolute,
		"ToggleOutputMute": d.ToggleOutputMute, "ToggleMicMute": d.ToggleMicMute, "TogglePanic": d.TogglePanic,
		"SelectDestination": d.SelectDestination, "ToggleDestination": d.ToggleDestination, "SelectPreset": d.SelectPreset,
		"ResetDefaults": d.ResetDefaults, "GetRawMicStreams": d.GetRawMicStreams, "MigrateRawMicStream": d.MigrateRawMicStream,
		"ToggleMicMonitoring": d.ToggleMicMonitoring, "SetMicMonitoring": d.SetMicMonitoring,
		"ToggleOutputSharing": d.ToggleOutputSharing, "SetOutputSharing": d.SetOutputSharing,
	}
	if err := conn.ExportMethodTable(methods, dbus.ObjectPath(DBusPath), DBusInterface); err != nil {
		return err
	}
	node := &introspect.Node{Name: DBusPath, Interfaces: []introspect.Interface{{Name: DBusInterface, Methods: introspect.Methods(d), Signals: []introspect.Signal{{Name: "StateChanged", Args: []introspect.Arg{{Name: "statusJson", Type: "s"}}}}}}}
	// Introspection describes precisely the whitelisted method signatures.
	filtered := []introspect.Method{}
	for _, m := range node.Interfaces[0].Methods {
		if _, ok := methods[m.Name]; ok {
			filtered = append(filtered, m)
		}
	}
	node.Interfaces[0].Methods = filtered
	return conn.Export(introspect.NewIntrospectable(node), dbus.ObjectPath(DBusPath), "org.freedesktop.DBus.Introspectable")
}
func (d *Daemon) updateStatus(err error) {
	err = errors.Join(err, d.ctrl.PersistenceError())
	dest := d.ctrl.ActiveDestination()
	mic := d.ctrl.Microphone()
	d.status.ActiveDestination = DestinationInfo{string(dest.ID), dest.Label, dest.RouteName, dest.PhysicalCeiling.Percent()}
	d.status.OutputVolume = d.ctrl.OutputVirtualVolume().Percent()
	d.status.OutputMuted = d.ctrl.OutputMuted()
	d.status.MicLevel = mic.PostDSPLevel.Percent()
	d.status.MicVirtualMuted = mic.VirtualMuted
	d.status.MicPhysicalMuted = mic.PhysicalMuted
	d.status.MicPrivacyMuted = d.status.ObservedMicVirtualMute != nil && d.status.ObservedMicPhysicalMute != nil && *d.status.ObservedMicVirtualMute && *d.status.ObservedMicPhysicalMute
	d.status.PanicActive = d.ctrl.PanicState().Active
	d.status.MicMonitoring = d.ctrl.MicMonitoring()
	d.status.ActivePreset = d.ctrl.ActiveOutputPreset()
	d.status.ObserveOnly = d.observeOnly
	if err == nil && d.status.ObservedOutputMute != nil && *d.status.ObservedOutputMute != d.ctrl.OutputMuted() {
		err = errors.New("output mute differs from requested state")
	}
	if err == nil && d.status.ObservedMicVirtualMute != nil && d.status.ObservedMicPhysicalMute != nil && (*d.status.ObservedMicVirtualMute != mic.VirtualMuted || *d.status.ObservedMicPhysicalMute != mic.PhysicalMuted) {
		err = errors.New("microphone mute differs from requested state")
	}
	if err == nil && d.status.ObservedDestination != "" && d.status.ObservedDestination != dest.RouteName {
		err = errors.New("output route differs from requested state")
	}
	d.status.AvailablePresets = dest.FilterCompatiblePresets(d.presets)
	if err == nil && d.ctrl.PanicState().Active && !d.panicSnapshotKnown {
		err = errors.New("panic snapshot incomplete; automatic restore unavailable")
	}
	if err == nil && (!d.status.DSPBound || !d.status.MonitorVolumesConfirmed) {
		err = errors.New("DSP graph or monitor gain is not confirmed")
	}
	d.status.LastError = ""
	if err != nil {
		d.status.LastError = err.Error()
	}
	d.status.ProtectionState = "confirmed"
	if d.status.ObservedMicVirtualMute == nil || d.status.ObservedMicPhysicalMute == nil {
		d.status.ProtectionState = "unknown"
	} else if *d.status.ObservedMicVirtualMute != *d.status.ObservedMicPhysicalMute {
		d.status.ProtectionState = "degraded_partial_mute"
	}
	if err != nil {
		d.status.ProtectionState = "degraded"
	}
	d.publishStatus()
}

func (d *Daemon) publishStatus() {
	data, err := json.Marshal(d.status)
	if err != nil {
		d.statusJSON.Store(`{"protection_state":"degraded","last_error":"status encoding failed"}`)
		return
	}
	d.statusJSON.Store(string(data))
}
func (d *Daemon) emit() {
	if d.conn == nil {
		return
	}
	data, err := json.Marshal(d.status)
	if err != nil {
		d.status.LastError = err.Error()
		return
	}
	if err := d.conn.Emit(dbus.ObjectPath(DBusPath), DBusInterface+".StateChanged", string(data)); err != nil {
		d.status.LastError = err.Error()
		d.status.ProtectionState = "degraded"
		d.publishStatus()
	}
}
func (d *Daemon) control() error {
	if d.observeOnly {
		return errors.New("observe-only: start daemon --control for hardware actions")
	}
	return nil
}
func (d *Daemon) refresh(ctx context.Context) error {
	graph, graphErr := d.pw.ReadGraphState(ctx, d.hardware.VirtualSink, d.hardware.VirtualSource, d.ctrl.ActiveDestination().PhysicalSinkName, d.hardware.PhysicalMicrophonePrefix)
	sharingErr := d.readSharing(ctx)
	devices, devicesErr := d.ee.ReadDevices(ctx)
	d.status.PhysicalMicrophone = graph.PhysicalMicrophone
	d.status.DSPBound = graphErr == nil && devicesErr == nil && graph.PhysicalMicrophone != "" && graph.DefaultSink == d.hardware.VirtualSink && graph.ConfiguredSink == d.hardware.VirtualSink && devices.Input == graph.PhysicalMicrophone && devices.Output == d.ctrl.ActiveDestination().PhysicalSinkName && !devices.UseDefaultInput && !devices.UseDefaultOutput
	d.status.MonitorVolumesConfirmed = graphErr == nil && graph.SinkMonitorVolumes && graph.SourceMonitorVolumes
	presets, e1 := d.ee.ListAvailablePresets(ctx)
	if e1 == nil {
		d.presets = presets
	}
	raw, e2 := d.pw.GetRawMicStreams(ctx, d.hardware.PhysicalMicrophonePrefix)
	if e2 == nil {
		d.status.RawMicStreams = raw
	}
	read := func(name string, p **bool) error {
		value, err := d.pw.GetNodeMute(ctx, name)
		if err != nil {
			*p = nil
		} else {
			*p = &value
		}
		return err
	}
	route, routeErr := d.pw.GetSinkPort(ctx, d.ctrl.ActiveDestination().PhysicalSinkName)
	d.status.ObservedDestination = route
	preset, presetErr := d.ee.GetActivePreset(ctx)
	d.status.ObservedPreset = preset
	mon, monErr := d.pw.GetMicMonitoring(ctx, d.hardware.VirtualSource, d.hardware.VirtualSink)
	if monErr == nil {
		d.ctrl.SetMicMonitoring(mon)
		d.status.MicMonitoring = mon
	}
	return errors.Join(graphErr, sharingErr, devicesErr, e1, e2, routeErr, presetErr, monErr, read(d.hardware.VirtualSink, &d.status.ObservedOutputMute), read(d.hardware.VirtualSource, &d.status.ObservedMicVirtualMute), read(d.hardware.PhysicalMicrophonePrefix, &d.status.ObservedMicPhysicalMute))
}

// Observe refreshes graph-dependent alerts and observed mutes without modifying hardware.
func (d *Daemon) Observe() *dbus.Error {
	return d.execute(false, func(ctx context.Context) error { return d.refresh(ctx) })
}
func (d *Daemon) InitHardware(ctx context.Context) error {
	returnErr := d.execute(false, func(c context.Context) error {
		c, cancel := context.WithCancel(c)
		stop := context.AfterFunc(ctx, cancel)
		defer func() { stop(); cancel() }()
		if err := ctx.Err(); err != nil {
			return err
		}
		// Read-only discovery may find a broken preset/device binding that startup repairs.
		initialErr := d.refresh(c)
		if d.status.ObservedOutputMute == nil || d.status.ObservedMicVirtualMute == nil || d.status.ObservedMicPhysicalMute == nil || d.status.PhysicalMicrophone == "" {
			return errors.Join(initialErr, errors.New("initial mute/USB identity readback incomplete"))
		}

		route, err := d.pw.GetSinkPort(c, d.ctrl.ActiveDestination().PhysicalSinkName)
		d.status.ObservedDestination = route
		if err != nil {
			return err
		}
		target := domain.DestUnknown
		for _, id := range []domain.DestinationID{domain.DestAKG, domain.DestSP166} {
			dest, err := d.ctrl.Destination(id)
			if err != nil {
				return err
			}
			if route == dest.RouteName {
				target = id
			}
		}
		if target == domain.DestUnknown {
			return fmt.Errorf("unsupported active route %q", route)
		}
		if d.observeOnly {
			_, _, err := d.ctrl.SwitchDestination(target)
			return errors.Join(initialErr, err)
		}

		applyLogin := false
		if d.sessionTracker != nil {
			should, sErr := d.sessionTracker.ShouldApplyLoginPolicy()
			if sErr != nil {
				return sErr
			}
			applyLogin = should
		}
		if !applyLogin && !d.ctrl.SessionStateAvailable() {
			return errors.New("session snapshot missing; refusing to apply default gains on restart")
		}
		if applyLogin {
			if d.ctrl.HasRestoredSession() {
				return errors.New("login marker inconsistent with existing session snapshot; automatic unmute refused")
			}
			if err := d.sessionTracker.MarkLoginPolicyApplied(); err != nil {
				return fmt.Errorf("record login policy: %w", err)
			}
		}
		if _, _, err := d.ctrl.SwitchDestination(target); err != nil {
			return err
		}

		if applyLogin {
			// Consume the one-time login permission durably BEFORE any automatic unmute.
			// If hardware later fails, a restart preserves observed privacy instead of retrying unmute.
			// LOGIN POLICY (docs/ARCHITECTURE_AND_REQUIREMENTS.md):
			// Output: Daily base preset, volume 95%, unmuted
			// Mic: level 100%, unmuted (both virtual and physical)
			d.ctrl.ResetToDailyBase()
			d.ctrl.SetMicrophoneState(false, false, domain.NewPercentageLevel(100))
			if d.ctrl.OutputMuted() {
				d.ctrl.ToggleOutputMute()
			}
			dest := d.ctrl.ActiveDestination()
			if dest.BasePreset != "" {
				if err := d.ctrl.SelectOutputPreset(dest.BasePreset); err != nil {
					return err
				}
			}
		} else {
			// RESTART WITHIN SAME SESSION:
			// Adopt observed mutes to respect existing user privacy
			if !d.ctrl.PanicState().Active {
				d.ctrl.SetMicrophoneState(*d.status.ObservedMicVirtualMute, *d.status.ObservedMicPhysicalMute, d.ctrl.Microphone().PostDSPLevel)
				if *d.status.ObservedOutputMute != d.ctrl.OutputMuted() {
					d.ctrl.ToggleOutputMute()
				}
			}
		}

		if err := d.ctrl.PersistenceError(); err != nil {
			return err
		}
		if err := d.applyDestination(c, d.ctrl.ActiveDestination()); err != nil {
			return err
		}
		return d.refresh(c)
	})
	if returnErr != nil {
		return errors.New(returnErr.Error())
	}
	return nil
}
func (d *Daemon) mute(ctx context.Context, node string, want bool, observed **bool) error {
	if observed != nil {
		*observed = nil
	}
	virtual := node == d.hardware.VirtualSink || node == d.hardware.VirtualSource
	if virtual && !want {
		level := d.ctrl.OutputVirtualVolume()
		if node == d.hardware.VirtualSource {
			level = d.ctrl.Microphone().PostDSPLevel
		}
		if err := d.pw.SetNodeVolume(ctx, node, level.LinearRatio()); err != nil {
			return err
		}
	}
	if err := d.pw.SetNodeMute(ctx, node, want); err != nil {
		return err
	}
	if virtual {
		if err := d.pw.EnsureMonitorChannelVolumes(ctx, node); err != nil {
			return err
		}
	}
	got, err := d.pw.GetNodeMute(ctx, node)
	if err != nil {
		return err
	}
	if observed != nil {
		*observed = &got
	}
	if got != want {
		return fmt.Errorf("mute readback mismatch on %s", node)
	}
	return nil
}
func (d *Daemon) applyMutes(ctx context.Context) error {
	mic := d.ctrl.Microphone()
	// Attempt each privacy target independently, even if another target fails.
	a := d.mute(ctx, d.hardware.VirtualSink, d.ctrl.OutputMuted(), &d.status.ObservedOutputMute)
	b := d.mute(ctx, d.hardware.VirtualSource, mic.VirtualMuted, &d.status.ObservedMicVirtualMute)
	c := d.mute(ctx, d.hardware.PhysicalMicrophonePrefix, mic.PhysicalMuted, &d.status.ObservedMicPhysicalMute)
	return errors.Join(a, b, c)
}
func (d *Daemon) applyDestination(ctx context.Context, dest *domain.Destination) (err error) {
	// Keep the hardware sink muted until route, inactive pins, DSP and session levels are ready.
	if err = d.mute(ctx, dest.PhysicalSinkName, true, nil); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			safe, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = errors.Join(err, d.pw.SetNodeMute(safe, dest.PhysicalSinkName, true))
		}
	}()
	for _, pin := range []string{dest.AlsaActiveControl, dest.AlsaInactiveControl} {
		if err = d.alsa.SetControl(ctx, d.hardware.AlsaCard, pin, 0, true); err != nil {
			return err
		}
	}
	if err = d.alsa.EnsureAutoMuteDisabled(ctx, d.hardware.AlsaCard); err != nil {
		return err
	}
	if err = d.ensureDSPBindings(ctx, dest); err != nil {
		return err
	}
	// Initialize virtual levels before enabling monitor gain; the live inspector may find an old request above 100%.
	for _, target := range []struct {
		node   string
		linear float64
	}{{d.hardware.VirtualSink, d.ctrl.OutputVirtualVolume().LinearRatio()}, {d.hardware.VirtualSource, d.ctrl.Microphone().PostDSPLevel.LinearRatio()}} {
		if err = d.pw.SetNodeVolume(ctx, target.node, target.linear); err != nil {
			return err
		}
		if err = d.pw.EnsureMonitorChannelVolumes(ctx, target.node); err != nil {
			return err
		}
	}
	if err = d.pw.SetSinkPort(ctx, dest.PhysicalSinkName, dest.RouteName); err != nil {
		return err
	}
	var route string
	var readErr error
	for attempt := 0; attempt < 10; attempt++ {
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		route, readErr = d.pw.GetSinkPort(ctx, dest.PhysicalSinkName)
		if readErr == nil && route == dest.RouteName {
			break
		}
	}
	if readErr != nil {
		return readErr
	}
	d.status.ObservedDestination = route
	if route != dest.RouteName {
		return errors.New("route readback mismatch")
	}
	// Route changes can rewrite ALSA; reassert the inactive pin after switching.
	if err = d.alsa.SetControl(ctx, d.hardware.AlsaCard, dest.AlsaInactiveControl, 0, true); err != nil {
		return err
	}
	if err = d.alsa.SetControl(ctx, d.hardware.AlsaCard, dest.AlsaActiveControl, 100, false); err != nil {
		return err
	}
	if err = d.pw.SetNodeVolume(ctx, dest.PhysicalSinkName, dest.PhysicalCeiling.LinearRatio()); err != nil {
		return err
	}
	if d.stabilization > 0 {
		timer := time.NewTimer(d.stabilization)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err = d.ee.LoadPreset(ctx, d.ctrl.ActiveOutputPreset()); err != nil {
		return err
	}
	d.status.ObservedPreset, err = d.ee.GetActivePreset(ctx)
	if err != nil {
		return err
	}
	if d.status.ObservedPreset != d.ctrl.ActiveOutputPreset() {
		return errors.New("preset readback mismatch")
	}
	if err = d.pw.SetNodeVolume(ctx, d.hardware.VirtualSink, d.ctrl.OutputVirtualVolume().LinearRatio()); err != nil {
		return err
	}
	if err = d.pw.SetNodeVolume(ctx, d.hardware.VirtualSource, d.ctrl.Microphone().PostDSPLevel.LinearRatio()); err != nil {
		return err
	}
	if err = d.applyMutes(ctx); err != nil {
		return err
	}
	if err = d.alsa.SetControl(ctx, d.hardware.AlsaCard, dest.AlsaInactiveControl, 0, true); err != nil {
		return err
	}
	if err = d.refresh(ctx); err != nil {
		return err
	}
	if !d.status.DSPBound || !d.status.MonitorVolumesConfirmed {
		return errors.New("DSP chain not confirmed; hardware remains muted")
	}
	return d.mute(ctx, dest.PhysicalSinkName, d.ctrl.OutputMuted(), nil)
}

func (d *Daemon) ensureDSPBindings(ctx context.Context, dest *domain.Destination) error {
	graph, err := d.pw.ReadGraphState(ctx, d.hardware.VirtualSink, d.hardware.VirtualSource, dest.PhysicalSinkName, d.hardware.PhysicalMicrophonePrefix)
	if err != nil {
		return err
	}
	if graph.PhysicalMicrophone == "" {
		return errors.New("physical microphone identity unresolved")
	}
	if err := d.ee.EnsureDevices(ctx, dest.PhysicalSinkName, graph.PhysicalMicrophone); err != nil {
		return err
	}
	if err := d.pw.EnsureDefaultSink(ctx, d.hardware.VirtualSink); err != nil {
		return err
	}
	return d.pw.EnsureDefaultSource(ctx, d.hardware.VirtualSource)
}
func (d *Daemon) GetStatus() (string, *dbus.Error) {
	return d.statusJSON.Load().(string), nil
}
func (d *Daemon) applyVolume(ctx context.Context, target int, mic bool) (int, error) {
	if err := d.control(); err != nil {
		return 0, err
	}
	node := d.hardware.VirtualSink
	if mic {
		node = d.hardware.VirtualSource
	}
	level := domain.NewPercentageLevel(target)
	if err := d.pw.SetNodeVolume(ctx, node, level.LinearRatio()); err != nil {
		return 0, err
	}
	if err := d.pw.EnsureMonitorChannelVolumes(ctx, node); err != nil {
		return 0, err
	}
	if mic {
		d.ctrl.SetMicLevel(level)
	} else {
		d.ctrl.SetOutputVolume(level)
	}
	return level.Percent(), nil
}
func (d *Daemon) volume(value int, absolute, mic bool) (int, *dbus.Error) {
	if !absolute && (value < -100 || value > 100) {
		return 0, dbus.MakeFailedError(errors.New("delta outside [-100,100]"))
	}
	level := &levelRequest{value: value, absolute: absolute, mic: mic}
	r := &request{level: level, created: time.Now(), done: make(chan error, 1)}
	err := d.submit(r)
	return level.result, err
}
func (d *Daemon) SetVolume(v int32) (int32, *dbus.Error) {
	n, e := d.volume(int(v), false, false)
	return int32(n), e
}
func (d *Daemon) SetVolumeAbsolute(v int32) (int32, *dbus.Error) {
	n, e := d.volume(int(v), true, false)
	return int32(n), e
}

// SetVolumeForDestination prevents a delayed UI call from changing another route.
func (d *Daemon) SetVolumeForDestination(destination string, v int32) (int32, *dbus.Error) {
	if destination != "akg" && destination != "sp166" {
		return 0, dbus.MakeFailedError(domain.ErrUnknownDestination)
	}
	level := &levelRequest{value: int(v), absolute: true, destination: destination}
	err := d.submit(&request{level: level, created: time.Now(), done: make(chan error, 1)})
	return int32(level.result), err
}
func (d *Daemon) SetMicVolume(v int32) (int32, *dbus.Error) {
	n, e := d.volume(int(v), false, true)
	return int32(n), e
}
func (d *Daemon) SetMicVolumeAbsolute(v int32) (int32, *dbus.Error) {
	n, e := d.volume(int(v), true, true)
	return int32(n), e
}
func (d *Daemon) ToggleOutputMute() (bool, *dbus.Error) {
	var value bool
	err := d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		observed, err := d.pw.GetNodeMute(ctx, d.hardware.VirtualSink)
		if err != nil {
			return err
		}
		if observed != d.ctrl.OutputMuted() {
			d.ctrl.ToggleOutputMute()
		}
		value = d.ctrl.ToggleOutputMute()
		return errors.Join(d.mute(ctx, d.hardware.VirtualSink, value, &d.status.ObservedOutputMute), d.mute(ctx, d.ctrl.ActiveDestination().PhysicalSinkName, value, nil))
	})
	return value, err
}
func (d *Daemon) ToggleMicMute() (bool, *dbus.Error) {
	var value bool
	err := d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if d.ctrl.OutputSharing() || d.status.OutputSharingRestorePending {
			stopErr := d.stopSharing(ctx)
			d.ctrl.MuteMicrophone()
			value = true
			return errors.Join(stopErr, d.mute(ctx, d.hardware.VirtualSource, true, &d.status.ObservedMicVirtualMute), d.mute(ctx, d.hardware.PhysicalMicrophonePrefix, true, &d.status.ObservedMicPhysicalMute))
		}
		virtual, e1 := d.pw.GetNodeMute(ctx, d.hardware.VirtualSource)
		physical, e2 := d.pw.GetNodeMute(ctx, d.hardware.PhysicalMicrophonePrefix)
		if err := errors.Join(e1, e2); err != nil {
			return err
		}
		d.ctrl.SetMicrophoneState(virtual, physical, d.ctrl.Microphone().PostDSPLevel)
		value = d.ctrl.ToggleMicMute()
		return errors.Join(d.mute(ctx, d.hardware.VirtualSource, value, &d.status.ObservedMicVirtualMute), d.mute(ctx, d.hardware.PhysicalMicrophonePrefix, value, &d.status.ObservedMicPhysicalMute))
	})
	return value, err
}
func (d *Daemon) TogglePanic() (bool, *dbus.Error) {
	var value bool
	err := d.execute(true, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		// Sharing is never re-enabled by panic restoration. The three-boolean
		// acoustic snapshot remains independent of the transient capture route.
		// Snapshot only the three mute targets. Preset/disk/stream inspection must never block emergency mute.
		if !d.ctrl.PanicState().Active {
			values, snapshotErr := d.panicSnapshot(ctx)
			out, virtual, physical := values[0], values[1], values[2]
			d.panicSnapshotKnown = snapshotErr == nil
			if snapshotErr == nil {
				d.ctrl.SetMicrophoneState(virtual, physical, d.ctrl.Microphone().PostDSPLevel)
				if out != d.ctrl.OutputMuted() {
					d.ctrl.ToggleOutputMute()
				}
			} else {
				// Unknown channels can be muted, but an unknown snapshot must never authorize a later unmute.
				d.ctrl.TogglePanicMute()
				value = true
				return errors.Join(snapshotErr, d.panicMutes(ctx))
			}
		} else if !d.panicSnapshotKnown {
			value = true
			return errors.Join(errors.New("panic snapshot incomplete; restore refused"), d.panicMutes(ctx))
		}
		_, _, value = d.ctrl.TogglePanicMute()
		return d.panicMutes(ctx)
	})
	return value, err
}
func (d *Daemon) selectDestination(ctx context.Context, name string) error {
	if err := d.control(); err != nil {
		return err
	}
	if err := d.stopSharing(ctx); err != nil {
		return err
	}
	dest, _, err := d.ctrl.SwitchDestination(domain.DestinationID(strings.ToLower(name)))
	if err != nil {
		return err
	}
	return d.applyDestination(ctx, dest)
}
func (d *Daemon) SelectDestination(name string) *dbus.Error {
	return d.execute(false, func(ctx context.Context) error { return d.selectDestination(ctx, name) })
}
func (d *Daemon) ToggleDestination() (string, *dbus.Error) {
	var value string
	err := d.execute(false, func(ctx context.Context) error {
		value = "akg"
		if d.ctrl.ActiveDestination().ID == domain.DestAKG {
			value = "sp166"
		}
		return d.selectDestination(ctx, value)
	})
	return value, err
}
func (d *Daemon) SelectPreset(name string) *dbus.Error {
	return d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if !d.ctrl.ActiveDestination().IsPresetCompatible(name) {
			return domain.ErrIncompatiblePreset
		}
		available, err := d.ee.ListAvailablePresets(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, p := range available {
			if p == name {
				found = true
			}
		}
		if !found {
			return errors.New("preset not available")
		}
		if err := d.ee.LoadPreset(ctx, name); err != nil {
			return err
		}
		d.status.ObservedPreset = name
		return d.ctrl.SelectOutputPreset(name)
	})
}
func (d *Daemon) ResetDefaults() *dbus.Error {
	return d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if err := d.stopSharing(ctx); err != nil {
			return err
		}
		// Read both mic states before reset; never collapse a divergent pair.
		virtual, e1 := d.pw.GetNodeMute(ctx, d.hardware.VirtualSource)
		physical, e2 := d.pw.GetNodeMute(ctx, d.hardware.PhysicalMicrophonePrefix)
		if err := errors.Join(e1, e2); err != nil {
			return err
		}
		d.ctrl.SetMicrophoneState(virtual, physical, d.ctrl.Microphone().PostDSPLevel)
		d.ctrl.ResetToDailyBase()
		return d.applyDestination(ctx, d.ctrl.ActiveDestination())
	})
}
func (d *Daemon) GetRawMicStreams() ([]string, *dbus.Error) {
	var result []string
	err := d.execute(false, func(ctx context.Context) error {
		var err error
		result, err = d.pw.GetRawMicStreams(ctx, d.hardware.PhysicalMicrophonePrefix)
		if err == nil {
			d.status.RawMicStreams = result
		}
		return err
	})
	return result, err
}

// Reconcile is driven by graph and GSettings events; session preset choice wins autoload.
func (d *Daemon) Reconcile() *dbus.Error {
	return d.execute(false, func(ctx context.Context) error {
		err := d.refresh(ctx)
		if d.observeOnly {
			confirmed, confirmErr := d.ee.GetActivePreset(ctx)
			d.status.ObservedPreset = confirmed
			return errors.Join(err, confirmErr)
		}
		if d.status.OutputSharingRestorePending || (d.ctrl.OutputSharing() && (err != nil || d.ctrl.PanicState().Active || d.ctrl.MicMonitoring())) {
			if stopErr := d.stopSharing(ctx); stopErr != nil {
				return errors.Join(err, stopErr)
			}
		}
		if d.status.ObservedDestination != d.ctrl.ActiveDestination().RouteName {
			return errors.Join(err, d.pw.SetNodeMute(ctx, d.ctrl.ActiveDestination().PhysicalSinkName, true), errors.New("external output route changed; select destination explicitly"))
		}
		if d.status.ObservedMicVirtualMute == nil || d.status.ObservedMicPhysicalMute == nil || d.status.ObservedOutputMute == nil {
			return errors.Join(err, d.pw.SetNodeMute(ctx, d.ctrl.ActiveDestination().PhysicalSinkName, true),
				d.pw.SetNodeMute(ctx, d.hardware.PhysicalMicrophonePrefix, true), errors.New("mute observation incomplete; fail-safe mute requested"))
		}
		// Background repair may add privacy, but must never authorize a new unmute.
		mic := d.ctrl.Microphone()
		d.ctrl.SetMicrophoneState(mic.VirtualMuted || *d.status.ObservedMicVirtualMute, mic.PhysicalMuted || *d.status.ObservedMicPhysicalMute, mic.PostDSPLevel)
		if *d.status.ObservedOutputMute && !d.ctrl.OutputMuted() {
			d.ctrl.ToggleOutputMute()
		}
		if !d.status.DSPBound || !d.status.MonitorVolumesConfirmed {
			if repairErr := d.applyDestination(ctx, d.ctrl.ActiveDestination()); repairErr != nil {
				return errors.Join(err, repairErr)
			}
		} else if *d.status.ObservedMicVirtualMute != d.ctrl.Microphone().VirtualMuted || *d.status.ObservedMicPhysicalMute != d.ctrl.Microphone().PhysicalMuted || *d.status.ObservedOutputMute != d.ctrl.OutputMuted() {
			if muteErr := d.applyMutes(ctx); muteErr != nil {
				return errors.Join(err, muteErr)
			}
		}
		active, readErr := d.ee.GetActivePreset(ctx)
		if readErr != nil || active != d.ctrl.ActiveOutputPreset() {
			if loadErr := d.ee.LoadPreset(ctx, d.ctrl.ActiveOutputPreset()); loadErr != nil {
				return errors.Join(err, loadErr)
			}
		}
		// Return the final readback: a successfully repaired initial mismatch is no longer an error.
		return d.refresh(ctx)
	})
}

func (d *Daemon) panicSnapshot(ctx context.Context) ([3]bool, error) {
	var values [3]bool
	var errs [3]error
	var group sync.WaitGroup
	for i, node := range []string{d.hardware.VirtualSink, d.hardware.VirtualSource, d.hardware.PhysicalMicrophonePrefix} {
		i, node := i, node
		group.Add(1)
		go func() {
			defer group.Done()
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			val, err := d.pw.GetNodeMute(bounded, node)
			if err != nil && node == d.hardware.PhysicalMicrophonePrefix {
				if strings.Contains(err.Error(), "unavailable") || strings.Contains(err.Error(), "not found") {
					values[i] = true
					return
				}
			}
			values[i] = val
			errs[i] = err
		}()
	}
	group.Wait()
	return values, errors.Join(errs[:]...)
}
func (d *Daemon) panicMutes(ctx context.Context) error {
	mic := d.ctrl.Microphone()
	var group sync.WaitGroup
	var errs [5]error
	group.Add(1)
	go func() { defer group.Done(); errs[4] = d.stopSharing(ctx) }()
	targets := []struct {
		node     string
		want     bool
		observed **bool
	}{{d.hardware.VirtualSink, d.ctrl.OutputMuted(), &d.status.ObservedOutputMute}, {d.hardware.VirtualSource, mic.VirtualMuted, &d.status.ObservedMicVirtualMute}, {d.hardware.PhysicalMicrophonePrefix, mic.PhysicalMuted, &d.status.ObservedMicPhysicalMute}, {d.ctrl.ActiveDestination().PhysicalSinkName, d.ctrl.OutputMuted(), nil}}
	for i, target := range targets {
		group.Add(1)
		go func() {
			defer group.Done()
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := d.mute(bounded, target.node, target.want, target.observed)
			if err != nil && target.node == d.hardware.PhysicalMicrophonePrefix {
				if target.want && (strings.Contains(err.Error(), "unavailable") || strings.Contains(err.Error(), "not found")) {
					m := true
					if target.observed != nil {
						*target.observed = &m
					}
					return
				}
			}
			errs[i] = err
		}()
	}
	group.Wait()
	return errors.Join(errs[:]...)
}

// MigrateRawMicStream is only called after an explicit per-application confirmation in the UI.
func (d *Daemon) MigrateRawMicStream(application string) *dbus.Error {
	return d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if err := d.pw.MigrateRawMicStream(ctx, d.hardware.PhysicalMicrophonePrefix, d.hardware.VirtualSource, application); err != nil {
			return err
		}
		raw, err := d.pw.GetRawMicStreams(ctx, d.hardware.PhysicalMicrophonePrefix)
		if err != nil {
			return err
		}
		d.status.RawMicStreams = raw
		return nil
	})
}

// ToggleMicMonitoring enables or disables routing filtered mic audio to the active playback output.
func (d *Daemon) ToggleMicMonitoring() (bool, *dbus.Error) {
	var value bool
	err := d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		current, err := d.pw.GetMicMonitoring(ctx, d.hardware.VirtualSource, d.hardware.VirtualSink)
		if err != nil {
			current = d.ctrl.MicMonitoring()
		}
		target := !current
		if target && (d.ctrl.OutputSharing() || d.status.OutputSharingRestorePending) {
			return errors.New("turn off output sharing before microphone listening")
		}
		if err := d.pw.SetMicMonitoring(ctx, d.hardware.VirtualSource, d.hardware.VirtualSink, target); err != nil {
			return err
		}
		d.ctrl.SetMicMonitoring(target)
		d.status.MicMonitoring = target
		value = target
		return nil
	})
	return value, err
}

// SetMicMonitoring directly sets the mic monitoring state.
func (d *Daemon) SetMicMonitoring(enabled bool) (bool, *dbus.Error) {
	var value bool
	err := d.execute(false, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if enabled && (d.ctrl.OutputSharing() || d.status.OutputSharingRestorePending) {
			return errors.New("turn off output sharing before microphone listening")
		}
		if err := d.pw.SetMicMonitoring(ctx, d.hardware.VirtualSource, d.hardware.VirtualSink, enabled); err != nil {
			return err
		}
		d.ctrl.SetMicMonitoring(enabled)
		d.status.MicMonitoring = enabled
		value = enabled
		return nil
	})
	return value, err
}
