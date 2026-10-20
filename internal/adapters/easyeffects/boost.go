package easyeffects

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/toru/easy-profile-app/internal/inspect"
)

const boostPlugin = "limiter#99"
const boostSchema = "com.github.wwmm.easyeffects.limiter:/com/github/wwmm/easyeffects/streamoutputs/limiter/99/"

func presetOverlayReady(requested, loaded string, chain, base []string) bool {
	return requested == loaded && (slices.Equal(chain, base) || slices.Equal(chain, append(slices.Clone(base), boostPlugin)))
}

// Native preset loading is asynchronous. Never attach the overlay to the old
// chain and then falsely assume it survived the requested preset's later load.
func (r *RealEasyEffects) restorePresetBoost(ctx context.Context, requested string) error {
	name, err := exec.CommandContext(ctx, "gsettings", "get", "com.github.wwmm.easyeffects", "last-used-output-preset").Output()
	if err != nil {
		return err
	}
	plugins, err := exec.CommandContext(ctx, "gsettings", "get", outputSchema, "plugins").Output()
	if err != nil {
		return err
	}
	base, err := r.presetPlugins(requested)
	if err != nil {
		return err
	}
	if !presetOverlayReady(requested, inspect.ParseGVariantString(string(name)), parsePlugins(string(plugins)), base) {
		return errors.New("waiting for requested preset before restoring boost")
	}
	return r.SetOutputBoost(ctx, true, r.boostPercent)
}

func boostParameters(percent int) map[string]string {
	gain := 60 * math.Log10(float64(max(100, min(112, percent)))/100)
	return map[string]string{
		"bypass": "false", "input-gain": strconv.FormatFloat(gain, 'f', 6, 64),
		"output-gain": "0.0", "threshold": "0.0", "gain-boost": "false",
		"external-sidechain": "false", "mode": "'Herm Thin'", "oversampling": "'Full x2(2L)'",
		"dithering": "'None'", "lookahead": "5.0", "attack": "1.0", "release": "50.0",
		"stereo-link": "100.0", "alr": "false",
	}
}

func sameBoostValue(actual, expected string) bool {
	a, ea := strconv.ParseFloat(actual, 64)
	b, eb := strconv.ParseFloat(expected, 64)
	if ea == nil && eb == nil {
		return math.Abs(a-b) < 0.00001
	}
	return actual == expected
}

func validateBoostSettings(values map[string]string, percent int) error {
	for key, expected := range boostParameters(percent) {
		if !sameBoostValue(values[key], expected) {
			return fmt.Errorf("boost limiter readback mismatch: %s", key)
		}
	}
	return nil
}

func (r *RealEasyEffects) boostSettings(ctx context.Context) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, "gsettings", "list-recursively", boostSchema).Output()
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.SplitN(line, " ", 3)
		if len(fields) == 3 {
			values[fields[1]] = strings.TrimSpace(fields[2])
		}
	}
	return values, nil
}

// configureBoost is tested with an in-memory backend. The public entry point
// supplies bounded commands and refuses host execution in test mode.
func configureBoost(enabled bool, percent int, chain, base []string, read func() (map[string]string, error), write func(string, string) error, setChain func([]string) error) error {
	if !enabled {
		if !slices.Contains(chain, boostPlugin) {
			return nil
		}
		// Remove extra gain before detaching the limiter.
		if err := write("input-gain", "0.0"); err != nil {
			return err
		}
		return setChain(slices.DeleteFunc(slices.Clone(chain), func(p string) bool { return p == boostPlugin }))
	}
	tail := append(slices.Clone(base), boostPlugin)
	if !slices.Equal(chain, base) && !slices.Equal(chain, tail) {
		return errors.New("boost refuses an unexpected preset chain")
	}
	values, err := read()
	if err != nil {
		return err
	}
	keys := make([]string, 0)
	parameters := boostParameters(percent)
	for key := range parameters {
		if key != "input-gain" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	keys = append(keys, "input-gain") // Configure the limiter before applying positive gain.
	for _, key := range keys {
		value := parameters[key]
		if !sameBoostValue(values[key], value) {
			if err := write(key, value); err != nil {
				return err
			}
		}
	}
	confirmed, err := read()
	if err != nil {
		return err
	}
	if err := validateBoostSettings(confirmed, percent); err != nil {
		return err
	}
	if !slices.Equal(chain, tail) {
		return setChain(tail)
	}
	return nil
}

func (r *RealEasyEffects) SetOutputBoost(ctx context.Context, enabled bool, percent int) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	if enabled && (percent < 0 || percent > 112) {
		return errors.New("boost level outside [0,112]")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	get := func(schema, key string) (string, error) {
		out, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
		return strings.TrimSpace(string(out)), err
	}
	set := func(schema, key, value string) error {
		out, err := exec.CommandContext(ctx, "gsettings", "set", schema, key, value).CombinedOutput()
		if err != nil {
			return fmt.Errorf("boost %s: %w: %s", key, err, out)
		}
		return nil
	}
	raw, err := get(outputSchema, "plugins")
	if err != nil {
		return err
	}
	chain := parsePlugins(raw)
	var base []string
	if enabled {
		if _, err := os.Stat("/usr/lib/lv2/lsp-plugins.lv2/sc_limiter_stereo.ttl"); err != nil {
			return fmt.Errorf("boost requires installed LSP stereo limiter: %w", err)
		}
		name, err := get("com.github.wwmm.easyeffects", "last-used-output-preset")
		if err != nil {
			return err
		}
		base, err = r.presetPlugins(inspect.ParseGVariantString(name))
		if err != nil {
			return err
		}
	}
	err = configureBoost(enabled, percent, chain, base,
		func() (map[string]string, error) { return r.boostSettings(ctx) },
		func(key, value string) error { return set(boostSchema, key, value) },
		func(plugins []string) error {
			quoted := make([]string, len(plugins))
			for i, p := range plugins {
				quoted[i] = strconv.Quote(p)
			}
			return set(outputSchema, "plugins", "["+strings.Join(quoted, ", ")+"]")
		})
	if err != nil {
		return err
	}
	raw, err = get(outputSchema, "plugins")
	if err != nil {
		return err
	}
	want := slices.DeleteFunc(slices.Clone(chain), func(p string) bool { return p == boostPlugin })
	if enabled {
		want = append(slices.Clone(base), boostPlugin)
	}
	if !slices.Equal(parsePlugins(raw), want) {
		return errors.New("boost chain readback mismatch")
	}
	r.boostEnabled, r.boostPercent = enabled, percent
	return nil
}
