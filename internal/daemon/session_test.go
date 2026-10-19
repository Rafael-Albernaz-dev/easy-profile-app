package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemorySessionTracker(t *testing.T) {
	st := NewMemorySessionTracker("session-1")

	// Initially fresh session
	should, err := st.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !should {
		t.Errorf("expected ShouldApplyLoginPolicy to be true on new session")
	}

	// Mark as applied
	if err := st.MarkLoginPolicyApplied(); err != nil {
		t.Fatalf("failed to mark policy applied: %v", err)
	}

	// Next check in same session should return false (restart in same session)
	should, err = st.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if should {
		t.Errorf("expected ShouldApplyLoginPolicy to be false after being marked applied in same session")
	}

	// Switch session (new login)
	st.SetSession("session-2")
	should, err = st.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !should {
		t.Errorf("expected ShouldApplyLoginPolicy to be true after switching to new session")
	}
}

func TestLoginMarkerUnreadableAndUnknownIdentityFailClosed(t *testing.T) {
	directory := t.TempDir()
	tracker := NewFileSessionTracker(directory, "session-1")
	if should, err := tracker.ShouldApplyLoginPolicy(); should || err == nil {
		t.Fatal("unreadable marker authorized unmute")
	}
	if err := tracker.MarkLoginPolicyApplied(); err == nil {
		t.Fatal("failed atomic rename ignored")
	}
	t.Setenv("XDG_SESSION_ID", "")
	t.Setenv("SESSION_MANAGER", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	unknown := NewFileSessionTracker(filepath.Join(directory, "missing.json"), "")
	if should, err := unknown.ShouldApplyLoginPolicy(); should || err == nil {
		t.Fatal("reused socket path authorized unmute")
	}
	if err := unknown.MarkLoginPolicyApplied(); err == nil {
		t.Fatal("unknown identity recorded")
	}
	if _, err := os.Stat(filepath.Join(directory, "missing.json")); !os.IsNotExist(err) {
		t.Fatal("failed marker wrote a file")
	}
}

func TestFileSessionTracker(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "login_session.json")

	st := NewFileSessionTracker(filePath, "login-session-42")

	// Initially, file does not exist -> new session
	should, err := st.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !should {
		t.Errorf("expected ShouldApplyLoginPolicy to be true when file does not exist")
	}

	// Mark applied
	if err := st.MarkLoginPolicyApplied(); err != nil {
		t.Fatalf("MarkLoginPolicyApplied failed: %v", err)
	}

	// Subsequent check in same session
	should, err = st.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if should {
		t.Errorf("expected ShouldApplyLoginPolicy to be false for same session file")
	}

	// New tracker instance with different session ID (e.g. after relogin)
	st2 := NewFileSessionTracker(filePath, "login-session-43")
	should, err = st2.ShouldApplyLoginPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !should {
		t.Errorf("expected ShouldApplyLoginPolicy to be true for different session ID")
	}
}
