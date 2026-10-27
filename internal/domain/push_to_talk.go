package domain

import "errors"

func (c *AudioController) PushToTalk() bool { c.mu.RLock(); defer c.mu.RUnlock(); return c.pushToTalk }

// Mode entry/exit are explicit privacy actions. Neither restores an open mic.
func (c *AudioController) SetPushToTalk(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if enabled || c.pushToTalk {
		c.pttRecovery = true
	}
	c.pushToTalk = enabled
	c.mic.SetDualMute(true)
	c.persist()
}
func (c *AudioController) SetTalkPressed(pressed bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pressed && (!c.pushToTalk || c.panicState.Active || c.outputSharing) {
		return errors.New("push-to-talk indisponível; mantenha o microfone mutado")
	}
	c.mic.SetDualMute(!pressed)
	if !pressed {
		c.persist()
	}
	return nil
}

func (c *AudioController) TalkRecoveryPending() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pttRecovery
}
func (c *AudioController) ConfirmTalkRecovery() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pttRecovery = false
	c.persist()
}
