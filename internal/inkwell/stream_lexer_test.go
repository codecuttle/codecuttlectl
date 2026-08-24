package inkwell

import (
	"strings"
	"testing"
)

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
