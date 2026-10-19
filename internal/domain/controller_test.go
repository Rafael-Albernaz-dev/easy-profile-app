package domain_test

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/toru/easy-profile-app/internal/domain"
)

func TestEveryPercentageRoundTripsAndRepeatedStepsClamp(t *testing.T) {
	for percent := 0; percent <= 100; percent++ {
		level := domain.NewPercentageLevel(percent)
		linear := level.LinearRatio()
		if math.IsNaN(linear) || linear < 0 || linear > 1 || domain.NewPercentageFromRatio(linear).Percent() != percent {
			t.Fatalf("linear/cubic round trip failed for %d", percent)
		}
	}
	level := domain.NewPercentageLevel(0)
	for range 150 {
		level = level.AddSteps(1)
	}
	if level.Percent() != 100 {
		t.Fatal("repeated +1 escaped clamp")
	}
	for range 150 {
		level = level.AddSteps(-1)
	}
	if level.Percent() != 0 {
		t.Fatal("repeated -1 escaped clamp")
	}
}

func TestPercentageLevel_ArithmeticAndClamping(t *testing.T) {
	lvl := domain.NewPercentageLevel(95)
	if lvl.Percent() != 95 {
		t.Errorf("expected 95, got %d", lvl.Percent())
	}

	// +2% step
	lvl = lvl.AddSteps(2)
	if lvl.Percent() != 97 {
		t.Errorf("expected 97, got %d", lvl.Percent())
	}

	// Clamp to 100
	lvl = lvl.AddSteps(10)
	if lvl.Percent() != 100 {
		t.Errorf("expected 100, got %d", lvl.Percent())
	}

	// Clamp to 0
	lvl = lvl.AddSteps(-120)
	if lvl.Percent() != 0 {
		t.Errorf("expected 0, got %d", lvl.Percent())
	}
}

func TestDestination_PresetCompatibility(t *testing.T) {
	akg := domain.DefaultAKGDestination()
	sp166 := domain.DefaultSP166Destination()

	if !akg.IsPresetCompatible("AKG K52 - Uso diario aprovado") {
		t.Errorf("expected AKG base preset to be compatible")
	}
	if !akg.IsPresetCompatible("AKG - Musica Rock") {
		t.Errorf("expected AKG prefixed preset to be compatible")
	}
	if akg.IsPresetCompatible("SP166 - Uso diario aprovado") {
		t.Errorf("expected SP166 preset to be INCOMPATIBLE with AKG")
	}

	if !sp166.IsPresetCompatible("SP166 - Uso diario aprovado") {
		t.Errorf("expected SP166 base preset to be compatible")
	}
	if !sp166.IsPresetCompatible("Caixas - Podcast") {
		t.Errorf("expected Caixas prefixed preset to be compatible with SP166")
	}
	if sp166.IsPresetCompatible("AKG K52 - Uso diario aprovado") {
		t.Errorf("expected AKG preset to be INCOMPATIBLE with SP166")
	}
}

func TestAudioController_PanicMute_3BooleansPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	store := domain.NewSessionStore(filepath.Join(tmpDir, "session.json"))
	ctrl := domain.NewAudioController(store)

	// Simulate state before panic:
	// Output: unmuted (false)
	// Mic: virtual unmuted (false), physical MUTED (true)
	ctrl.SetMicrophoneState(false, true, domain.NewPercentageLevel(100))

	// Verify that entering panic mutes both
	outMuted, micMuted, panicActive := ctrl.TogglePanicMute()
	if !panicActive || !outMuted || !micMuted {
		t.Fatalf("expected all channels muted during panic")
	}

	snap := ctrl.PanicState()
	if snap.SavedOutMute != false {
		t.Errorf("expected SavedOutMute == false")
	}
	if snap.SavedMicVirtualMute != false {
		t.Errorf("expected SavedMicVirtualMute == false")
	}
	if snap.SavedMicPhysicalMute != true {
		t.Errorf("expected SavedMicPhysicalMute == true (preserving physical mute!)")
	}

	// Exit panic without manual modifications:
	outMuted, _, panicActive = ctrl.TogglePanicMute()
	if panicActive {
		t.Errorf("expected panic to be inactive")
	}
	if outMuted != false {
		t.Errorf("expected output to be restored to unmuted (false)")
	}

	restoredMic := ctrl.Microphone()
	if restoredMic.VirtualMuted != false {
		t.Errorf("expected mic virtual mute to be restored to false")
	}
	if restoredMic.PhysicalMuted != true {
		t.Errorf("CRITICAL BUG REGRESSION: physical mic was accidentally unmuted! Expected true, got %v", restoredMic.PhysicalMuted)
	}
}

func TestAudioController_PanicMute_ManualOverride(t *testing.T) {
	tmpDir := t.TempDir()
	store := domain.NewSessionStore(filepath.Join(tmpDir, "session.json"))
	ctrl := domain.NewAudioController(store)

	// Start in unmuted state
	ctrl.TogglePanicMute() // Enter panic

	// While panic is active, user manually un-mutes mic
	ctrl.ToggleMicMute() // sets mic to unmuted

	// Now exit panic by toggling panic again
	ctrl.TogglePanicMute()

	mic := ctrl.Microphone()
	if mic.IsPrivacyMuted() {
		t.Errorf("expected manual un-mute during panic to be preserved after exiting panic")
	}
}

