package daemon

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
)

func (d *Daemon) setOutputBoost(ctx context.Context, enabled bool) error {
	if err := d.control(); err != nil {
		return err
	}
	physical := d.ctrl.ActiveDestination().PhysicalSinkName
	wasMuted, err := d.pw.GetNodeMute(ctx, physical)
	if err != nil {
		return errors.Join(err, d.pw.SetNodeMute(ctx, physical, true))
	}
	if err := d.mute(ctx, physical, true, nil); err != nil {
		return err
	}
	percent := d.ctrl.OutputVolumePercent()
	if !enabled {
		percent = min(percent, 100)
	}
	if err := d.ee.SetOutputBoost(ctx, enabled, percent); err != nil {
		return errors.Join(err, d.pw.SetNodeMute(ctx, d.ctrl.ActiveDestination().PhysicalSinkName, true))
	}
	d.ctrl.SetOutputBoost(enabled)
	return d.mute(ctx, physical, wasMuted, nil)
}

func (d *Daemon) SetOutputBoost(enabled bool) (bool, *dbus.Error) {
	var active bool
	err := d.execute(false, func(ctx context.Context) error {
		err := d.setOutputBoost(ctx, enabled)
		active = d.ctrl.OutputBoost()
		return err
	})
	return active, err
}

func (d *Daemon) ToggleOutputBoost() (bool, *dbus.Error) {
	var active bool
	err := d.execute(false, func(ctx context.Context) error {
		err := d.setOutputBoost(ctx, !d.ctrl.OutputBoost())
		active = d.ctrl.OutputBoost()
		return err
	})
	return active, err
}
