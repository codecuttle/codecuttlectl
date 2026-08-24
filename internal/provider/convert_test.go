package provider

import (
	"testing"
)

func TestSanitizeHistoryForProvider(t *testing.T) {
	// Case 1: Assistant with only reasoning gets downgraded to TextBlock
	msgs := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				TextBlock{Text: "Please think about this"},
			},
		},
		{
			Role: RoleAssistant,
			Content: []ContentBlock{
				ReasoningBlock{Text: "This is pure internal reasoning text without a signature."},
			},
		},
	}

	sanitized := SanitizeHistoryForProvider(msgs, "google:gemini-3.1-flash")
	if len(sanitized) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(sanitized))
	}

	asstMsg := sanitized[1]
	if len(asstMsg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(asstMsg.Content))
	}

	if tb, ok := asstMsg.Content[0].(TextBlock); !ok || tb.Text != "This is pure internal reasoning text without a signature." {
		t.Errorf("expected reasoning to be converted to TextBlock, got: %#v", asstMsg.Content[0])
	}

	// Case 2: Empty assistant message with no tool calls or text is dropped
	emptyMsgs := []Message{
		{
			Role:    RoleAssistant,
			Content: []ContentBlock{},
		},
	}
	sanitizedEmpty := SanitizeHistoryForProvider(emptyMsgs, "google:gemini-3.1-flash")
	if len(sanitizedEmpty) != 0 {
		t.Errorf("expected empty assistant message to be dropped, got %d messages", len(sanitizedEmpty))
	}
}
