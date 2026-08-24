package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// CopyToClipboardOSC52 generates an ANSI OSC 52 sequence to copy text to the system clipboard.
// Handles tmux / screen DCS passthrough wrapping automatically if detected.
func CopyToClipboardOSC52(text string) string {
	if text == "" {
		return ""
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc := fmt.Sprintf("\x1b]52;c;%s\x07", encoded)

	// If running inside tmux or screen, wrap in DCS passthrough envelope
	term := os.Getenv("TERM")
	if strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") || os.Getenv("TMUX") != "" {
		// Escape each ESC in the payload with double-ESC for tmux passthrough
		escaped := strings.ReplaceAll(osc, "\x1b", "\x1b\x1b")
		return fmt.Sprintf("\x1bPtmux;%s\x1b\\", escaped)
	}

	return osc
}
