package inspect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TestModeEnv blocks every external command when set to "1".
// The test suite and the quality gate always set it, so an automated test can
// never reach PipeWire, ALSA, GSettings or systemd on the developer's machine.
const TestModeEnv = "EASYPROFILE_TEST_MODE"

var (
	ErrTestModeExec = errors.New("external command blocked: " + TestModeEnv + "=1")
	ErrNotReadOnly  = errors.New("command is not in the read-only allowlist")
)

// CommandRunner executes an external program and returns its stdout.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ReadOnlyRunner only executes commands that cannot change audio state.
// Anything outside the allowlist is refused before a process is spawned.
type ReadOnlyRunner struct {
	Timeout time.Duration
}

// IsReadOnlyCommand reports whether name+args is one of the inspection-only
// invocations used in EP-001. Matching is exact on the verb position, so
// "amixer ... sset", "gsettings set", "wpctl set-volume" or "systemctl stop"
// can never pass.
func IsReadOnlyCommand(name string, args []string) bool {
	switch name {
	case "pw-dump":
		return len(args) == 1 && args[0] == "--no-colors"
	case "amixer":
		return len(args) == 4 && args[0] == "-c" && args[2] == "sget"
	case "gsettings":
		return len(args) == 3 && args[0] == "get"
	case "systemctl":
		return len(args) == 3 && args[0] == "--user" && (args[1] == "is-active" || args[1] == "is-enabled")
	default:
		return false
	}
}

func (r ReadOnlyRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if os.Getenv(TestModeEnv) == "1" {
		return nil, ErrTestModeExec
	}
	if !IsReadOnlyCommand(name, args) {
		return nil, fmt.Errorf("%w: %s %s", ErrNotReadOnly, name, strings.Join(args, " "))
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		// systemctl is-active exits non-zero for inactive units but still prints the state.
		if name == "systemctl" && len(out) > 0 {
			return out, nil
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}
