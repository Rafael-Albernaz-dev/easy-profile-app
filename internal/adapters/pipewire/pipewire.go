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
	"sync"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/inspect"
)

var (
	ErrTestModeBlocked = errors.New("pipewire real execution blocked in test mode (EASYPROFILE_TEST_MODE=1)")
	ErrNodeNotFound    = errors.New("pipewire node not found")
)

// RealPipeWire implements adapters.PipeWirePort using PipeWire tools (pw-cli, wpctl).
// Adheres strictly to docs/RULES.md §1.4: Direct exec.Command execution, timeouts on all calls,
// and hermetic execution blocks when EASYPROFILE_TEST_MODE=1.
type RealPipeWire struct {
	sharing   *sharingSession // accessed only by the daemon's serialized worker
	sharingIO *sharingIO
	micPrefix string
	timeout   time.Duration

	mu              sync.RWMutex
	nodeIDCache     map[string]int
	monitorVolumes  map[string]bool
	cacheSerials    map[string]int
	cacheGeneration uint64
}

// NewRealPipeWire creates an instance with safe default timeouts.
func NewRealPipeWire(timeout time.Duration) *RealPipeWire {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &RealPipeWire{
		timeout:        timeout,
		micPrefix:      "alsa_input.usb-MU900_KA-1333",
		nodeIDCache:    make(map[string]int),
		monitorVolumes: make(map[string]bool),
		cacheSerials:   make(map[string]int),
	}
}

var _ adapters.PipeWirePort = (*RealPipeWire)(nil)

func (r *RealPipeWire) checkTestMode() error {
	if _, present := os.LookupEnv("EASYPROFILE_TEST_MODE"); present {
		return ErrTestModeBlocked
	}
	return nil
}

// InvalidateCache clears the ephemeral node ID and monitor.channel-volumes cache.
func (r *RealPipeWire) InvalidateCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodeIDCache = make(map[string]int)
	r.monitorVolumes = make(map[string]bool)
	r.cacheSerials = make(map[string]int)
	r.cacheGeneration++
}

// FindNodeID resolves a node name (e.g. "easyeffects_sink") to its ephemeral PipeWire ID.
func (r *RealPipeWire) FindNodeID(ctx context.Context, nodeName string) (int, error) {
	if err := r.checkTestMode(); err != nil {
		return 0, err
	}

	r.mu.RLock()
	if id, ok := r.nodeIDCache[nodeName]; ok && id > 0 {
		r.mu.RUnlock()
		return id, nil
	}
	r.mu.RUnlock()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		bCtx, cancel := context.WithTimeout(ctx, r.timeout)
		cmd := exec.CommandContext(bCtx, "pw-dump", "--no-colors")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			cancel()
			r.InvalidateCache()
			lastErr = fmt.Errorf("pw-dump failed: %w (stderr: %s)", err, stderr.String())
			continue
		}
		cancel()

		g, err := inspect.ParseGraph(stdout.Bytes())
		if err != nil {
			r.InvalidateCache()
			lastErr = fmt.Errorf("failed to parse pw-dump graph: %w", err)
			continue
		}

		r.mu.Lock()
		if id, ok := r.nodeIDCache[nodeName]; ok && id > 0 {
			r.mu.Unlock()
			return id, nil
		}
		if r.nodeIDCache == nil {
			r.nodeIDCache = make(map[string]int)
		}
		if r.cacheSerials == nil {
			r.cacheSerials = make(map[string]int)
		}

		for id, node := range g.Nodes {
			if node.Name != "" {
				r.nodeIDCache[node.Name] = id
				r.cacheSerials[node.Name] = node.Serial
			}
		}

		targetID := 0
		if nodeName == r.micPrefix {
			mic, err := resolveMicrophone(g, nodeName)
			if err != nil {
				r.mu.Unlock()
				lastErr = err
				continue
			}
			targetID = mic.ID
			r.nodeIDCache[nodeName] = targetID
			r.cacheSerials[nodeName] = mic.Serial
		} else {
			targetID = r.nodeIDCache[nodeName]
		}

		if targetID == 0 {
			r.mu.Unlock()
			lastErr = fmt.Errorf("%w: %q", ErrNodeNotFound, nodeName)
			continue
		}
		r.mu.Unlock()
		return targetID, nil
	}
	if lastErr != nil {
		return 0, lastErr
	}
	return 0, fmt.Errorf("%w: %q", ErrNodeNotFound, nodeName)
}

