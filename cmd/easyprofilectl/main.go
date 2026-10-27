// Command easyprofilectl is the EasyProfileApp CLI and Daemon.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/adapters/alsa"
	"github.com/toru/easy-profile-app/internal/adapters/easyeffects"
	"github.com/toru/easy-profile-app/internal/adapters/pipewire"
	"github.com/toru/easy-profile-app/internal/daemon"
	"github.com/toru/easy-profile-app/internal/domain"
	"github.com/toru/easy-profile-app/internal/inspect"
)

const version = "0.2.0-mvp"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "volume":
		return runVolume(args[1:], stdout, stderr)
	case "mic":
		return runMic(args[1:], stdout, stderr)
	case "mute":
		return runMute(args[1:], stdout, stderr)
	case "panic":
		return runPanic(args[1:], stdout, stderr)
	case "route":
		return runRoute(args[1:], stdout, stderr)
	case "preset":
		return runPreset(args[1:], stdout, stderr)
	case "reset":
		return runReset(args[1:], stdout, stderr)
	case "listen", "monitor":
		return runListen(args[1:], stdout, stderr)
	case "ptt":
		return runPTT(args[1:], stdout, stderr)
	case "boost":
		return runBoost(args[1:], stdout, stderr)
	case "share":
		return runShare(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "easyprofilectl", version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: easyprofilectl <command> [args...]

commands:
  daemon                  run the background D-Bus daemon (org.easyprofile.Daemon)
  status                  query and display current audio routes, volume, mutes and presets
  route [akg|sp166|toggle] switch or toggle active audio destination
  volume [+1|-1|<level>]  adjust or set output volume (0..100; Boost: 0..150)
  mic [+1|-1|<level>]     adjust or set microphone post-DSP level (0..100)
  mute [out|mic]          toggle mute on output or microphone (Dual-Mute)
  panic                   emergency mute all channels or restore previous state
  preset [list|next|prev|base|<name>] list, cycle or apply a compatible equalizer preset
  reset                   restore daily base preset, output 95%, mic 100% (preserves mic mute)
  listen [on|off|toggle]  toggle or set mic monitoring (listening with DSP filters)
  ptt [on|off]           push-to-talk mode (always enters/exits with microphone muted)
  boost [on|off|toggle]   unlock output ceiling to 150% without raising current level
  share [on|off|toggle]   replace an active call's microphone with the whole output
  inspect                 read-only inspection of PipeWire, ALSA, and EasyEffects
  version                 print version`)
}

func getDBusObject() (*dbus.Conn, dbus.BusObject, error) {
	if _, test := os.LookupEnv("EASYPROFILE_TEST_MODE"); test && !privateTestBus() {
		return nil, nil, fmt.Errorf("test client requires private D-Bus integration harness")
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to session bus: %w", err)
	}
	obj := conn.Object(daemon.DBusService, dbus.ObjectPath(daemon.DBusPath))
	return conn, obj, nil
}

func privateTestBus() bool {
	parent, address := os.Getenv("EASYPROFILE_PARENT_BUS_ADDRESS"), os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	return os.Getenv("EASYPROFILE_PRIVATE_BUS") == "1" && parent != "" && address != "" && parent != address
}

type daemonOptions struct {
	fake        bool
	control     bool
	observe     bool
	observeOnly bool
	configPath  string
	observeMode bool
}

func parseDaemonFlags(args []string, stderr io.Writer) (*daemonOptions, error) {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fake := fs.Bool("fake", false, "in-memory adapters (requires EASYPROFILE_TEST_MODE=1)")
	control := fs.Bool("control", true, "enable hardware writes (default true)")
	observe := fs.Bool("observe", false, "run in read-only observation mode without hardware writes")
	observeOnly := fs.Bool("observe-only", false, "alias for --observe")
	configPath := fs.String("config", "", "read hardware/preset config JSON (defaults to calibrated setup)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	opts := &daemonOptions{
		fake:        *fake,
		control:     *control,
		observe:     *observe,
		observeOnly: *observeOnly,
		configPath:  *configPath,
	}
	opts.observeMode = (*observe || *observeOnly || !*control) && !*fake
	return opts, nil
}

func runDaemon(args []string, stdout, stderr io.Writer) int {
	opts, err := parseDaemonFlags(args, stderr)
	if err != nil {
		return 2
	}
	if opts.fake && os.Getenv("EASYPROFILE_TEST_MODE") != "1" {
		fmt.Fprintln(stderr, "daemon --fake requires EASYPROFILE_TEST_MODE=1")
		return 2
	}
	if _, test := os.LookupEnv("EASYPROFILE_TEST_MODE"); test && !opts.fake {
		fmt.Fprintln(stderr, "test mode requires daemon --fake")
		return 2
	}
	if opts.fake && !privateTestBus() {
		fmt.Fprintln(stderr, "fake daemon requires a private dbus-run-session harness")
		return 2
	}
	// Acquire the process lock before loading mutable session state or touching audio.
	// Fake daemons use the harness's private runtime directory, never the host lock.
	lock, err := daemon.AcquireProcessLock(daemon.DefaultLockFilePath())
	if err != nil {
		fmt.Fprintln(stderr, "daemon lock:", err)
		return 1
	}
	defer lock.Close()
	cfg, err := daemon.LoadConfig(opts.configPath)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return 2
	}
	var ctrl *domain.AudioController
	if opts.fake || opts.observeMode {
		ctrl, err = cfg.Controller()
	} else {
		ctrl, err = cfg.ControllerWithSession(domain.NewSessionStore(daemon.DefaultSessionFilePath()), daemon.ResolveCurrentSessionID())
	}
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return 2
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()
	var pw adapters.PipeWirePort
	var al adapters.AlsaPort
	var ee adapters.EasyEffectsPort
	if opts.fake {
		pw = pipewire.NewFakePipeWire()
		al = alsa.NewFakeAlsa()
		ee = easyeffects.NewFakeEasyEffects("AKG K52 - Uso diario aprovado", "AKG - Rock", "SP166 - Uso diario aprovado")
	} else {
		realPW := pipewire.NewRealPipeWire(3 * time.Second)
		realPW.SetMicrophonePrefix(cfg.PhysicalMicrophonePrefix)
		pw = realPW
		al = alsa.NewRealAlsa(3 * time.Second)
		ee = easyeffects.NewRealEasyEffects("", 3*time.Second)
	}
	d := daemon.NewDaemon(ctrl, pw, al, ee)
	if opts.fake {
		d.SetSessionTracker(daemon.NewMemorySessionTracker("fake-session"))
	} else {
		sessionPath := daemon.DefaultLoginFilePath()
		sessionID := daemon.ResolveCurrentSessionID()
		d.SetSessionTracker(daemon.NewFileSessionTracker(sessionPath, sessionID))
	}
	if err := d.Configure(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		d.Close()
		return 2
	}
	defer d.Close()
	d.SetObserveOnly(opts.observeMode)
	if opts.fake {
		d.SetStabilization(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := d.InitHardware(ctx); err != nil {
		fmt.Fprintln(stderr, "daemon initialization:", err)
		return 1
	}
	if err := d.Export(conn); err != nil {
		fmt.Fprintln(stderr, "daemon export:", err)
		return 1
	}
	// Request well-known name only AFTER methods and introspection are exported.
	// This prevents the race condition where clients observe notify::g-name-owner
	// and call methods before they are registered.
	reply, err := conn.RequestName(daemon.DBusService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintln(stderr, "daemon name already owned or unavailable", err)
		return 1
	}
	fmt.Fprintln(stdout, "easyprofile daemon ready on org.easyprofile.Daemon")
	// One bounded event channel coalesces graph/settings bursts, with no idle polling.
	events := make(chan struct{}, 1)
	watchCtx, cancelWatch := context.WithCancel(ctx)
	var watchers sync.WaitGroup
	for _, adapter := range []any{pw, ee} {
		if w, ok := adapter.(interface {
			Watch(context.Context, chan<- struct{}) error
		}); ok {
			watchers.Add(1)
			go func() {
				defer watchers.Done()
				for watchCtx.Err() == nil {
					err := w.Watch(watchCtx, events)
					if watchCtx.Err() != nil {
						return
					}
					fmt.Fprintln(stderr, "observer disconnected:", err)
					timer := time.NewTimer(time.Second)
					select {
					case <-watchCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}()
		}
	}
	defer func() { cancelWatch(); watchers.Wait() }()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-conn.Context().Done():
			fmt.Fprintln(stderr, "session D-Bus disconnected")
			return 1
		case <-events:
			if !waitForReconcileBatch(ctx, events, 100*time.Millisecond) {
				return 0
			}
			if err := d.Reconcile(); err != nil {
				fmt.Fprintln(stderr, "reconciliation:", err)
			}
		}
	}
}

// Calls have an explicit deadline so clients cannot hang behind an unavailable daemon.
func busCall(obj dbus.BusObject, method string, flags dbus.Flags, args ...interface{}) *dbus.Call {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return obj.CallWithContext(ctx, method, flags, args...)
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "status: %v\n", err)
		return 1
	}
	defer conn.Close()

	var raw string
	if err := busCall(obj, daemon.DBusInterface+".GetStatus", 0).Store(&raw); err != nil {
		fmt.Fprintf(stderr, "status: D-Bus call failed (is easyprofiled running?): %v\n", err)
		return 1
	}

	if len(args) == 1 && args[0] == "--json" {
		fmt.Fprintln(stdout, raw)
		return 0
	}
	var s daemon.DaemonStatus
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		fmt.Fprintln(stdout, raw)
		return 0
	}

	outMuteStr := "unmuted"
	if s.OutputMuted {
		outMuteStr = "MUTED"
	}
	micMuteStr := "unmuted"
	if s.MicPrivacyMuted {
		micMuteStr = "MUTED (Dual-Mute)"
	} else if s.MicVirtualMuted || s.MicPhysicalMuted {
		micMuteStr = "PARTIAL MUTE (Degraded)"
	}
	panicStr := "inactive"
	if s.PanicActive {
		panicStr = "ACTIVE (Emergency Mute All)"
	}

	fmt.Fprintln(stdout, "EasyProfileApp Status:")
	fmt.Fprintf(stdout, "  Destination:  %s (%s, ceiling: %d%%)\n", s.ActiveDestination.DisplayName, s.ActiveDestination.PortName, s.ActiveDestination.CeilingPct)
	fmt.Fprintf(stdout, "  Output Vol:   %d%% [%s]\n", s.OutputVolume, outMuteStr)
	fmt.Fprintf(stdout, "  Mic Level:    %d%% [%s]\n", s.MicLevel, micMuteStr)
	monStr := "disabled"
	if s.MicMonitoring {
		monStr = "ENABLED (listening with filters)"
	}
	fmt.Fprintf(stdout, "  Mic Listen:   %s\n", monStr)
	fmt.Fprintf(stdout, "  Output Share: %t (restoration pending: %t)\n", s.OutputSharing, s.OutputSharingRestorePending)
	fmt.Fprintf(stdout, "  Push-to-talk: %t (held: %t)\n", s.PushToTalk, s.TalkPressed)
	fmt.Fprintf(stdout, "  Output Boost: %t (ceiling: %d%%)\n", s.OutputBoost, s.OutputMaxPercent)
	fmt.Fprintf(stdout, "  Panic Mute:   %s\n", panicStr)
	fmt.Fprintf(stdout, "  Preset:       %s\n", s.ActivePreset)
	if len(s.AvailablePresets) > 0 {
		fmt.Fprintf(stdout, "  Presets:      %s\n", strings.Join(s.AvailablePresets, ", "))
	}
	if len(s.RawMicStreams) > 0 {
		fmt.Fprintf(stdout, "  ⚠️  Raw Mic:    Apps connected directly: %s\n", strings.Join(s.RawMicStreams, ", "))
	}

	if s.LastError != "" {
		fmt.Fprintln(stdout, "  Degraded:", s.LastError)
	}
	return 0
}

func runVolume(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: easyprofilectl volume [+1|-1|<level 0-150>]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "volume: %v\n", err)
		return 1
	}
	defer conn.Close()

	arg := args[0]
	var newVol int32
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		delta, err := strconv.Atoi(arg)
		if err != nil {
			fmt.Fprintf(stderr, "invalid delta %q: %v\n", arg, err)
			return 2
		}
		if err := busCall(obj, daemon.DBusInterface+".SetVolume", 0, int32(delta)).Store(&newVol); err != nil {
			fmt.Fprintf(stderr, "volume call failed: %v\n", err)
			return 1
		}
	} else {
		level, err := strconv.Atoi(arg)
		if err != nil || level < 0 || level > domain.BoostMaximumPercent {
			fmt.Fprintf(stderr, "invalid volume level %q (must be 0..150; above 100 requires Boost): %v\n", arg, err)
			return 2
		}
		if err := busCall(obj, daemon.DBusInterface+".SetVolumeAbsolute", 0, int32(level)).Store(&newVol); err != nil {
			fmt.Fprintf(stderr, "volume call failed: %v\n", err)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Output Volume: %d%%\n", newVol)
	return 0
}

func runMic(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: easyprofilectl mic [+1|-1|<level 0-100>]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "mic: %v\n", err)
		return 1
	}
	defer conn.Close()

	arg := args[0]
	var newVol int32
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		delta, err := strconv.Atoi(arg)
		if err != nil {
			fmt.Fprintf(stderr, "invalid delta %q: %v\n", arg, err)
			return 2
		}
		if err := busCall(obj, daemon.DBusInterface+".SetMicVolume", 0, int32(delta)).Store(&newVol); err != nil {
			fmt.Fprintf(stderr, "mic call failed: %v\n", err)
			return 1
		}
	} else {
		level, err := strconv.Atoi(arg)
		if err != nil || level < 0 || level > 100 {
			fmt.Fprintf(stderr, "invalid mic level %q (must be 0..100): %v\n", arg, err)
			return 2
		}
		if err := busCall(obj, daemon.DBusInterface+".SetMicVolumeAbsolute", 0, int32(level)).Store(&newVol); err != nil {
			fmt.Fprintf(stderr, "mic call failed: %v\n", err)
			return 1
		}
	}

	fmt.Fprintf(stdout, "Mic Level: %d%%\n", newVol)
	return 0
}

func runMute(args []string, stdout, stderr io.Writer) int {
	target := "out"
	if len(args) > 0 {
		target = args[0]
	}

	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "mute: %v\n", err)
		return 1
	}
	defer conn.Close()

	switch target {
	case "out", "output":
		var muted bool
		if err := busCall(obj, daemon.DBusInterface+".ToggleOutputMute", 0).Store(&muted); err != nil {
			fmt.Fprintf(stderr, "mute output failed: %v\n", err)
			return 1
		}
		state := "UNMUTED"
		if muted {
			state = "MUTED"
		}
		fmt.Fprintf(stdout, "Output is now %s\n", state)
	case "mic", "microphone":
		var muted bool
		if err := busCall(obj, daemon.DBusInterface+".ToggleMicMute", 0).Store(&muted); err != nil {
			fmt.Fprintf(stderr, "mute mic failed: %v\n", err)
			return 1
		}
		state := "UNMUTED"
		if muted {
			state = "MUTED (Dual-Mute: Virtual + Physical)"
		}
		fmt.Fprintf(stdout, "Microphone is now %s\n", state)
	default:
		fmt.Fprintf(stderr, "unknown mute target %q (use 'out' or 'mic')\n", target)
		return 2
	}
	return 0
}

func runPanic(args []string, stdout, stderr io.Writer) int {
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "panic: %v\n", err)
		return 1
	}
	defer conn.Close()

	var panicActive bool
	if err := busCall(obj, daemon.DBusInterface+".TogglePanic", 0).Store(&panicActive); err != nil {
		fmt.Fprintf(stderr, "panic toggle failed: %v\n", err)
		return 1
	}

	if panicActive {
		fmt.Fprintln(stdout, "🚨 PANIC MUTE ACTIVE: All outputs and microphone muted.")
	} else {
		fmt.Fprintln(stdout, "✓ Exited Panic Mute: Restored prior channel mute states.")
	}
	return 0
}

func runListen(args []string, stdout, stderr io.Writer) int {
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "listen: %v\n", err)
		return 1
	}
	defer conn.Close()

	var active bool
	if len(args) > 0 {
		switch args[0] {
		case "on", "enable", "1", "true":
			if err := busCall(obj, daemon.DBusInterface+".SetMicMonitoring", 0, true).Store(&active); err != nil {
				fmt.Fprintf(stderr, "listen call failed: %v\n", err)
				return 1
			}
		case "off", "disable", "0", "false":
			if err := busCall(obj, daemon.DBusInterface+".SetMicMonitoring", 0, false).Store(&active); err != nil {
				fmt.Fprintf(stderr, "listen call failed: %v\n", err)
				return 1
			}
		case "toggle":
			if err := busCall(obj, daemon.DBusInterface+".ToggleMicMonitoring", 0).Store(&active); err != nil {
				fmt.Fprintf(stderr, "listen toggle failed: %v\n", err)
				return 1
			}
		default:
			fmt.Fprintf(stderr, "unknown argument %q (use 'on', 'off', or 'toggle')\n", args[0])
			return 2
		}
	} else {
		if err := busCall(obj, daemon.DBusInterface+".ToggleMicMonitoring", 0).Store(&active); err != nil {
			fmt.Fprintf(stderr, "listen toggle failed: %v\n", err)
			return 1
		}
	}

	if active {
		fmt.Fprintln(stdout, "🎧 Microphone monitoring ENABLED (listening with DSP filters).")
	} else {
		fmt.Fprintln(stdout, "Microphone monitoring DISABLED.")
	}
	return 0
}

func runRoute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: easyprofilectl route [akg|sp166|toggle]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "route: %v\n", err)
		return 1
	}
	defer conn.Close()

	dest := strings.ToLower(args[0])
	if dest == "toggle" {
		var active string
		if err := busCall(obj, daemon.DBusInterface+".ToggleDestination", 0).Store(&active); err != nil {
			fmt.Fprintf(stderr, "route toggle failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Switched route to: %s\n", strings.ToUpper(active))
		return 0
	}

	if err := busCall(obj, daemon.DBusInterface+".SelectDestination", 0, dest).Err; err != nil {
		fmt.Fprintf(stderr, "route switch failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Switched route to: %s\n", strings.ToUpper(dest))
	return 0
}

func runPreset(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: easyprofilectl preset [list|next|prev|base|<preset-name>]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "preset: %v\n", err)
		return 1
	}
	defer conn.Close()

	if args[0] == "list" {
		var raw string
		if err := busCall(obj, daemon.DBusInterface+".GetStatus", 0).Store(&raw); err != nil {
			fmt.Fprintf(stderr, "preset list failed: %v\n", err)
			return 1
		}
		var s daemon.DaemonStatus
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "Active preset: %s\n", s.ActivePreset)
		fmt.Fprintln(stdout, "Compatible presets:")
		for _, p := range s.AvailablePresets {
			marker := "  "
			if p == s.ActivePreset {
				marker = "* "
			}
			fmt.Fprintf(stdout, "  %s%s\n", marker, p)
		}
		return 0
	}

	if args[0] == "next" || args[0] == "prev" || args[0] == "previous" || args[0] == "base" {
		var raw string
		if err := busCall(obj, daemon.DBusInterface+".GetStatus", 0).Store(&raw); err != nil {
			fmt.Fprintf(stderr, "preset %s failed: %v\n", args[0], err)
			return 1
		}
		var s daemon.DaemonStatus
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(s.AvailablePresets) == 0 {
			fmt.Fprintln(stderr, "no compatible presets available")
			return 1
		}
		var target string
		if args[0] == "base" {
			target = s.AvailablePresets[0]
		} else {
			delta := 1
			if args[0] == "prev" || args[0] == "previous" {
				delta = -1
			}
			idx := 0
			for i, p := range s.AvailablePresets {
				if p == s.ActivePreset {
					idx = i
					break
				}
			}
			nextIdx := (idx + delta + len(s.AvailablePresets)) % len(s.AvailablePresets)
			target = s.AvailablePresets[nextIdx]
		}
		if err := busCall(obj, daemon.DBusInterface+".SelectPreset", 0, target).Err; err != nil {
			fmt.Fprintf(stderr, "select preset failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Applied preset: %s\n", target)
		return 0
	}

	presetName := strings.Join(args, " ")
	if err := busCall(obj, daemon.DBusInterface+".SelectPreset", 0, presetName).Err; err != nil {
		fmt.Fprintf(stderr, "select preset failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Applied preset: %s\n", presetName)
	return 0
}

func runReset(args []string, stdout, stderr io.Writer) int {
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintf(stderr, "reset: %v\n", err)
		return 1
	}
	defer conn.Close()

	if err := busCall(obj, daemon.DBusInterface+".ResetDefaults", 0).Err; err != nil {
		fmt.Fprintf(stderr, "reset failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "✓ Audio settings reset to daily baseline (output 95%, mic 100%, mic mute preserved).")
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "")
	dumpFile := fs.String("pw-dump-file", "", "")
	offline := fs.Bool("offline", false, "")
	defaultPresetDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		defaultPresetDir = filepath.Join(home, ".config", "easyeffects")
	}
	presetDir := fs.String("preset-dir", defaultPresetDir, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ins, err := inspect.Collect(ctx, inspect.Options{
		Identity:   inspect.DefaultIdentity(),
		Runner:     inspect.ReadOnlyRunner{Timeout: 5 * time.Second},
		PWDumpFile: *dumpFile,
		Offline:    *offline,
		PresetDir:  *presetDir,
	})
	if err != nil {
		fmt.Fprintln(stderr, "inspect:", err)
		return 1
	}
	if *asJSON {
		if err := inspect.RenderJSON(stdout, ins); err != nil {
			fmt.Fprintln(stderr, "inspect:", err)
			return 1
		}
	} else {
		inspect.RenderText(stdout, ins)
	}
	for _, f := range ins.Findings {
		if f.Severity == inspect.SevBlocker {
			return 3
		}
	}
	return 0
}
