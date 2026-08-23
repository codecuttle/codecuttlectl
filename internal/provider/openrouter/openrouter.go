package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// Client implements provider.Provider for OpenRouter.
type Client struct {
	baseURL    string
	model      string
	fallbacks  []string
	enforceZDR bool
	apiKey     string
	httpClient *http.Client
}

// Config holds configuration for creating an OpenRouter client.
type Config struct {
	BaseURL    string   // Optional, defaults to "https://openrouter.ai/api/v1"
	Model      string   // Primary model (e.g. "qwen/qwen3.8-max")
	Fallbacks  []string // Optional fallback models
	EnforceZDR bool     // If true, strictly route to ZDR-compliant endpoints
	APIKey     string   // Required
}

// New creates a new OpenRouter provider client.
func New(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	return &Client{
		baseURL:    baseURL,
		model:      cfg.Model,
		fallbacks:  cfg.Fallbacks,
		enforceZDR: cfg.EnforceZDR,
		apiKey:     cfg.APIKey,
		httpClient: &http.Client{},
	}
}

// ID returns the provider identifier.
func (c *Client) ID() string {
	return "openrouter:" + c.model
}

// Name returns a human-friendly display name.
func (c *Client) Name() string {
	return c.model
}

var (
	modelsCacheMu sync.RWMutex
	modelsCache   map[string]int32
)

// ContextWindow returns the model's context window size in tokens.
func (c *Client) ContextWindow() int32 {
	modelsCacheMu.RLock()
	if modelsCache != nil {
		val := modelsCache[c.model]
		modelsCacheMu.RUnlock()
		return val
	}
	modelsCacheMu.RUnlock()

	modelsCacheMu.Lock()
	defer modelsCacheMu.Unlock()
	// Check again in case another goroutine initialized it
	if modelsCache != nil {
		return modelsCache[c.model]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cache, err := fetchModels(ctx, c.httpClient, c.baseURL)
	if err != nil {
		return 0
	}
	modelsCache = cache
	return modelsCache[c.model]
}

// fetchModels retrieves the context windows for all models from OpenRouter.
func fetchModels(ctx context.Context, httpClient *http.Client, baseURL string) (map[string]int32, error) {
	url := baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: fetching models HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int32  `json:"context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	cache := make(map[string]int32)
	for _, m := range result.Data {
		cache[m.ID] = m.ContextLength
	}
	return cache, nil
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
		return nil, fmt.Errorf("openrouter: decoding response: %w", err)
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

// buildRequest constructs the OpenRouter-compatible chat completion request body.
func (c *Client) buildRequest(req provider.Request, stream bool) []byte {
	oaiReq := chatRequest{
		Model:  c.model,
		Stream: stream,
	}

	// Add fallbacks if configured
	if len(c.fallbacks) > 0 {
		oaiReq.Models = c.fallbacks
	}

	// Set up provider preferences if needed
	if c.enforceZDR || len(c.fallbacks) > 0 {
		oaiReq.Provider = &ProviderPrefs{}

		if len(c.fallbacks) > 0 {
			oaiReq.Provider.AllowFallbacks = true
		}

		if c.enforceZDR {
			oaiReq.Provider.ZDR = true
			oaiReq.Provider.DataCollection = "deny"
		}
	}

	// Request usage stats in streaming mode
	if stream {
		oaiReq.StreamOptions = &streamOptions{IncludeUsage: true}
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

// doRequest performs the HTTP POST to the OpenRouter API.
func (c *Client) doRequest(ctx context.Context, body []byte) (io.ReadCloser, error) {
	url := c.baseURL + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openrouter: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	// OpenRouter specific App Attribution Headers
	httpReq.Header.Set("HTTP-Referer", "https://github.com/codecuttle/codecuttlectl")
	httpReq.Header.Set("X-OpenRouter-Title", "codecuttlectl")
	httpReq.Header.Set("X-OpenRouter-Categories", "cli-agent")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		errStr := string(errBody)

		// Provide an actionable error message if OpenRouter cannot find a ZDR-compliant provider
		if resp.StatusCode == http.StatusNotFound && strings.Contains(errStr, "Zero data retention") {
			return nil, fmt.Errorf("openrouter: no ZDR-compliant endpoints available for model %q. Use --openrouter-zdr=false to allow data collection and proceed", c.model)
		}

		return nil, fmt.Errorf("openrouter: HTTP %d: %s", resp.StatusCode, errStr)
	}

	return resp.Body, nil
}

// providerMsgToOAI converts a provider.Message to one or more OpenAI chat messages.
func providerMsgToOAI(msg provider.Message) []chatMessage {
	switch msg.Role {
	case provider.RoleUser:
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
		var reasoningParts []string
		for _, block := range msg.Content {
			switch b := block.(type) {
			case provider.TextBlock:
				textParts = append(textParts, b.Text)
			case provider.ReasoningBlock:
				reasoningParts = append(reasoningParts, b.Text)
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

		// An assistant message with empty content and no tool calls is
		// rejected by some upstream providers (e.g. Google returns
		// INVALID_ARGUMENT). If the message consisted only of reasoning,
		// downgrade the reasoning to plain text; otherwise drop it.
		if m.Content == "" && len(m.ToolCalls) == 0 {
			if len(reasoningParts) == 0 {
				return nil
			}
			m.Content = strings.Join(reasoningParts, "\n")
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
