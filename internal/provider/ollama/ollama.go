// Package ollama implements the provider.Provider interface using the Ollama
// OpenAI-compatible HTTP API (localhost:11434/v1/).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// Client implements provider.Provider for Ollama local models.
type Client struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// Config holds configuration for creating an Ollama client.
type Config struct {
	// BaseURL is the Ollama server URL. Default: "http://localhost:11434"
	BaseURL string
	// Model is the Ollama model name (e.g., "gemma4:31b", "llama3.3", "qwen3")
	Model string
}

// New creates a new Ollama provider client.
func New(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Client{
		baseURL:    baseURL,
		model:      cfg.Model,
		httpClient: &http.Client{},
	}
}

// ID returns the provider identifier.
func (c *Client) ID() string {
	return "ollama:" + c.model
}

// Name returns a human-friendly display name.
func (c *Client) Name() string {
	return c.model
}

// Converse sends messages and returns a complete response (non-streaming).
func (c *Client) Converse(ctx context.Context, req provider.Request) (*provider.Response, error) {
	body := c.buildRequest(req, false)

	respBody, err := c.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	var completion chatCompletion
	if err := json.NewDecoder(respBody).Decode(&completion); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}

	return completionToResponse(completion), nil
}

// ConverseStream sends messages and streams the response via channel.
func (c *Client) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	events := make(chan provider.StreamEvent, 64)

	go func() {
		defer close(events)

		body := c.buildRequest(req, true)

		respBody, err := c.doRequest(ctx, body)
		if err != nil {
			events <- provider.StreamErrorEvent{Err: err}
			return
		}
		defer respBody.Close()

		parseSSEStream(respBody, events)
	}()

	return events
}

// buildRequest constructs the OpenAI-compatible chat completion request body.
func (c *Client) buildRequest(req provider.Request, stream bool) []byte {
	oaiReq := chatRequest{
		Model:  c.model,
		Stream: stream,
	}

	// Build messages
	if req.System != "" {
		oaiReq.Messages = append(oaiReq.Messages, chatMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, msg := range req.Messages {
		oaiReq.Messages = append(oaiReq.Messages, providerMsgToOAI(msg)...)
	}

	// Build tools
	for _, tool := range req.Tools {
		oaiReq.Tools = append(oaiReq.Tools, oaiTool{
			Type: "function",
			Function: oaiFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	// Config
	if req.Config.MaxTokens > 0 {
		oaiReq.MaxTokens = &req.Config.MaxTokens
	}
	if req.Config.Temperature != nil {
		oaiReq.Temperature = req.Config.Temperature
	}

	data, _ := json.Marshal(oaiReq)
	return data
}

// doRequest performs the HTTP POST to the Ollama API.
func (c *Client) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	url := c.baseURL + "/v1/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	return resp.Body, nil
}

// providerMsgToOAI converts a provider.Message to one or more OpenAI chat messages.
// Tool results become separate "tool" role messages.
func providerMsgToOAI(msg provider.Message) []chatMessage {
	switch msg.Role {
	case provider.RoleUser:
		// Collect text blocks into a single user message, and tool results as separate messages.
		var textParts []string
		var toolResults []chatMessage

		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.TextBlock:
				textParts = append(textParts, b.Text)
			case provider.ToolResultBlock:
				toolResults = append(toolResults, chatMessage{
					Role:       "tool",
					Content:    b.Content,
					ToolCallID: b.ToolUseID,
				})
			}
		}

		var result []chatMessage
		if len(textParts) > 0 {
			combined := ""
			for i, p := range textParts {
				if i > 0 {
					combined += "\n"
				}
				combined += p
			}
			result = append(result, chatMessage{
				Role:    "user",
				Content: combined,
			})
		}
		result = append(result, toolResults...)
		return result

	case provider.RoleAssistant:
		m := chatMessage{
			Role: "assistant",
		}

		var textParts []string
		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.TextBlock:
				textParts = append(textParts, b.Text)
			case provider.ToolUseBlock:
				m.ToolCalls = append(m.ToolCalls, oaiToolCall{
					ID:   b.ToolUseID,
					Type: "function",
					Function: oaiToolCallFunction{
						Name:      b.Name,
						Arguments: string(b.Input),
					},
				})
			}
		}

		if len(textParts) > 0 {
			combined := ""
			for i, p := range textParts {
				if i > 0 {
					combined += "\n"
				}
				combined += p
			}
			m.Content = combined
		}

		return []chatMessage{m}

	default:
		return nil
	}
}

// completionToResponse converts a non-streaming chat completion to a provider.Response.
func completionToResponse(c chatCompletion) *provider.Response {
	resp := &provider.Response{}

	if len(c.Choices) > 0 {
		choice := c.Choices[0]
		resp.Content = choice.Message.Content
		resp.StopReason = string(choice.FinishReason)

		for _, tc := range choice.Message.ToolCalls {
			resp.ToolUses = append(resp.ToolUses, provider.ToolUseRequest{
				ToolUseID: tc.ID,
				Name:      tc.Function.Name,
				Input:     json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	if c.Usage != nil {
		resp.Usage = provider.Usage{
			InputTokens:  int32(c.Usage.PromptTokens),
			OutputTokens: int32(c.Usage.CompletionTokens),
		}
	}

	return resp
}
