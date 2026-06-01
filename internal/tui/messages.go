package tui

import (
	"encoding/json"

	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// --- Bubble Tea message types for the TUI ---

// StreamStartMsg signals that streaming has begun for a new turn.
type StreamStartMsg struct{}

// StreamTextMsg carries a text delta from the model.
type StreamTextMsg struct {
	Text string
}

// StreamReasoningMsg carries a reasoning/thinking delta from the model.
type StreamReasoningMsg struct {
	Text string
}

// StreamReasoningDoneMsg signals reasoning is complete (signature received).
type StreamReasoningDoneMsg struct {
	Signature string
}

// StreamToolStartMsg signals that the model is beginning a tool call.
type StreamToolStartMsg struct {
	ToolUseID string
	Name      string
}

// StreamToolInputMsg carries partial tool input JSON.
type StreamToolInputMsg struct {
	Delta string
}

// StreamToolStopMsg signals that a tool call block is complete.
type StreamToolStopMsg struct{}

// StreamDoneMsg signals the stream has finished.
type StreamDoneMsg struct {
	StopReason string
}

// StreamUsageMsg reports token usage.
type StreamUsageMsg struct {
	InputTokens  int32
	OutputTokens int32
}

// StreamErrorMsg reports a streaming error.
type StreamErrorMsg struct {
	Err error
}

// ToolExecStartMsg signals that a tool is being executed.
type ToolExecStartMsg struct {
	Name      string
	ToolUseID string
}

// ToolExecResultMsg carries the result of tool execution.
type ToolExecResultMsg struct {
	ToolUseID string
	Name      string
	Output    string
	IsError   bool
}

// TodoUpdatedMsg signals that the todo list has changed.
type TodoUpdatedMsg struct {
	Items []todo.Item
}

// ContinueStreamMsg signals the agent should continue after tool results.
type ContinueStreamMsg struct {
	Messages   []bedrock.ToolResult
	TodoInputs []json.RawMessage // Raw todo_manage inputs to apply in Update
}

// UserSubmitMsg carries a user's submitted message.
type UserSubmitMsg struct {
	Text string
}
