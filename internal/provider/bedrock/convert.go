package bedrockprov

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// providerToBedrock converts provider-agnostic messages to Bedrock SDK messages.
func providerToBedrock(msgs []provider.Message) []types.Message {
	var result []types.Message
	for _, msg := range msgs {
		result = append(result, providerMsgToBedrock(msg))
	}
	return result
}

// providerMsgToBedrock converts a single provider message to Bedrock format.
func providerMsgToBedrock(msg provider.Message) types.Message {
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
	for _, block := range msg.Content {
		switch b := block.(type) {
		case provider.TextBlock:
			content = append(content, &types.ContentBlockMemberText{Value: b.Text})
		case provider.ReasoningBlock:
			var sig *string
			if b.Signature != "" {
				sig = aws.String(b.Signature)
			}
			content = append(content, &types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberReasoningText{
					Value: types.ReasoningTextBlock{
						Text:      aws.String(b.Text),
						Signature: sig,
					},
				},
			})
		case provider.ToolUseBlock:
			var inputMap interface{}
			if len(b.Input) > 0 {
				_ = json.Unmarshal(b.Input, &inputMap)
			}
			// Bedrock rejects toolUse.input that serializes to JSON null with
			// "The value at messages.N.content.M.toolUse.input is empty."
			// Zero-argument tool calls (empty, "null", or unparsable input)
			// must be sent as an empty JSON object instead.
			if inputMap == nil {
				inputMap = map[string]interface{}{}
			}
			content = append(content, &types.ContentBlockMemberToolUse{
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
			content = append(content, &types.ContentBlockMemberToolResult{
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

	return types.Message{Role: role, Content: content}
}

// providerToolsToBedrock converts provider tool definitions to bedrock tool definitions.
func providerToolsToBedrock(tools []provider.ToolDefinition) []bedrock.ToolDefinition {
	var result []bedrock.ToolDefinition
	for _, t := range tools {
		result = append(result, bedrock.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return result
}
