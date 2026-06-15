package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestBuildSystemBlocks_NoDynamicMarker(t *testing.T) {
	// When there's no dynamic marker, the entire prompt should be cached
	system := "You are a helpful assistant.\n\nFollow instructions carefully."
	blocks := buildSystemBlocks(system)

	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + cache point), got %d", len(blocks))
	}

	// First block: full system text
	textBlock, ok := blocks[0].(*types.SystemContentBlockMemberText)
	if !ok {
		t.Fatal("first block should be text")
	}
	if textBlock.Value != system {
		t.Errorf("text block content mismatch: got %q", textBlock.Value)
	}

	// Second block: cache point
	_, ok = blocks[1].(*types.SystemContentBlockMemberCachePoint)
	if !ok {
		t.Fatal("second block should be cache point")
	}
}

func TestBuildSystemBlocks_WithTaskContextMarker(t *testing.T) {
	stablePart := "You are a helpful assistant.\n\n## Tools\nUse tools."
	dynamicPart := "\n\n## Current Task Context\n**Goal:** Fix bugs"
	system := stablePart + dynamicPart

	blocks := buildSystemBlocks(system)

	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks (stable text + cache point + dynamic text), got %d", len(blocks))
	}

	// Block 0: stable text
	text0, ok := blocks[0].(*types.SystemContentBlockMemberText)
	if !ok {
		t.Fatal("block 0 should be text")
	}
	if text0.Value != stablePart {
		t.Errorf("stable part mismatch:\n  got:  %q\n  want: %q", text0.Value, stablePart)
	}

	// Block 1: cache point
	_, ok = blocks[1].(*types.SystemContentBlockMemberCachePoint)
	if !ok {
		t.Fatal("block 1 should be cache point")
	}

	// Block 2: dynamic text
	text2, ok := blocks[2].(*types.SystemContentBlockMemberText)
	if !ok {
		t.Fatal("block 2 should be text")
	}
	if text2.Value != dynamicPart {
		t.Errorf("dynamic part mismatch:\n  got:  %q\n  want: %q", text2.Value, dynamicPart)
	}
}

func TestApplyCachePoints_EmptyMessages(t *testing.T) {
	result := applyCachePoints(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result = applyCachePoints([]types.Message{})
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(result))
	}
}

func TestApplyCachePoints_SingleUserMessage(t *testing.T) {
	msgs := []types.Message{
		BuildUserTextMessage("hello"),
	}

	result := applyCachePoints(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	// Should have the cache point appended to content
	if len(result[0].Content) != 2 {
		t.Fatalf("expected 2 content blocks (text + cache point), got %d", len(result[0].Content))
	}
	_, ok := result[0].Content[1].(*types.ContentBlockMemberCachePoint)
	if !ok {
		t.Fatal("second content block should be cache point")
	}
}

func TestApplyCachePoints_DoesNotMutateOriginal(t *testing.T) {
	msgs := []types.Message{
		BuildUserTextMessage("hello"),
	}
	origLen := len(msgs[0].Content)

	_ = applyCachePoints(msgs)

	if len(msgs[0].Content) != origLen {
		t.Error("applyCachePoints mutated the original message content slice")
	}
}

func TestApplyCachePoints_AnchorOnLastUserText(t *testing.T) {
	// Simulate: user msg → assistant → tool_result (also Role=User but no text)
	msgs := []types.Message{
		BuildUserTextMessage("please list files"),
		BuildAssistantMessage([]types.ContentBlock{
			&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: strPtr("tu1"),
				Name:      strPtr("bash_exec"),
			}},
		}),
		BuildToolResultMessage([]ToolResult{{ToolUseID: "tu1", Content: "file1.go", Status: types.ToolResultStatusSuccess}}),
	}

	result := applyCachePoints(msgs)

	// Cache point should be on the user text message (index 0), NOT on the tool_result
	if len(result[0].Content) != 2 {
		t.Fatalf("expected cache point on user text message (idx 0), got %d content blocks", len(result[0].Content))
	}
	_, ok := result[0].Content[1].(*types.ContentBlockMemberCachePoint)
	if !ok {
		t.Fatal("cache point should be on user text message")
	}

	// Tool result message should NOT have a cache point
	if len(result[2].Content) != 1 {
		t.Error("tool result message should not have cache point appended")
	}
}

func TestApplyCachePoints_NeverAdvancesMidTurn(t *testing.T) {
	// With many tool rounds in a single turn, the cache point should stay
	// on the user text message and NEVER advance mid-turn. This avoids
	// Bedrock cache corruption from removing a CachePointBlock that was
	// part of a prior message's content array.
	msgs := []types.Message{
		BuildUserTextMessage("do many things"),
	}

	// Add 4 tool rounds (assistant tool_use + user tool_result) = 8 messages
	for i := 0; i < 4; i++ {
		msgs = append(msgs, BuildAssistantMessage([]types.ContentBlock{
			&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: strPtr("tu"),
				Name:      strPtr("bash_exec"),
			}},
		}))
		msgs = append(msgs, BuildToolResultMessage([]ToolResult{{ToolUseID: "tu", Content: "ok", Status: types.ToolResultStatusSuccess}}))
	}

	// Total: 1 user + 8 tool messages = 9 messages
	if len(msgs) != 9 {
		t.Fatalf("expected 9 messages, got %d", len(msgs))
	}

	result := applyCachePoints(msgs)

	// Cache point should remain on the user text message at index 0
	foundCachePoint := false
	for _, block := range result[0].Content {
		if _, ok := block.(*types.ContentBlockMemberCachePoint); ok {
			foundCachePoint = true
			break
		}
	}
	if !foundCachePoint {
		t.Error("expected cache point to stay on user text message (idx 0)")
	}

	// No other messages should have a cache point
	for i := 1; i < len(result); i++ {
		for _, block := range result[i].Content {
			if _, ok := block.(*types.ContentBlockMemberCachePoint); ok {
				t.Errorf("message at index %d should not have a cache point", i)
			}
		}
	}
}

