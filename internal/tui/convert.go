package tui

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// bedrockToProviderMessages converts Bedrock SDK messages to provider-agnostic messages.
// Used when loading saved sessions that were serialized in Bedrock format.
func bedrockToProviderMessages(msgs []types.Message) []provider.Message {
	var result []provider.Message
	toolNames := make(map[string]string) // ToolUseID -> Name

	for _, msg := range msgs {
		var role provider.Role
		switch msg.Role {
		case types.ConversationRoleUser:
			role = provider.RoleUser
		case types.ConversationRoleAssistant:
			role = provider.RoleAssistant
		default:
			role = provider.Role(string(msg.Role))
		}

		var blocks []provider.ContentBlock
		for _, block := range msg.Content {
			switch b := block.(type) {
			case *types.ContentBlockMemberText:
				blocks = append(blocks, provider.TextBlock{Text: b.Value})
			case *types.ContentBlockMemberReasoningContent:
				switch r := b.Value.(type) {
				case *types.ReasoningContentBlockMemberReasoningText:
					blocks = append(blocks, provider.ReasoningBlock{
						Text:      aws.ToString(r.Value.Text),
						Signature: aws.ToString(r.Value.Signature),
					})
				case *types.ReasoningContentBlockMemberRedactedContent:
					blocks = append(blocks, provider.ReasoningBlock{
						Text: "[redacted reasoning content]",
					})
				}
			case *types.ContentBlockMemberToolUse:
				var inputMap interface{}
				if b.Value.Input != nil {
					_ = b.Value.Input.UnmarshalSmithyDocument(&inputMap)
				}
				inputJSON, _ := json.Marshal(inputMap)
				id := aws.ToString(b.Value.ToolUseId)
				name := aws.ToString(b.Value.Name)
				toolNames[id] = name
				blocks = append(blocks, provider.ToolUseBlock{
					ToolUseID: id,
					Name:      name,
					Input:     inputJSON,
				})
			case *types.ContentBlockMemberToolResult:
				var content string
				for _, rc := range b.Value.Content {
					if text, ok := rc.(*types.ToolResultContentBlockMemberText); ok {
						content += text.Value
					}
				}
				id := aws.ToString(b.Value.ToolUseId)
				blocks = append(blocks, provider.ToolResultBlock{
					ToolUseID: id,
					Name:      toolNames[id],
					Content:   content,
					IsError:   b.Value.Status == types.ToolResultStatusError,
				})
			case *types.ContentBlockMemberCachePoint:
				// Skip cache points — Bedrock-specific
				continue
			}
		}

		result = append(result, provider.Message{Role: role, Content: blocks})
	}
	return result
}

// providerToBedrockMessages converts provider-agnostic messages back to Bedrock SDK types.
// Used for cache keepalive pings which use the raw Bedrock client.
func providerToBedrockMessages(msgs []provider.Message) []types.Message {
	var result []types.Message
	toolUseCounts := make(map[string]int)

	for _, msg := range msgs {
		var role types.ConversationRole
		switch msg.Role {
		case provider.RoleUser:
			role = types.ConversationRoleUser
		case provider.RoleAssistant:
			role = types.ConversationRoleAssistant
		default:
			role = types.ConversationRole(string(msg.Role))
		}

		var content []types.ContentBlock
		var skippedReasoning string

		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.TextBlock:
				content = append(content, &types.ContentBlockMemberText{Value: b.Text})
			case provider.ReasoningBlock:
				if b.Signature == "" {
					if skippedReasoning != "" {
						skippedReasoning += "\n"
					}
					skippedReasoning += b.Text
					continue
				}
				content = append(content, &types.ContentBlockMemberReasoningContent{
					Value: &types.ReasoningContentBlockMemberReasoningText{
						Value: types.ReasoningTextBlock{
							Text:      aws.String(b.Text),
							Signature: aws.String(b.Signature),
						},
					},
				})
			case provider.ToolUseBlock:
				var inputMap map[string]interface{}
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &inputMap)
				}
				// Bedrock rejects toolUse.input that serializes to JSON null.
				// Normalize empty/"null"/unparsable input to an empty object.
				if inputMap == nil {
					inputMap = map[string]interface{}{}
				}
				toolUseID := b.ToolUseID
				toolUseCounts[toolUseID]++
				if count := toolUseCounts[toolUseID]; count > 1 {
					toolUseID = fmt.Sprintf("%s_%d", toolUseID, count)
				}
				content = append(content, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(toolUseID),
						Name:      aws.String(b.Name),
						Input:     document.NewLazyDocument(inputMap),
					},
				})
			case provider.ToolResultBlock:
				status := types.ToolResultStatusSuccess
				if b.IsError {
					status = types.ToolResultStatusError
				}
				toolUseID := b.ToolUseID
				if count := toolUseCounts[toolUseID]; count > 1 {
					toolUseID = fmt.Sprintf("%s_%d", toolUseID, count)
				}
				content = append(content, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(toolUseID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: b.Content},
						},
						Status: status,
					},
				})
			}
		}

		if len(content) == 0 {
			if skippedReasoning == "" {
				continue
			}
			content = append(content, &types.ContentBlockMemberText{Value: skippedReasoning})
		}

		result = append(result, types.Message{Role: role, Content: content})
	}
	return result
}
