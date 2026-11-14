package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// On Windows cobra refuses to run anything Explorer started unless this is empty, so a
// double-click would show "This is a command line tool" instead of the flasher.
func TestDoubleClickNotBlocked(t *testing.T) {
	if cobra.MousetrapHelpText != "" {
		t.Fatalf("cobra.MousetrapHelpText = %q; a double-click on Windows would not open the flasher", cobra.MousetrapHelpText)
	}
}
