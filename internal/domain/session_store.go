package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PortSessionState tracks the session state of an output destination.
type PortSessionState struct {
	VirtualVolume int    `json:"virtual_volume_pct"`
	ActivePreset  string `json:"active_preset"`
}

// SessionData represents the schema saved to disk in $XDG_STATE_HOME/easyprofile/session.json.
type SessionData struct {
	Version           int                                `json:"version"`
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
	return &session, nil
}

// Save atomically writes session data to disk using a temporary file.
func (s *SessionStore) Save(session *SessionData) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating session store dir: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session data: %w", err)
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("writing session tmp file: %w", err)
	}
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("committing session file: %w", err)
	}
	return nil
}
