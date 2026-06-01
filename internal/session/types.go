// Package session manages persistent session state for codecuttlectl conversations.
// Sessions capture the full conversation history, tool execution diagnostics (Inkwell),
// and task state (todos), enabling resume, audit, and the Phase 3 reconciliation loop.
package session

import (
	"encoding/json"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// SessionMeta is the lightweight metadata shown in session listings.
// It is also the header of a full SessionState file.
type SessionMeta struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`    // Agent-generated after first turn
	Model     string    `json:"model"`    // Bedrock model ID used
	Region    string    `json:"region"`   // AWS region
	WorkDir   string    `json:"work_dir"` // Working directory at session start
	Stats     Stats     `json:"stats"`
}

// Stats tracks aggregate metrics for a session.
type Stats struct {
	InputTokens  int32 `json:"input_tokens"`
	OutputTokens int32 `json:"output_tokens"`
	ToolCalls    int   `json:"tool_calls"`
	Turns        int   `json:"turns"`
}

// SessionState is the full persisted state of a session.
// This is what gets serialized to disk and loaded on resume.
type SessionState struct {
	Meta     SessionMeta `json:"meta"`
	Messages []Message   `json:"messages"` // Serializable conversation history
	Todos    []todo.Item `json:"todos"`    // Current task state
	Inkwell  []InkEntry  `json:"inkwell"`  // Tool execution diagnostics
}

// InkEntry records a single tool execution event for diagnostic analysis.
// Named after the cuttlefish ink defense mechanism — the system's local cache
// where defensive diagnostic "ink" is gathered and analyzed for the
// Phase 3 Inkwell reconciliation loop.
type InkEntry struct {
	Timestamp  time.Time       `json:"timestamp"`
	Turn       int             `json:"turn"`
	ToolName   string          `json:"tool_name"`
	ToolUseID  string          `json:"tool_use_id"`
	Input      json.RawMessage `json:"input"`
	Output     string          `json:"output"`
	ExitCode   *int            `json:"exit_code,omitempty"`   // For bash_exec results
	Stderr     string          `json:"stderr,omitempty"`      // Captured separately when available
	DurationMs int64           `json:"duration_ms"`
	IsError    bool            `json:"is_error"`
	ErrorType  string          `json:"error_type,omitempty"`  // "compile", "runtime", "permission", "not_found", etc.
}
