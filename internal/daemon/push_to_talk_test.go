package daemon

import (
	"context"
	"github.com/toru/easy-profile-app/internal/domain"
	"testing"
	"time"
)

func TestPushToTalkHoldReleaseTokensAndRenewal(t *testing.T) {
	d, pw, _, _ := setupTestDaemon(t)
	token, err := d.SetPushToTalk(true)
	if err != nil || token == 0 {
		t.Fatal(token, err)
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("mode entry did not mute")
	}
	if active, err := d.SetTalkPressed(token, 1, true); err != nil || !active {
		t.Fatal(active, err)
	}
	before := len(pw.Calls)
	if _, err := d.SetTalkPressed(token, 2, true); err != nil {
		t.Fatal(err)
	}
	if len(pw.Calls) != before {
		t.Fatal("heartbeat spawned audio work")
	}
	if active, err := d.SetTalkPressed(token, 4, false); err != nil || active {
		t.Fatal(active, err)
	}
	if _, err := d.SetTalkPressed(token, 3, true); err == nil {
		t.Fatal("late press reopened microphone")
	}
	if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
		t.Fatal("release not muted")
	}
	if _, err := d.SetPushToTalk(false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetTalkPressed(token, 5, true); err == nil {
		t.Fatal("old mode reopened microphone")
	}
	if statusOf(t, d).PushToTalk {
		t.Fatal("mode survived exit")
	}
}
func TestPushToTalkLeaseExpiresWithoutExtension(t *testing.T) {
	d, _, _, _ := setupTestDaemon(t)
	token, err := d.SetPushToTalk(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetTalkPressed(token, 1, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(talkLease + time.Second)
	for statusOf(t, d).TalkPressed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s := statusOf(t, d)
	if s.TalkPressed || !s.MicPrivacyMuted {
		t.Fatal("lease left microphone open", s)
	}
	if _, err := d.SetTalkPressed(token, 2, true); err == nil {
		t.Fatal("expired hold resurrected")
	}
}
func TestPushToTalkPanicResetRouteMuteAndFailures(t *testing.T) {
	for _, action := range []string{"panic", "reset", "route", "mute", "failure", "observe"} {
		t.Run(action, func(t *testing.T) {
			d, pw, _, _ := setupTestDaemon(t)
			token, err := d.SetPushToTalk(true)
			if err != nil {
				t.Fatal(err)
			}
			if action == "failure" {
				d.pw = &failingMic{FakePipeWire: pw, readFails: true}
			} // failed readback must remute
			if action == "observe" {
				d.SetObserveOnly(true)
				if _, err := d.SetTalkPressed(token, 1, true); err == nil {
					t.Fatal("observe unmuted")
				}
				return
			}
			_, pressErr := d.SetTalkPressed(token, 1, true)
			if action == "failure" {
				if pressErr == nil {
					t.Fatal("unknown readback accepted")
				}
				d.pw = pw
			} else if pressErr != nil {
				t.Fatal(pressErr)
			}
			switch action {
			case "panic":
				if _, err := d.TogglePanic(); err != nil {
					t.Fatal(err)
				}
				if _, err := d.TogglePanic(); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := d.ResetDefaults(); err != nil {
					t.Fatal(err)
				}
			case "route":
				if err := d.SelectDestination("sp166"); err != nil {
					t.Fatal(err)
				}
			case "mute":
				if _, err := d.ToggleMicMute(); err != nil {
					t.Fatal(err)
				}
			}
			if !pw.NodeMutes[VirtualSourceNode] || !pw.NodeMutes[PhysicalMicPrefix] {
				t.Fatal("privacy lost")
			}
			if _, err := d.SetTalkPressed(token, 9, true); err == nil {
				t.Fatal("old hold resumed")
			}
		})
	}
}

func TestPushToTalkRestartDoesNotAdoptOpenTransientMicrophone(t *testing.T) {
	cfg := DefaultConfig()
	store := domain.NewSessionStore(t.TempDir() + "/session.json")
	ctrl, err := cfg.ControllerWithSession(store, "ptt-session")
	if err != nil {
		t.Fatal(err)
	}
	d, pw, al, ee := setupTestDaemon(t)
	d.ctrl = ctrl
	tracker := NewMemorySessionTracker("ptt-session")
	d.SetSessionTracker(tracker)
	if err := d.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	token, err2 := d.SetPushToTalk(true)
	if err2 != nil {
		t.Fatal(err2)
	}
	if _, err := d.SetTalkPressed(token, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetVolumeAbsolute(60); err != nil {
		t.Fatal(err)
	} // unrelated persistence during hold
	data, err := store.Load()
	if err != nil || !data.PushToTalkPendingMute || !data.MicVirtualMuted || !data.MicPhysicalMuted {
		t.Fatal("open transient mic persisted", data, err)
	}
	restartedCtrl, err := cfg.ControllerWithSession(store, "ptt-session")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate lost process without touching real hardware: stop its lease only.
	d.mu.Lock()
	d.cancelTalkLease()
	d.mu.Unlock()
	restarted := NewDaemon(restartedCtrl, pw, al, ee)
	t.Cleanup(restarted.Close)
	restarted.SetSessionTracker(tracker)
	if err := restarted.InitHardware(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := statusOf(t, restarted)
	if s.PushToTalk || !s.MicPrivacyMuted {
		t.Fatal("restart opened transient hold", s)
	}
}
