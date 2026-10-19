package domain

import (
	"sync"
)

// PanicSnapshot preserves independent mute states for Output, Virtual Mic, and Physical Mic.
// Storing 3 distinct booleans prevents accidental unmuting of the physical microphone when exiting panic.
type PanicSnapshot struct {
	Active               bool `json:"active"`
	SavedOutMute         bool `json:"saved_out_mute"`
	SavedMicVirtualMute  bool `json:"saved_mic_virtual_mute"`
	SavedMicPhysicalMute bool `json:"saved_mic_physical_mute"`
}

// AudioController is the root domain aggregate governing audio routes, per-port volume memory,
// Dual-Mute privacy, Panic Mute snapshots with manual overrides, and daily baseline resets.
type AudioController struct {
	mu                   sync.RWMutex
	destinations         map[DestinationID]*Destination
	activeDestination    DestinationID
	portMemory           map[DestinationID]PortSessionState
	outputMuted          bool
	volumeStepPercent    int
	defaultVirtualVolume PercentageLevel
	mic                  *Microphone
	panicState           PanicSnapshot
	store                *SessionStore
}

// NewAudioController constructs the controller, optionally restoring previous session data.
func NewAudioController(store *SessionStore) *AudioController {
	return newAudioController(store, DefaultAKGDestination(), DefaultSP166Destination())
}

// NewAudioControllerWithDestinations uses validated setup configuration and session-only memory.
func NewAudioControllerWithDestinations(akg, sp166 *Destination) *AudioController {
	return newAudioController(nil, akg, sp166)
}
func newAudioController(store *SessionStore, akg, sp166 *Destination) *AudioController {
	defaultVol := NewPercentageLevel(95)

	c := &AudioController{
		destinations: map[DestinationID]*Destination{
			DestAKG:   akg,
			DestSP166: sp166,
		},
		activeDestination: DestAKG,
		portMemory: map[DestinationID]PortSessionState{
			DestAKG: {
				VirtualVolume: defaultVol.Percent(),
				ActivePreset:  akg.BasePreset,
			},
			DestSP166: {
				VirtualVolume: defaultVol.Percent(),
				ActivePreset:  sp166.BasePreset,
			},
		},
		outputMuted:          false,
		volumeStepPercent:    2,
		defaultVirtualVolume: defaultVol,
		mic:                  NewDefaultKA1333(),
		store:                store,
	}

	if store != nil {
		if data, err := store.Load(); err == nil && data != nil {
			c.restoreFromSessionData(data)
		}
	}

	return c
}

func (c *AudioController) restoreFromSessionData(data *SessionData) {
	if _, ok := c.destinations[data.ActiveDestination]; ok {
		c.activeDestination = data.ActiveDestination
	}
	c.outputMuted = data.OutputMuted

	if data.MicLevelPct >= 0 && data.MicLevelPct <= 100 {
		c.mic.PostDSPLevel = NewPercentageLevel(data.MicLevelPct)
	}
	c.mic.VirtualMuted = data.MicVirtualMuted
	c.mic.PhysicalMuted = data.MicPhysicalMuted

	for id, portState := range data.Ports {
		if _, ok := c.destinations[id]; ok {
			portState.VirtualVolume = NewPercentageLevel(portState.VirtualVolume).Percent()
			if !c.destinations[id].IsPresetCompatible(portState.ActivePreset) {
				portState.ActivePreset = c.destinations[id].BasePreset
			}
			c.portMemory[id] = portState
		}
	}
}

// persistAsync saves current session data to disk if store is available.
func (c *AudioController) persist() {
	if c.store == nil {
		return
	}
	session := &SessionData{
		Version:           1,
		ActiveDestination: c.activeDestination,
		OutputMuted:       c.outputMuted,
		MicLevelPct:       c.mic.PostDSPLevel.Percent(),
		MicVirtualMuted:   c.mic.VirtualMuted,
		MicPhysicalMuted:  c.mic.PhysicalMuted,
		Ports:             make(map[DestinationID]PortSessionState, len(c.portMemory)),
	}
	for id, state := range c.portMemory {
		session.Ports[id] = state
	}
	_ = c.store.Save(session)
}

