package domain

import "testing"

func TestBoostOnlyUnlocksOutputAndDoesNotPersistExtraGain(t *testing.T) {
	store := NewSessionStore(t.TempDir() + "/session.json")
	c := NewAudioController(store)
	c.SetOutputVolume(NewPercentageLevel(100))
	c.SetOutputBoost(true)
	if c.OutputVolumePercent() != 100 {
		t.Fatal("unlock raised volume")
	}
	c.StepOutputVolume(50)
	if c.OutputVolumePercent() != 112 || c.OutputVirtualVolume().LinearRatio() != 1 {
		t.Fatal("wrong logical or PipeWire ceiling")
	}
	c.SetMicLevel(NewOutputLevel(112, true))
	if c.Microphone().PostDSPLevel.Percent() != 100 || c.OutputPhysicalCeiling().Percent() != 97 {
		t.Fatal("boost escaped output")
	}
	data, err := store.Load()
	if err != nil || data.Ports[DestAKG].VirtualVolume != 100 {
		t.Fatal("extra gain persisted", data, err)
	}
	restored := NewAudioController(store)
	if restored.OutputBoost() || restored.OutputVolumePercent() != 100 {
		t.Fatal("restart restored boost")
	}
	c.SetOutputBoost(false)
	if c.OutputVolumePercent() != 100 || c.StepOutputVolume(10).Percent() != 100 {
		t.Fatal("disable did not clamp")
	}
	c.SetOutputVolume(NewPercentageLevel(57))
	c.SetOutputBoost(true)
	if c.OutputVolumePercent() != 57 {
		t.Fatal("unlock changed low volume")
	}
	c.ResetToDailyBase()
	if c.OutputBoost() || c.OutputVolumePercent() != 95 {
		t.Fatal("reset retained boost")
	}
}
