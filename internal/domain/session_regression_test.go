package domain

import (
	"path/filepath"
	"testing"
)

func TestSessionRestoreKeepsZeroMicAndRejectsCrossRoutePreset(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "session.json"))
	if err := store.Save(&SessionData{Version: 1, ActiveDestination: DestAKG, MicLevelPct: 0, MicPhysicalMuted: true, Ports: map[DestinationID]PortSessionState{DestAKG: {VirtualVolume: 150, ActivePreset: DefaultSP166Destination().BasePreset}}}); err != nil {
		t.Fatal(err)
	}
	ctrl := NewAudioController(store)
	if ctrl.Microphone().PostDSPLevel.Percent() != 0 {
		t.Fatal("zero mic level lost")
	}
	if ctrl.ActiveOutputPreset() != DefaultAKGDestination().BasePreset {
		t.Fatal("cross-route preset restored")
	}
	if ctrl.OutputVirtualVolume().Percent() != 100 {
		t.Fatal("invalid persisted volume escaped clamp")
	}
	dest := ctrl.ActiveDestination()
	dest.PhysicalSinkName = "tampered"
	dest.PresetPrefixes[0] = "SP166"
	if ctrl.ActiveDestination().PhysicalSinkName == "tampered" || ctrl.ActiveDestination().IsPresetCompatible("SP166 - unsafe") {
		t.Fatal("destination pointer exposed controller invariants")
	}
}
