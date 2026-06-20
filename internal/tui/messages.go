package tui

import (
	"encoding/json"

	"github.com/codecuttle/codecuttlectl/internal/todo"
	"github.com/codecuttle/codecuttlectl/internal/provider"
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
	ToolUseID        string
	Name             string
	ThoughtSignature string
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
	InputTokens           int32
	OutputTokens          int32
	CacheReadInputTokens  int32
	CacheWriteInputTokens int32
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

// ToolOutputDeltaMsg carries incremental output from a streaming tool execution.
type ToolOutputDeltaMsg struct {
	ToolUseID string
	Name      string
	Delta     string
	IsStderr  bool
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
	Messages   []provider.ToolResultBlock
	TodoInputs []json.RawMessage // Raw todo_manage inputs to apply in Update
}

// ApprovalRequestMsg signals that a tool requires user confirmation before execution.
type ApprovalRequestMsg struct {
	ToolIndex       int    // Index in the pending tools slice where approval is needed
	ToolName        string // Tool name for display
	ToolUseID       string
	ThoughtSignature string
	Input           json.RawMessage // Original tool input
	Command         string          // Human-readable command description
	Reason          string          // Why confirmation is needed
	Risk            string          // Risk level string
	CompletedResults []provider.ToolResultBlock  // Results already collected before this tool
	CompletedTodos   []json.RawMessage     // Todos collected before this tool
	RemainingTools   []pendingToolForApproval // Tools after this one still to execute
}

// pendingToolForApproval holds serializable pending tool data for approval flow.
type pendingToolForApproval struct {
	ID               string
	Name             string
	Input            json.RawMessage
	ThoughtSignature string
}

// ApprovalDecisionMsg carries the user's approval decision for a pending tool.
type ApprovalDecisionMsg struct {
	ToolIndex int
	ToolUseID string
	Approved  bool
}

// UserSubmitMsg carries a user's submitted message.
type UserSubmitMsg struct {
	Text string
}

// CacheKeepaliveTickMsg fires periodically to trigger a cache TTL refresh.
type CacheKeepaliveTickMsg struct{}

// CacheKeepaliveDoneMsg signals that a keepalive ping completed (success or failure).
type CacheKeepaliveDoneMsg struct {
	Err error
}