// ActiveDestination returns the currently active destination entity.
func (c *AudioController) ActiveDestination() *Destination {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneDestination(c.destinations[c.activeDestination])
}

// Destination retrieves a specific destination by ID.
func (c *AudioController) Destination(id DestinationID) (*Destination, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	dest, ok := c.destinations[id]
	if !ok {
		return nil, ErrUnknownDestination
	}
	return cloneDestination(dest), nil
}

// OutputVirtualVolume returns the active destination's current virtual volume level.
func (c *AudioController) OutputVirtualVolume() PercentageLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return NewPercentageLevel(c.portMemory[c.activeDestination].VirtualVolume)
}

// OutputPhysicalCeiling returns the calibrated physical ceiling for the active destination.
func (c *AudioController) OutputPhysicalCeiling() PercentageLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.destinations[c.activeDestination].PhysicalCeiling
}

// ActiveOutputPreset returns the currently selected preset for the active destination.
func (c *AudioController) ActiveOutputPreset() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.portMemory[c.activeDestination].ActivePreset
}

// OutputMuted returns whether the output is currently muted.
func (c *AudioController) OutputMuted() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.outputMuted
}

// Microphone returns a copy of the microphone entity.
func (c *AudioController) Microphone() Microphone {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return *c.mic
}

// SetMicrophoneState updates the microphone entity directly (e.g. from PipeWire observed state).
func (c *AudioController) SetMicrophoneState(virtualMuted, physicalMuted bool, level PercentageLevel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mic.VirtualMuted = virtualMuted
	c.mic.PhysicalMuted = physicalMuted
	c.mic.PostDSPLevel = level
	c.persist()
}

// PanicState returns the current panic mute snapshot.
func (c *AudioController) PanicState() PanicSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.panicState
}

// SwitchDestination switches output to target destination and retrieves that port's session volume and preset.
func (c *AudioController) SwitchDestination(target DestinationID) (*Destination, PortSessionState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dest, ok := c.destinations[target]
	if !ok {
		return nil, PortSessionState{}, ErrUnknownDestination
	}
	c.activeDestination = target
	state := c.portMemory[target]
	c.persist()
	return cloneDestination(dest), state, nil
}

// ToggleDestination switches between AKG headphones and SP166 speakers.
func (c *AudioController) ToggleDestination() (*Destination, PortSessionState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	target := DestAKG
	if c.activeDestination == DestAKG {
		target = DestSP166
	}
	c.activeDestination = target
	state := c.portMemory[target]
	c.persist()
	return cloneDestination(c.destinations[target]), state
}

// StepOutputVolume adjusts the current port's virtual volume by steps * 2%.
func (c *AudioController) StepOutputVolume(steps int) PercentageLevel {
	c.mu.Lock()
	defer c.mu.Unlock()

	mem := c.portMemory[c.activeDestination]
	lvl := NewPercentageLevel(mem.VirtualVolume).AddSteps(steps * c.volumeStepPercent)
	mem.VirtualVolume = lvl.Percent()
	c.portMemory[c.activeDestination] = mem
	c.persist()
	return lvl
}

// SetOutputVolume sets an exact virtual volume for the active destination.
func (c *AudioController) SetOutputVolume(level PercentageLevel) PercentageLevel {
	c.mu.Lock()
	defer c.mu.Unlock()

	mem := c.portMemory[c.activeDestination]
	mem.VirtualVolume = level.Percent()
	c.portMemory[c.activeDestination] = mem
	c.persist()
	return level
}

// StepMicLevel adjusts the post-DSP microphone level by steps * 2%.
func (c *AudioController) StepMicLevel(steps int) PercentageLevel {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mic.PostDSPLevel = c.mic.PostDSPLevel.AddSteps(steps * c.volumeStepPercent)
	c.persist()
	return c.mic.PostDSPLevel
}

