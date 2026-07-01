package session

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	providerPkg "github.com/codecuttle/codecuttlectl/internal/provider"
)

// Message is the JSON-serializable representation of a conversation message.
// It provides a clean round-trip between Bedrock SDK types and persistent storage.
type Message struct {
	Role   string        `json:"role"`   // "user", "assistant"
	Blocks []ContentItem `json:"blocks"`
}

// ContentItem is a tagged union for serialized content blocks.
// The Type field determines which other fields are populated.
type ContentItem struct {
	// Type discriminator: "text", "tool_use", "tool_result", "reasoning"
	Type string `json:"type"`

	// For Type=="text": the text content
	Text string `json:"text,omitempty"`

	// For Type=="reasoning": thought signature for Gemini 3 multi-turn continuity
	// For Type=="tool_use": thought signature attached to function call (Gemini 3)
	Signature string `json:"signature,omitempty"`

	// For Type=="tool_use": the tool invocation details
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`

	// For Type=="tool_result": the execution result
	Content   string `json:"content,omitempty"`   // Result text
	Status    string `json:"status,omitempty"`    // "success" or "error"
	ResultFor string `json:"result_for,omitempty"` // ToolUseID this result corresponds to
}

// MarshalProviderHistory converts provider-agnostic messages into the session
// serializable format. This enables session persistence for non-Bedrock providers
// (Ollama, etc.) which use provider.Message instead of Bedrock SDK types.
func MarshalProviderHistory(messages []providerPkg.Message) ([]Message, error) {
	result := make([]Message, 0, len(messages))

	for _, msg := range messages {
		serialized := Message{
			Role: string(msg.Role),
		}

		for _, block := range msg.Content {
			item := marshalProviderBlock(block)
			serialized.Blocks = append(serialized.Blocks, item)
		}

		result = append(result, serialized)
	}

	return result, nil
}

// UnmarshalProviderHistory converts session messages into provider-agnostic format.
// This enables session resume for non-Bedrock providers without going through
// the Bedrock SDK types as an intermediate step.
func UnmarshalProviderHistory(messages []Message) []providerPkg.Message {
	var result []providerPkg.Message
	toolNames := make(map[string]string) // ToolUseID -> Name

	for _, msg := range messages {
		var role providerPkg.Role
		switch msg.Role {
		case "user":
			role = providerPkg.RoleUser
		case "assistant":
			role = providerPkg.RoleAssistant
		default:
			role = providerPkg.Role(msg.Role)
		}

		var blocks []providerPkg.ContentBlock
		for _, item := range msg.Blocks {
			switch item.Type {
			case "text":
				blocks = append(blocks, providerPkg.TextBlock{Text: item.Text})
			case "reasoning":
				blocks = append(blocks, providerPkg.ReasoningBlock{
					Text:      item.Text,
					Signature: item.Signature,
				})
			case "tool_use":
				input := json.RawMessage(item.Input)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				toolNames[item.ToolUseID] = item.Name
				blocks = append(blocks, providerPkg.ToolUseBlock{
					ToolUseID:        item.ToolUseID,
					Name:             item.Name,
					Input:            input,
					ThoughtSignature: item.Signature,
				})
			case "tool_result":
				name := item.Name
				if name == "" {
					name = toolNames[item.ResultFor]
				}
				blocks = append(blocks, providerPkg.ToolResultBlock{
					ToolUseID: item.ResultFor,
					Name:      name,
					Content:   item.Content,
					IsError:   item.Status == "error",
				})
			}
		}

		result = append(result, providerPkg.Message{Role: role, Content: blocks})
	}

	return result
}

// marshalProviderBlock converts a provider ContentBlock to a session ContentItem.
func marshalProviderBlock(block providerPkg.ContentBlock) ContentItem {
	switch b := block.(type) {
	case providerPkg.TextBlock:
		return ContentItem{Type: "text", Text: b.Text}
	case providerPkg.ReasoningBlock:
		return ContentItem{Type: "reasoning", Text: b.Text, Signature: b.Signature}
	case providerPkg.ToolUseBlock:
		input := json.RawMessage(b.Input)
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return ContentItem{
			Type:      "tool_use",
			ToolUseID: b.ToolUseID,
			Name:      b.Name,
			Input:     input,
			Signature: b.ThoughtSignature,
		}
	case providerPkg.ToolResultBlock:
		status := "success"
		if b.IsError {
			status = "error"
		}
		return ContentItem{
			Type:      "tool_result",
			Name:      b.Name,
			Content:   b.Content,
			Status:    status,
			ResultFor: b.ToolUseID,
		}
	default:
		return ContentItem{Type: "text", Text: "[unsupported content block]"}
	}
}

// MarshalHistory converts Bedrock SDK message types into our serializable format.
// This handles the tricky document.Interface → json.RawMessage conversion for
// tool_use Input fields.
func MarshalHistory(messages []types.Message) ([]Message, error) {
	result := make([]Message, 0, len(messages))

	for i, msg := range messages {
		serialized := Message{
			Role: string(msg.Role),
		}

		for j, block := range msg.Content {
			item, err := marshalContentBlock(block)
			if err != nil {
				return nil, fmt.Errorf("message %d block %d: %w", i, j, err)
			}
			serialized.Blocks = append(serialized.Blocks, item)
		}

		result = append(result, serialized)
	}

	return result, nil
}

// UnmarshalHistory converts our serializable format back into Bedrock SDK types.
// This reconstructs document.NewLazyDocument() for tool_use Input fields.
func UnmarshalHistory(messages []Message) ([]types.Message, error) {
	result := make([]types.Message, 0, len(messages))

	for i, msg := range messages {
		bedrockMsg := types.Message{
			Role: types.ConversationRole(msg.Role),
		}

		for j, item := range msg.Blocks {
			block, err := unmarshalContentItem(item)
			if err != nil {
				return nil, fmt.Errorf("message %d block %d: %w", i, j, err)
			}
			bedrockMsg.Content = append(bedrockMsg.Content, block)
		}

		result = append(result, bedrockMsg)
	}

	return result, nil
}

// marshalContentBlock converts a single Bedrock ContentBlock into a ContentItem.
func marshalContentBlock(block types.ContentBlock) (ContentItem, error) {
	switch b := block.(type) {
	case *types.ContentBlockMemberText:
		return ContentItem{
			Type: "text",
			Text: b.Value,
		}, nil

	case *types.ContentBlockMemberReasoningContent:
		switch r := b.Value.(type) {
		case *types.ReasoningContentBlockMemberReasoningText:
			return ContentItem{
				Type:      "reasoning",
				Text:      aws.ToString(r.Value.Text),
				Signature: aws.ToString(r.Value.Signature),
			}, nil
		case *types.ReasoningContentBlockMemberRedactedContent:
			return ContentItem{
				Type: "reasoning",
				Text: "[redacted reasoning content]",
			}, nil
		default:
			return ContentItem{
				Type: "reasoning",
				Text: "[unsupported reasoning block type]",
			}, nil
		}

	case *types.ContentBlockMemberToolUse:
		var inputJSON json.RawMessage

		if b.Value.Input != nil {
			// Use MarshalSmithyDocument to get JSON bytes from the document interface.
			// This works for both marshaler (outbound) and unmarshaler (inbound) document types.
			data, err := b.Value.Input.MarshalSmithyDocument()
			if err != nil {
				return ContentItem{}, fmt.Errorf("marshaling tool_use input to JSON: %w", err)
			}
			inputJSON = json.RawMessage(data)
		} else {
			inputJSON = json.RawMessage("{}")
		}

		return ContentItem{
			Type:      "tool_use",
			ToolUseID: aws.ToString(b.Value.ToolUseId),
			Name:      aws.ToString(b.Value.Name),
			Input:     inputJSON,
		}, nil

	case *types.ContentBlockMemberToolResult:
		var content string
		for _, rc := range b.Value.Content {
			if textBlock, ok := rc.(*types.ToolResultContentBlockMemberText); ok {
				content += textBlock.Value
			}
		}

		status := "success"
		if b.Value.Status == types.ToolResultStatusError {
			status = "error"
		}

		return ContentItem{
			Type:      "tool_result",
			Content:   content,
			Status:    status,
			ResultFor: aws.ToString(b.Value.ToolUseId),
		}, nil

	default:
		// For any block types we don't handle (images, docs, etc.), store as text placeholder
		return ContentItem{
			Type: "text",
			Text: "[unsupported content block]",
		}, nil
	}
}

// unmarshalContentItem converts a ContentItem back into a Bedrock ContentBlock.
func unmarshalContentItem(item ContentItem) (types.ContentBlock, error) {
	switch item.Type {
	case "text":
		return &types.ContentBlockMemberText{Value: item.Text}, nil

	case "reasoning":
		var sig *string
		if item.Signature != "" {
			sig = aws.String(item.Signature)
		}
		return &types.ContentBlockMemberReasoningContent{
			Value: &types.ReasoningContentBlockMemberReasoningText{
				Value: types.ReasoningTextBlock{
					Text:      aws.String(item.Text),
					Signature: sig,
				},
			},
		}, nil

	case "tool_use":
		var inputMap map[string]interface{}
		if len(item.Input) > 0 {
			if err := json.Unmarshal(item.Input, &inputMap); err != nil {
				return nil, fmt.Errorf("unmarshaling tool_use input JSON: %w", err)
			}
		}
		if inputMap == nil {
			inputMap = map[string]interface{}{}
		}

		return &types.ContentBlockMemberToolUse{
			Value: types.ToolUseBlock{
				ToolUseId: aws.String(item.ToolUseID),
				Name:      aws.String(item.Name),
				Input:     document.NewLazyDocument(inputMap),
			},
		}, nil

	case "tool_result":
		status := types.ToolResultStatusSuccess
		if item.Status == "error" {
			status = types.ToolResultStatusError
		}

		return &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(item.ResultFor),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: item.Content},
				},
				Status: status,
			},
		}, nil

	default:
		return nil, fmt.Errorf("unknown content item type: %q", item.Type)
	}
}
