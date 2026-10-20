package inspect

import "testing"

func TestCaptureTargetMetadataPreservesValueAndType(t *testing.T) {
	g, err := ParseGraph([]byte(`[{"id":7,"type":"PipeWire:Interface:Metadata","props":{"metadata.name":"default"},"metadata":[{"subject":22,"key":"target.object","type":"Spa:Id","value":"183"},{"subject":0,"key":"default.audio.sink","type":"Spa:String:JSON","value":{"name":"easyeffects_sink"}}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if g.CaptureTargets[22] != (TargetMetadata{Value: "183", Type: "Spa:Id"}) {
		t.Fatalf("lost capture target: %+v", g.CaptureTargets)
	}
	if g.Defaults["default.audio.sink"] != "easyeffects_sink" {
		t.Fatal("capture metadata changed defaults parsing")
	}
}
