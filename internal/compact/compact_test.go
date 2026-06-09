package compact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func buildToolResultMsg(toolUseID, content string) types.Message {
	return types.Message{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: aws.String(toolUseID),
					Content: []types.ToolResultContentBlock{
						&types.ToolResultContentBlockMemberText{Value: content},
					},
					Status: types.ToolResultStatusSuccess,
				},
			},
		},
	}
}

func buildUserTextMsg(text string) types.Message {
	return types.Message{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: text},
		},
	}
}

func buildAssistantMsg(text string) types.Message {
	return types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: text},
		},
	}
}

func TestSummarizeToolResult(t *testing.T) {
	// Build a 100-line file content
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d: content here", i))
	}
	content := strings.Join(lines, "\n")

	summary := summarizeToolResult(content, 8)

	// Should contain head lines
	if !strings.Contains(summary, "line 1: content here") {
		t.Error("summary should contain first line")
	}
	if !strings.Contains(summary, "line 4: content here") {
		t.Error("summary should contain line 4 (head)")
	}

	// Should contain tail lines
	if !strings.Contains(summary, "line 100: content here") {
		t.Error("summary should contain last line")
	}
	if !strings.Contains(summary, "line 97: content here") {
		t.Error("summary should contain line 97 (tail)")
	}

	// Should contain omission marker
	if !strings.Contains(summary, "lines omitted") {
		t.Error("summary should contain omission marker")
	}
	if !strings.Contains(summary, "read_file with offset/limit") {
		t.Error("summary should hint at how to retrieve full content")
	}

	// Should NOT contain middle content
	if strings.Contains(summary, "line 50: content here") {
		t.Error("summary should NOT contain middle lines")
	}

	// Should be significantly shorter
	if len(summary) >= len(content) {
		t.Errorf("summary (%d chars) should be shorter than original (%d chars)", len(summary), len(content))
	}
}

func TestSummarizeShortContent(t *testing.T) {
	content := "line 1\nline 2\nline 3"
	summary := summarizeToolResult(content, 8)

	// Short content should pass through unchanged
	if summary != content {
		t.Errorf("short content should not be modified, got %q", summary)
	}
}

func TestCompactPreservesRecentTurns(t *testing.T) {
	// Build a conversation: 3 user turns with tool results
	// Content must be multi-line and > MinResultSize to be eligible
	var bigLines []string
	for i := 0; i < 200; i++ {
		bigLines = append(bigLines, fmt.Sprintf("line %d: some file content that is realistic", i))
	}
	bigContent := strings.Join(bigLines, "\n")

	messages := []types.Message{
		// Turn 1 (old)
		buildUserTextMsg("first question"),
		buildAssistantMsg("let me check"),
		buildToolResultMsg("tool_1", bigContent),
		// Turn 2 (old)
		buildUserTextMsg("second question"),
		buildAssistantMsg("checking again"),
		buildToolResultMsg("tool_2", bigContent),
		// Turn 3 (recent — should be preserved)
		buildUserTextMsg("third question"),
		buildAssistantMsg("one more check"),
		buildToolResultMsg("tool_3", bigContent),
	}

	cfg := DefaultConfig()
	cfg.PreserveRecentTurns = 1 // Only preserve last 1 user turn

	result := Compact(messages, 3, cfg)

	// Turn 1 and 2 tool results should be compacted
	if result.Compacted < 2 {
		t.Errorf("expected at least 2 compacted results, got %d", result.Compacted)
	}

	// Turn 3 tool result should be preserved (full content)
	lastToolResult := result.Messages[8]
	text := extractMsgText(lastToolResult)
	if len(text) < len(bigContent) {
		t.Errorf("recent turn tool result should be preserved full, got %d chars (expected %d)", len(text), len(bigContent))
	}

	// Token savings should be positive
	if result.TokensEstimate <= 0 {
		t.Error("expected positive token savings")
	}
}

func TestCompactSkipsSmallResults(t *testing.T) {
	smallContent := "short result"

	messages := []types.Message{
		buildUserTextMsg("question"),
		buildAssistantMsg("checking"),
		buildToolResultMsg("tool_1", smallContent),
		buildUserTextMsg("follow up"), // This makes the above "old"
		buildAssistantMsg("done"),
	}

	cfg := DefaultConfig()
	cfg.PreserveRecentTurns = 1

	result := Compact(messages, 2, cfg)

	// Small content should not be compacted
	if result.Compacted != 0 {
		t.Errorf("expected 0 compacted (content too small), got %d", result.Compacted)
	}
}

func TestShouldCompact(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxContextPercent = 0.50

	tests := []struct {
		name       string
		tokens     int32
		window     int32
		wantResult bool
	}{
		{"below threshold", 400_000, 1_000_000, false},
		{"at threshold", 500_000, 1_000_000, true},
		{"above threshold", 700_000, 1_000_000, true},
		{"zero tokens", 0, 1_000_000, false},
		{"zero window", 100_000, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldCompact(tt.tokens, tt.window, cfg)
			if got != tt.wantResult {
				t.Errorf("ShouldCompact(%d, %d) = %v, want %v", tt.tokens, tt.window, got, tt.wantResult)
			}
		})
	}
}

func TestCompactEmptyHistory(t *testing.T) {
	result := Compact(nil, 0, DefaultConfig())
	if result.Messages != nil {
		t.Error("nil input should return nil messages")
	}
	if result.Compacted != 0 {
		t.Error("nil input should have 0 compacted")
	}
}

// extractMsgText extracts text from a tool_result message for testing.
func extractMsgText(msg types.Message) string {
	for _, block := range msg.Content {
		if tr, ok := block.(*types.ContentBlockMemberToolResult); ok {
			for _, c := range tr.Value.Content {
				if tb, ok := c.(*types.ToolResultContentBlockMemberText); ok {
					return tb.Value
				}
			}
		}
	}
	return ""
}
