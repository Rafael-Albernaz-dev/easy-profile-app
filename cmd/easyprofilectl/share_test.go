package main

import (
	"bytes"
	"testing"
)

func TestShareRejectsInvalidArgumentsBeforeConnecting(t *testing.T) {
	t.Setenv("EASYPROFILE_TEST_MODE", "1")
	for _, args := range [][]string{{"invalid"}, {"on", "extra"}} {
		var out, err bytes.Buffer
		if code := runShare(args, &out, &err); code != 2 {
			t.Fatalf("invalid share args accepted: %v", args)
		}
	}
}
