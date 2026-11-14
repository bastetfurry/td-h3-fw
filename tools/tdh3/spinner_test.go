package main

import (
	"bytes"
	"testing"
	"time"
)

func TestSpinnerNotATerminal(t *testing.T) {
	old := out
	defer func() { out = old }()
	var b bytes.Buffer
	out = &b
	sp := spin("reading the radio")
	time.Sleep(350 * time.Millisecond) // long enough for several frames on a terminal
	sp.stop()
	if got, want := b.String(), "  ... reading the radio\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
