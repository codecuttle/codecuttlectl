// Package compact implements heuristic context compaction for conversation history.
//
// Tool results (especially read_file, grep, glob) accumulate large volumes of
// content in the message history. This content stays in the context window for
// every subsequent API call even when it's no longer relevant. Compaction replaces
// old, verbose tool results with concise summaries while preserving enough context
// for the model to know what was there and re-read if needed.
//
// Design principles:
//   - Never mutate the Inkwell or session file — those remain full fidelity
//   - Only compact tool_result content blocks in the message history
//   - Preserve recent results (last N turns) untouched
//   - Produce summaries that tell the model WHAT was there and WHERE to look
//   - Accept one cache miss at the compaction boundary (amortized quickly)
package compact

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Config controls when and how compaction occurs.
type Config struct {
	// MaxContextPercent triggers compaction when the most recent API call's
	// input tokens exceed this fraction of the context window. Default: 0.5 (50%).
	MaxContextPercent float64

	// PreserveRecentTurns is the number of recent user turns whose tool results
	// are never compacted. Default: 7 (keeps a generous working window of recent
	// context intact; with 1M context window we can afford the space).
	PreserveRecentTurns int

	// SummaryMaxLines is the maximum number of lines to include in a compacted
	// summary (head + tail combined). Default: 8.
	SummaryMaxLines int

	// MinResultSize is the minimum character length of a tool result before it's
	// eligible for compaction. Small results aren't worth compacting. Default: 1000.
	MinResultSize int
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxContextPercent:   0.50,
		PreserveRecentTurns: 7,
		SummaryMaxLines:     8,
		MinResultSize:       1000,
	}
}

// Result holds the outcome of a compaction pass.
type Result struct {
	Messages       []types.Message // The compacted message history
	TokensEstimate int             // Estimated tokens freed (rough: chars/4)
	Compacted      int             // Number of tool results that were compacted
}

// Compact rewrites old tool_result messages in the history with summaries.
// It returns a new message slice (the original is not mutated).
//
// The currentTurn parameter indicates the current conversation turn number,
// used to determine which messages are "recent" and should be preserved.
func Compact(messages []types.Message, currentTurn int, cfg Config) Result {
	if len(messages) == 0 {
		return Result{Messages: messages}
	}

	// Identify the boundary: messages belonging to the last N user turns are preserved.
	// We count user messages backward to find the preserve boundary.
	preserveFromIdx := findPreserveBoundary(messages, cfg.PreserveRecentTurns)

	result := make([]types.Message, len(messages))
	totalFreed := 0
	compacted := 0

	for i, msg := range messages {
		if i >= preserveFromIdx {
			// Recent messages — preserve as-is
			result[i] = msg
			continue
		}

		// Only compact user messages (tool results are sent as user role)
		if msg.Role != types.ConversationRoleUser {
			result[i] = msg
			continue
		}

		// Check if this message contains tool_result blocks worth compacting
		newContent, freed, count := compactToolResults(msg.Content, cfg)
		if count > 0 {
			result[i] = types.Message{
				Role:    msg.Role,
				Content: newContent,
			}
			totalFreed += freed
			compacted += count
		} else {
			result[i] = msg
		}
	}

	return Result{
		Messages:       result,
		TokensEstimate: totalFreed / 4, // Rough chars-to-tokens ratio
		Compacted:      compacted,
	}
}

// ShouldCompact returns true if compaction should be triggered based on
// the most recent API call's token usage relative to the context window.
func ShouldCompact(lastCallTokens int32, contextWindowSize int32, cfg Config) bool {
	if lastCallTokens <= 0 || contextWindowSize <= 0 {
		return false
	}
	pct := float64(lastCallTokens) / float64(contextWindowSize)
	return pct >= cfg.MaxContextPercent
}

// findPreserveBoundary walks backward through messages counting user text turns
// and returns the index at or after which all messages should be preserved.
// A "user turn" starts with a user text message and includes all subsequent
// assistant/tool messages until the next user text message.
func findPreserveBoundary(messages []types.Message, preserveTurns int) int {
	if preserveTurns <= 0 {
		return len(messages)
	}

	// Find indices of user text messages (turn boundaries)
	var turnStarts []int
	for i, msg := range messages {
		if msg.Role == types.ConversationRoleUser && hasTextContent(msg) {
			turnStarts = append(turnStarts, i)
		}
	}

	if len(turnStarts) <= preserveTurns {
		// Not enough turns to compact anything
		return 0
	}

	// Preserve from the Nth-from-last turn start onward
	preserveIdx := turnStarts[len(turnStarts)-preserveTurns]
	return preserveIdx
}

// hasTextContent returns true if a message contains at least one text content block
// (as opposed to only tool_result blocks).
func hasTextContent(msg types.Message) bool {
	for _, block := range msg.Content {
		if _, ok := block.(*types.ContentBlockMemberText); ok {
			return true
		}
	}
	return false
}

// compactToolResults processes content blocks, replacing large tool_result blocks
// with summaries. Returns the new content slice, chars freed, and count compacted.
func compactToolResults(content []types.ContentBlock, cfg Config) ([]types.ContentBlock, int, int) {
	newContent := make([]types.ContentBlock, 0, len(content))
	totalFreed := 0
	count := 0

	for _, block := range content {
		tr, ok := block.(*types.ContentBlockMemberToolResult)
		if !ok {
			newContent = append(newContent, block)
			continue
		}

		// Extract text content from the tool result
		text := extractToolResultText(tr)
		if len(text) < cfg.MinResultSize {
			// Too small to bother compacting
			newContent = append(newContent, block)
			continue
		}

		// Generate a summary
		summary := summarizeToolResult(text, cfg.SummaryMaxLines)
		freed := len(text) - len(summary)
		if freed <= 0 {
			newContent = append(newContent, block)
			continue
		}

		// Build a new tool result with the summary
		newContent = append(newContent, &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: tr.Value.ToolUseId,
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: summary},
				},
				Status: tr.Value.Status,
			},
		})

		totalFreed += freed
		count++
	}

	return newContent, totalFreed, count
}

// extractToolResultText extracts the text content from a tool result block.
func extractToolResultText(tr *types.ContentBlockMemberToolResult) string {
	var sb strings.Builder
	for _, content := range tr.Value.Content {
		if textBlock, ok := content.(*types.ToolResultContentBlockMemberText); ok {
			sb.WriteString(textBlock.Value)
		}
	}
	return sb.String()
}

// summarizeToolResult produces a heuristic summary of a tool result.
// It preserves the first few lines and last few lines with a count of omitted lines.
func summarizeToolResult(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	totalLines := len(lines)

	if totalLines <= maxLines {
		return text // Already short enough
	}

	headLines := maxLines / 2
	tailLines := maxLines - headLines
	omitted := totalLines - headLines - tailLines

	var sb strings.Builder
	// Head
	for i := 0; i < headLines && i < totalLines; i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}

	// Omission marker
	sb.WriteString(fmt.Sprintf("\n[... %d lines omitted — use read_file with offset/limit to retrieve specific sections ...]\n\n", omitted))

	// Tail
	start := totalLines - tailLines
	if start < headLines {
		start = headLines
	}
	for i := start; i < totalLines; i++ {
		sb.WriteString(lines[i])
		if i < totalLines-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
