package pipewire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/inspect"
)

var (
	ErrTestModeBlocked = errors.New("pipewire real execution blocked in test mode (EASYPROFILE_TEST_MODE=1)")
	ErrNodeNotFound    = errors.New("pipewire node not found")
)

// RealPipeWire implements adapters.PipeWirePort using PipeWire tools (pw-cli, wpctl).
// Adheres strictly to RULES.md §1.4: Direct exec.Command execution, timeouts on all calls,
// and hermetic execution blocks when EASYPROFILE_TEST_MODE=1.
type RealPipeWire struct {
	micPrefix string
	timeout   time.Duration
}

// NewRealPipeWire creates an instance with safe default timeouts.
func NewRealPipeWire(timeout time.Duration) *RealPipeWire {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &RealPipeWire{
		timeout:   timeout,
		micPrefix: "alsa_input.usb-MU900_KA-1333",
	}
}

var _ adapters.PipeWirePort = (*RealPipeWire)(nil)

func (r *RealPipeWire) checkTestMode() error {
	if _, present := os.LookupEnv("EASYPROFILE_TEST_MODE"); present {
		return ErrTestModeBlocked
	}
	return nil
}

// FindNodeID resolves a node name (e.g. "easyeffects_sink") to its ephemeral PipeWire ID.
func (r *RealPipeWire) FindNodeID(ctx context.Context, nodeName string) (int, error) {
	if err := r.checkTestMode(); err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pw-dump", "--no-colors")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("pw-dump failed: %w (stderr: %s)", err, stderr.String())
	}

	g, err := inspect.ParseGraph(stdout.Bytes())
	if err != nil {
		return 0, fmt.Errorf("failed to parse pw-dump graph: %w", err)
	}
	targetID := 0
	for id, node := range g.Nodes {
		if node.Name == nodeName || (nodeName == r.micPrefix && strings.HasPrefix(node.Name, nodeName+"_")) {
			if targetID != 0 {
				return 0, fmt.Errorf("ambiguous node identity %q", nodeName)
			}
			targetID = id
		}
	}

	if targetID == 0 {
		return 0, fmt.Errorf("%w: %q", ErrNodeNotFound, nodeName)
	}
	return targetID, nil
}

