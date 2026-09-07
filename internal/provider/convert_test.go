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

	// Case 3: Target-aware signature preservation / stripping and orphan tool results dropping
	mixedMsgs := []Message{
		{
			Role: RoleAssistant,
			Content: []ContentBlock{
				ReasoningBlock{Text: "Thinking on Bedrock", Signature: "bedrock_sig_123"},
				ToolUseBlock{ToolUseID: "tool_1", Name: "read_file", ThoughtSignature: "gemini_thought_sig_456"},
			},
		},
		{
			Role: RoleUser,
			Content: []ContentBlock{
				ToolResultBlock{ToolUseID: "tool_1", Content: "file content"},
				ToolResultBlock{ToolUseID: "orphan_tool_99", Content: "orphan result"},
			},
		},
	}

	// 3a. Target is OpenRouter: Bedrock reasoning signature & Gemini thought signature should be stripped; orphan tool dropped
	sanitizedOpenRouter := SanitizeHistoryForProvider(mixedMsgs, "openrouter:deepseek/deepseek-v4-pro-0813")
	if len(sanitizedOpenRouter) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(sanitizedOpenRouter))
	}
	asstRB := sanitizedOpenRouter[0].Content[0].(ReasoningBlock)
	if asstRB.Signature != "" {
		t.Errorf("expected Bedrock signature stripped for OpenRouter, got: %q", asstRB.Signature)
	}
	asstTUB := sanitizedOpenRouter[0].Content[1].(ToolUseBlock)
	if asstTUB.ThoughtSignature != "" {
		t.Errorf("expected Gemini thought signature stripped for OpenRouter, got: %q", asstTUB.ThoughtSignature)
	}
	userContent := sanitizedOpenRouter[1].Content
	if len(userContent) != 1 {
		t.Fatalf("expected 1 tool result (orphan dropped), got %d", len(userContent))
	}
	if trb := userContent[0].(ToolResultBlock); trb.ToolUseID != "tool_1" {
		t.Errorf("expected tool_1 result preserved, got: %s", trb.ToolUseID)
	}

	// 3b. Target is Bedrock: Bedrock signature preserved
	sanitizedBedrock := SanitizeHistoryForProvider(mixedMsgs, "bedrock:us.anthropic.claude-opus-4-6-v1")
	bedrockRB := sanitizedBedrock[0].Content[0].(ReasoningBlock)
	if bedrockRB.Signature != "bedrock_sig_123" {
		t.Errorf("expected Bedrock signature preserved for Bedrock target, got: %q", bedrockRB.Signature)
	}

	// 3c. Target is Google: Gemini thought signature preserved
	sanitizedGoogle := SanitizeHistoryForProvider(mixedMsgs, "google:gemini-3.1-flash")
	googleTUB := sanitizedGoogle[0].Content[1].(ToolUseBlock)
	if googleTUB.ThoughtSignature != "gemini_thought_sig_456" {
		t.Errorf("expected Gemini thought signature preserved for Google target, got: %q", googleTUB.ThoughtSignature)
	}
}
