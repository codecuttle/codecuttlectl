package compact

import (
	"fmt"
	"strings"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

func TestCompactProvider_PreservesRecentTurns(t *testing.T) {
	// Build a provider message history with 3 user text turns
	// and tool results between them. Use multi-line content so summarizer works.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d: some content here for the file", i))
	}
	longContent := strings.Join(lines, "\n")

	messages := []provider.Message{
		// Turn 1: user + assistant + tool result
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Read the file"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.ToolUseBlock{ToolUseID: "tu1", Name: "read_file", Input: []byte(`{"path":"/foo"}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu1", Content: longContent}}},
		// Turn 2: user + assistant + tool result
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Now edit it"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.ToolUseBlock{ToolUseID: "tu2", Name: "edit_file", Input: []byte(`{"path":"/foo"}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu2", Content: longContent}}},
		// Turn 3 (most recent): user text
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Check the result"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.ToolUseBlock{ToolUseID: "tu3", Name: "bash_exec", Input: []byte(`{"command":"cat /foo"}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu3", Content: longContent}}},
	}

	cfg := SmallModelConfig()
	// PreserveRecentTurns: 2 — should preserve turns 2 and 3, compact turn 1

	result := CompactProvider(messages, 3, cfg)

	if result.Compacted != 1 {
		t.Errorf("expected 1 compacted tool result (turn 1), got %d", result.Compacted)
	}

	// Check that turn 1's tool result was compacted
	trBlock, ok := result.Messages[2].Content[0].(provider.ToolResultBlock)
	if !ok {
		t.Fatalf("expected ToolResultBlock at messages[2], got %T", result.Messages[2].Content[0])
	}
	if len(trBlock.Content) >= len(longContent) {
		t.Errorf("turn 1 tool result should have been compacted (len=%d, original=%d)", len(trBlock.Content), len(longContent))
	}

	// Check that turn 3's tool result is preserved
	trBlock3, ok := result.Messages[8].Content[0].(provider.ToolResultBlock)
	if !ok {
		t.Fatalf("expected ToolResultBlock at messages[8], got %T", result.Messages[8].Content[0])
	}
	if trBlock3.Content != longContent {
		t.Error("turn 3 tool result should be preserved verbatim")
	}
}

func TestCompactProvider_SmallResultsNotCompacted(t *testing.T) {
	shortContent := "OK"
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Do something"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.ToolUseBlock{ToolUseID: "tu1", Name: "bash_exec", Input: []byte(`{}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu1", Content: shortContent}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Next step"}}},
	}

	cfg := SmallModelConfig()
	result := CompactProvider(messages, 2, cfg)

	if result.Compacted != 0 {
		t.Errorf("expected 0 compacted (content too small), got %d", result.Compacted)
	}
}

func TestCompactProviderIfNeeded_AlwaysCompactsWhenZeroPercent(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d: content here for padding", i))
	}
	longContent := strings.Join(lines, "\n")

	messages := []provider.Message{
		// Turn 1 (oldest — should be compacted)
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Initial request"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.ToolUseBlock{ToolUseID: "tu1", Name: "read_file", Input: []byte(`{}`)}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu1", Content: longContent}}},
		// Turn 2
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Continue working"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.TextBlock{Text: "OK"}}},
		// Turn 3 (most recent)
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Final step"}}},
	}

	cfg := SmallModelConfig() // MaxContextPercent: 0.0, PreserveRecentTurns: 2

	// With 0 tokens and 0 window, it should still compact because MaxContextPercent is 0
	result, compacted := CompactProviderIfNeeded(messages, 3, 0, 0, cfg)
	if !compacted {
		t.Error("expected compaction to occur when MaxContextPercent is 0")
	}

	// Verify the tool result was actually compacted (turn 1 is outside preserve window)
	trBlock, ok := result[2].Content[0].(provider.ToolResultBlock)
	if !ok {
		t.Fatalf("expected ToolResultBlock, got %T", result[2].Content[0])
	}
	if len(trBlock.Content) >= len(longContent) {
		t.Errorf("tool result should have been compacted (len=%d, original=%d)", len(trBlock.Content), len(longContent))
	}
}

func TestFindProviderPreserveBoundary(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Turn 1"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.TextBlock{Text: "Response 1"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.ToolResultBlock{ToolUseID: "tu1", Content: "result"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Turn 2"}}},
		{Role: provider.RoleAssistant, Content: []provider.ContentBlock{provider.TextBlock{Text: "Response 2"}}},
		{Role: provider.RoleUser, Content: []provider.ContentBlock{provider.TextBlock{Text: "Turn 3"}}},
	}

	tests := []struct {
		name          string
		preserveTurns int
		wantIdx       int
	}{
		{"preserve 1", 1, 5},
		{"preserve 2", 2, 3},
		{"preserve 3", 3, 0},
		{"preserve 0", 0, 6}, // preserve nothing → len(messages)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findProviderPreserveBoundary(messages, tt.preserveTurns)
			if got != tt.wantIdx {
				t.Errorf("findProviderPreserveBoundary(..., %d) = %d, want %d", tt.preserveTurns, got, tt.wantIdx)
			}
		})
	}
}

// Silence unused import
var _ = fmt.Sprint
