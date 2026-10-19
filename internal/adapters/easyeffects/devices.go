package easyeffects

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/toru/easy-profile-app/internal/adapters"
	"github.com/toru/easy-profile-app/internal/inspect"
)

const inputSchema = "com.github.wwmm.easyeffects.streaminputs"
const outputSchema = "com.github.wwmm.easyeffects.streamoutputs"

func (r *RealEasyEffects) ReadDevices(ctx context.Context) (adapters.DSPDevices, error) {
	if err := r.checkTestMode(); err != nil {
		return adapters.DSPDevices{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	get := func(schema, key string) (string, error) {
		out, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
		return strings.TrimSpace(string(out)), err
	}
	input, e1 := get(inputSchema, "input-device")
	output, e2 := get(outputSchema, "output-device")
	defaultInput, e3 := get(inputSchema, "use-default-input-device")
	defaultOutput, e4 := get(outputSchema, "use-default-output-device")
	if err := errors.Join(e1, e2, e3, e4); err != nil {
		return adapters.DSPDevices{}, err
	}
	in, out := inspect.ParseGVariantBool(defaultInput), inspect.ParseGVariantBool(defaultOutput)
	if in == nil || out == nil {
		return adapters.DSPDevices{}, errors.New("invalid device selection readback")
	}
	return adapters.DSPDevices{Input: inspect.ParseGVariantString(input), Output: inspect.ParseGVariantString(output), UseDefaultInput: *in, UseDefaultOutput: *out}, nil
}

type deviceChange struct{ schema, key, value string }

// Build only necessary writes so our watcher does not feed its own reconciliation loop.
func deviceChanges(current adapters.DSPDevices, output, input string) []deviceChange {
	var changes []deviceChange
	if current.UseDefaultInput {
		changes = append(changes, deviceChange{inputSchema, "use-default-input-device", "false"})
	}
	if current.UseDefaultOutput {
		changes = append(changes, deviceChange{outputSchema, "use-default-output-device", "false"})
	}
	if current.Input != input {
		changes = append(changes, deviceChange{inputSchema, "input-device", strconv.Quote(input)})
	}
	if current.Output != output {
		changes = append(changes, deviceChange{outputSchema, "output-device", strconv.Quote(output)})
	}
	return changes
}

func (r *RealEasyEffects) EnsureDevices(ctx context.Context, output, input string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	if input == "" || output == "" {
		return errors.New("missing DSP endpoint identity")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	current, err := r.ReadDevices(ctx)
	if err != nil {
		return err
	}
	changes := deviceChanges(current, output, input)
	for _, change := range changes {
		if out, err := exec.CommandContext(ctx, "gsettings", "set", change.schema, change.key, change.value).CombinedOutput(); err != nil {
			return fmt.Errorf("bind DSP %s: %w: %s", change.key, err, out)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	observed, err := r.ReadDevices(ctx)
	if err != nil {
		return err
	}
	if len(deviceChanges(observed, output, input)) != 0 {
		return errors.New("DSP device binding readback mismatch")
	}
	return nil
}