// SetMicLevel sets an exact post-DSP microphone level.
func (c *AudioController) SetMicLevel(level PercentageLevel) PercentageLevel {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mic.PostDSPLevel = level
	c.persist()
	return c.mic.PostDSPLevel
}

// SelectOutputPreset checks compatibility with the active destination before applying and saving.
func (c *AudioController) SelectOutputPreset(presetName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	dest := c.destinations[c.activeDestination]
	if !dest.IsPresetCompatible(presetName) {
		return ErrIncompatiblePreset
	}
	mem := c.portMemory[c.activeDestination]
	mem.ActivePreset = presetName
	c.portMemory[c.activeDestination] = mem
	c.persist()
	return nil
}

// ToggleOutputMute toggles output mute. If Panic Mute is active, manual override updates the saved
// state so exiting panic preserves the user's manual decision.
func (c *AudioController) ToggleOutputMute() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.outputMuted = !c.outputMuted
	if c.panicState.Active {
		c.panicState.SavedOutMute = c.outputMuted
	}
	c.persist()
	return c.outputMuted
}

// ToggleMicMute toggles Dual-Mute on the microphone. If Panic Mute is active, manual override
// updates the saved states so exiting panic preserves the user's manual decision.
func (c *AudioController) ToggleMicMute() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	nextMuted := !c.mic.IsPrivacyMuted()
	c.mic.SetDualMute(nextMuted)

	if c.panicState.Active {
		c.panicState.SavedMicVirtualMute = nextMuted
		c.panicState.SavedMicPhysicalMute = nextMuted
	}
	c.persist()
	return nextMuted
}

// TogglePanicMute toggles Emergency Mute All:
// - Entering panic: captures independent mute states and mutes all channels.
// - Exiting panic: restores saved states without ever unmuting a channel that was muted beforehand.
func (c *AudioController) TogglePanicMute() (outMuted bool, micMuted bool, panicActive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.panicState.Active {
		c.panicState = PanicSnapshot{
			Active:               true,
			SavedOutMute:         c.outputMuted,
			SavedMicVirtualMute:  c.mic.VirtualMuted,
			SavedMicPhysicalMute: c.mic.PhysicalMuted,
		}
		c.outputMuted = true
		c.mic.SetDualMute(true)
		c.persist()
		return true, true, true
	}

	c.outputMuted = c.panicState.SavedOutMute
	c.mic.VirtualMuted = c.panicState.SavedMicVirtualMute
	c.mic.PhysicalMuted = c.panicState.SavedMicPhysicalMute
	c.panicState = PanicSnapshot{}
	c.persist()
	return c.outputMuted, c.mic.IsPrivacyMuted(), false
}

// ResetToDailyBase restores the active destination's daily base preset, output volume 95%,
// microphone level 100%, and STRICTLY PRESERVES microphone mute if currently muted.
func (c *AudioController) ResetToDailyBase() (preset string, outVol PercentageLevel, micVol PercentageLevel, micStillMuted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dest := c.destinations[c.activeDestination]
	mem := PortSessionState{
		VirtualVolume: c.defaultVirtualVolume.Percent(),
		ActivePreset:  dest.BasePreset,
	}
	c.portMemory[c.activeDestination] = mem
	c.outputMuted = false
	c.mic.PostDSPLevel = NewPercentageLevel(100)

	if c.panicState.Active {
		c.panicState.Active = false
		c.panicState.SavedOutMute = false
	}
	c.persist()
	return mem.ActivePreset, c.defaultVirtualVolume, c.mic.PostDSPLevel, c.mic.IsPrivacyMuted()
}

func cloneDestination(d *Destination) *Destination {
	copy := *d
	copy.PresetPrefixes = append([]string(nil), d.PresetPrefixes...)
	copy.allowedExplicit = make(map[string]struct{}, len(d.allowedExplicit))
	for p := range d.allowedExplicit {
		copy.allowedExplicit[p] = struct{}{}
	}
	return &copy
}
