package easyeffects

import (
	"context"
	"encoding/xml"
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
const installedLimiterSchemaPath = "/usr/share/glib-2.0/schemas/com.github.wwmm.easyeffects.limiter.gschema.xml"

type limiterKeySpec struct {
	kind   string // "b", "d", "s"
	minVal float64
	maxVal float64
	enums  []string
}

// limiterSchemaSpecs reflects the exact types, ranges, and allowed enums of
// com.github.wwmm.easyeffects.limiter in EasyEffects 7.1.6.
var limiterSchemaSpecs = map[string]limiterKeySpec{
	"bypass":             {kind: "b"},
	"input-gain":         {kind: "d", minVal: -36.0, maxVal: 36.0},
	"output-gain":        {kind: "d", minVal: -36.0, maxVal: 36.0},
	"threshold":          {kind: "d", minVal: -48.0, maxVal: 0.0},
	"gain-boost":         {kind: "b"},
	"external-sidechain": {kind: "b"},
	"mode": {kind: "s", enums: []string{
		"Herm Thin", "Herm Wide", "Herm Tail", "Herm Duck",
		"Exp Thin", "Exp Wide", "Exp Tail", "Exp Duck",
		"Line Thin", "Line Wide", "Line Tail", "Line Duck",
	}},
	"oversampling": {kind: "s", enums: []string{
		"None",
		"Half x2(2L)", "Half x2(3L)", "Half x3(2L)", "Half x3(3L)", "Half x4(2L)", "Half x4(3L)", "Half x6(2L)", "Half x6(3L)", "Half x8(2L)", "Half x8(3L)",
		"Full x2(2L)", "Full x2(3L)", "Full x3(2L)", "Full x3(3L)", "Full x4(2L)", "Full x4(3L)", "Full x6(2L)", "Full x6(3L)", "Full x8(2L)", "Full x8(3L)",
	}},
	"dithering": {kind: "s", enums: []string{
		"None", "7bit", "8bit", "11bit", "12bit", "15bit", "16bit", "23bit", "24bit",
	}},
	"lookahead":   {kind: "d", minVal: 0.1, maxVal: 20.0},
	"attack":      {kind: "d", minVal: 0.25, maxVal: 20.0},
	"release":     {kind: "d", minVal: 0.25, maxVal: 20.0},
	"stereo-link": {kind: "d", minVal: 0.0, maxVal: 100.0},
	"alr":         {kind: "b"},
}

func validateBoostParameters(parameters map[string]string) error {
	for key, spec := range limiterSchemaSpecs {
		val, exists := parameters[key]
		if !exists {
			return fmt.Errorf("missing parameter for schema key: %s", key)
		}
		switch spec.kind {
		case "b":
			if val != "true" && val != "false" {
				return fmt.Errorf("parameter %s must be boolean, got %q", key, val)
			}
		case "d":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("parameter %s must be float, got %q: %w", key, val, err)
			}
			if math.IsNaN(f) || math.IsInf(f, 0) || f < spec.minVal || f > spec.maxVal {
				return fmt.Errorf("parameter %s out of schema range [%g, %g]: %g", key, spec.minVal, spec.maxVal, f)
			}
		case "s":
			unquoted := val
			if strings.HasPrefix(unquoted, "'") && strings.HasSuffix(unquoted, "'") && len(unquoted) >= 2 {
				unquoted = unquoted[1 : len(unquoted)-1]
			}
			if len(spec.enums) > 0 && !slices.Contains(spec.enums, unquoted) {
				return fmt.Errorf("parameter %s value %q not in allowed enums", key, unquoted)
			}
		}
	}
	return nil
}

type limiterSchemaListXML struct {
	XMLName xml.Name                 `xml:"schemalist"`
	Enums   []limiterEnumXML         `xml:"enum"`
	Schemas []limiterSchemaRecordXML `xml:"schema"`
}

type limiterEnumXML struct {
	ID     string                `xml:"id,attr"`
	Values []limiterEnumValueXML `xml:"value"`
}

type limiterEnumValueXML struct {
	Nick  string `xml:"nick,attr"`
	Value string `xml:"value,attr"`
}

type limiterSchemaRecordXML struct {
	ID   string                `xml:"id,attr"`
	Keys []limiterKeyRecordXML `xml:"key"`
}

