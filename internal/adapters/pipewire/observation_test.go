package pipewire

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func observationFixture(t *testing.T) *inspect.Graph {
	t.Helper()
	data, err := os.ReadFile("../../../test/fixtures/pw-dump-live-2026-10-18.json")
	if err != nil {
		t.Fatal(err)
	}
	g, err := inspect.ParseGraph(data)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestObservationOneFreshSnapshotMatchesIndependentChecks(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	g := observationFixture(t)
	r := NewRealPipeWire(0)
	reads := 0
	read := func(context.Context) (*inspect.Graph, error) { reads++; return g, nil }
	const sink = "easyeffects_sink"
	const source = "easyeffects_source"
	const physical = "alsa_output.pci-0000_00_1f.3.analog-stereo"
	const mic = "alsa_input.usb-MU900_KA-1333"
	obs, err := r.readObservation(context.Background(), sink, source, physical, mic, read)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := graphState(g, sink, source, physical, mic)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := rawMicStreams(g, mic)
	if err != nil {
		t.Fatal(err)
	}
	route, err := sinkPort(g, physical)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || obs.Graph != graph || !reflect.DeepEqual(raw, obs.RawMicStreams) || obs.SinkPort != route || obs.MicMonitoring != micMonitoring(g, source, sink) || !obs.MicMonitoringKnown {
		t.Fatalf("inconsistent snapshot: %+v, reads=%d", obs, reads)
	}
	g.Defaults["default.audio.sink"] = "external-change"
	next, err := r.readObservation(context.Background(), sink, source, physical, mic, read)
	if err != nil || reads != 2 || next.Graph.DefaultSink != "external-change" {
		t.Fatalf("stale observation reused: %+v %v", next, err)
	}
}

func TestObservationSnapshotFailurePreservesUnknownState(t *testing.T) {
	r := NewRealPipeWire(0)
	want := errors.New("snapshot unavailable")
	obs, err := r.readObservation(context.Background(), "sink", "source", "physical", "mic", func(context.Context) (*inspect.Graph, error) { return nil, want })
	if !errors.Is(err, want) || obs.GraphError == nil || obs.MicMonitoringKnown || obs.RawMicStreams != nil {
		t.Fatalf("failure reported as observed state: %+v %v", obs, err)
	}
}

func TestObservationPublicEntryBlocksRealCommands(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	t.Setenv("PATH", t.TempDir())
	if _, err := NewRealPipeWire(0).ReadObservation(context.Background(), "sink", "source", "physical", "mic"); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatal(err)
	}
}

func TestObservationFailureCannotHideLiveSharingTransport(t *testing.T) {
	f := newSharingRig(t)
	if err := f.r.startOutputSharing(context.Background(), "output"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.r.stopOutputSharing(context.Background()) })
	_, err := f.r.readObservation(context.Background(), "sink", "source", "physical", "mic", func(context.Context) (*inspect.Graph, error) { return nil, errors.New("graph read failed") })
	if err == nil {
		t.Fatal("failed graph read was hidden")
	}
	obs, _ := f.r.readObservation(context.Background(), "sink", "source", "physical", "mic", func(context.Context) (*inspect.Graph, error) { return nil, errors.New("still unavailable") })
	if !obs.Sharing.Active || !obs.Sharing.RestorePending || obs.MicMonitoringKnown {
		t.Fatal("failed read hid the live sharing transport")
	}
}
