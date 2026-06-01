package session

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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
	// Type discriminator: "text", "tool_use", "tool_result"
	Type string `json:"type"`

	// For Type=="text": the text content
	Text string `json:"text,omitempty"`

	// For Type=="tool_use": the tool invocation details
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`

	// For Type=="tool_result": the execution result
	Content   string `json:"content,omitempty"`   // Result text
	Status    string `json:"status,omitempty"`    // "success" or "error"
	ResultFor string `json:"result_for,omitempty"` // ToolUseID this result corresponds to
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
