// Package bedrockprov wraps the existing bedrock.Client to implement the provider.Provider interface.
package bedrockprov

import (
	"context"

	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// Provider wraps a bedrock.Client to implement provider.Provider.
type Provider struct {
	client *bedrock.Client
}

// New creates a new Bedrock provider wrapping the given client.
func New(client *bedrock.Client) *Provider {
	return &Provider{client: client}
}

// ID returns the provider identifier (e.g., "bedrock:us.anthropic.claude-opus-4-6-v1").
func (p *Provider) ID() string {
	return "bedrock:" + p.client.ModelID()
}

// Name returns a human-friendly display name derived from the model ID.
func (p *Provider) Name() string {
	return p.client.ModelID()
}

// Client returns the underlying bedrock.Client for use with Bedrock-specific features
// (caching, keepalive, etc.) that aren't part of the provider interface.
func (p *Provider) Client() *bedrock.Client {
	return p.client
}

// Converse sends messages and returns a complete response.
func (p *Provider) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	// Convert provider types to Bedrock types
	messages := providerToBedrock(req.Messages)
	tools := providerToolsToBedrock(req.Tools)

	resp, err := p.client.Converse(ctx, req.System, messages, tools)
	if err != nil {
		return nil, err
	}

	// Convert Bedrock response to provider response
	var toolUses []provider.ToolUseRequest
	for _, tu := range resp.ToolUses {
		toolUses = append(toolUses, provider.ToolUseRequest{
			ToolUseID: tu.ToolUseID,
			Name:      tu.Name,
			Input:     tu.Input,
		})
	}

	return &provider.Response{
		Content:    resp.Content,
		ToolUses:   toolUses,
		StopReason: resp.StopReason,
		Usage: provider.Usage{
			InputTokens:      resp.InputTokens,
			OutputTokens:     resp.OutputTokens,
			CacheReadTokens:  resp.CacheReadInputTokens,
			CacheWriteTokens: resp.CacheWriteInputTokens,
		},
	}, nil
}

// ConverseStream sends messages and streams the response via channel.
func (p *Provider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	messages := providerToBedrock(req.Messages)
	tools := providerToolsToBedrock(req.Tools)

	var streamCfg bedrock.StreamConfig
	if req.Config.EnableThinking {
		streamCfg.EnableThinking = true
		streamCfg.ThinkingBudget = req.Config.ThinkingBudget
	}

	bedrockCh := p.client.ConverseStream(ctx, req.System, messages, tools, streamCfg)

	// Translate bedrock events to provider events
	events := make(chan provider.StreamEvent, 64)
	go func() {
		defer close(events)
		for ev := range bedrockCh {
			switch e := ev.(type) {
			case bedrock.TextDeltaEvent:
				events <- provider.TextDeltaEvent{Text: e.Text}
			case bedrock.ReasoningDeltaEvent:
				events <- provider.ReasoningDeltaEvent{Text: e.Text}
			case bedrock.ReasoningSignatureEvent:
				events <- provider.ReasoningSignatureEvent{Signature: e.Signature}
			case bedrock.ToolUseStartEvent:
				events <- provider.ToolUseStartEvent{ToolUseID: e.ToolUseID, Name: e.Name}
			case bedrock.ToolInputDeltaEvent:
				events <- provider.ToolInputDeltaEvent{Delta: e.Delta}
			case bedrock.ToolUseStopEvent:
				events <- provider.ToolUseStopEvent{}
			case bedrock.MessageStopEvent:
				events <- provider.MessageStopEvent{StopReason: e.StopReason}
			case bedrock.UsageEvent:
				events <- provider.UsageEvent{
					InputTokens:      e.InputTokens,
					OutputTokens:     e.OutputTokens,
					CacheReadTokens:  e.CacheReadInputTokens,
					CacheWriteTokens: e.CacheWriteInputTokens,
				}
			case bedrock.StreamErrorEvent:
				events <- provider.StreamErrorEvent{Err: e.Err}
			}
		}
	}()

	return events
}
