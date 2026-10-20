package alsa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/toru/easy-profile-app/internal/inspect"
	"os"
	"os/exec"
	"time"

	"github.com/toru/easy-profile-app/internal/adapters"
)

var (
	ErrTestModeBlocked = errors.New("alsa real execution blocked in test mode (EASYPROFILE_TEST_MODE=1)")
)

// RealAlsa controls ALSA mixer settings on physical cards via amixer.
// Strictly obeys docs/RULES.md §1.4 (zero shell concatenation, timeouts, test mode guard).
type RealAlsa struct {
	timeout time.Duration
}

// NewRealAlsa creates an ALSA adapter with a safe timeout.
func NewRealAlsa(timeout time.Duration) *RealAlsa {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &RealAlsa{timeout: timeout}
}

var _ adapters.AlsaPort = (*RealAlsa)(nil)

func (r *RealAlsa) checkTestMode() error {
	if _, present := os.LookupEnv("EASYPROFILE_TEST_MODE"); present {
		return ErrTestModeBlocked
	}
	return nil
}

// EnsureAutoMuteDisabled disables the hardware Auto-Mute Mode on the ALC887-VD codec.
func (r *RealAlsa) EnsureAutoMuteDisabled(ctx context.Context, card string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "amixer", "-c", card, "sset", "Auto-Mute Mode", "Disabled")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("amixer set Auto-Mute Mode Disabled failed: %w (stderr: %s)", err, stderr.String())
	}
	out, err := exec.CommandContext(ctx, "amixer", "-c", card, "sget", "Auto-Mute Mode").Output()
	if err != nil {
		return err
	}
	if inspect.ParseAmixerSget("Auto-Mute Mode", string(out)).EnumItem != "Disabled" {
		return errors.New("Auto-Mute readback not Disabled")
	}
	return nil
}

// SetControl configures a mixer control (e.g. "Headphone" or "Front") with volume percent and mute state.
func (r *RealAlsa) SetControl(ctx context.Context, card string, control string, volumePercent int, muted bool) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	if volumePercent < 0 || volumePercent > 100 || (control != "Headphone" && control != "Front") {
		return errors.New("invalid hardware control or percentage")
	}
	volArg := fmt.Sprintf("%d%%", volumePercent)
	muteArg := "unmute"
	if muted {
		muteArg = "mute"
	}

	cmd := exec.CommandContext(ctx, "amixer", "-c", card, "sset", control, volArg, muteArg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("amixer set %s %s %s on card %s failed: %w (stderr: %s)", control, volArg, muteArg, card, err, stderr.String())
	}
	out, err := exec.CommandContext(ctx, "amixer", "-c", card, "sget", control).Output()
	if err != nil {
		return err
	}
	state := inspect.ParseAmixerSget(control, string(out))
	if !state.Found || len(state.SwitchOn) == 0 || len(state.Percent) != len(state.SwitchOn) {
		return errors.New("ALSA control readback missing")
	}
	for i, on := range state.SwitchOn {
		if on == muted || (muted && state.Percent[i] != 0) || (!muted && state.Percent[i] != volumePercent) {
			return errors.New("ALSA control readback mismatch")
		}
	}
	return nil
}
