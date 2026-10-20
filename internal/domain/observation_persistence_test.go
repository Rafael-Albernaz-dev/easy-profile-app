package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnchangedObservationDoesNotRewriteSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	c := NewAudioController(NewSessionStore(path))
	level := NewPercentageLevel(77)
	c.SetMicrophoneState(true, false, level)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		c.SetMicrophoneState(true, false, level)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("identical observation rewrote atomic session", err)
	}
	c.SetMicrophoneState(true, true, level)
	changed, err := os.Stat(path)
	if err != nil || os.SameFile(after, changed) {
		t.Fatal("real mute change was not persisted", err)
	}
}

func TestUnchangedObservationRetriesFailedPersistence(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	c := NewAudioController(NewSessionStore(filepath.Join(blocked, "session.json")))
	level := NewPercentageLevel(77)
	c.SetMicrophoneState(true, false, level)
	if c.PersistenceError() == nil {
		t.Fatal("expected write failure")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	c.SetMicrophoneState(true, false, level)
	if c.PersistenceError() != nil {
		t.Fatal("unchanged state prevented persistence retry", c.PersistenceError())
	}
	data, err := c.store.Load()
	if err != nil || data == nil || data.MicLevelPct != 77 || !data.MicVirtualMuted || data.MicPhysicalMuted {
		t.Fatal("retry did not preserve independent mute fields", err)
	}
}
