package ollama

import "encoding/json"

// --- OpenAI-compatible API types ---

// chatRequest is the OpenAI chat completions request body.
type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Tools         []oaiTool      `json:"tools,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
}

// streamOptions controls streaming behavior.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage is a message in the OpenAI format.
type chatMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Reasoning  string        `json:"reasoning,omitempty"`
}

// oaiTool is an OpenAI-format tool definition.
type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

// oaiFunction describes a callable function.
type oaiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// oaiToolCall represents a tool invocation from the model.
type oaiToolCall struct {
	ID       string              `json:"id"`
	Index    int                 `json:"index,omitempty"`
	Type     string              `json:"type"`
	Function oaiToolCallFunction `json:"function"`
}

// oaiToolCallFunction is the function details within a tool call.
type oaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatCompletion is the non-streaming response.
type chatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

// chatChoice represents a single completion choice.
type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// chatUsage reports token consumption.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Streaming types ---

// chatCompletionChunk is a single SSE chunk in streaming mode.
type chatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chatUsage    `json:"usage,omitempty"`
}

// chunkChoice is a streaming choice delta.
type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

// chunkDelta is the incremental content in a streaming chunk.
type chunkDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []oaiToolCall `json:"tool_calls,omitempty"`
	Reasoning string        `json:"reasoning,omitempty"`
}