type limiterKeyRecordXML struct {
	Name    string           `xml:"name,attr"`
	Type    string           `xml:"type,attr"`
	Enum    string           `xml:"enum,attr"`
	Range   *limiterRangeXML `xml:"range"`
	Default string           `xml:"default"`
}

type limiterRangeXML struct {
	Min string `xml:"min,attr"`
	Max string `xml:"max,attr"`
}

func validateInstalledLimiterSchema(schemaPath string, parameters map[string]string) error {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	var list limiterSchemaListXML
	if err := xml.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parsing limiter schema XML: %w", err)
	}
	enumMap := make(map[string][]string)
	for _, e := range list.Enums {
		var nicks []string
		for _, v := range e.Values {
			nicks = append(nicks, v.Nick)
		}
		enumMap[e.ID] = nicks
	}
	keyMap := make(map[string]limiterKeyRecordXML)
	for _, s := range list.Schemas {
		if s.ID == "com.github.wwmm.easyeffects.limiter" {
			for _, k := range s.Keys {
				keyMap[k.Name] = k
			}
		}
	}
	for key, val := range parameters {
		k, found := keyMap[key]
		if !found {
			return fmt.Errorf("key %q not found in installed limiter schema", key)
		}
		if k.Type == "b" {
			if val != "true" && val != "false" {
				return fmt.Errorf("key %q expected boolean, got %q", key, val)
			}
		} else if k.Type == "d" {
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("key %q expected finite double, got %q", key, val)
			}
			if k.Range != nil {
				minVal, errMin := strconv.ParseFloat(k.Range.Min, 64)
				maxVal, errMax := strconv.ParseFloat(k.Range.Max, 64)
				if errMin != nil || errMax != nil {
					return fmt.Errorf("invalid installed range for %s", key)
				}
				if errMin == nil && errMax == nil {
					if f < minVal || f > maxVal {
						return fmt.Errorf("key %q value %g outside schema range [%g, %g]", key, f, minVal, maxVal)
					}
				}
			}
		} else if k.Enum != "" {
			nicks, ok := enumMap[k.Enum]
			if !ok {
				return fmt.Errorf("enum %q for key %q not found in schema", k.Enum, key)
			}
			unquoted := val
			if strings.HasPrefix(unquoted, "'") && strings.HasSuffix(unquoted, "'") && len(unquoted) >= 2 {
				unquoted = unquoted[1 : len(unquoted)-1]
			}
			if !slices.Contains(nicks, unquoted) {
				return fmt.Errorf("key %q value %q not allowed in enum %q", key, unquoted, k.Enum)
			}
		} else if k.Type != "s" {
			return fmt.Errorf("unsupported installed schema type for %s: %s", key, k.Type)
		}
	}
	return nil
}

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
	gain := 60 * math.Log10(float64(max(100, min(150, percent)))/100)
	// EasyEffects 7.1.6 limiter schema defines release range [0.25, 20.0] with default 5.0.
	// alr-release (default 50.0) only applies when alr is true.
	// Use the longest fixed release allowed by this schema (20 ms) to reduce
	// rapid gain modulation. No oversampling keeps the temporary DSP stage lighter.
	// Audible quality still requires an accompanied hardware comparison.
	return map[string]string{
		"bypass": "false", "input-gain": strconv.FormatFloat(gain, 'f', 6, 64),
		"output-gain": "0.0", "threshold": "0.0", "gain-boost": "false",
		"external-sidechain": "false", "mode": "'Herm Thin'", "oversampling": "'None'",
		"dithering": "'None'", "lookahead": "5.0", "attack": "1.0", "release": "20.0",
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

func pluginListValue(plugins []string) string {
	quoted := make([]string, len(plugins))
	for i, p := range plugins {
		quoted[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
func rollbackBoostSettings(modified map[string]string, base []string, write func(string, string) error, setChain func([]string) error) error {
	errs := []error{write("input-gain", "0.0")}
	keys := make([]string, 0, len(modified))
	for k := range modified {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if k != "input-gain" && modified[k] != "" {
			errs = append(errs, write(k, modified[k]))
		}
	}
	errs = append(errs, setChain(base))
	return errors.Join(errs...)
}

// configureBoost is tested with an in-memory backend. The public entry point
// supplies bounded commands and refuses host execution in test mode.
func configureBoost(enabled bool, percent int, chain, base []string, read func() (map[string]string, error), write func(string, string) error, setChain func([]string) error, cleanup ...func(map[string]string, []string) error) error {
	if !enabled {
		if !slices.Contains(chain, boostPlugin) {
			return nil
		}
		// Remove extra gain before detaching the limiter.
		if err := write("input-gain", "0.0"); err != nil {
			return err
		}
		confirmed, err := read()
		if err != nil {
			return err
		}
		if !sameBoostValue(confirmed["input-gain"], "0.0") {
			return errors.New("boost gain removal not confirmed")
		}
		return setChain(slices.DeleteFunc(slices.Clone(chain), func(p string) bool { return p == boostPlugin }))
	}
	tail := append(slices.Clone(base), boostPlugin)
	if !slices.Equal(chain, base) && !slices.Equal(chain, tail) {
		return errors.New("boost refuses an unexpected preset chain")
	}
	parameters := boostParameters(percent)
	if err := validateBoostParameters(parameters); err != nil {
		return fmt.Errorf("boost parameter pre-validation failed: %w", err)
	}
	values, err := read()
	if err != nil {
		return err
	}
	if err := validateBoostParameters(values); err != nil {
		return fmt.Errorf("invalid limiter snapshot before mutation: %w", err)
	}
	keys := make([]string, 0)
	for key := range parameters {
		if key != "input-gain" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	keys = append(keys, "input-gain") // Configure the limiter before applying positive gain.

	modified := make(map[string]string)
	rollback := func(origErr error) error {
		if len(cleanup) > 0 {
			return errors.Join(origErr, cleanup[0](modified, base))
		}
		return errors.Join(origErr, rollbackBoostSettings(modified, base, write, setChain))
	}

	for _, key := range keys {
		value := parameters[key]
		if !sameBoostValue(values[key], value) {
			oldVal := values[key]
			modified[key] = oldVal // A failed write can still have reached the backend.
			if err := write(key, value); err != nil {
				return rollback(err)
			}
		}
	}
	confirmed, err := read()
	if err != nil {
		return rollback(err)
	}
	if err := validateBoostSettings(confirmed, percent); err != nil {
		return rollback(err)
	}
	if !slices.Equal(chain, tail) {
		if err := setChain(tail); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (r *RealEasyEffects) SetOutputBoost(ctx context.Context, enabled bool, percent int) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	if enabled && (percent < 0 || percent > 150) {
		return errors.New("boost level outside [0,150]")
	}
	if enabled {
		parameters := boostParameters(percent)
		if err := validateBoostParameters(parameters); err != nil {
			return fmt.Errorf("boost parameter pre-validation failed: %w", err)
		}
		if err := validateInstalledLimiterSchema(installedLimiterSchemaPath, parameters); err != nil {
			return fmt.Errorf("boost installed schema validation failed: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	get := func(schema, key string) (string, error) {
		out, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
		return strings.TrimSpace(string(out)), err
	}
	setWithContext := func(commandCtx context.Context, schema, key, value string) error {
		out, err := exec.CommandContext(commandCtx, "gsettings", "set", schema, key, value).CombinedOutput()
		if err != nil {
			return fmt.Errorf("boost %s: %w: %s", key, err, out)
		}
		return nil
	}
	set := func(schema, key, value string) error { return setWithContext(ctx, schema, key, value) }
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
		func(plugins []string) error { return set(outputSchema, "plugins", pluginListValue(plugins)) },
		func(modified map[string]string, base []string) error {
			safe, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			return rollbackBoostSettings(modified, base, func(k, v string) error { return setWithContext(safe, boostSchema, k, v) }, func(p []string) error { return setWithContext(safe, outputSchema, "plugins", pluginListValue(p)) })
		})
	if err != nil {
		return err
	}
	want := slices.DeleteFunc(slices.Clone(chain), func(p string) bool { return p == boostPlugin })
	if enabled {
		want = append(slices.Clone(base), boostPlugin)
	}
	if err := waitBoostConfirmation(ctx, func() error {
		raw, err := get(outputSchema, "plugins")
		if err != nil {
			return err
		}
		if !slices.Equal(parsePlugins(raw), want) {
			return errors.New("waiting for Boost chain readback")
		}
		if enabled != r.boostEnabled || !slices.Equal(chain, want) {
			return r.confirmOutputDSP(ctx, want)
		}
		return nil
	}); err != nil {
		return err
	}

	r.boostEnabled, r.boostPercent = enabled, percent
	return nil
}
