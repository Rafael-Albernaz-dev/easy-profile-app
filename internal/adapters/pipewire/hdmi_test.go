package pipewire

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func TestHDMIOutputRequiresExactAvailableRoute(t *testing.T) {
	const card = "alsa_card.pci-0000_01_00.1"
	const sink = "alsa_output.pci-0000_01_00.1.hdmi-stereo-extra1"
	const route = "hdmi-output-1"

	data := `[
	{"id":47,"type":"PipeWire:Interface:Device","info":{"props":{"device.name":"alsa_card.pci-0000_01_00.1"},"params":{
		"EnumRoute":[
			{"name":"hdmi-output-0","direction":"Output","available":"yes","profiles":[1]},
			{"name":"hdmi-output-1","direction":"Output","available":"no","profiles":[2,5]}
		],
		"EnumProfile":[
			{"index":1,"name":"output:hdmi-stereo","available":"yes"},
			{"index":2,"name":"output:hdmi-stereo-extra1","available":"yes"}
		]
	}}},
	{"id":55,"type":"PipeWire:Interface:Node","info":{"props":{
		"device.id":47,"node.name":"alsa_output.pci-0000_01_00.1.hdmi-stereo","media.class":"Audio/Sink"
	}}}
	]`
	g, err := inspect.ParseGraph([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := hdmiOutput(g, card, sink, route); err == nil || !strings.Contains(err.Error(), "indisponível") {
		t.Fatalf("DisplayPort present with HDMI disconnected: %v", err)
	}
	g.Devices[47].EnumRoutes[1].Available = "yes"
	id, profile, err := hdmiOutput(g, card, sink, route)
	if err != nil || id != 47 || profile == nil || profile.Index != 2 {
		t.Fatalf("connected with inactive profile: id=%d profile=%+v err=%v", id, profile, err)
	}
	g.Devices[47].EnumRoutes[1].Profiles = []int{1, 5}
	if _, _, err := hdmiOutput(g, card, sink, route); err == nil {
		t.Fatal("profile not listed on the configured route must be rejected")
	}
	g.Nodes[67] = &inspect.NodeInfo{ID: 67, DeviceID: 47, Name: sink, MediaClass: "Audio/Sink"}
	if id, profile, err := hdmiOutput(g, card, sink, route); err != nil || id != 47 || profile != nil {
		t.Fatalf("ready sink must not trigger a profile switch: id=%d profile=%+v err=%v", id, profile, err)
	}
}

func TestPrepareHDMIOutputBlocksRealExecutionInTests(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	if err := NewRealPipeWire(0).PrepareHDMIOutput(context.Background(), "", "", ""); !errors.Is(err, ErrTestModeBlocked) {
		t.Fatalf("expected test-mode block, got %v", err)
	}
}
