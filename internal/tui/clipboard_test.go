package tui

import (
	"strings"
	"testing"
)

func TestCopyToClipboardOSC52(t *testing.T) {
	seq := CopyToClipboardOSC52("hello world")
	if !strings.Contains(seq, "]52;c;") {
		t.Errorf("expected OSC 52 sequence, got: %q", seq)
	}

	empty := CopyToClipboardOSC52("")
	if empty != "" {
		t.Errorf("expected empty string for empty input")
	}
}
