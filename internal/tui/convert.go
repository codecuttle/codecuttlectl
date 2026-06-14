package tui

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// bedrockToProviderMessages converts Bedrock SDK messages to provider-agnostic messages.
// Used when loading saved sessions that were serialized in Bedrock format.
func bedrockToProviderMessages(msgs []types.Message) []provider.Message {
	var result []provider.Message
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
			case *types.ContentBlockMemberToolUse:
				var inputMap interface{}
				if b.Value.Input != nil {
					_ = b.Value.Input.UnmarshalSmithyDocument(&inputMap)
				}
				inputJSON, _ := json.Marshal(inputMap)
				blocks = append(blocks, provider.ToolUseBlock{
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
				blocks = append(blocks, provider.ToolResultBlock{
					ToolUseID: aws.ToString(b.Value.ToolUseId),
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

		var blocks []types.ContentBlock
		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.TextBlock:
				blocks = append(blocks, &types.ContentBlockMemberText{Value: b.Text})
			case provider.ToolUseBlock:
				var inputMap map[string]interface{}
				if len(b.Input) > 0 {
					_ = json.Unmarshal(b.Input, &inputMap)
				}
				blocks = append(blocks, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: aws.String(b.ToolUseID),
						Name:      aws.String(b.Name),
						Input:     document.NewLazyDocument(inputMap),
					},
				})
			case provider.ToolResultBlock:
				status := types.ToolResultStatusSuccess
				if b.IsError {
					status = types.ToolResultStatusError
				}
				blocks = append(blocks, &types.ContentBlockMemberToolResult{
					Value: types.ToolResultBlock{
						ToolUseId: aws.String(b.ToolUseID),
						Content: []types.ToolResultContentBlock{
							&types.ToolResultContentBlockMemberText{Value: b.Content},
						},
						Status: status,
					},
				})
			}
		}

		result = append(result, types.Message{Role: role, Content: blocks})
	}
	return result
}
