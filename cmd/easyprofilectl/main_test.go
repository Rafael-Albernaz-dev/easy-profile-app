package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDaemonFlags_Defaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseDaemonFlags([]string{}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invariant: daemon MUST default to active hardware control (observeMode = false)
	// to prevent UI button and CLI invocation failures.
	if !opts.control {
		t.Errorf("expected control=true by default, got %v", opts.control)
	}
	if opts.observe {
		t.Errorf("expected observe=false by default, got %v", opts.observe)
	}
	if opts.observeMode {
		t.Errorf("expected observeMode=false by default, got %v", opts.observeMode)
	}
}

func TestParseDaemonFlags_ObserveFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantObserve bool
	}{
		{
			name:        "observe flag activates observeMode",
			args:        []string{"--observe"},
			wantObserve: true,
		},
		{
			name:        "observe-only alias activates observeMode",
			args:        []string{"--observe-only"},
			wantObserve: true,
		},
		{
			name:        "control false activates observeMode",
			args:        []string{"--control=false"},
			wantObserve: true,
		},
		{
			name:        "explicit control true keeps observeMode false",
			args:        []string{"--control=true"},
			wantObserve: false,
		},
		{
			name:        "fake mode prevents observeMode",
			args:        []string{"--fake", "--observe"},
			wantObserve: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, err := parseDaemonFlags(tt.args, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.observeMode != tt.wantObserve {
				t.Errorf("got observeMode=%v, want %v", opts.observeMode, tt.wantObserve)
			}
		})
	}
}

func TestParseDaemonFlags_Errors(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseDaemonFlags([]string{"--invalid-flag"}, &stderr)
	if err == nil {
		t.Errorf("expected error for invalid flag, got nil")
	}

	_, err = parseDaemonFlags([]string{"unexpected_arg"}, &stderr)
	if err == nil {
		t.Errorf("expected error for trailing positional arg, got nil")
	}
}

func TestRun_Commands(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Empty args
	if code := run([]string{}, &stdout, &stderr); code != 2 {
		t.Errorf("expected exit code 2 for empty args, got %d", code)
	}

	// Unknown command
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"nonexistent-command"}, &stdout, &stderr); code != 2 {
		t.Errorf("expected exit code 2 for unknown command, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Errorf("expected unknown command in stderr, got: %s", stderr.String())
	}

	// Version
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected exit code 0 for version, got %d", code)
	}
	if !strings.Contains(stdout.String(), "easyprofilectl") {
		t.Errorf("expected easyprofilectl in version output, got: %s", stdout.String())
	}

	// Help
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected exit code 0 for help, got %d", code)
	}
	if !strings.Contains(stdout.String(), "usage: easyprofilectl") {
		t.Errorf("expected usage in help output, got: %s", stdout.String())
	}

	// Volume without args
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"volume"}, &stdout, &stderr); code != 2 {
		t.Errorf("expected exit code 2 for volume without args, got %d", code)
	}

	// Mic without args
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"mic"}, &stdout, &stderr); code != 2 {
		t.Errorf("expected exit code 2 for mic without args, got %d", code)
	}
}
