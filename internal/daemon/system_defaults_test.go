package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/toru/easy-profile-app/internal/adapters/pipewire"
	"github.com/toru/easy-profile-app/internal/domain"
)

func TestReconcileRepairsEachDefaultWithoutChangingSessionOrMicMutes(t *testing.T) {
	for _, drift := range []string{"effective input", "configured input", "effective output", "configured output"} {
		for _, mutes := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			t.Run(fmt.Sprintf("%s/%t-%t", drift, mutes[0], mutes[1]), func(t *testing.T) {
				d, pw, _, _ := setupTestDaemon(t)
				if _, err := d.SetVolumeAbsolute(63); err != nil {
					t.Fatal(err)
				}
				if err := d.SelectPreset("AKG K52 - Rock e Metal"); err != nil {
					t.Fatal(err)
				}
				d.ctrl.SetMicrophoneState(mutes[0], mutes[1], domain.NewPercentageLevel(72))
				pw.NodeMutes[VirtualSourceNode], pw.NodeMutes[PhysicalMicPrefix] = mutes[0], mutes[1]
				if _, err := d.ToggleOutputMute(); err != nil {
					t.Fatal(err)
				}
				switch drift {
				case "effective input":
					pw.DefaultSource = pw.PhysicalMicrophone
				case "configured input":
					pw.ConfiguredSource = "missing-device"
				case "effective output":
					pw.DefaultSink = PhysicalSinkNode
				case "configured output":
					pw.ConfiguredSink = "missing-device"
				}
				if err := d.Observe(); err != nil || statusOf(t, d).DSPBound {
					t.Fatal("drift not reported", err)
				}
				reads := pw.ObservationReads
				if err := d.Reconcile(); err != nil {
					t.Fatal(err)
				}
				s := statusOf(t, d)
				expectedProtection := "confirmed"
				if mutes[0] != mutes[1] {
					expectedProtection = "degraded_partial_mute"
				}
				if !s.DSPBound || s.ProtectionState != expectedProtection || pw.ObservationReads-reads < 2 || pw.DefaultSource != VirtualSourceNode || pw.ConfiguredSource != VirtualSourceNode || pw.DefaultSink != VirtualSinkNode || pw.ConfiguredSink != VirtualSinkNode {
					t.Fatalf("defaults not freshly confirmed: %+v", s)
				}
				if pw.NodeMutes[VirtualSourceNode] != mutes[0] || pw.NodeMutes[PhysicalMicPrefix] != mutes[1] || !pw.NodeMutes[PhysicalSinkNode] || s.OutputVolume != 63 || s.MicLevel != 72 || s.ActivePreset != "AKG K52 - Rock e Metal" {
					t.Fatalf("repair changed session or mutes: %+v", s)
				}
			})
		}
	}
}

type unconfirmedSource struct {
	*pipewire.FakePipeWire
	err error
}

func (f unconfirmedSource) EnsureDefaultSource(context.Context, string) error { return f.err }

func TestInputRepairFailsClosedEvenWhenSetterReportsSuccess(t *testing.T) {
	for _, failure := range []error{nil, errors.New("selection timed out")} {
		d, pw, _, _ := setupTestDaemon(t)
		if _, err := d.ToggleMicMute(); err != nil {
			t.Fatal(err)
		}
		pw.DefaultSource, pw.ConfiguredSource = pw.PhysicalMicrophone, pw.PhysicalMicrophone
		d.pw = unconfirmedSource{FakePipeWire: pw, err: failure}
		if err := d.Reconcile(); err == nil {
			t.Fatal("unconfirmed selection accepted")
		}
		s := statusOf(t, d)
		if s.DSPBound || s.ProtectionState == "confirmed" || !pw.NodeMutes[PhysicalSinkNode] || !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
			t.Fatalf("failed repair not safe: %+v", s)
		}
	}
}

func TestObserveOnlyDoesNotRepairInputDefaults(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	d.SetObserveOnly(true)
	pw.DefaultSource, pw.ConfiguredSource = "", ""
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if statusOf(t, d).DSPBound || len(pw.Calls) != 0 || pw.DefaultSource != "" || pw.ConfiguredSource != "" {
		t.Fatal("observation changed defaults or falsely confirmed them")
	}
}

func TestInputDefaultRepairPreservesPanicAndUnknownGraphIsNotConfirmed(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	if _, err := d.TogglePanic(); err != nil {
		t.Fatal(err)
	}
	pw.DefaultSource = pw.PhysicalMicrophone
	if err := d.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if !statusOf(t, d).PanicActive || !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] || !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("repair broke panic")
	}
	pw.GraphErr = errors.New("nodes unavailable")
	if err := d.Reconcile(); err == nil || statusOf(t, d).DSPBound || statusOf(t, d).ProtectionState == "confirmed" {
		t.Fatal("unknown graph accepted")
	}
	// Endpoint recreation delivers a new graph event. The next reconciliation
	// must recover without relying on the previous binding confirmation.
	pw.GraphErr = nil
	pw.DefaultSource, pw.ConfiguredSource = "", ""
	pw.MonitorChannelVolumes[VirtualSourceNode] = false
	if err := d.Reconcile(); err != nil || !statusOf(t, d).DSPBound || !statusOf(t, d).MonitorVolumesConfirmed {
		t.Fatal("recreated input endpoint not repaired", err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] || !pw.NodeMutes[PhysicalSinkNode] {
		t.Fatal("recovery lost panic mutes")
	}
}
