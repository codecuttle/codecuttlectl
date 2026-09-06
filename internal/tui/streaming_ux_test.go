package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
)

func TestWrapTextPreservesANSIAndUnicode(t *testing.T) {
	m := Model{width: 24}
	plain := strings.Repeat("界e\u0301", 20)
	styled := "\x1b[38;5;252m" + plain + "\x1b[0m"
	got := m.wrapText(styled)
	if !utf8.ValidString(got) {
		t.Fatal("wrapping split a UTF-8 character")
	}
	if unwrapped := strings.ReplaceAll(ansi.Strip(got), "\n", ""); unwrapped != plain {
		t.Fatalf("wrapping corrupted content or escape sequences: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if width := ansi.StringWidth(line); width > 20 {
			t.Fatalf("line width %d exceeds viewport: %q", width, line)
		}
	}
}

func TestViewLeavesSynchronizedOutputToRenderer(t *testing.T) {
	m := New(Config{PluginMgr: pluginhost.NewManager(false)})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.(Model).View()
	if strings.Contains(view.Content, "\x1b[?2026") {
		t.Fatal("View content must not contain renderer-owned synchronized output modes")
	}
}

func TestStickyScrollNavigation(t *testing.T) {
	m := New(Config{PluginMgr: pluginhost.NewManager(false)})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.messages = []chatMessage{{role: "user", content: strings.Repeat("history line\n", 80)}}
	m.updateViewportContent()
	if !m.viewport.AtBottom() {
		t.Fatal("new output should follow the bottom by default")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = updated.(Model)
	if m.autoScroll || m.viewport.YOffset() != 0 {
		t.Fatal("Home should pause following and scroll to top")
	}
	m.messages = append(m.messages, chatMessage{role: "user", content: "new output"})
	m.updateViewportContent()
	if m.viewport.YOffset() != 0 {
		t.Fatal("new output moved a paused viewport")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = updated.(Model)
	if !m.autoScroll || !m.viewport.AtBottom() {
		t.Fatal("End should resume following at the bottom")
	}
	m.messages = append(m.messages, chatMessage{role: "user", content: "more output"})
	m.updateViewportContent()
	if !m.viewport.AtBottom() {
		t.Fatal("new output should follow after End")
	}
}