func TestApplyCachePoints_StaysOnUserDuringToolLoop(t *testing.T) {
	// Cache point always stays on user text regardless of tool round count
	msgs := []types.Message{
		BuildUserTextMessage("do stuff"),
	}
	// Add 3 tool rounds (6 messages)
	for i := 0; i < 3; i++ {
		msgs = append(msgs, BuildAssistantMessage([]types.ContentBlock{
			&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
				ToolUseId: strPtr("tu"),
				Name:      strPtr("bash_exec"),
			}},
		}))
		msgs = append(msgs, BuildToolResultMessage([]ToolResult{{ToolUseID: "tu", Content: "ok", Status: types.ToolResultStatusSuccess}}))
	}

	result := applyCachePoints(msgs)

	// Anchor should be on index 0 (user text)
	foundCachePoint := false
	for _, block := range result[0].Content {
		if _, ok := block.(*types.ContentBlockMemberCachePoint); ok {
			foundCachePoint = true
		}
	}
	if !foundCachePoint {
		t.Error("expected cache point to stay on user text message during tool loop")
	}
}

func TestApplyCachePoints_FallbackWhenNoUserText(t *testing.T) {
	// Edge case: only tool_result messages (no text user message)
	msgs := []types.Message{
		BuildToolResultMessage([]ToolResult{{ToolUseID: "tu", Content: "ok", Status: types.ToolResultStatusSuccess}}),
	}

	result := applyCachePoints(msgs)

	// Should fallback to last message (index 0)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	foundCachePoint := false
	for _, block := range result[0].Content {
		if _, ok := block.(*types.ContentBlockMemberCachePoint); ok {
			foundCachePoint = true
		}
	}
	if !foundCachePoint {
		t.Error("expected cache point on fallback last message")
	}
}

func TestHasTextBlock(t *testing.T) {
	tests := []struct {
		name string
		msg  types.Message
		want bool
	}{
		{
			name: "user text message",
			msg:  BuildUserTextMessage("hello"),
			want: true,
		},
		{
			name: "tool result message",
			msg:  BuildToolResultMessage([]ToolResult{{ToolUseID: "tu1", Content: "output", Status: types.ToolResultStatusSuccess}}),
			want: false,
		},
		{
			name: "assistant with tool use only",
			msg: BuildAssistantMessage([]types.ContentBlock{
				&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: strPtr("tu"),
					Name:      strPtr("test"),
				}},
			}),
			want: false,
		},
		{
			name: "assistant with text",
			msg: BuildAssistantMessage([]types.ContentBlock{
				&types.ContentBlockMemberText{Value: "I'll help you."},
			}),
			want: true,
		},
		{
			name: "empty content",
			msg:  types.Message{Content: nil},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasTextBlock(tt.msg)
			if got != tt.want {
				t.Errorf("hasTextBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildToolsWithCache_EmptyTools(t *testing.T) {
	result := buildToolsWithCache(nil)
	if result != nil {
		t.Error("expected nil for empty tools")
	}

	result = buildToolsWithCache([]ToolDefinition{})
	if result != nil {
		t.Error("expected nil for zero-length tools")
	}
}

func TestBuildToolsWithCache_AppendsCachePoint(t *testing.T) {
	tools := []ToolDefinition{
		{
			Name:        "test_tool",
			Description: "A test tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		},
	}

	result := buildToolsWithCache(tools)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have 2 entries: the tool + cache point
	if len(result.Tools) != 2 {
		t.Fatalf("expected 2 tool entries (tool + cache point), got %d", len(result.Tools))
	}

	// First should be the tool spec
	_, ok := result.Tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Error("first entry should be a tool spec")
	}

	// Last should be cache point
	_, ok = result.Tools[1].(*types.ToolMemberCachePoint)
	if !ok {
		t.Error("last entry should be a cache point")
	}
}

func TestBuildToolsWithCache_MultipleTools(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "tool_a", Description: "A", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "tool_b", Description: "B", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "tool_c", Description: "C", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}

	result := buildToolsWithCache(tools)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// 3 tools + 1 cache point = 4
	if len(result.Tools) != 4 {
		t.Fatalf("expected 4 tool entries, got %d", len(result.Tools))
	}

	// Cache point is always last
	_, ok := result.Tools[3].(*types.ToolMemberCachePoint)
	if !ok {
		t.Error("last entry should be a cache point")
	}
}

func TestToBedrockToolsSorted_Deterministic(t *testing.T) {
	tools := []ToolDefinition{
		{
			Name:        "my_tool",
			Description: "does stuff",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"zebra":{"type":"string"},"alpha":{"type":"number"}},"required":["alpha"]}`),
		},
	}

	// Call twice and verify we get the same result
	result1 := toBedrockToolsSorted(tools)
	result2 := toBedrockToolsSorted(tools)

	if len(result1) != len(result2) {
		t.Fatal("determinism check: different lengths")
	}

	// Both should have 1 tool
	if len(result1) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result1))
	}

	spec1, ok := result1[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatal("expected ToolMemberToolSpec")
	}
	spec2, ok := result2[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatal("expected ToolMemberToolSpec")
	}

	if *spec1.Value.Name != *spec2.Value.Name {
		t.Error("names differ between calls")
	}
	if *spec1.Value.Description != *spec2.Value.Description {
		t.Error("descriptions differ between calls")
	}
}
