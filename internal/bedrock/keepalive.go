package bedrock

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// PingCache sends a minimal Converse request with max_tokens=1 to refresh
// the Bedrock prompt cache TTL (5 minutes). The request includes the same
// tools, system prompt, and message history that the real calls use, so the
// cached prefix (tools + system + message prefix) stays hot.
//
// Cost: ~$0.01 per ping (cached prefix read at $0.50/MTok + 1 output token).
// This prevents the ~$0.12 cache rebuild penalty that occurs after 5 minutes idle.
//
// The response is discarded — this is purely a cache refresh operation.
func (c *Client) PingCache(ctx context.Context, system string, messages []types.Message, tools []ToolDefinition) error {
	if len(messages) == 0 {
		// No history to cache yet — skip.
		return nil
	}

	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(c.modelID),
		Messages: applyCachePoints(messages),
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: aws.Int32(1),
		},
	}

	if system != "" {
		input.System = buildSystemBlocks(system)
	}

	if len(tools) > 0 {
		input.ToolConfig = buildToolsWithCache(tools)
	}

	_, err := c.runtime.Converse(ctx, input)
	if err != nil {
		return fmt.Errorf("cache keepalive ping: %w", err)
	}

	return nil
}
