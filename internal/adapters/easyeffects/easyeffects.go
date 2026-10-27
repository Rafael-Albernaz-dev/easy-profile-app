package easyeffects

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/toru/easy-profile-app/internal/inspect"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
)

var (
	ErrTestModeBlocked = errors.New("easyeffects real execution blocked in test mode (EASYPROFILE_TEST_MODE=1)")
)

// RealEasyEffects implements adapters.EasyEffectsPort by communicating with EasyEffects 7.1.6
// via CLI (easyeffects -l), GSettings (com.github.wwmm.easyeffects), and filesystem read of output presets.
// Invariants: Never modifies preset files (docs/RULES.md §1.3), blocks host execution when EASYPROFILE_TEST_MODE=1.
type RealEasyEffects struct {
	boostEnabled bool
	boostPercent int
	configDir    string
	timeout      time.Duration
}

// NewRealEasyEffects creates an EasyEffects adapter instance.
func NewRealEasyEffects(configDir string, timeout time.Duration) *RealEasyEffects {
	if configDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			configDir = filepath.Join(home, ".config", "easyeffects")
		}
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &RealEasyEffects{
		configDir: configDir,
		timeout:   timeout,
	}
}

var _ adapters.EasyEffectsPort = (*RealEasyEffects)(nil)

func (r *RealEasyEffects) checkTestMode() error {
	if _, present := os.LookupEnv("EASYPROFILE_TEST_MODE"); present {
		return ErrTestModeBlocked
	}
	return nil
}

// LoadPreset applies an output preset by name using easyeffects -l and ensures bypass is disabled.
func (r *RealEasyEffects) LoadPreset(ctx context.Context, presetName string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	if _, err := r.presetPlugins(presetName); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "easyeffects", "-l", presetName).CombinedOutput(); err != nil {
		return fmt.Errorf("load preset: %w: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "gsettings", "set", "com.github.wwmm.easyeffects", "bypass", "false").CombinedOutput(); err != nil {
		return fmt.Errorf("disable bypass: %w: %s", err, out)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active string
		var err error
		if r.boostEnabled {
			err = r.restorePresetBoost(ctx, presetName)
		}
		if err == nil {
			active, err = r.GetActivePreset(ctx)
		}
		if err == nil && active == presetName {
			chain, chainErr := r.presetPlugins(presetName)
			if chainErr == nil {
				if r.boostEnabled {
					chain = append(chain, boostPlugin)
				}
				err = r.confirmOutputDSP(ctx, chain)
				if err == nil {
					return nil
				}
			} else {
				err = chainErr
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("preset confirmation %q: %w (last read: %v)", presetName, ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

// GetActivePreset queries GSettings for the currently active output preset.
func (r *RealEasyEffects) GetActivePreset(ctx context.Context) (string, error) {
	if err := r.checkTestMode(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gsettings", "get", "com.github.wwmm.easyeffects", "last-used-output-preset")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gsettings get last-used-output-preset failed: %w (stderr: %s)", err, stderr.String())
	}

	raw := strings.TrimSpace(stdout.String())
	// gsettings returns single-quoted string: 'AKG K52 - Uso diario aprovado'
	preset := inspect.ParseGVariantString(raw)
	expected, err := r.presetPlugins(preset)
	if err != nil {
		return "", err
	}
	bypass, err := exec.CommandContext(ctx, "gsettings", "get", "com.github.wwmm.easyeffects", "bypass").Output()
	if err != nil || strings.TrimSpace(string(bypass)) != "false" {
		return "", fmt.Errorf("bypass readback is not false: %s (%v)", bypass, err)
	}
	plugins, err := exec.CommandContext(ctx, "gsettings", "get", "com.github.wwmm.easyeffects.streamoutputs", "plugins").Output()
	if err != nil {
		return "", err
	}
	if r.boostEnabled {
		expected = append(expected, boostPlugin)
		values, err := r.boostSettings(ctx)
		if err != nil {
			return "", err
		}
		if err := validateBoostSettings(values, r.boostPercent); err != nil {
			return "", err
		}
	}
	if !slices.Equal(parsePlugins(string(plugins)), expected) {
		return "", errors.New("active plugin chain differs from preset")
	}
	return preset, nil
}

// ListAvailablePresets inspects ~/.config/easyeffects/output/ and returns all preset names.
func (r *RealEasyEffects) ListAvailablePresets(ctx context.Context) ([]string, error) {
	if err := r.checkTestMode(); err != nil {
		return nil, err
	}

	outputDir := filepath.Join(r.configDir, "output")
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read easyeffects output dir: %w", err)
	}

	var presets []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") {
			presetName := strings.TrimSuffix(name, ".json")
			if _, err := r.presetPlugins(presetName); err == nil {
				presets = append(presets, presetName)
			}
		}
	}
	return presets, nil
}

// presetPlugins validates direction, schema and ordered DSP chain without writing a preset.
func (r *RealEasyEffects) presetPlugins(name string) ([]string, error) {
	if name == "" || strings.HasPrefix(name, "-") || filepath.Base(name) != name || strings.ContainsAny(name, "\x00/\\") {
		return nil, errors.New("invalid preset name")
	}
	if _, err := os.Lstat(filepath.Join(r.configDir, "input", name+".json")); err == nil {
		return nil, errors.New("input/output preset name collision")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	path := filepath.Join(r.configDir, "output", name+".json")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("preset is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var preset struct {
		Output struct {
			Plugins []string `json:"plugins_order"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &preset); err != nil {
		return nil, err
	}
	if slices.Contains(preset.Output.Plugins, boostPlugin) {
		return nil, errors.New("preset uses reserved Boost limiter instance")
	}
	if len(preset.Output.Plugins) == 0 {
		return nil, errors.New("empty output plugin chain")
	}
	return preset.Output.Plugins, nil
}
func parsePlugins(raw string) []string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "@as ")
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	result := []string{}
	for _, item := range strings.Split(raw, ",") {
		result = append(result, inspect.ParseGVariantString(strings.TrimSpace(item)))
	}
	return result
}

// Watch observes GSettings changes without polling or changing the user's settings.
func (r *RealEasyEffects) Watch(ctx context.Context, events chan<- struct{}) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	// Watch the root and both device schemas, including USB reconnect corrections.
	cmd := exec.CommandContext(ctx, "dconf", "watch", "/com/github/wwmm/easyeffects/")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	readErr := watchSettingsChanges(stdout, events)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(readErr, err)
}
