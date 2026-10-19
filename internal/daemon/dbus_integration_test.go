package daemon

import (
	"context"
	"encoding/json"
	"github.com/godbus/dbus/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDBusIntegrationAllMethodsAndSignals(t *testing.T) {
	if os.Getenv("EASYPROFILE_DBUS_INTEGRATION") != "1" {
		t.Skip("requires private dbus-run-session integration gate")
	}
	if os.Getenv("EASYPROFILE_TEST_MODE") != "1" {
		t.Fatal("test mode missing")
	}
	if os.Getenv("EASYPROFILE_PRIVATE_BUS") != "1" || os.Getenv("EASYPROFILE_PARENT_BUS_ADDRESS") == "" || os.Getenv("DBUS_SESSION_BUS_ADDRESS") == os.Getenv("EASYPROFILE_PARENT_BUS_ADDRESS") {
		t.Fatal("private bus harness required")
	}
	server, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	d, pw, _, _ := setupTestDaemon(t)
	pw.RawStreams = []string{"Recorder"}
	if err := d.Export(server); err != nil {
		t.Fatal(err)
	}
	reply, err := server.RequestName(DBusService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", reply, err)
	}
	client, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.AddMatchSignal(dbus.WithMatchObjectPath(dbus.ObjectPath(DBusPath)), dbus.WithMatchInterface(DBusInterface), dbus.WithMatchMember("StateChanged")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 32)
	client.Signal(signals)
	defer client.RemoveSignal(signals)
	obj := client.Object(DBusService, dbus.ObjectPath(DBusPath))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	calls := []struct {
		name string
		args []interface{}
	}{
		{"SetVolume", []interface{}{int32(2)}}, {"SetVolumeAbsolute", []interface{}{int32(50)}},
		{"SetVolumeForDestination", []interface{}{"akg", int32(50)}},
		{"SetMicVolume", []interface{}{int32(-2)}}, {"SetMicVolumeAbsolute", []interface{}{int32(90)}},
		{"ToggleOutputMute", nil}, {"ToggleMicMute", nil}, {"TogglePanic", nil}, {"TogglePanic", nil},
		{"SelectDestination", []interface{}{"sp166"}}, {"ToggleDestination", nil},
		{"SelectPreset", []interface{}{"AKG K52 - Rock e Metal"}}, {"ResetDefaults", nil}, {"GetRawMicStreams", nil}, {"MigrateRawMicStream", []interface{}{"Recorder"}},
	}
	for _, tc := range calls {
		if err := obj.CallWithContext(ctx, DBusInterface+"."+tc.name, 0, tc.args...).Err; err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		select {
		case sig := <-signals:
			var state DaemonStatus
			if len(sig.Body) != 1 {
				t.Fatalf("bad signal %+v", sig)
			}
			if err := json.Unmarshal([]byte(sig.Body[0].(string)), &state); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatalf("no StateChanged for %s", tc.name)
		}
	}
	var raw string
	if err := obj.CallWithContext(ctx, DBusInterface+".GetStatus", 0).Store(&raw); err != nil {
		t.Fatal(err)
	}
	var state DaemonStatus
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	if state.ActiveDestination.ID != "akg" || state.OutputVolume != 95 {
		t.Fatalf("bad final state: %s", raw)
	}
	if err := obj.CallWithContext(ctx, DBusInterface+".SelectDestination", 0, "unknown").Err; err == nil {
		t.Fatal("invalid route accepted")
	}
	if err := obj.CallWithContext(ctx, DBusInterface+".Close", 0).Err; err == nil {
		t.Fatal("lifecycle method exposed")
	}
	if err := obj.CallWithContext(ctx, DBusInterface+".Restart", 0).Err; err == nil {
		t.Fatal("restart exposed over IPC")
	}
	var xml string
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, `type="i"`) || strings.Contains(xml, `name="Close"`) || strings.Contains(xml, `name="Restart"`) {
		t.Fatal("unexpected IPC contract", xml)
	}
	// Measure the in-memory IPC path, independently from audio subprocess latency.
	start := time.Now()
	for range 100 {
		if err := obj.CallWithContext(ctx, DBusInterface+".GetStatus", 0).Store(&raw); err != nil {
			t.Fatal(err)
		}
	}
	average := time.Since(start) / 100
	t.Logf("in-memory GetStatus IPC average: %s", average)
	if average >= 20*time.Millisecond {
		t.Fatalf("IPC exceeded target: %s", average)
	}
}
