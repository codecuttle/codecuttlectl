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
		bedrockMsg, ok := providerMsgToBedrock(msg)
		if ok {
			result = append(result, bedrockMsg)
		}
	}
	return result
}

// providerMsgToBedrock converts a single provider message to Bedrock format.
// It returns false if the message has no valid content blocks for Bedrock
// (e.g. signatureless reasoning blocks that cannot be replayed).
func providerMsgToBedrock(msg provider.Message) (types.Message, bool) {
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
			// Reasoning blocks without a signature (e.g. from Gemini or OpenRouter)
			// cannot be replayed to Bedrock: Anthropic models reject them with
			// "thinking.signature: Field required". Skip them, but remember the text
			// so reasoning-only messages can be downgraded to plain text.
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

	if len(content) == 0 {
		if skippedReasoning == "" {
			return types.Message{}, false
		}
		content = append(content, &types.ContentBlockMemberText{Value: skippedReasoning})
	}

	return types.Message{Role: role, Content: content}, true
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
