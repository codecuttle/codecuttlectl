// Package convert provides conversion utilities between provider-agnostic types
// and AWS Bedrock SDK types. This allows the existing codebase (TUI, agent) to
// continue using Bedrock SDK types internally while the provider layer uses
// agnostic types for cross-provider compatibility.
package provider

import (
	"encoding/json"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// MessagesToProvider converts Bedrock SDK messages to provider-agnostic messages.
func MessagesToProvider(msgs []types.Message) []Message {
	var result []Message
	for _, msg := range msgs {
		result = append(result, MessageToProvider(msg))
	}
	return result
}

// MessageToProvider converts a single Bedrock SDK message to a provider-agnostic message.
func MessageToProvider(msg types.Message) Message {
	var role Role
	switch msg.Role {
	case types.ConversationRoleUser:
		role = RoleUser
	case types.ConversationRoleAssistant:
		role = RoleAssistant
	default:
		role = Role(string(msg.Role))
	}

	var blocks []ContentBlock
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *types.ContentBlockMemberText:
			blocks = append(blocks, TextBlock{Text: b.Value})
		case *types.ContentBlockMemberReasoningContent:
			switch r := b.Value.(type) {
			case *types.ReasoningContentBlockMemberReasoningText:
				blocks = append(blocks, ReasoningBlock{
					Text:      aws.ToString(r.Value.Text),
					Signature: aws.ToString(r.Value.Signature),
				})
			case *types.ReasoningContentBlockMemberRedactedContent:
				blocks = append(blocks, ReasoningBlock{
					Text: "[redacted reasoning content]",
				})
			}
		case *types.ContentBlockMemberToolUse:
			var inputMap interface{}
			if b.Value.Input != nil {
				_ = b.Value.Input.UnmarshalSmithyDocument(&inputMap)
			}
			inputJSON, _ := json.Marshal(inputMap)
			blocks = append(blocks, ToolUseBlock{
				ToolUseID: aws.ToString(b.Value.ToolUseId),
				Name:      aws.ToString(b.Value.Name),
				Input:     inputJSON,
			})
		case *types.ContentBlockMemberToolResult:
			var content string
			for _, rc := range b.Value.Content {
				if text, ok := rc.(*types.ToolResultContentBlockMemberText); ok {
					content += text.Value
				}
			}
			blocks = append(blocks, ToolResultBlock{
				ToolUseID: aws.ToString(b.Value.ToolUseId),
				Content:   content,
				IsError:   b.Value.Status == types.ToolResultStatusError,
			})
		// Skip CachePoint blocks — they're Bedrock-specific
		case *types.ContentBlockMemberCachePoint:
			continue
		}
	}

	return Message{Role: role, Content: blocks}
}

// ToolDefsFromBedrock converts bedrock.ToolDefinition (internal) to provider.ToolDefinition.
// Since they have the same structure, this is a straightforward copy.
func ToolDefsFromBedrock(defs []struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}) []ToolDefinition {
	var result []ToolDefinition
	for _, d := range defs {
		result = append(result, ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}
	return result
}

// SanitizeHistoryForProvider cleans and transcodes conversation messages when switching
// across different LLM backends (e.g. Bedrock Claude -> Google Gemini / OpenRouter),
// preventing HTTP 400 INVALID_ARGUMENT errors from foreign block formats or missing signatures.
func SanitizeHistoryForProvider(msgs []Message, targetProvider string) []Message {
	if len(msgs) == 0 {
		return nil
	}

	knownToolCalls := make(map[string]bool)
	var sanitized []Message

	for _, msg := range msgs {
		var cleanBlocks []ContentBlock
		var textBlocks []string
		var reasoningText []string

		for _, b := range msg.Content {
			switch block := b.(type) {
			case TextBlock:
				if block.Text != "" {
					cleanBlocks = append(cleanBlocks, block)
					textBlocks = append(textBlocks, block.Text)
				}
			case ReasoningBlock:
				// For non-Bedrock or cross-provider transitions, keep reasoning text
				// but allow downgrading to text if the provider doesn't support reasoning blocks
				if block.Text != "" {
					cleanBlocks = append(cleanBlocks, block)
					reasoningText = append(reasoningText, block.Text)
				}
			case ToolUseBlock:
				if block.ToolUseID != "" && block.Name != "" {
					knownToolCalls[block.ToolUseID] = true
					cleanBlocks = append(cleanBlocks, block)
				}
			case ToolResultBlock:
				// Ensure tool result has valid non-empty ToolUseID
				if block.ToolUseID != "" {
					cleanBlocks = append(cleanBlocks, block)
				}
			}
		}

		// Assistant turn validation: if empty content & no tool calls, downgrade reasoning to text if available
		if msg.Role == RoleAssistant {
			hasToolUse := false
			hasText := false
			for _, cb := range cleanBlocks {
				if _, ok := cb.(ToolUseBlock); ok {
					hasToolUse = true
				}
				if _, ok := cb.(TextBlock); ok {
					hasText = true
				}
			}

			if !hasToolUse && !hasText {
				if len(reasoningText) > 0 {
					// Convert reasoning to standard text block
					cleanBlocks = []ContentBlock{TextBlock{Text: strings.Join(reasoningText, "\n")}}
				} else {
					// Drop empty assistant turns that cause upstream 400s
					continue
				}
			}
		}

		if len(cleanBlocks) > 0 {
			sanitized = append(sanitized, Message{
				Role:    msg.Role,
				Content: cleanBlocks,
			})
		}
	}

	return sanitized
}
