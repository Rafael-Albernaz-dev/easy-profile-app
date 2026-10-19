package daemon

import (
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
