package inkwell

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStreamMarkdownLexerBullets(t *testing.T) {
	for _, input := range []string{"- item", "* item"} {
		got := ansi.Strip(NewStreamMarkdownLexer().RenderStreamChunk(input))
		if got != "  • item" {
			t.Errorf("RenderStreamChunk(%q) = %q", input, got)
		}
	}
}

func TestStreamMarkdownLexerSnapshots(t *testing.T) {
	lexer := NewStreamMarkdownLexer()
	for _, snapshot := range []string{"```go\n界", "```go\n界\n```\nplain", "a new response"} {
		got := lexer.RenderStreamChunk(snapshot)
		want := NewStreamMarkdownLexer().RenderStreamChunk(snapshot)
		if got != want {
			t.Fatalf("snapshot depends on previous render: %q", got)
		}
	}
}

func TestStreamMarkdownLexer_RenderStreamChunk(t *testing.T) {
	lexer := NewStreamMarkdownLexer()

	// Test header formatting
	raw := "## Section Title\n- First point\n- Second point"
	rendered := lexer.RenderStreamChunk(raw)

	if !strings.Contains(rendered, "Section Title") {
		t.Errorf("expected rendered output to contain Section Title")
	}
	if !strings.Contains(rendered, "•") {
		t.Errorf("expected rendered output to format bullet point with dot")
	}

	// Test code fence highlighting
	codeRaw := "```go\npackage main\n\nfunc main() {}\n```"
	codeRendered := lexer.RenderStreamChunk(codeRaw)

	if !strings.Contains(codeRendered, "package main") {
		t.Errorf("expected code block to contain package main")
	}

	// Test partial code fence during active streaming
	partialCode := "```go\nfunc add(a, b int) int {"
	partialRendered := lexer.RenderStreamChunk(partialCode)
	if !strings.Contains(partialRendered, "func add") {
		t.Errorf("expected partial code block to render without error")
	}
}
