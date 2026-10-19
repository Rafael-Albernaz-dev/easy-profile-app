package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/toru/easy-profile-app/internal/statefile"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionTracker manages graphical session identity and login policy initialization.
type SessionTracker interface {
	// ShouldApplyLoginPolicy returns true if this is a fresh login session and the login policy should be applied.
	ShouldApplyLoginPolicy() (bool, error)
	// MarkLoginPolicyApplied records that the login policy has been applied for the current session.
	MarkLoginPolicyApplied() error
}

type sessionData struct {
	SessionID  string    `json:"session_id"`
	AppliedAt  time.Time `json:"applied_at"`
	PolicyDone bool      `json:"policy_done"`
}

// FileSessionTracker tracks session state using a file in state/runtime dir.
type FileSessionTracker struct {
	mu        sync.Mutex
	filePath  string
	sessionID string
}

// NewFileSessionTracker creates a session tracker bound to a specific file and session ID.
func NewFileSessionTracker(filePath, sessionID string) *FileSessionTracker {
	if sessionID == "" {
		sessionID = ResolveCurrentSessionID()
	}
	return &FileSessionTracker{
		filePath:  filePath,
		sessionID: sessionID,
	}
}

// ResolveCurrentSessionID determines the current graphical session identifier:
// 1. XDG_SESSION_ID (standard logind session ID)
// 2. SESSION_MANAGER (GNOME session manager socket)
// Socket paths and WAYLAND_DISPLAY can survive logout; they cannot authorize unmute.
func ResolveCurrentSessionID() string {
	if s := os.Getenv("XDG_SESSION_ID"); s != "" {
		return s
	}
	if s := os.Getenv("SESSION_MANAGER"); s != "" {
		return s
	}
	return ""
}

// DefaultSessionFilePath returns the standard session state file path.
func DefaultSessionFilePath() string {
	return runtimeStatePath("session.json")
}

// DefaultLoginFilePath is separate from the volume/preset snapshot.
func DefaultLoginFilePath() string { return runtimeStatePath("login_session.json") }

func runtimeStatePath(name string) string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "easyprofile", name)
	}
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "easyprofile", name)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "easyprofile", name)
}

// ShouldApplyLoginPolicy checks whether the session file matches the current session ID and was completed.
func (f *FileSessionTracker) ShouldApplyLoginPolicy() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionID == "" {
		return false, errors.New("graphical session identity unavailable; automatic unmute refused")
	}

	data, err := os.ReadFile(f.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	var state sessionData
	if err := json.Unmarshal(data, &state); err != nil {
		return false, fmt.Errorf("invalid login marker: %w", err)
	}
	if state.SessionID == "" || state.AppliedAt.IsZero() || !state.PolicyDone {
		return false, errors.New("incomplete login marker; automatic unmute refused")
	}

	if state.SessionID != f.sessionID {
		return true, nil
	}

	return false, nil
}

// MarkLoginPolicyApplied marks that the login policy has been applied for this session.
func (f *FileSessionTracker) MarkLoginPolicyApplied() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionID == "" {
		return errors.New("cannot record unknown graphical session")
	}

	state := sessionData{
		SessionID:  f.sessionID,
		AppliedAt:  time.Now().UTC(),
		PolicyDone: true,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return statefile.Write(f.filePath, data)
}

// MemorySessionTracker provides an in-memory session tracker for isolated unit tests.
type MemorySessionTracker struct {
	mu         sync.Mutex
	sessionID  string
	policyDone bool
}

// NewMemorySessionTracker creates an in-memory session tracker.
func NewMemorySessionTracker(sessionID string) *MemorySessionTracker {
	return &MemorySessionTracker{sessionID: sessionID}
}

// ShouldApplyLoginPolicy returns true if the policy has not yet been applied for this session.
func (m *MemorySessionTracker) ShouldApplyLoginPolicy() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.policyDone, nil
}

// MarkLoginPolicyApplied marks the policy as applied.
func (m *MemorySessionTracker) MarkLoginPolicyApplied() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policyDone = true
	return nil
}

// SetSession updates the session ID, resetting policyDone if the session changed.
func (m *MemorySessionTracker) SetSession(newSessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionID != newSessionID {
		m.sessionID = newSessionID
		m.policyDone = false
	}
}
