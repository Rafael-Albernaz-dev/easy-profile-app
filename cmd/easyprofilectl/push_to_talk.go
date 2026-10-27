package main

import (
	"fmt"
	"github.com/toru/easy-profile-app/internal/daemon"
	"io"
)

func runPTT(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		fmt.Fprintln(stderr, "usage: easyprofilectl ptt on|off")
		return 2
	}
	conn, obj, err := getDBusObject()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()
	var token uint32
	if err := busCall(obj, daemon.DBusInterface+".SetPushToTalk", 0, args[0] == "on").Store(&token); err != nil {
		fmt.Fprintln(stderr, "ptt:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Push-to-talk %s; microfone mutado. Segure 0 no modo áudio para falar.\n", args[0])
	return 0
}
