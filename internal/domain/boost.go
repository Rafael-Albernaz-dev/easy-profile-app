package domain

const BoostMaximumPercent = 150

// NewOutputLevel is the only constructor permitting more than 100%. Microphone
// and calibrated physical endpoints continue using NewPercentageLevel.
func NewOutputLevel(percent int, boost bool) PercentageLevel {
	maximum := 100
	if boost {
		maximum = BoostMaximumPercent
	}
	return PercentageLevel{percent: max(0, min(maximum, percent))}
}

func (c *AudioController) OutputBoost() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.outputBoost
}

func (c *AudioController) OutputVolumePercent() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.portMemory[c.activeDestination].VirtualVolume
}

func (c *AudioController) SetOutputBoost(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outputBoost = enabled
	if !enabled {
		for id, state := range c.portMemory {
			state.VirtualVolume = min(state.VirtualVolume, 100)
			c.portMemory[id] = state
		}
		c.persist()
	}
}
