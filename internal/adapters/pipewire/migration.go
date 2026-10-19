package pipewire

import (
	"context"
	"errors"
	"fmt"
	"github.com/toru/easy-profile-app/internal/inspect"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// migrationTargets only accepts one active application capture stream on the physical mic.
func migrationTargets(g *inspect.Graph, micPrefix, virtualName, application string) (*inspect.NodeInfo, *inspect.NodeInfo, error) {
	var mic, virtual *inspect.NodeInfo
	for _, node := range g.Nodes {
		if node.Name == micPrefix || strings.HasPrefix(node.Name, micPrefix+"_") {
			if mic != nil {
				return nil, nil, errors.New("ambiguous physical microphone")
			}
			mic = node
		}
		if node.Name == virtualName {
			if virtual != nil {
				return nil, nil, errors.New("ambiguous virtual microphone")
			}
			virtual = node
		}
	}
	if mic == nil || virtual == nil {
		return nil, nil, errors.New("microphone nodes unavailable")
	}
	var stream *inspect.NodeInfo
	for _, link := range g.Links {
		node := g.Nodes[link.InNode]
		if link.OutNode != mic.ID || link.State != "active" || node == nil || node.MediaClass != "Stream/Input/Audio" || strings.HasPrefix(node.Name, "ee_") || strings.Contains(strings.ToLower(node.AppName), "easyeffects") {
			continue
		}
		name := node.AppName
		if name == "" {
			name = node.Name
		}
		if name != application {
			continue
		}
		if stream != nil && stream.ID != node.ID {
			return nil, nil, errors.New("application has multiple raw streams; select source in the application")
		}
		stream = node
	}
	if stream == nil {
		return nil, nil, errors.New("selected raw capture stream no longer exists")
	}
	return stream, virtual, nil
}

// MigrateRawMicStream changes only the explicitly selected stream's WirePlumber target,
// then confirms active links from the DSP source and absence of physical-mic links.
func (r *RealPipeWire) MigrateRawMicStream(ctx context.Context, micPrefix, virtualName, application string) error {
	if err := r.checkTestMode(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	graph, err := r.graph(ctx)
	if err != nil {
		return err
	}
	stream, virtual, err := migrationTargets(graph, micPrefix, virtualName, application)
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "pw-metadata", "-n", "default", strconv.Itoa(stream.ID), "target.object", "Spa:String", virtual.Name).CombinedOutput(); err != nil {
		r.InvalidateCache()
		return fmt.Errorf("stream migration: %w: %s", err, out)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		graph, err = r.graph(ctx)
		if err != nil {
			return err
		}
		current := graph.Nodes[stream.ID]
		if current == nil || current.Serial != stream.Serial {
			return errors.New("capture stream disappeared or changed identity")
		}
		processed, raw := false, false
		for _, link := range graph.Links {
			if link.InNode != stream.ID || link.State != "active" {
				continue
			}
			source := graph.Nodes[link.OutNode]
			if source == nil {
				continue
			}
			if source.Name == virtualName {
				processed = true
			}
			if source.Name == micPrefix || strings.HasPrefix(source.Name, micPrefix+"_") {
				raw = true
			}
		}
		if processed && !raw {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("stream migration not confirmed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
