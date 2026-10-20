package easyeffects

import (
	"strings"
	"testing"
)

func TestSettingsWatcherWakesOncePerPathNotPerValueLine(t *testing.T) {
	events := make(chan struct{}, 10)
	input := "/com/github/wwmm/easyeffects/last-used-output-preset\n'Preset'\n\n/com/github/wwmm/easyeffects/streamoutputs/plugins\n['equalizer#0',\n'limiter#0']\n\n/com/github/wwmm/easyeffects/streaminputs/input-device\n'mic'\n\n"
	if err := watchSettingsChanges(strings.NewReader(input), events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected three changes, got %d wakeups", len(events))
	}
}

func TestSettingsWatcherCoalescesBurstAndRejectsMalformedStream(t *testing.T) {
	events := make(chan struct{}, 1)
	if err := watchSettingsChanges(strings.NewReader(strings.Repeat("/some/unknown/key\ntrue\n\n", 100)), events); err != nil || len(events) != 1 {
		t.Fatalf("unknown keys lost or burst not coalesced: %v", err)
	}
	if err := watchSettingsChanges(strings.NewReader("unexpected value\n"), events); err == nil {
		t.Fatal("malformed watch was silently ignored")
	}
}
