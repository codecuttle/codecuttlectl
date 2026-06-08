// Package audit provides structured event logging and session-level audit
// trail accumulation for governance, cost attribution, and security monitoring.
//
// Events are emitted as one-line JSON to stderr, suitable for piping to
// external log aggregation systems (CloudWatch Logs, Datadog, Splunk, etc.).
// The same data accumulates in the session file's AuditTrail struct for
// retroactive querying.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Logger emits structured audit events as one-line JSON.
// Safe for concurrent use.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	enabled bool
}

// NewLogger creates an audit logger that writes to the given writer.
// If w is nil, defaults to stderr.
func NewLogger(w io.Writer, enabled bool) *Logger {
	if w == nil {
		w = os.Stderr
	}
	return &Logger{w: w, enabled: enabled}
}

// Event represents a single structured audit log entry.
// Each event is a self-contained JSON object with all context needed
// for downstream processing without session file access.
type Event struct {
	// Common fields (present on every event)
	Timestamp time.Time `json:"ts"`
	Level     string    `json:"level"`     // "info", "warn", "security"
	Type      string    `json:"type"`      // Event type identifier
	SessionID string    `json:"session_id"`

	// Tool execution events
	ToolName  string `json:"tool_name,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	DurationMs int64 `json:"duration_ms,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`

	// Token accounting
	InputTokens      int32 `json:"input_tokens,omitempty"`
	OutputTokens     int32 `json:"output_tokens,omitempty"`
	CacheReadTokens  int32 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int32 `json:"cache_write_tokens,omitempty"`

	// Safety/governance
	Risk             string `json:"risk,omitempty"`
	ApprovalDecision string `json:"approval_decision,omitempty"`
	BlockReason      string `json:"block_reason,omitempty"`
	WasBlocked       bool   `json:"was_blocked,omitempty"`

	// Context
	ModelID string `json:"model_id,omitempty"`
	Turn    int    `json:"turn,omitempty"`
	Step    int    `json:"step,omitempty"`

	// Free-form message for human readability
	Message string `json:"msg,omitempty"`
}

// Emit writes a single event as one-line JSON to the logger's writer.
// No-op if the logger is disabled. Never returns errors — logging must
// not interrupt the main execution flow.
func (l *Logger) Emit(event Event) {
	if !l.enabled {
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(event)
	if err != nil {
		return // Silently drop malformed events
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s\n", data)
}

// Enabled returns whether the logger is active.
func (l *Logger) Enabled() bool {
	return l.enabled
}

// --- Convenience methods for common event types ---

// ToolExec emits a tool execution event.
func (l *Logger) ToolExec(sessionID, toolName, toolUseID string, durationMs int64, isError bool, errorType string, turn, step int) {
	level := "info"
	if isError {
		level = "warn"
	}
	l.Emit(Event{
		Level:      level,
		Type:       "tool_exec",
		SessionID:  sessionID,
		ToolName:   toolName,
		ToolUseID:  toolUseID,
		DurationMs: durationMs,
		IsError:    isError,
		ErrorType:  errorType,
		Turn:       turn,
		Step:       step,
	})
}

// TokenUsage emits a token accounting event (after each model call).
func (l *Logger) TokenUsage(sessionID, modelID string, inputTokens, outputTokens, cacheRead, cacheWrite int32, turn, step int) {
	l.Emit(Event{
		Level:            "info",
		Type:             "token_usage",
		SessionID:        sessionID,
		ModelID:          modelID,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
		Turn:             turn,
		Step:             step,
	})
}

// ApprovalEvent emits when a destructive operation hits the approval gate.
func (l *Logger) ApprovalEvent(sessionID, toolName, risk, decision string, turn int) {
	level := "security"
	l.Emit(Event{
		Level:            level,
		Type:             "approval",
		SessionID:        sessionID,
		ToolName:         toolName,
		Risk:             risk,
		ApprovalDecision: decision,
		Turn:             turn,
	})
}

// ToolDisciplineBlock emits when the tool discipline system blocks a command.
func (l *Logger) ToolDisciplineBlock(sessionID, toolName, reason string, turn int) {
	l.Emit(Event{
		Level:       "security",
		Type:        "tool_discipline_block",
		SessionID:   sessionID,
		ToolName:    toolName,
		BlockReason: reason,
		WasBlocked:  true,
		Turn:        turn,
	})
}

// SessionStart emits when a session begins.
func (l *Logger) SessionStart(sessionID, modelID, authMethod, authIdentity string) {
	l.Emit(Event{
		Level:     "info",
		Type:      "session_start",
		SessionID: sessionID,
		ModelID:   modelID,
		Message:   fmt.Sprintf("auth=%s identity=%s", authMethod, authIdentity),
	})
}

// SessionEnd emits when a session ends with summary stats.
func (l *Logger) SessionEnd(sessionID string, turns int, totalInputTokens, totalOutputTokens int64, wallClockMs int64) {
	l.Emit(Event{
		Level:     "info",
		Type:      "session_end",
		SessionID: sessionID,
		Message:   fmt.Sprintf("turns=%d input_tokens=%d output_tokens=%d wall_ms=%d", turns, totalInputTokens, totalOutputTokens, wallClockMs),
	})
}
