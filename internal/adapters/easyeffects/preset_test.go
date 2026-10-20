package easyeffects

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPresetValidationRejectsCollisionAndPreservesFile(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"input", "output"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte(`{"output":{"plugins_order":["equalizer#0","limiter#0"]}}`)
	path := filepath.Join(root, "output", "AKG - Rock.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	real := NewRealEasyEffects(root, 0)
	plugins, err := real.presetPlugins("AKG - Rock")
	if err != nil || !slices.Equal(plugins, []string{"equalizer#0", "limiter#0"}) {
		t.Fatalf("chain %v %v", plugins, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(data) {
		t.Fatal("preset modified")
	}
	if err := os.WriteFile(filepath.Join(root, "input", "AKG - Rock.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := real.presetPlugins("AKG - Rock"); err == nil {
		t.Fatal("direction collision accepted")
	}
	for _, name := range []string{"../AKG", "-preset", "", "AKG/../../input"} {
		if _, err := real.presetPlugins(name); err == nil {
			t.Fatal("invalid name accepted", name)
		}
	}
}
func TestParseOrderedGSettingsPlugins(t *testing.T) {
	if got := parsePlugins("['equalizer#0', 'limiter#0']"); !slices.Equal(got, []string{"equalizer#0", "limiter#0"}) {
		t.Fatal(got)
	}
	if got := parsePlugins("@as []"); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestReservedBoostInstanceCannotComeFromPresetFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "output"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"output":{"plugins_order":["equalizer#0","limiter#99"]}}`)
	path := filepath.Join(root, "output", "AKG - Reserved.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRealEasyEffects(root, 0).presetPlugins("AKG - Reserved"); err == nil {
		t.Fatal("reserved limiter collision accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(data) {
		t.Fatal("preset rewritten")
	}
}
