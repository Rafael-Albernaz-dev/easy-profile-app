package pipewire

import (
	"context"
	"github.com/toru/easy-profile-app/internal/inspect"
	"testing"
)

func TestMigrationRequiresUniqueActiveApplicationStream(t *testing.T) {
	graph := &inspect.Graph{Nodes: map[int]*inspect.NodeInfo{1: {ID: 1, Name: "mic_42"}, 2: {ID: 2, Name: "dsp"}, 3: {ID: 3, Name: "capture", AppName: "Recorder", MediaClass: "Stream/Input/Audio"}}, Links: []inspect.LinkInfo{{ID: 10, OutNode: 1, InNode: 3, State: "active"}}}
	stream, virtual, err := migrationTargets(graph, "mic", "dsp", "Recorder")
	if err != nil || stream.ID != 3 || virtual.ID != 2 {
		t.Fatalf("%+v %+v %v", stream, virtual, err)
	}
	graph.Nodes[4] = &inspect.NodeInfo{ID: 4, Name: "capture2", AppName: "Recorder", MediaClass: "Stream/Input/Audio"}
	graph.Links = append(graph.Links, inspect.LinkInfo{ID: 11, OutNode: 1, InNode: 4, State: "active"})
	if _, _, err := migrationTargets(graph, "mic", "dsp", "Recorder"); err == nil {
		t.Fatal("ambiguous application stream accepted")
	}
	graph.Links[1].State = "paused"
	if _, _, err := migrationTargets(graph, "mic", "dsp", "Recorder"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	if err := NewRealPipeWire(0).MigrateRawMicStream(context.Background(), "mic", "dsp", "Recorder"); err != ErrTestModeBlocked {
		t.Fatal(err)
	}
}
