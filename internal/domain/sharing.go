package domain

import "errors"

func (c *AudioController) CanShareOutput() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.panicState.Active {
		return errors.New("output sharing is unavailable during panic")
	}
	if c.micMonitoring {
		return errors.New("turn off microphone listening before sharing output")
	}
	return nil
}

func (c *AudioController) SetOutputSharing(active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outputSharing = active
}

func (c *AudioController) OutputSharing() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.outputSharing
}

// MuteMicrophone is an explicit privacy action, including an override of the
// saved panic state when a sharing restoration is still pending.
func (c *AudioController) MuteMicrophone() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mic.SetDualMute(true)
	if c.panicState.Active {
		c.panicState.SavedMicVirtualMute = true
		c.panicState.SavedMicPhysicalMute = true
	}
	c.persist()
}
