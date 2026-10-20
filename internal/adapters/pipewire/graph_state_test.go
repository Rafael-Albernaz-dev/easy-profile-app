package pipewire

import (
	"context"
	"os"
	"testing"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func TestDSPGraphUsesUSBIdentityAndRejectsAmbiguity(t *testing.T) {
	data, err := os.ReadFile("../../../test/fixtures/pw-dump-live-2026-10-18.json")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := inspect.ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	const micPrefix = "alsa_input.usb-MU900_KA-1333"
	state, err := graphState(graph, "easyeffects_sink", "easyeffects_source", "alsa_output.pci-0000_00_1f.3.analog-stereo", micPrefix)
	if err != nil || state.PhysicalMicrophone == "" {
		t.Fatal("real fixture identity not resolved", err)
	}
	mic, err := resolveMicrophone(graph, micPrefix)
	if err != nil {
		t.Fatal(err)
	}
	copy := *mic
	copy.ID = 99999
	graph.Nodes[copy.ID] = &copy
	if _, err := resolveMicrophone(graph, micPrefix); err == nil {
		t.Fatal("ambiguous microphone accepted")
	}
	delete(graph.Nodes, copy.ID)
	graph.Devices[mic.DeviceID].Serial = "different USB hardware"
	if _, err := resolveMicrophone(graph, micPrefix); err == nil {
		t.Fatal("name prefix bypassed USB identity")
	}
}

func TestCacheInvalidationOnGraphChangesAndRecycledID(t *testing.T) {
	real := NewRealPipeWire(0)
	seed := func() {
		real.nodeIDCache["easyeffects_sink"] = 197
		real.cacheSerials["easyeffects_sink"] = 1000
		real.monitorVolumes["easyeffects_sink"] = true
	}
	seed()
	real.invalidateObject(197)
	if len(real.nodeIDCache) != 0 || real.monitorVolumes["easyeffects_sink"] {
		t.Fatal("watcher retained removed node")
	}
	seed()
	real.observeGraph(&inspect.Graph{Nodes: map[int]*inspect.NodeInfo{197: {ID: 197, Name: "other-app", Serial: 1001}}})
	if len(real.nodeIDCache) != 0 || real.monitorVolumes["easyeffects_sink"] {
		t.Fatal("recycled ID retained")
	}
	seed()
	disabled := false
	real.observeGraph(&inspect.Graph{Nodes: map[int]*inspect.NodeInfo{197: {ID: 197, Name: "easyeffects_sink", Serial: 1000, MonitorChannelVolumes: &disabled}}})
	if real.monitorVolumes["easyeffects_sink"] {
		t.Fatal("fresh readback failed to invalidate monitor confirmation")
	}
	real.observeGraph(&inspect.Graph{Nodes: map[int]*inspect.NodeInfo{}})
	if len(real.nodeIDCache) != 0 || len(real.monitorVolumes) != 0 {
		t.Fatal("snapshot retained absent nodes")
	}
}

func TestGraphAndDefaultOperationsCannotEscapeTestMode(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	t.Setenv("PATH", t.TempDir())
	real := NewRealPipeWire(0)
	if _, err := real.ReadGraphState(context.Background(), "sink", "source", "physical", "mic"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
	if err := real.EnsureDefaultSink(context.Background(), "sink"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
	if err := real.EnsureDefaultSource(context.Background(), "source"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
}

func TestGraphStateIncludesEffectiveAndConfiguredInputAndOutput(t *testing.T) {
	g := observationFixture(t)
	g.Defaults["default.audio.source"] = "effective-input"
	g.Defaults["default.configured.audio.source"] = "configured-input"
	g.Defaults["default.audio.sink"] = "effective-output"
	g.Defaults["default.configured.audio.sink"] = "configured-output"
	state, err := graphState(g, "easyeffects_sink", "easyeffects_source", "alsa_output.pci-0000_00_1f.3.analog-stereo", "alsa_input.usb-MU900_KA-1333")
	if err != nil || state.DefaultSource != "effective-input" || state.ConfiguredSource != "configured-input" || state.DefaultSink != "effective-output" || state.ConfiguredSink != "configured-output" {
		t.Fatalf("default values lost or confused: %+v %v", state, err)
	}
	delete(g.Defaults, "default.audio.source")
	state, err = graphState(g, "easyeffects_sink", "easyeffects_source", "alsa_output.pci-0000_00_1f.3.analog-stereo", "alsa_input.usb-MU900_KA-1333")
	if err != nil || state.DefaultSource != "" || state.ConfiguredSource != "configured-input" {
		t.Fatal("missing effective default fabricated from configured value", state, err)
	}
}
