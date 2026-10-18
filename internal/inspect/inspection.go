package inspect

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// GuardUnits are the production services that must stay active until EP-006.
var GuardUnits = []string{"ka1333-input-guard.service", "easyeffects-output-guard.service"}

// Options configures one inspection run.
type Options struct {
	Identity Identity
	Runner   CommandRunner
	// PWDumpFile, when set, is parsed instead of running pw-dump.
	PWDumpFile string
	// Offline skips every external command (ALSA, GSettings, systemd).
	Offline bool
	// PresetDir is the EasyEffects config dir; empty skips the preset scan.
	PresetDir string
}

// Inspection is the full read-only report.
type Inspection struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Source      string            `json:"source"`
	Identity    Identity          `json:"identity"`
	Graph       GraphAnalysis     `json:"graph"`
	Alsa        *AlsaState        `json:"alsa,omitempty"`
	EasyEffects *EasyEffectsState `json:"easyeffects,omitempty"`
	Presets     *PresetInventory  `json:"presets,omitempty"`
	Guards      map[string]string `json:"guards,omitempty"`
	ProbeErrors []string          `json:"probe_errors,omitempty"`
	Findings    []Finding         `json:"findings"`
	ParseTime   time.Duration     `json:"parse_time_ns"`
}

// Collect gathers every probe. A failing probe is recorded and the report
// continues: an inspector must show partial truth, never fake success.
func Collect(ctx context.Context, opts Options) (*Inspection, error) {
	ins := &Inspection{GeneratedAt: time.Now(), Identity: opts.Identity}

	var dump []byte
	var err error
	if opts.PWDumpFile != "" {
		ins.Source = opts.PWDumpFile
		dump, err = os.ReadFile(opts.PWDumpFile)
	} else if opts.Offline {
		return nil, fmt.Errorf("offline mode requires a pw-dump file")
	} else {
		ins.Source = "live pw-dump"
		dump, err = opts.Runner.Run(ctx, "pw-dump", "--no-colors")
	}
	if err != nil {
		return nil, fmt.Errorf("reading PipeWire graph: %w", err)
	}

	start := time.Now()
	g, err := ParseGraph(dump)
	if err != nil {
		return nil, err
	}
	ins.Graph = Analyze(g, opts.Identity)
	ins.ParseTime = time.Since(start)

	if !opts.Offline {
		cardIndex := "PCH"
		if alsa, err := ProbeAlsa(ctx, opts.Runner, cardIndex); err != nil {
			ins.ProbeErrors = append(ins.ProbeErrors, "ALSA: "+err.Error())
		} else {
			ins.Alsa = &alsa
		}
		ee, err := ProbeEasyEffects(ctx, opts.Runner)
		if err != nil {
			ins.ProbeErrors = append(ins.ProbeErrors, "EasyEffects/GSettings: "+err.Error())
		}
		ins.EasyEffects = &ee
		ins.Guards = map[string]string{}
		for _, unit := range GuardUnits {
			out, err := opts.Runner.Run(ctx, "systemctl", "--user", "is-active", unit)
			state := strings.TrimSpace(string(out))
			if err != nil && state == "" {
				state = "erro: " + err.Error()
			}
			ins.Guards[unit] = state
		}
	}

	if opts.PresetDir != "" {
		if inv, err := ScanPresets(opts.PresetDir); err != nil {
			ins.ProbeErrors = append(ins.ProbeErrors, "presets: "+err.Error())
		} else {
			ins.Presets = &inv
		}
	}

	ins.Findings = Evaluate(ins)
	return ins, nil
}
