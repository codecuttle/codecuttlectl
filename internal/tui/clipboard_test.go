package tui

import (
	"strings"
	"testing"
)

func TestCopyToClipboardEnvelopes(t *testing.T) {
	const osc = "\x1b]52;c;aGk=\x07"
	for _, tc := range []struct {
		name, term, tmux, want string
	}{
		{"terminal", "xterm-256color", "", osc},
		{"screen", "screen-256color", "", "\x1bP" + osc + "\x1b\\"},
		{"tmux-term", "tmux-256color", "", "\x1bPtmux;\x1b" + osc + "\x1b\\"},
		{"tmux-screen-term", "screen-256color", "/tmp/tmux", "\x1bPtmux;\x1b" + osc + "\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("TMUX", tc.tmux)
			if got := CopyToClipboardOSC52("hi"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

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
