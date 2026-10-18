package inspect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EasyEffectsState is what EasyEffects 7.1.6 reports through GSettings.
// Note: last-used-output-preset lives in the ROOT schema
// com.github.wwmm.easyeffects, not in .streamoutputs. Reading it tells what
// was last loaded; writing it would NOT load a preset.
type EasyEffectsState struct {
	LastOutputPreset       string `json:"last_used_output_preset"`
	LastInputPreset        string `json:"last_used_input_preset"`
	Bypass                 *bool  `json:"bypass"`
	ProcessAllOutputs      *bool  `json:"process_all_outputs"`
	ProcessAllInputs       *bool  `json:"process_all_inputs"`
	UseCubicVolumes        *bool  `json:"use_cubic_volumes"`
	OutputDevice           string `json:"output_device"`
	UseDefaultOutputDevice *bool  `json:"use_default_output_device"`
	InputDevice            string `json:"input_device"`
	UseDefaultInputDevice  *bool  `json:"use_default_input_device"`
}

// ParseGVariantString decodes the text form printed by `gsettings get` for a string key.
func ParseGVariantString(out string) string {
	s := strings.TrimSpace(out)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\'`, `'`)
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}

// ParseGVariantBool returns nil when the text is not a boolean.
func ParseGVariantBool(out string) *bool {
	switch strings.TrimSpace(out) {
	case "true":
		v := true
		return &v
	case "false":
		v := false
		return &v
	}
	return nil
}

func ProbeEasyEffects(ctx context.Context, r CommandRunner) (EasyEffectsState, error) {
	const root = "com.github.wwmm.easyeffects"
	var st EasyEffectsState
	var firstErr error
	get := func(schema, key string) string {
		out, err := r.Run(ctx, "gsettings", "get", schema, key)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return ""
		}
		return string(out)
	}
	st.LastOutputPreset = ParseGVariantString(get(root, "last-used-output-preset"))
	st.LastInputPreset = ParseGVariantString(get(root, "last-used-input-preset"))
	st.Bypass = ParseGVariantBool(get(root, "bypass"))
	st.ProcessAllOutputs = ParseGVariantBool(get(root, "process-all-outputs"))
	st.ProcessAllInputs = ParseGVariantBool(get(root, "process-all-inputs"))
	st.UseCubicVolumes = ParseGVariantBool(get(root, "use-cubic-volumes"))
	st.OutputDevice = ParseGVariantString(get(root+".streamoutputs", "output-device"))
	st.UseDefaultOutputDevice = ParseGVariantBool(get(root+".streamoutputs", "use-default-output-device"))
	st.InputDevice = ParseGVariantString(get(root+".streaminputs", "input-device"))
	st.UseDefaultInputDevice = ParseGVariantBool(get(root+".streaminputs", "use-default-input-device"))
	return st, firstErr
}

// PresetFile is one preset JSON found on disk (read-only).
type PresetFile struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
	SHA256    string `json:"sha256"`
	// Compatible lists destinations allowed by the name prefix rule (AKG / SP166 / Caixas).
	Compatible []string `json:"compatible_destinations,omitempty"`
}

// PresetInventory is the list of presets plus names present in BOTH directions.
// easyeffects -l searches input presets before output presets, so a shared
// name would load the wrong chain.
type PresetInventory struct {
	Dir        string       `json:"dir"`
	Presets    []PresetFile `json:"presets"`
	Collisions []string     `json:"name_collisions"`
}

// CompatibleDestinations applies the approved prefix rule. A preset matching
// no prefix is offered to no destination (safe default).
func CompatibleDestinations(presetName string) []string {
	upper := strings.ToUpper(strings.TrimSpace(presetName))
	var out []string
	for _, p := range []string{"AKG", "[AKG]"} {
		if strings.HasPrefix(upper, p) {
			out = append(out, DestAKG)
			break
		}
	}
	for _, p := range []string{"SP166", "[SP166]", "CAIXAS"} {
		if strings.HasPrefix(upper, p) {
			out = append(out, DestSP166)
			break
		}
	}
	return out
}

// ScanPresets lists and hashes preset files. It only opens files for reading.
func ScanPresets(baseDir string) (PresetInventory, error) {
	inv := PresetInventory{Dir: baseDir}
	seen := map[string][]string{}
	for _, direction := range []string{"input", "output"} {
		paths, err := filepath.Glob(filepath.Join(baseDir, direction, "*.json"))
		if err != nil {
			return inv, err
		}
		sort.Strings(paths)
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				return inv, err
			}
			sum := sha256.Sum256(data)
			name := strings.TrimSuffix(filepath.Base(path), ".json")
			pf := PresetFile{Name: name, Direction: direction, SHA256: hex.EncodeToString(sum[:])}
			if direction == "output" {
				pf.Compatible = CompatibleDestinations(name)
			}
			inv.Presets = append(inv.Presets, pf)
			seen[name] = append(seen[name], direction)
		}
	}
	for name, dirs := range seen {
		if len(dirs) > 1 {
			inv.Collisions = append(inv.Collisions, name)
		}
	}
	sort.Strings(inv.Collisions)
	return inv, nil
}