// EnsureMonitorChannelVolumes writes SPA Props.params and confirms a fresh readback.
func (r *RealPipeWire) EnsureMonitorChannelVolumes(ctx context.Context, nodeName string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	id, err := r.FindNodeID(ctx, nodeName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Custom SPA properties live in Props.params, not as top-level fields.
	cmd := exec.CommandContext(ctx, "pw-cli", "s", strconv.Itoa(id), "Props", `{"params": ["monitor.channel-volumes", true]}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pw-cli set monitor.channel-volumes failed on node %d: %w (stderr: %s)", id, err, stderr.String())
	}
	g, err := r.graph(ctx)
	if err != nil {
		return err
	}
	node := g.Nodes[id]
	if node == nil || node.MonitorChannelVolumes == nil || !*node.MonitorChannelVolumes {
		return errors.New("monitor.channel-volumes readback is not true")
	}
	return nil
}

// SetNodeVolume sets volume on a node by name using wpctl.
func (r *RealPipeWire) SetNodeVolume(ctx context.Context, nodeName string, linearVolume float64) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	id, err := r.FindNodeID(ctx, nodeName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	volStr, err := wpctlVolume(linearVolume)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "wpctl", "set-volume", strconv.Itoa(id), volStr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wpctl set-volume %d %s failed: %w (stderr: %s)", id, volStr, err, stderr.String())
	}
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", strconv.Itoa(id)).Output()
	if err != nil {
		return err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "Volume:" {
		return errors.New("invalid volume readback")
	}
	volume, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || math.Abs(volume-math.Cbrt(linearVolume)) > 0.011 {
		return errors.New("volume readback mismatch")
	}
	return nil
}

// SetNodeMute sets mute flag (1 or 0) on a node by name using wpctl.
func (r *RealPipeWire) SetNodeMute(ctx context.Context, nodeName string, muted bool) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	id, err := r.FindNodeID(ctx, nodeName)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	flag := "0"
	if muted {
		flag = "1"
	}

	cmd := exec.CommandContext(ctx, "wpctl", "set-mute", strconv.Itoa(id), flag)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wpctl set-mute %d %s failed: %w (stderr: %s)", id, flag, err, stderr.String())
	}
	return nil
}

// SetSinkPort sets the active analog port on the physical sink using pactl.
func (r *RealPipeWire) SetSinkPort(ctx context.Context, sinkName string, portName string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	g, err := r.graph(ctx)
	if err != nil {
		return err
	}
	var deviceID int
	for _, n := range g.Nodes {
		if n.Name == sinkName {
			deviceID = n.DeviceID
		}
	}
	device := g.Devices[deviceID]
	if device == nil {
		return fmt.Errorf("device not found for sink %q", sinkName)
	}
	for _, route := range device.EnumRoutes {
		if route.Name == portName && route.Direction == "Output" {
			props := fmt.Sprintf(`{"index": %d, "device": %d, "save": false}`, route.Index, route.Device)
			ctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			if out, err := exec.CommandContext(ctx, "pw-cli", "s", strconv.Itoa(deviceID), "Route", props).CombinedOutput(); err != nil {
				return fmt.Errorf("switch route: %w: %s", err, out)
			}
			return nil
		}
	}
	return fmt.Errorf("route %q unavailable on %q", portName, sinkName)
}

// GetRawMicStreams inspects active links to identify any app stream directly reading the physical mic.
func (r *RealPipeWire) GetRawMicStreams(ctx context.Context, micNodeName string) ([]string, error) {
	if err := r.checkTestMode(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pw-dump", "--no-colors")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pw-dump failed: %w (stderr: %s)", err, stderr.String())
	}

	g, err := inspect.ParseGraph(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to parse pw-dump: %w", err)
	}

	// Find physical mic node ID (matching prefix/name)
	micID := 0
	for id, n := range g.Nodes {
		if strings.HasPrefix(n.Name, micNodeName) || n.Name == micNodeName {
			micID = id
			break
		}
	}
	if micID == 0 {
		return nil, nil // Mic not currently present in graph
	}

	// Find links where outNode == micID
	rawApps := []string{}
	seen := map[string]bool{}
	for _, link := range g.Links {
		if link.OutNode == micID && link.State == "active" {
			inNode, ok := g.Nodes[link.InNode]
			if !ok {
				continue
			}
			// Safe target nodes: easyeffects_source, or internal easyeffects filters
			if strings.HasPrefix(inNode.Name, "easyeffects") || strings.HasPrefix(inNode.Name, "ee_") {
				continue
			}
			appName := inNode.AppName
			if appName == "" {
				appName = inNode.Name
			}
			if !seen[appName] {
				rawApps = append(rawApps, appName)
				seen[appName] = true
			}
		}
	}

	sort.Strings(rawApps)
	return rawApps, nil
}

// graph always resolves current IDs; PipeWire may recycle IDs after a restart.
func (r *RealPipeWire) graph(ctx context.Context) (*inspect.Graph, error) {
	if err := r.checkTestMode(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pw-dump", "--no-colors").Output()
	if err != nil {
		return nil, err
	}
	return inspect.ParseGraph(out)
}
func (r *RealPipeWire) GetNodeMute(ctx context.Context, name string) (bool, error) {
	id, err := r.FindNodeID(ctx, name)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", strconv.Itoa(id)).Output()
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(string(out), "Volume:") {
		return false, errors.New("invalid wpctl mute readback")
	}
	return strings.Contains(string(out), "[MUTED]"), nil
}
func (r *RealPipeWire) GetSinkPort(ctx context.Context, name string) (string, error) {
	g, err := r.graph(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range g.Nodes {
		if n.Name == name {
			if d := g.Devices[n.DeviceID]; d != nil {
				for _, route := range d.ActiveRoutes {
					if route.Direction == "Output" {
						return route.Name, nil
					}
				}
			}
		}
	}
	return "", errors.New("active output route not found")
}

// Watch wakes reconciliation on graph events. The child is killed and reaped on cancellation.
// Incremental payloads are wakeups only; a fresh snapshot avoids treating deltas as a full graph.
func (r *RealPipeWire) Watch(ctx context.Context, events chan<- struct{}) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "pw-dump", "--monitor", "--no-colors")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	decoder := json.NewDecoder(stdout)
	types := map[int]string{}
	for {
		var event []struct {
			ID   int             `json:"id"`
			Type string          `json:"type"`
			Info json.RawMessage `json:"info"`
		}
		err = decoder.Decode(&event)
		if err != nil {
			break
		}
		relevant := false
		for _, obj := range event {
			kind := obj.Type
			if kind == "" {
				kind = types[obj.ID]
			}
			if obj.Type != "" {
				types[obj.ID] = kind
			}
			if strings.HasSuffix(kind, ":Node") || strings.HasSuffix(kind, ":Link") || strings.HasSuffix(kind, ":Device") {
				relevant = true
			}
			if string(obj.Info) == "null" {
				delete(types, obj.ID)
			}
		}
		if relevant {
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}
	if err != nil && err != io.EOF {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil && err != io.EOF {
		return err
	}
	return waitErr
}

// wpctl speaks the cubic UI scale; the domain/port contract speaks linear gain.
func wpctlVolume(linear float64) (string, error) {
	if math.IsNaN(linear) || math.IsInf(linear, 0) || linear < 0 || linear > 1 {
		return "", errors.New("volume must be finite linear gain in [0,1]")
	}
	return fmt.Sprintf("%.9f", math.Cbrt(linear)), nil
}

// SetMicrophonePrefix configures the only name allowed to match a reconnect suffix.
// Call it during construction, before the adapter is shared with the daemon.
func (r *RealPipeWire) SetMicrophonePrefix(prefix string) { r.micPrefix = prefix }
