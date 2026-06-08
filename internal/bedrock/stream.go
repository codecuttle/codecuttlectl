package bedrock

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// StreamEvent represents a single event from ConverseStream.
type StreamEvent interface {
	streamEvent()
}

// TextDeltaEvent is emitted when the model produces text.
type TextDeltaEvent struct {
	Text string
}

func (TextDeltaEvent) streamEvent() {}

// ReasoningDeltaEvent is emitted when the model streams reasoning/thinking text.
type ReasoningDeltaEvent struct {
	Text string
}

func (ReasoningDeltaEvent) streamEvent() {}

// ReasoningSignatureEvent carries the verification signature for multi-turn reasoning continuity.
type ReasoningSignatureEvent struct {
	Signature string
}

func (ReasoningSignatureEvent) streamEvent() {}

// ToolUseStartEvent is emitted when the model begins a tool call.
type ToolUseStartEvent struct {
	ToolUseID string
	Name      string
}

func (ToolUseStartEvent) streamEvent() {}

// ToolInputDeltaEvent is emitted as tool input JSON is streamed.
type ToolInputDeltaEvent struct {
	Delta string
}

func (ToolInputDeltaEvent) streamEvent() {}

// ToolUseStopEvent is emitted when a tool call block is complete.
type ToolUseStopEvent struct{}

func (ToolUseStopEvent) streamEvent() {}

// MessageStopEvent is emitted when the entire message is complete.
type MessageStopEvent struct {
	StopReason string
}

func (MessageStopEvent) streamEvent() {}

// UsageEvent reports token consumption.
type UsageEvent struct {
	InputTokens  int32
	OutputTokens int32
}

func (UsageEvent) streamEvent() {}

// StreamErrorEvent reports an error during streaming.
type StreamErrorEvent struct {
	Err error
}

func (StreamErrorEvent) streamEvent() {}

// StreamConfig configures the ConverseStream behavior.
type StreamConfig struct {
	// EnableThinking enables extended thinking/reasoning for supported models.
	EnableThinking bool
	// ThinkingBudget is the maximum tokens for reasoning. Default: 16000.
	ThinkingBudget int
}

// ConverseStream initiates a streaming conversation and sends events to the returned channel.
// The channel is closed when the stream completes or an error occurs.
func (c *Client) ConverseStream(ctx context.Context, system string, messages []types.Message, tools []ToolDefinition, opts ...StreamConfig) <-chan StreamEvent {
	events := make(chan StreamEvent, 64)

	var cfg StreamConfig
	if len(opts) > 0 {
		cfg = opts[0]
	}

	go func() {
		defer close(events)

		input := &bedrockruntime.ConverseStreamInput{
			ModelId:  aws.String(c.modelID),
			Messages: messages,
		}

		if system != "" {
			input.System = []types.SystemContentBlock{
				&types.SystemContentBlockMemberText{Value: system},
				// Cache checkpoint after system prompt — stable across turns
				&types.SystemContentBlockMemberCachePoint{Value: types.CachePointBlock{
					Type: types.CachePointTypeDefault,
				}},
			}
		}

		if len(tools) > 0 {
			input.ToolConfig = &types.ToolConfiguration{
				Tools: toBedrockTools(tools),
			}
		}

		// Enable extended thinking if requested
		if cfg.EnableThinking {
			budget := cfg.ThinkingBudget
			if budget <= 0 {
				budget = 16000
			}
			input.AdditionalModelRequestFields = document.NewLazyDocument(map[string]interface{}{
				"reasoning_config": map[string]interface{}{
					"type":         "enabled",
					"budget_tokens": budget,
				},
			})
		}

		output, err := c.runtime.ConverseStream(ctx, input)
		if err != nil {
			events <- StreamErrorEvent{Err: fmt.Errorf("bedrock converse stream: %w", err)}
			return
		}

		stream := output.GetStream()
		defer stream.Close()

		inToolUse := false
		inReasoning := false

		for event := range stream.Events() {
			switch e := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockStart:
				if start := e.Value.Start; start != nil {
					if tu, ok := start.(*types.ContentBlockStartMemberToolUse); ok {
						inToolUse = true
						events <- ToolUseStartEvent{
							ToolUseID: aws.ToString(tu.Value.ToolUseId),
							Name:      aws.ToString(tu.Value.Name),
						}
					}
					// Reasoning blocks don't have a distinct start type in the SDK,
					// but we detect them when we receive reasoning deltas.
				}

			case *types.ConverseStreamOutputMemberContentBlockDelta:
				if delta := e.Value.Delta; delta != nil {
					switch d := delta.(type) {
					case *types.ContentBlockDeltaMemberText:
						events <- TextDeltaEvent{Text: d.Value}
					case *types.ContentBlockDeltaMemberToolUse:
						if d.Value.Input != nil {
							events <- ToolInputDeltaEvent{Delta: *d.Value.Input}
						}
					case *types.ContentBlockDeltaMemberReasoningContent:
						inReasoning = true
						if rc := d.Value; rc != nil {
							switch r := rc.(type) {
							case *types.ReasoningContentBlockDeltaMemberText:
								events <- ReasoningDeltaEvent{Text: r.Value}
							case *types.ReasoningContentBlockDeltaMemberSignature:
								events <- ReasoningSignatureEvent{Signature: r.Value}
							}
						}
					}
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:
				if inToolUse {
					events <- ToolUseStopEvent{}
					inToolUse = false
				}
				if inReasoning {
					inReasoning = false
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				events <- MessageStopEvent{
					StopReason: string(e.Value.StopReason),
				}

			case *types.ConverseStreamOutputMemberMetadata:
				if e.Value.Usage != nil {
					events <- UsageEvent{
						InputTokens:  aws.ToInt32(e.Value.Usage.InputTokens),
						OutputTokens: aws.ToInt32(e.Value.Usage.OutputTokens),
					}
				}
			}
		}

		if err := stream.Err(); err != nil {
			events <- StreamErrorEvent{Err: fmt.Errorf("stream error: %w", err)}
		}
	}()

	return events
}

// CollectToolInput accumulates ToolInputDelta events into a complete JSON input.
func CollectToolInput(deltas []string) json.RawMessage {
	var combined string
	for _, d := range deltas {
		combined += d
	}
	if combined == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(combined)
}
