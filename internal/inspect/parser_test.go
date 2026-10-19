package inspect_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/toru/easy-profile-app/internal/inspect"
)

func TestParseGraph_LiveFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "test", "fixtures", "pw-dump-live-2026-10-18.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	g, err := inspect.ParseGraph(data)
	if err != nil {
		t.Fatalf("ParseGraph failed: %v", err)
	}

	if len(g.Nodes) == 0 {
		t.Fatalf("expected nodes in graph, got 0")
	}
	if len(g.Devices) == 0 {
		t.Fatalf("expected devices in graph, got 0")
	}

	analysis := inspect.Analyze(g, inspect.DefaultIdentity())

	if !analysis.CardFound {
		t.Errorf("expected CardFound == true")
	}
	if analysis.ActiveDestination != inspect.DestSP166 {
		t.Errorf("expected ActiveDestination == %q, got %q", inspect.DestSP166, analysis.ActiveDestination)
	}
	if analysis.ActiveOutputRoute != "analog-output-lineout" {
		t.Errorf("expected ActiveOutputRoute == 'analog-output-lineout', got %q", analysis.ActiveOutputRoute)
	}

	// KA-1333 microphone resolution test
	if analysis.MicResolved == nil {
		t.Fatalf("expected MicResolved != nil")
	}
	if analysis.MicResolved.Name != "alsa_input.usb-MU900_KA-1333_20190805V001-00.iec958-stereo.3" {
		t.Errorf("unexpected mic resolved name: %q", analysis.MicResolved.Name)
	}

	// Verify monitor.channel-volumes = false detection on EasyEffects virtual nodes
	if analysis.VirtualSink == nil {
		t.Fatalf("expected VirtualSink != nil")
	}
	if analysis.VirtualSink.MonitorChannelVolumes == nil || *analysis.VirtualSink.MonitorChannelVolumes != false {
		t.Errorf("expected VirtualSink.MonitorChannelVolumes == false, got %v", analysis.VirtualSink.MonitorChannelVolumes)
	}

	if analysis.VirtualSource == nil {
		t.Fatalf("expected VirtualSource != nil")
	}
	if analysis.VirtualSource.MonitorChannelVolumes == nil || *analysis.VirtualSource.MonitorChannelVolumes != false {
		t.Errorf("expected VirtualSource.MonitorChannelVolumes == false, got %v", analysis.VirtualSource.MonitorChannelVolumes)
	}

	// Run Findings evaluation
	ins := &inspect.Inspection{
		Source:   fixturePath,
		Identity: inspect.DefaultIdentity(),
		Graph:    analysis,
	}
	findings := inspect.Evaluate(ins)
	if len(findings) == 0 {
		t.Errorf("expected findings, got 0")
	}

	// Verify that monitor.channel-volumes = false is flagged as warning
	foundVSinkWarn := false
	foundVSourceWarn := false
	for _, f := range findings {
		if f.Code == "VSINK_MONITOR_VOLUMES" && f.Severity == inspect.SevWarn {
			foundVSinkWarn = true
		}
		if f.Code == "VSOURCE_MONITOR_VOLUMES" && f.Severity == inspect.SevWarn {
			foundVSourceWarn = true
		}
	}
	if !foundVSinkWarn {
		t.Errorf("expected warning for VSINK_MONITOR_VOLUMES")
	}
	if !foundVSourceWarn {
		t.Errorf("expected warning for VSOURCE_MONITOR_VOLUMES")
	}
}

func TestParseGraph_EdgeCases(t *testing.T) {
	// Test case: 0 candidates for Mic
	emptyGraph := &inspect.Graph{
		Nodes:    map[int]*inspect.NodeInfo{},
		Devices:  map[int]*inspect.DeviceInfo{},
		Defaults: map[string]string{},
	}
	analysis := inspect.Analyze(emptyGraph, inspect.DefaultIdentity())
	if analysis.MicResolved != nil {
		t.Errorf("expected MicResolved == nil for empty graph")
	}

	ins := &inspect.Inspection{Graph: analysis, Identity: inspect.DefaultIdentity()}
	findings := inspect.Evaluate(ins)
	foundCardBlocker := false
	foundMicBlocker := false
	for _, f := range findings {
		if f.Code == "CARD" && f.Severity == inspect.SevBlocker {
			foundCardBlocker = true
		}
		if f.Code == "MIC_RESOLVE" && f.Severity == inspect.SevBlocker {
			foundMicBlocker = true
		}
	}
	if !foundCardBlocker {
		t.Errorf("expected CARD blocker finding")
	}
	if !foundMicBlocker {
		t.Errorf("expected MIC_RESOLVE blocker finding")
	}
}

func TestReadOnlyRunner_Security(t *testing.T) {
	runner := inspect.ReadOnlyRunner{}
	ctx := context.Background()

	// Mutations must be rejected
	mutations := [][]string{
		{"wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", "50%"},
		{"wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", "1"},
		{"amixer", "-c", "PCH", "sset", "Headphone", "unmute"},
		{"gsettings", "set", "com.github.wwmm.easyeffects", "bypass", "true"},
		{"systemctl", "--user", "stop", "ka1333-input-guard.service"},
	}

	for _, cmd := range mutations {
		_, err := runner.Run(ctx, cmd[0], cmd[1:]...)
		if err == nil {
			t.Errorf("expected security error for %v, got nil", cmd)
		}
	}

	// TestModeEnv kill switch
	t.Setenv(inspect.TestModeEnv, "1")

	_, err := runner.Run(ctx, "pw-dump", "--no-colors")
	if err != inspect.ErrTestModeExec {
		t.Errorf("expected ErrTestModeExec when %s=1, got %v", inspect.TestModeEnv, err)
	}
}

func BenchmarkParseGraph(b *testing.B) {
	data, err := os.ReadFile("../../test/fixtures/pw-dump-live-2026-10-18.json")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := inspect.ParseGraph(data); err != nil {
			b.Fatal(err)
		}
	}
}
