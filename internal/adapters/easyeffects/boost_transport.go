package easyeffects

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/toru/easy-profile-app/internal/inspect"
)

// Settings describe intent. A connected path through the native plugin nodes
// independently confirms that EasyEffects has finished rebuilding its graph.
func outputDSPReady(g *inspect.Graph, chain []string) bool {
	native, ok := inspect.OutputPluginPath(g, "easyeffects_sink", "")
	return ok && nativeChainMatches(native, chain)
}

func nativeChainMatches(native, chain []string) bool {
	expected := make([]string, len(chain))
	for i, p := range chain {
		expected[i] = strings.Split(p, "#")[0]
	}
	return slices.Equal(native, expected)
}

// ValidateOutputDSP reuses PipeWire's observation; no extra subprocess or timer.
func (r *RealEasyEffects) ValidateOutputDSP(preset string, native []string, enabled, idle bool) error {
	chain, err := r.presetPlugins(preset)
	if err != nil {
		return err
	}
	if enabled {
		chain = append(chain, boostPlugin)
	}
	if idle && len(native) == 0 {
		return nil
	}
	if !nativeChainMatches(native, chain) {
		return errors.New("native output DSP path differs from requested preset/Boost")
	}
	return nil
}

func waitBoostConfirmation(ctx context.Context, check func() error) error {
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := check()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("boost confirmation: %w (%v)", ctx.Err(), err)
		case <-tick.C:
		}
	}
}

func (r *RealEasyEffects) confirmOutputDSP(ctx context.Context, chain []string) error {
	raw, err := exec.CommandContext(ctx, "pw-dump").Output()
	if err != nil {
		return err
	}
	g, err := inspect.ParseGraph(raw)
	if err != nil {
		return err
	}
	if !outputDSPReady(g, chain) && !inspect.OutputIsIdle(g, "easyeffects_sink") {
		return errors.New("waiting for connected EasyEffects output DSP chain")
	}
	return nil
}
