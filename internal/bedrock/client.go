// Package bedrock provides the AWS Bedrock Converse API client for codecuttlectl.
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Client wraps the Bedrock Runtime client with Converse API convenience methods.
type Client struct {
	runtime *bedrockruntime.Client
	modelID string
	region  string
}

// Config holds the configuration for creating a Bedrock client.
type Config struct {
	// Region is the AWS region. Defaults to us-west-2 if empty.
	Region string
	// ModelID is the Bedrock model identifier (e.g. "us.anthropic.claude-opus-4-6-v1").
	ModelID string
	// Profile is an optional AWS profile name from ~/.aws/credentials.
	Profile string
}

// NewClient creates a new Bedrock client using the default AWS credential chain.
// This supports: environment variables, shared credentials, EC2 instance roles,
// ECS task roles, and web identity tokens.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	region := cfg.Region
	if region == "" {
		region = "us-west-2"
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(awsCfg)
	return &Client{
		runtime: client,
		modelID: cfg.ModelID,
		region:  region,
	}, nil
}

// ToolDefinition describes a tool that the model can invoke.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the tool's input
}

// ToolUseRequest represents the model requesting to use a tool.
type ToolUseRequest struct {
	ToolUseID string
	Name      string
	Input     json.RawMessage
}

// Response holds the result of a Converse API call.
type Response struct {
	Content      string
	StopReason   string
	ToolUses     []ToolUseRequest
	InputTokens  int32
	OutputTokens int32
	// Cache token metrics
	CacheReadInputTokens  int32
	CacheWriteInputTokens int32
	// RawOutput preserves the full output for building history
	RawOutput types.ConverseOutput
	// RawContentBlocks preserves the full content blocks from the assistant message
	RawContentBlocks []types.ContentBlock
}

// Converse sends the full message history and returns the response.
// The messages slice should contain proper types.Message values.
// Applies prompt caching: cache checkpoint placed after stable system prompt,
// dynamic injections (skills, reconciler) come after without a checkpoint.
func (c *Client) Converse(ctx context.Context, system string, messages []types.Message, tools []ToolDefinition) (*Response, error) {
	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(c.modelID),
		Messages: applyCachePoints(messages),
	}

	if system != "" {
		input.System = buildSystemBlocks(system)
	}

	if len(tools) > 0 {
		input.ToolConfig = &types.ToolConfiguration{
			Tools: toBedrockTools(tools),
		}
	}

	output, err := c.runtime.Converse(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}

	return parseConverseOutput(output), nil
}

func toBedrockTools(tools []ToolDefinition) []types.Tool {
	var result []types.Tool
	for _, t := range tools {
		var schema map[string]interface{}
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}

		result = append(result, &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String(t.Name),
				Description: aws.String(t.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(schema)},
			},
		})
	}
	return result
}

func parseConverseOutput(output *bedrockruntime.ConverseOutput) *Response {
	resp := &Response{}

	if output.StopReason != "" {
		resp.StopReason = string(output.StopReason)
	}

	if output.Usage != nil {
		resp.InputTokens = aws.ToInt32(output.Usage.InputTokens)
		resp.OutputTokens = aws.ToInt32(output.Usage.OutputTokens)
		resp.CacheReadInputTokens = aws.ToInt32(output.Usage.CacheReadInputTokens)
		resp.CacheWriteInputTokens = aws.ToInt32(output.Usage.CacheWriteInputTokens)
	}

	if output.Output != nil {
		resp.RawOutput = output.Output
		if msg, ok := output.Output.(*types.ConverseOutputMemberMessage); ok {
			resp.RawContentBlocks = msg.Value.Content
			for _, block := range msg.Value.Content {
				switch b := block.(type) {
				case *types.ContentBlockMemberText:
					resp.Content += b.Value
				case *types.ContentBlockMemberToolUse:
					var inputMap interface{}
					if b.Value.Input != nil {
						_ = b.Value.Input.UnmarshalSmithyDocument(&inputMap)
					}
					inputJSON, _ := json.Marshal(inputMap)
					resp.ToolUses = append(resp.ToolUses, ToolUseRequest{
						ToolUseID: aws.ToString(b.Value.ToolUseId),
						Name:      aws.ToString(b.Value.Name),
						Input:     inputJSON,
					})
				}
			}
		}
	}

	return resp
}

// BuildUserTextMessage creates a simple user text message.
func BuildUserTextMessage(text string) types.Message {
	return types.Message{
		Role: types.ConversationRoleUser,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: text},
		},
	}
}

// BuildAssistantMessage creates an assistant message from raw content blocks.
func BuildAssistantMessage(blocks []types.ContentBlock) types.Message {
	return types.Message{
		Role:    types.ConversationRoleAssistant,
		Content: blocks,
	}
}

// BuildToolResultMessage creates a user message containing tool results.
func BuildToolResultMessage(results []ToolResult) types.Message {
	var content []types.ContentBlock
	for _, r := range results {
		content = append(content, &types.ContentBlockMemberToolResult{
			Value: types.ToolResultBlock{
				ToolUseId: aws.String(r.ToolUseID),
				Content: []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: r.Content},
				},
				Status: r.Status,
			},
		})
	}
	return types.Message{
		Role:    types.ConversationRoleUser,
		Content: content,
	}
}

// ToolResult represents the result of executing a tool.
type ToolResult struct {
	ToolUseID string
	Content   string
	Status    types.ToolResultStatus
}

// ModelID returns the configured model ID.
func (c *Client) ModelID() string {
	return c.modelID
}

// Region returns the configured AWS region.
func (c *Client) Region() string {
	return c.region
}
