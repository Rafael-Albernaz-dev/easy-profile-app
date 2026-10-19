package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigCalibratedExampleAndUnsafeOverrides(t *testing.T) {
	cfg, err := LoadConfig("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	ctrl, err := cfg.Controller()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl.OutputPhysicalCeiling().Percent() != 97 {
		t.Fatal("lost AKG ceiling")
	}
	for _, tc := range []string{`{"destinations":[]}`, `{"unknown":true}`, `{"stabilization_ms":1}`, `{"virtual_sink":"easyeffects_source"}`, `{"alsa_card":"-PCH"}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("unsafe config accepted:", tc)
		}
	}
	cfg.Destinations[0].PhysicalCeilingPct = 100
	if err := cfg.Validate(); err == nil {
		t.Fatal("AKG ceiling above calibration accepted")
	}
}