func TestAudioController_ResetToDailyBase_PreservesMicMute(t *testing.T) {
	tmpDir := t.TempDir()
	store := domain.NewSessionStore(filepath.Join(tmpDir, "session.json"))
	ctrl := domain.NewAudioController(store)

	// Mute mic for privacy
	ctrl.ToggleMicMute()
	if !ctrl.Microphone().IsPrivacyMuted() {
		t.Fatalf("expected mic to be muted")
	}

	// Change volume to 70%
	ctrl.SetOutputVolume(domain.NewPercentageLevel(70))

	// Execute Reset to Daily Base [Key 5]
	preset, outVol, micVol, micStillMuted := ctrl.ResetToDailyBase()

	if preset != "AKG K52 - Uso diario aprovado" {
		t.Errorf("unexpected base preset: %s", preset)
	}
	if outVol.Percent() != 95 {
		t.Errorf("expected output volume reset to 95%%, got %d%%", outVol.Percent())
	}
	if micVol.Percent() != 100 {
		t.Errorf("expected mic level reset to 100%%, got %d%%", micVol.Percent())
	}
	if !micStillMuted {
		t.Errorf("CRITICAL PRIVACY BUG REGRESSION: ResetBase unmuted the microphone!")
	}
}

func TestSessionStore_PersistenceAndRecall(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "session.json")
	store1 := domain.NewSessionStore(sessionPath)
	ctrl1 := domain.NewAudioController(store1)

	// Set AKG volume to 80%
	ctrl1.SetOutputVolume(domain.NewPercentageLevel(80))

	// Switch to SP166 and set volume to 60%
	ctrl1.SwitchDestination(domain.DestSP166)
	ctrl1.SetOutputVolume(domain.NewPercentageLevel(60))

	// Switch back to AKG - should recall 80%
	_, akgState, err := ctrl1.SwitchDestination(domain.DestAKG)
	if err != nil {
		t.Fatalf("SwitchDestination failed: %v", err)
	}
	if akgState.VirtualVolume != 80 {
		t.Errorf("expected recalled AKG volume == 80%%, got %d%%", akgState.VirtualVolume)
	}

	// Simulate app restart by loading new controller from same file
	store2 := domain.NewSessionStore(sessionPath)
	ctrl2 := domain.NewAudioController(store2)

	if ctrl2.ActiveDestination().ID != domain.DestAKG {
		t.Errorf("expected active destination to be AKG after restart")
	}
	if ctrl2.OutputVirtualVolume().Percent() != 80 {
		t.Errorf("expected AKG volume to be 80%% after restart, got %d%%", ctrl2.OutputVirtualVolume().Percent())
	}

	// Check that SP166 volume was also persisted
	_, sp166State, _ := ctrl2.SwitchDestination(domain.DestSP166)
	if sp166State.VirtualVolume != 60 {
		t.Errorf("expected SP166 volume to be 60%% after restart, got %d%%", sp166State.VirtualVolume)
	}
}

func TestAudioController_VolumeStep1Percent(t *testing.T) {
	tmpDir := t.TempDir()
	store := domain.NewSessionStore(filepath.Join(tmpDir, "session.json"))
	ctrl := domain.NewAudioController(store)

	ctrl.SetOutputVolume(domain.NewPercentageLevel(50))
	newVol := ctrl.StepOutputVolume(1)
	if newVol.Percent() != 51 {
		t.Errorf("expected 51%% after StepOutputVolume(1), got %d%%", newVol.Percent())
	}

	newVol = ctrl.StepOutputVolume(-1)
	if newVol.Percent() != 50 {
		t.Errorf("expected 50%% after StepOutputVolume(-1), got %d%%", newVol.Percent())
	}

	ctrl.SetMicLevel(domain.NewPercentageLevel(80))
	newMic := ctrl.StepMicLevel(1)
	if newMic.Percent() != 81 {
		t.Errorf("expected 81%% after StepMicLevel(1), got %d%%", newMic.Percent())
	}

	newMic = ctrl.StepMicLevel(-1)
	if newMic.Percent() != 80 {
		t.Errorf("expected 80%% after StepMicLevel(-1), got %d%%", newMic.Percent())
	}
}

func TestAudioController_MicMonitoring(t *testing.T) {
	ctrl := domain.NewAudioController(nil)
	if ctrl.MicMonitoring() {
		t.Errorf("expected mic monitoring to be false by default")
	}

	toggled := ctrl.ToggleMicMonitoring()
	if !toggled || !ctrl.MicMonitoring() {
		t.Errorf("expected mic monitoring to be true after toggle")
	}

	ctrl.SetMicMonitoring(false)
	if ctrl.MicMonitoring() {
		t.Errorf("expected mic monitoring to be false after SetMicMonitoring(false)")
	}
}
