package inspect

import "testing"

func TestMonitorChannelVolumesFromSPAParams(t *testing.T) {
	graph, err := ParseGraph([]byte(`[{"id":42,"type":"PipeWire:Interface:Node","info":{"props":{"node.name":"easyeffects_sink","monitor.channel-volumes":false},"params":{"Props":[{"params":["unrelated",1,"monitor.channel-volumes",true]}]}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes[42]
	if node.MonitorChannelVolumes == nil || !*node.MonitorChannelVolumes {
		t.Fatal("SPA Props.params not applied")
	}
}
