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
// Inkwell reconciliation loop, governance auditing, and liability tracing.
//
// Design principle: NEVER truncate Input or Output in InkEntry. Display layers
// may truncate for UX, but the Inkwell is the authoritative record and must
// contain full, unmodified I/O for audit and replay.
type InkEntry struct {
	// Identity & Timing
	Timestamp  time.Time       `json:"timestamp"`
	EndTime    time.Time       `json:"end_time,omitempty"`    // When execution completed
	Turn       int             `json:"turn"`
	Step       int             `json:"step,omitempty"`        // Step within turn (0-indexed)

	// Tool Execution
	ToolName   string          `json:"tool_name"`
	ToolUseID  string          `json:"tool_use_id"`
	Input      json.RawMessage `json:"input"`                 // Full input JSON (NEVER truncated)
	Output     string          `json:"output"`                // Full output (NEVER truncated)
	Stderr     string          `json:"stderr,omitempty"`      // Separated stderr when available
	ExitCode   *int            `json:"exit_code,omitempty"`   // For bash_exec results
	DurationMs int64           `json:"duration_ms"`

	// Error Classification
	IsError    bool            `json:"is_error"`
	ErrorType  string          `json:"error_type,omitempty"`  // "compile", "runtime", "permission", "not_found", etc.

	// Context — what led to this tool call
	ReasoningContext string    `json:"reasoning_context,omitempty"` // Model's reasoning/text before this call
	UserIntent       string    `json:"user_intent,omitempty"`       // User message that initiated this turn

	// Plugin metadata
	PluginVersion string       `json:"plugin_version,omitempty"`

	// Governance / Safety
	WasBlocked    bool         `json:"was_blocked,omitempty"`       // Tool discipline blocked execution
	WasOverridden bool         `json:"was_overridden,omitempty"`    // Allowed through after N blocks
	BlockReason   string       `json:"block_reason,omitempty"`
}
