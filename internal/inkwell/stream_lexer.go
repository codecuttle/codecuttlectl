package inkwell

import (
	"strings"
)

// StreamMarkdownLexer incrementally lexes and formats streaming markdown text
// into styled ANSI escape sequences on a line-by-line basis.
type StreamMarkdownLexer struct {
	inCodeBlock bool
	codeLang    string
}

// NewStreamMarkdownLexer creates a new incremental markdown streaming lexer.
func NewStreamMarkdownLexer() *StreamMarkdownLexer {
	return &StreamMarkdownLexer{}
}

// Reset clears the lexer state for a new stream.
func (l *StreamMarkdownLexer) Reset() {
	l.inCodeBlock = false
	l.codeLang = ""
}

// RenderStreamChunk formats streaming markdown text up to the current progress.
func (l *StreamMarkdownLexer) RenderStreamChunk(raw string) string {
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	var formattedLines []string

	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inBlock {
				inBlock = false
				continue
			} else {
				inBlock = true
				continue
			}
		}

		if inBlock {
			formattedLines = append(formattedLines, "\x1b[38;5;252m    "+line+"\x1b[0m")
			continue
		}

		// Normal markdown line formatting (headers, bullet points, inline styles)
		formattedLines = append(formattedLines, formatMarkdownLine(line))
	}

	return strings.Join(formattedLines, "\n")
}

func formatMarkdownLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Headers
	if strings.HasPrefix(trimmed, "### ") {
		return "\x1b[1;34m" + line + "\x1b[0m" // Bold Blue
	}
	if strings.HasPrefix(trimmed, "## ") {
		return "\x1b[1;36m" + line + "\x1b[0m" // Bold Cyan
	}
	if strings.HasPrefix(trimmed, "# ") {
		return "\x1b[1;35m" + line + "\x1b[0m" // Bold Magenta
	}

	// Bullet points
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return "  \x1b[32m•\x1b[0m " + strings.TrimPrefix(trimmed, "- ")
	}

	// Inline code highlighting (simple replace `code` with styled ANSI)
	if strings.Contains(line, "`") {
		parts := strings.Split(line, "`")
		if len(parts) >= 3 {
			var sb strings.Builder
			for idx, part := range parts {
				if idx%2 == 1 {
					sb.WriteString("\x1b[48;5;236;38;5;222m " + part + " \x1b[0m")
				} else {
					sb.WriteString(part)
				}
			}
			return sb.String()
		}
	}

	return line
}
