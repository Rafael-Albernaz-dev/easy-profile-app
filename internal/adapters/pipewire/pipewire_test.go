package pipewire

import (
	"context"
	"os"
	"testing"
)

func TestRealPipeWire_TestModeProtection(t *testing.T) {
	orig := os.Getenv("EASYPROFILE_TEST_MODE")
	defer os.Setenv("EASYPROFILE_TEST_MODE", orig)
	os.Setenv("EASYPROFILE_TEST_MODE", "1")

	rpw := NewRealPipeWire(0)
	ctx := context.Background()

	if err := rpw.EnsureMonitorChannelVolumes(ctx, "test"); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if err := rpw.SetNodeVolume(ctx, "test", 0.5); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if err := rpw.SetNodeMute(ctx, "test", true); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if err := rpw.SetSinkPort(ctx, "sink", "port"); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if _, err := rpw.GetRawMicStreams(ctx, "mic"); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
}

func TestFakePipeWire_InMemoryBehavior(t *testing.T) {
	fake := NewFakePipeWire()
	ctx := context.Background()

	_ = fake.EnsureMonitorChannelVolumes(ctx, "easyeffects_sink")
	if !fake.MonitorChannelVolumes["easyeffects_sink"] {
		t.Errorf("expected monitor.channel-volumes true")
	}

	_ = fake.SetNodeVolume(ctx, "easyeffects_sink", 0.95)
	if fake.NodeVolumes["easyeffects_sink"] != 0.95 {
		t.Errorf("expected volume 0.95, got %f", fake.NodeVolumes["easyeffects_sink"])
	}

	_ = fake.SetNodeMute(ctx, "easyeffects_sink", true)
	if !fake.NodeMutes["easyeffects_sink"] {
		t.Errorf("expected muted true")
	}

	_ = fake.SetSinkPort(ctx, "analog-stereo", "analog-output-headphones")
	if fake.SinkPorts["analog-stereo"] != "analog-output-headphones" {
		t.Errorf("expected headphones port")
	}
}
