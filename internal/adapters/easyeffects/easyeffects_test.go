package easyeffects

import (
	"context"
	"os"
	"testing"
)

func TestRealEasyEffects_TestModeProtection(t *testing.T) {
	orig := os.Getenv("EASYPROFILE_TEST_MODE")
	defer os.Setenv("EASYPROFILE_TEST_MODE", orig)
	os.Setenv("EASYPROFILE_TEST_MODE", "1")

	ree := NewRealEasyEffects("", 0)
	ctx := context.Background()

	if err := ree.LoadPreset(ctx, "test"); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if _, err := ree.GetActivePreset(ctx); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
	if _, err := ree.ListAvailablePresets(ctx); err != ErrTestModeBlocked {
		t.Fatalf("expected ErrTestModeBlocked, got %v", err)
	}
}

func TestFakeEasyEffects_InMemoryBehavior(t *testing.T) {
	fake := NewFakeEasyEffects()
	ctx := context.Background()

	presets, err := fake.ListAvailablePresets(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != 3 {
		t.Errorf("expected 3 presets, got %d", len(presets))
	}

	active, _ := fake.GetActivePreset(ctx)
	if active != "AKG K52 - Uso diario aprovado" {
		t.Errorf("expected initial preset, got %s", active)
	}

	_ = fake.LoadPreset(ctx, "AKG K52 - Rock e Metal")
	active, _ = fake.GetActivePreset(ctx)
	if active != "AKG K52 - Rock e Metal" {
		t.Errorf("expected updated preset, got %s", active)
	}
}
