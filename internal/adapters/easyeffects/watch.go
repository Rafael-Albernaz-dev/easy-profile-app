package easyeffects

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// dconf watch emits a path, a value (possibly multiple lines), then a blank line.
// Wake on each path once, never on the value or separator. No key is filtered.
func watchSettingsChanges(input io.Reader, events chan<- struct{}) error {
	scanner := bufio.NewScanner(input)
	havePath := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "/") {
			havePath = true
			select {
			case events <- struct{}{}:
			default:
			}
		} else if strings.TrimSpace(line) != "" && !havePath {
			return errors.New("dconf watch returned a value without a path")
		}
	}
	return scanner.Err()
}
