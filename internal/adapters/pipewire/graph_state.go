package pipewire

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/domain"
	"github.com/toru/easy-profile-app/internal/inspect"
)

// resolveMicrophone verifies USB identity and profile, never merely a recycled ID.
func resolveMicrophone(g *inspect.Graph, prefix string) (*inspect.NodeInfo, error) {
	var match *inspect.NodeInfo
	mic := domain.NewDefaultKA1333()
	for _, n := range g.Nodes {
		if n.Name != prefix && !strings.HasPrefix(n.Name, prefix+"_") {
			continue
		}
		device := g.Devices[n.DeviceID]
		if n.MediaClass != "Audio/Source" || n.ProfileName != mic.ProfileName || device == nil || !mic.MatchesUSBDevice(device.VendorID, device.ProductID, device.Serial) {
			continue
		}
		if match != nil {
			return nil, errors.New("ambiguous KA-1333 USB identity")
		}
		match = n
	}
	if match == nil {
		return nil, errors.New("KA-1333 USB identity unavailable")
	}
	return match, nil
}

func graphState(g *inspect.Graph, sink, source, physicalSink, micPrefix string) (adapters.GraphState, error) {
	state := adapters.GraphState{DefaultSink: g.Defaults["default.audio.sink"], ConfiguredSink: g.Defaults["default.configured.audio.sink"]}
	mic, err := resolveMicrophone(g, micPrefix)
	if err != nil {
		return state, err
	}
	state.PhysicalMicrophone = mic.Name
	found := map[string]*inspect.NodeInfo{}
	for _, node := range g.Nodes {
		if node.Name == sink || node.Name == source || node.Name == physicalSink {
			if found[node.Name] != nil {
				return state, fmt.Errorf("ambiguous endpoint %s", node.Name)
			}
			found[node.Name] = node
		}
	}
	if found[sink] == nil || found[source] == nil || found[physicalSink] == nil {
		return state, errors.New("DSP graph endpoints unavailable")
	}
	if found[sink].MediaClass != "Audio/Sink" || found[physicalSink].MediaClass != "Audio/Sink" || !strings.HasPrefix(found[source].MediaClass, "Audio/Source") {
		return state, errors.New("invalid DSP endpoint media class")
	}
	state.SinkMonitorVolumes = found[sink].MonitorChannelVolumes != nil && *found[sink].MonitorChannelVolumes
	state.SourceMonitorVolumes = found[source].MonitorChannelVolumes != nil && *found[source].MonitorChannelVolumes
	return state, nil
}

func (r *RealPipeWire) ReadGraphState(ctx context.Context, sink, source, physicalSink, micPrefix string) (adapters.GraphState, error) {
	g, err := r.graph(ctx)
	if err != nil {
		return adapters.GraphState{}, err
	}
	return graphState(g, sink, source, physicalSink, micPrefix)
}

func (r *RealPipeWire) EnsureDefaultSink(ctx context.Context, sink string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	g, err := r.graph(ctx)
	if err != nil {
		return err
	}
	if g.Defaults["default.audio.sink"] == sink && g.Defaults["default.configured.audio.sink"] == sink {
		return nil
	}
	id, err := r.FindNodeID(ctx, sink)
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "wpctl", "set-default", strconv.Itoa(id)).CombinedOutput(); err != nil {
		r.InvalidateCache()
		return fmt.Errorf("set DSP default sink: %w: %s", err, out)
	}
	g, err = r.graph(ctx)
	if err != nil {
		return err
	}
	if g.Defaults["default.audio.sink"] != sink || g.Defaults["default.configured.audio.sink"] != sink {
		return errors.New("default DSP sink readback mismatch")
	}
	return nil
}

func (r *RealPipeWire) EnsureDefaultSource(ctx context.Context, source string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	g, err := r.graph(ctx)
	if err != nil {
		return err
	}
	if g.Defaults["default.audio.source"] == source && g.Defaults["default.configured.audio.source"] == source {
		return nil
	}
	val := fmt.Sprintf(`{"name":"%s"}`, source)
	if out, err := exec.CommandContext(ctx, "pw-metadata", "-n", "default", "0", "default.configured.audio.source", val).CombinedOutput(); err != nil {
		r.InvalidateCache()
		return fmt.Errorf("set DSP default source: %w: %s", err, out)
	}
	return nil
}
