package alsa

import (
	"context"
	"os"
	"testing"
)

func TestRealAlsa_TestModeProtection(t *testing.T) {
	orig := os.Getenv("EASYPROFILE_TEST_MODE")
	defer os.Setenv("EASYPROFILE_TEST_MODE", orig)
	os.Setenv("EASYPROFILE_TEST_MODE", "1")

	ra := NewRealAlsa(0)
	ctx := context.Background()

	if err := ra.EnsureAutoMuteDisabled(ctx, "PCH"); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if err := ra.SetControl(ctx, "PCH", "Headphone", 97, false); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
}

func TestFakeAlsa_InMemoryBehavior(t *testing.T) {
	fake := NewFakeAlsa()
	ctx := context.Background()

	_ = fake.EnsureAutoMuteDisabled(ctx, "PCH")
	if !fake.AutoMuteDisabled["PCH"] {
		t.Errorf("expected auto-mute disabled on PCH")
	}

	_ = fake.SetControl(ctx, "PCH", "Headphone", 97, false)
	key := "PCH:Headphone"
	if fake.ControlVolumes[key] != 97 {
		t.Errorf("expected 97 volume, got %d", fake.ControlVolumes[key])
	}
	if fake.ControlMutes[key] {
		t.Errorf("expected muted false")
	}
}
