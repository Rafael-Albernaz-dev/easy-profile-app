package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/toru/easy-profile-app/internal/statefile"
)

// PortSessionState tracks the session state of an output destination.
type PortSessionState struct {
	VirtualVolume int    `json:"virtual_volume_pct"`
	ActivePreset  string `json:"active_preset"`
}

// SessionData represents the schema saved to disk in $XDG_STATE_HOME/easyprofile/session.json.
type SessionData struct {
	Version           int                                `json:"version"`
	SessionID         string                             `json:"session_id,omitempty"`
	Panic             PanicSnapshot                      `json:"panic"`
	ActiveDestination DestinationID                      `json:"active_destination"`
	OutputMuted       bool                               `json:"output_muted"`
	MicLevelPct       int                                `json:"mic_level_pct"`
	MicVirtualMuted   bool                               `json:"mic_virtual_muted"`
	MicPhysicalMuted  bool                               `json:"mic_physical_muted"`
	Ports             map[DestinationID]PortSessionState `json:"ports"`
}

// SessionStore handles loading and atomic saving of the session state.
type SessionStore struct {
	filePath string
}

// NewSessionStore creates a store saving to custom path or default $XDG_STATE_HOME/easyprofile/session.json.
func NewSessionStore(customPath string) *SessionStore {
	if customPath != "" {
		return &SessionStore{filePath: customPath}
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return &SessionStore{filePath: filepath.Join(stateHome, "easyprofile", "session.json")}
}

// FilePath returns the configured file path.
func (s *SessionStore) FilePath() string {
	return s.filePath
}

// Load reads and unmarshals the session data. If not found, returns nil without error.
func (s *SessionStore) Load() (*SessionData, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session store: %w", err)
	}
	var session SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing session store JSON: %w", err)
	}
	if session.Version != 1 {
		return nil, fmt.Errorf("unsupported session version %d", session.Version)
	}
	return &session, nil
}

// Save atomically writes session data to disk using a temporary file.
func (s *SessionStore) Save(session *SessionData) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session data: %w", err)
	}

	return statefile.Write(s.filePath, data)
}
