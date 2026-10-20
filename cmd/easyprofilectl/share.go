package main

import (
	"fmt"
	"github.com/toru/easy-profile-app/internal/daemon"
	"io"
)

func runShare(args []string, stdout, stderr io.Writer) int {
	mode := "toggle"
	if len(args) == 1 {
		mode = args[0]
	}
	if len(args) > 1 || (mode != "on" && mode != "off" && mode != "toggle") {
		fmt.Fprintln(stderr, "usage: easyprofilectl share [on|off|toggle]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintln(stderr, "share:", err)
		return 1
	}
	defer conn.Close()
	method := "ToggleOutputSharing"
	var params []interface{}
	if mode != "toggle" {
		method = "SetOutputSharing"
		params = []interface{}{mode == "on"}
	}
	var active bool
	if err := busCall(obj, daemon.DBusInterface+"."+method, 0, params...).Store(&active); err != nil {
		fmt.Fprintln(stderr, "share:", err)
		return 1
	}
	if active {
		fmt.Fprintln(stdout, "Output sharing ON — all output replaces the call microphone (includes participants and notifications).")
	} else {
		fmt.Fprintln(stdout, "Output sharing OFF — previous capture route restored if still available.")
	}
	return 0
}