// EnsureMonitorChannelVolumes writes SPA Props.params and confirms a fresh readback.
func (r *RealPipeWire) EnsureMonitorChannelVolumes(ctx context.Context, nodeName string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	r.mu.RLock()
	if r.monitorVolumes[nodeName] {
		r.mu.RUnlock()
		return nil
	}
	r.mu.RUnlock()

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
		r.InvalidateCache()
		return fmt.Errorf("pw-cli set monitor.channel-volumes failed on node %d: %w (stderr: %s)", id, err, stderr.String())
	}
	g, err := r.graph(ctx)
	if err != nil {
		r.InvalidateCache()
		return err
	}
	node := g.Nodes[id]
	if node == nil || node.Name != nodeName || node.MonitorChannelVolumes == nil || !*node.MonitorChannelVolumes {
		r.InvalidateCache()
		return errors.New("monitor.channel-volumes readback is not true")
	}

	r.mu.Lock()
	if r.monitorVolumes == nil {
		r.monitorVolumes = make(map[string]bool)
	}
	r.monitorVolumes[nodeName] = true
	r.mu.Unlock()

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
		r.InvalidateCache()
		return fmt.Errorf("wpctl set-volume %d %s failed: %w (stderr: %s)", id, volStr, err, stderr.String())
	}
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", strconv.Itoa(id)).Output()
	if err != nil {
		r.InvalidateCache()
		return err
	}
	volume, _, err := parseVolumeReadback(out)
	if err != nil || !volumeReadbackMatches(volume, linearVolume) {
		r.InvalidateCache()
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
		r.InvalidateCache()
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
				r.InvalidateCache()
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

	g, err := r.graph(ctx)
	if err != nil {
		return nil, err
	}
	return rawMicStreams(g, micNodeName)
}

func rawMicStreams(g *inspect.Graph, micNodeName string) ([]string, error) {
	mic, err := resolveMicrophone(g, micNodeName)
	if err != nil {
		return nil, err
	}
	micID := mic.ID

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
		r.InvalidateCache()
		return nil, err
	}
	g, err := inspect.ParseGraph(out)
	if err != nil {
		r.InvalidateCache()
		return nil, err
	}
	r.observeGraph(g)
	return g, nil
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
		r.InvalidateCache()
		return false, err
	}
	_, muted, err := parseVolumeReadback(out)
	if err != nil {
		r.InvalidateCache()
		return false, err
	}
	return muted, nil
}
func (r *RealPipeWire) GetSinkPort(ctx context.Context, name string) (string, error) {
	g, err := r.graph(ctx)
	if err != nil {
		return "", err
	}
	return sinkPort(g, name)
}

