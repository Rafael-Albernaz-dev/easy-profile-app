package inspect

import "testing"

func TestIdleExceptionNeverTreatsUnknownOrActiveAsProof(t *testing.T) {
	g := &Graph{Nodes: map[int]*NodeInfo{1: {ID: 1, Name: "easyeffects_sink", State: "suspended", MediaClass: "Audio/Sink"}}}
	if !OutputIsIdle(g, "easyeffects_sink") {
		t.Fatal("explicit idle rejected")
	}
	g.Nodes[1].State = ""
	if OutputIsIdle(g, "easyeffects_sink") {
		t.Fatal("unknown assumed idle")
	}
	g.Nodes[1].State = "running"
	if OutputIsIdle(g, "easyeffects_sink") {
		t.Fatal("running assumed idle")
	}
	g.Nodes[1].State = "suspended"
	g.Links = []LinkInfo{{InNode: 1, OutNode: 2, State: "active"}}
	if OutputIsIdle(g, "easyeffects_sink") {
		t.Fatal("active input bypassed DSP proof")
	}
}

func TestParsePreservesNativeIdleState(t *testing.T) {
	g, err := ParseGraph([]byte(`[{"id":1,"type":"PipeWire:Interface:Node","info":{"state":"suspended","props":{"node.name":"easyeffects_sink","media.class":"Audio/Sink"}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[1].State != "suspended" || !OutputIsIdle(g, "easyeffects_sink") {
		t.Fatal("idle state lost in parser")
	}
}
