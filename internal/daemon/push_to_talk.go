package daemon

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"time"
)

const talkLease = 2 * time.Second

func (d *Daemon) cancelTalkLease() {
	if d.talkTimer != nil {
		d.talkTimer.Stop()
		d.talkTimer = nil
	}
	d.talkPressed = false
}
func (d *Daemon) muteTalk(ctx context.Context) error {
	d.cancelTalkLease()
	d.ctrl.MuteMicrophone()
	return errors.Join(d.mute(ctx, d.hardware.VirtualSource, true, &d.status.ObservedMicVirtualMute), d.mute(ctx, d.hardware.PhysicalMicrophonePrefix, true, &d.status.ObservedMicPhysicalMute))
}
func (d *Daemon) setPushToTalk(ctx context.Context, enabled bool) error {
	if err := d.control(); err != nil {
		return err
	}
	if enabled && d.ctrl.MicMonitoring() {
		return errors.New("desative a escuta antes do push-to-talk")
	}
	if enabled && d.ctrl.PanicState().Active {
		return errors.New("saia do pânico antes de ativar push-to-talk")
	}
	// Revoke old tokens before any operation that can fail.
	d.talkEpoch++
	d.talkSequence = 0
	d.ctrl.SetPushToTalk(false)
	muteErr := d.muteTalk(ctx)
	if muteErr == nil {
		d.ctrl.ConfirmTalkRecovery()
	}
	stopErr := d.stopSharing(ctx)
	if err := errors.Join(muteErr, stopErr); err != nil {
		return err
	}
	d.ctrl.SetPushToTalk(enabled)
	return nil
}

// A mode token prevents a late press from an earlier mode/session reopening the mic.
func (d *Daemon) SetPushToTalk(enabled bool) (uint32, *dbus.Error) {
	var token uint32
	err := d.execute(true, func(ctx context.Context) error {
		err := d.setPushToTalk(ctx, enabled)
		if err == nil && enabled {
			token = d.talkEpoch
		}
		return err
	})
	return token, err
}
func (d *Daemon) SetTalkPressed(epoch, sequence uint32, pressed bool) (bool, *dbus.Error) {
	var active bool
	err := d.execute(!pressed, func(ctx context.Context) error {
		if err := d.control(); err != nil {
			return err
		}
		if epoch != d.talkEpoch || !d.ctrl.PushToTalk() || sequence <= d.talkSequence {
			return errors.New("comando push-to-talk expirado")
		}
		d.talkSequence = sequence
		if !pressed {
			return d.muteTalk(ctx)
		}
		if d.ctrl.PanicState().Active {
			return errors.New("push-to-talk bloqueado pelo pânico")
		}
		if !d.talkPressed {
			if err := d.ctrl.SetTalkPressed(true); err != nil {
				return err
			}
			err := errors.Join(d.mute(ctx, d.hardware.VirtualSource, false, &d.status.ObservedMicVirtualMute), d.mute(ctx, d.hardware.PhysicalMicrophonePrefix, false, &d.status.ObservedMicPhysicalMute))
			if err != nil {
				d.talkEpoch++
				safe, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return errors.Join(err, d.muteTalk(safe))
			}
		}
		d.cancelTalkLease()
		d.talkPressed = true
		active = true
		d.talkTimer = time.AfterFunc(talkLease, func() {
			_ = d.execute(true, func(ctx context.Context) error {
				if d.talkEpoch != epoch || d.talkSequence != sequence || !d.talkPressed {
					return nil
				}
				d.talkEpoch++
				return d.muteTalk(ctx)
			})
		})
		return nil
	})
	return active, err
}
