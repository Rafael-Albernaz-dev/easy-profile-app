package daemon

import (
	"context"
	"errors"
	"github.com/toru/easy-profile-app/internal/domain"
	"time"

	"github.com/godbus/dbus/v5"
)

func (d *Daemon) setOutputBoost(ctx context.Context, enabled bool) (retErr error) {
	defer func() {
		if retErr != nil && !d.observeOnly {
			safe, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			retErr = errors.Join(retErr, d.pw.SetNodeMute(safe, d.ctrl.ActiveDestination().PhysicalSinkName, true))
		}
	}()
	if err := d.control(); err != nil {
		return err
	}
	d.status.OutputBoostConfirmed = false
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
		// A failed OFF must never retain a desired state that can reapply excess gain.
		d.ctrl.SetOutputBoost(false)
	}
	if err := d.ee.SetOutputBoost(ctx, enabled, percent); err != nil {
		return errors.Join(err, d.pw.SetNodeMute(ctx, d.ctrl.ActiveDestination().PhysicalSinkName, true))
	}
	// Reapply the normal virtual level explicitly; never depend on a previous
	// above-100 request having left the sink at exactly unity.
	if err := d.pw.SetNodeVolume(ctx, d.hardware.VirtualSink, domain.NewPercentageLevel(percent).LinearRatio()); err != nil {
		return err
	}
	d.ctrl.SetOutputBoost(enabled)
	if err := d.refresh(ctx); err != nil {
		return err
	}
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
