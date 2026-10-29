//go:build !darwin && !linux

package cli

import "testing"

func pseudoTerminal(t *testing.T) string {
	t.Helper()
	t.Skip("opening a pseudo terminal is written for darwin and linux only")

	return ""
}