func sinkPort(g *inspect.Graph, name string) (string, error) {
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
	r.InvalidateCache()
	defer r.InvalidateCache()
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
			isAudioObject := strings.HasSuffix(kind, ":Node") || strings.HasSuffix(kind, ":Link") || strings.HasSuffix(kind, ":Device")
			if isAudioObject || (kind == "" && string(obj.Info) == "null") {
				relevant = true
			}
			if (strings.HasSuffix(kind, ":Node") || strings.HasSuffix(kind, ":Device") || kind == "") && string(obj.Info) == "null" {
				r.invalidateObject(obj.ID)
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

// An incomplete/malformed readback is unknown, never an implicit unmuted snapshot.
func parseVolumeReadback(out []byte) (float64, bool, error) {
	fields := strings.Fields(string(out))
	if len(fields) < 2 || len(fields) > 3 || fields[0] != "Volume:" {
		return 0, false, errors.New("invalid wpctl volume/mute readback")
	}
	volume, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || math.IsNaN(volume) || math.IsInf(volume, 0) || volume < 0 {
		return 0, false, errors.New("invalid wpctl numeric volume")
	}
	if len(fields) == 3 && fields[2] != "[MUTED]" {
		return 0, false, errors.New("unknown wpctl mute state")
	}
	return volume, len(fields) == 3, nil
}

// wpctl prints two decimals: allow rounding error, never an entire 1% step.
func volumeReadbackMatches(cubic, linear float64) bool {
	return math.Abs(cubic-math.Cbrt(linear)) <= 0.005001
}

// SetMicrophonePrefix configures the only name allowed to match a reconnect suffix.
// Call it during construction, before the adapter is shared with the daemon.
func (r *RealPipeWire) SetMicrophonePrefix(prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.micPrefix = prefix
	r.nodeIDCache = make(map[string]int)
	r.monitorVolumes = make(map[string]bool)
	r.cacheSerials = make(map[string]int)
	r.cacheGeneration++
}

// invalidateObject is shared by watcher updates/removals; aliases are dropped together.
func (r *RealPipeWire) invalidateObject(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	invalidated := false
	for name, cached := range r.nodeIDCache {
		if cached == id {
			delete(r.nodeIDCache, name)
			delete(r.cacheSerials, name)
			delete(r.monitorVolumes, name)
			invalidated = true
		}
	}
	if invalidated {
		r.monitorVolumes = make(map[string]bool)
		r.cacheGeneration++
	}
}

// Fresh snapshots also detect changes missed while a watcher was disconnected.
func (r *RealPipeWire) observeGraph(g *inspect.Graph) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, id := range r.nodeIDCache {
		n := g.Nodes[id]
		matches := n != nil && (n.Name == name || (name == r.micPrefix && strings.HasPrefix(n.Name, name+"_")))
		if !matches || n.Serial != r.cacheSerials[name] {
			delete(r.nodeIDCache, name)
			delete(r.cacheSerials, name)
			delete(r.monitorVolumes, name)
			r.cacheGeneration++
		}
	}
	r.monitorVolumes = make(map[string]bool)
	for _, n := range g.Nodes {
		r.monitorVolumes[n.Name] = n.MonitorChannelVolumes != nil && *n.MonitorChannelVolumes
	}
}

// SetMicMonitoring links or unlinks filtered microphone audio (easyeffects_source) to playback (easyeffects_sink).
func (r *RealPipeWire) SetMicMonitoring(ctx context.Context, sourceNode, sinkNode string, enabled bool) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	linked, _ := r.GetMicMonitoring(ctx, sourceNode, sinkNode)
	if linked == enabled {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	pairs := [][2]string{
		{sourceNode + ":capture_FL", sinkNode + ":playback_FL"},
		{sourceNode + ":capture_FR", sinkNode + ":playback_FR"},
	}

	for _, pair := range pairs {
		var cmd *exec.Cmd
		if enabled {
			cmd = exec.CommandContext(ctx, "pw-link", pair[0], pair[1])
		} else {
			cmd = exec.CommandContext(ctx, "pw-link", "-d", pair[0], pair[1])
		}
		_ = cmd.Run()
	}

	after, err := r.GetMicMonitoring(ctx, sourceNode, sinkNode)
	if err != nil {
		return err
	}
	if after != enabled {
		return fmt.Errorf("mic monitoring state mismatch: wanted %v, got %v", enabled, after)
	}
	return nil
}

// GetMicMonitoring checks if active links exist between sourceNode and sinkNode.
func (r *RealPipeWire) GetMicMonitoring(ctx context.Context, sourceNode, sinkNode string) (bool, error) {
	if err := r.checkTestMode(); err != nil {
		return false, err
	}

	g, err := r.graph(ctx)
	if err != nil {
		return false, err
	}
	return micMonitoring(g, sourceNode, sinkNode), nil
}

func micMonitoring(g *inspect.Graph, sourceNode, sinkNode string) bool {
	var sourceID, sinkID int
	for id, n := range g.Nodes {
		if n.Name == sourceNode {
			sourceID = id
		}
		if n.Name == sinkNode {
			sinkID = id
		}
	}
	if sourceID == 0 || sinkID == 0 {
		return false
	}

	for _, link := range g.Links {
		if link.OutNode == sourceID && link.InNode == sinkID {
			return true
		}
	}
	return false
}
