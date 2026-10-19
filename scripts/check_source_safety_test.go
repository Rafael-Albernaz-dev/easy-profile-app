package main

import "testing"

func TestASTGateRejectsShellCallsAndAdapterWritesButNotComments(t *testing.T) {
	cases := []struct {
		path, source string
		reject       bool
	}{
		{"cmd/app/main.go", `package main;import "os/exec";func run(){exec.CommandContext(ctx,"bash","-c",input)}`, true},
		{"internal/adapters/audio.go", `package audio;import "os";func run(){os.WriteFile(path,data,0600)}`, true},
		{"internal/daemon/audio_test.go", `package audio;import "os/exec";func run(){}`, true},
		{"internal/adapters/audio.go", `package audio;import e "os/exec";func run(){e.Command("wpctl","set-volume")}`, true},
		{"internal/adapters/audio.go", `package audio;import "os/exec";func run(){exec.CommandContext(ctx,"wpctl","set-volume",id,value)}`, false},
		{"internal/adapters/audio.go", "package audio\n// forbidden words bash -c and os.WriteFile in a comment\n", false},
	}
	for _, tc := range cases {
		if got := len(auditSource(tc.path, []byte(tc.source))) > 0; got != tc.reject {
			t.Fatalf("%s rejected=%v expected=%v", tc.source, got, tc.reject)
		}
	}
}
