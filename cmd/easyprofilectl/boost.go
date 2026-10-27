package main

import (
	"fmt"
	"io"

	"github.com/toru/easy-profile-app/internal/daemon"
)

func runBoost(args []string, stdout, stderr io.Writer) int {
	mode := "toggle"
	if len(args) == 1 {
		mode = args[0]
	}
	if len(args) > 1 || (mode != "on" && mode != "off" && mode != "toggle") {
		fmt.Fprintln(stderr, "usage: easyprofilectl boost [on|off|toggle]")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintln(stderr, "boost:", err)
		return 1
	}
	defer conn.Close()
	method := "ToggleOutputBoost"
	var params []interface{}
	if mode != "toggle" {
		method = "SetOutputBoost"
		params = []interface{}{mode == "on"}
	}
	var active bool
	if err := busCall(obj, daemon.DBusInterface+"."+method, 0, params...).Store(&active); err != nil {
		fmt.Fprintln(stderr, "boost:", err)
		return 1
	}
	if active {
		fmt.Fprintln(stdout, "Output Boost ON — ceiling 150%; current level unchanged.")
	} else {
		fmt.Fprintln(stdout, "Output Boost OFF — ceiling 100%; extra output gain removed.")
	}
	return 0
}
