package daemon

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/toru/easy-profile-app/internal/adapters"
	"time"
)

func (d *Daemon) readSharing(ctx context.Context) error {
	state, err := d.pw.ReadOutputSharing(ctx)
	d.setObservedSharing(state)
	return err
}

func (d *Daemon) setObservedSharing(state adapters.OutputSharingState) {
	d.status.OutputSharing = state.Active
	d.status.OutputSharingRestorePending = state.RestorePending
	d.ctrl.SetOutputSharing(state.Active)
}

func (d *Daemon) stopSharing(ctx context.Context) error {
	err := d.pw.StopOutputSharing(ctx)
	return errors.Join(err, d.readSharing(ctx))
}

func (d *Daemon) setSharing(ctx context.Context, enabled bool) error {
	if err := d.control(); err != nil {
		return err
	}
	if !enabled {
		return d.stopSharing(ctx)
	}
	if err := d.ctrl.CanShareOutput(); err != nil {
		return err
	}
	if d.status.OutputSharingRestorePending {
		return errors.New("finish restoring the previous input before sharing again")
	}
	if d.ctrl.OutputSharing() {
		return d.readSharing(ctx)
	}
	err := d.pw.StartOutputSharing(ctx, d.ctrl.ActiveDestination().PhysicalSinkName)
	err = errors.Join(err, d.readSharing(ctx))
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		err = errors.Join(err, d.stopSharing(cleanup))
	}
	return err
}

func (d *Daemon) SetOutputSharing(enabled bool) (bool, *dbus.Error) {
	var active bool
	err := d.execute(false, func(ctx context.Context) error {
		err := d.setSharing(ctx, enabled)
		active = d.ctrl.OutputSharing()
		return err
	})
	return active, err
}

func (d *Daemon) ToggleOutputSharing() (bool, *dbus.Error) {
	var active bool
	err := d.execute(false, func(ctx context.Context) error {
		err := d.setSharing(ctx, !d.ctrl.OutputSharing() && !d.status.OutputSharingRestorePending)
		active = d.ctrl.OutputSharing()
		return err
	})
	return active, err
}
