package main

import (
	"bytes"
	"testing"
)

func TestBoostRejectsInvalidArgumentsBeforeConnecting(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	for _, args := range [][]string{{"invalid"}, {"on", "extra"}} {
		var out, err bytes.Buffer
		if code := runBoost(args, &out, &err); code != 2 {
			t.Fatalf("invalid boost args accepted: %v", args)
		}
	}
}
