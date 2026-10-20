package pipewire

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func (r *RealPipeWire) EnsureDefaultSink(ctx context.Context, sink string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	err := ensureDefault(ctx, sink, "sink", r.graph, func(ctx context.Context, node *inspect.NodeInfo) error {
		out, err := exec.CommandContext(ctx, "wpctl", "set-default", strconv.Itoa(node.ID)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("set DSP default sink: %w: %s", err, out)
		}
		return nil
	})
	if err != nil {
		r.InvalidateCache()
	}
	return err
}

func (r *RealPipeWire) EnsureDefaultSource(ctx context.Context, source string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	err := ensureDefault(ctx, source, "source", r.graph, func(ctx context.Context, node *inspect.NodeInfo) error {
		value, _ := json.Marshal(map[string]string{"name": node.Name})
		out, err := exec.CommandContext(ctx, "pw-metadata", "-n", "default", "0", "default.configured.audio.source", string(value), "Spa:String:JSON").CombinedOutput()
		if err != nil {
			return fmt.Errorf("set DSP default source: %w: %s", err, out)
		}
		return nil
	})
	if err != nil {
		r.InvalidateCache()
	}
	return err
}

// ensureDefault is exercised with in-memory readers/selectors in tests. Public
// entry points block host execution in test mode and supply a bounded context.
func ensureDefault(ctx context.Context, name, kind string, read func(context.Context) (*inspect.Graph, error), selectNode func(context.Context, *inspect.NodeInfo) error) error {
	selected := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("default DSP %s readback not confirmed: %w", kind, err)
		}
		graph, err := read(ctx)
		if err != nil {
			return err
		}
		var endpoint *inspect.NodeInfo
		for _, node := range graph.Nodes {
			if node.Name != name {
				continue
			}
			if endpoint != nil {
				return fmt.Errorf("ambiguous default DSP %s endpoint %s", kind, name)
			}
			endpoint = node
		}
		class := "Audio/Sink"
		if kind == "source" {
			class = "Audio/Source"
		}
		if endpoint == nil || endpoint.ID <= 0 || (endpoint.MediaClass != class && !strings.HasPrefix(endpoint.MediaClass, class+"/")) {
			return fmt.Errorf("default DSP %s endpoint %s unavailable or invalid", kind, name)
		}
		if graph.Defaults["default.audio."+kind] == name && graph.Defaults["default.configured.audio."+kind] == name {
			return ctx.Err()
		}
		if !selected {
			if err := selectNode(ctx, endpoint); err != nil {
				return err
			}
			selected = true
			continue // Always use a new snapshot after selection.
		}
		// WirePlumber applies configured defaults asynchronously. Retry readback
		// only while repairing, never poll on the healthy reconciliation path.
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("default DSP %s readback not confirmed: %w", kind, ctx.Err())
		case <-timer.C:
		}
	}
}
