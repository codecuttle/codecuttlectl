# Streaming Tool Output & Comprehensive Telemetry

## Overview

This design covers two interrelated changes:

1. **Live tool output preview** — the TUI shows a rolling preview of tool execution as it happens, rather than waiting for completion
2. **Comprehensive Inkwell telemetry** — all tool I/O, reasoning, and execution context is captured at full fidelity for auditing, governance, security monitoring, and liability determination

These are interrelated because streaming output requires protocol changes that also enable richer telemetry capture.

## Problem Statement

### UX Problem
Currently when a tool runs, the user sees:
```
⚡ Calling bash_exec...
```
...then nothing until completion, when they get:
```
✓ <truncated 200-char result>
```

For long-running commands (builds, tests, searches), this is opaque. The user doesn't know if the tool is working, stuck, or producing errors.

### Governance Problem
The current Inkwell captures:
- Tool name, input JSON, final output string, duration, error flag
- But NOT: intermediate output, stdout/stderr separation, reasoning context, partial results, timing breakdown

For insurance/liability investigations, security audits, or compliance reviews, we need:
- Full untruncated I/O at every stage
- Reasoning text that led to each tool invocation
- Timestamps at sub-second granularity
- Clear causal chain: user intent → reasoning → tool call → output → next action
- Ability to replay the exact sequence of events

## Architecture

### Plugin Protocol Extension

Add a streaming output capability to the Cuttlebone gRPC protocol:

```protobuf
// ExecuteStream is an optional RPC that streams output incrementally.
// Plugins that support it declare supports_streaming=true in capabilities.
// The orchestrator falls back to Execute() for non-streaming plugins.
rpc ExecuteStream(ExecuteRequest) returns (stream ExecuteStreamEvent);

message ExecuteStreamEvent {
  oneof event {
    OutputDelta output_delta = 1;     // Incremental stdout text
    ErrorDelta error_delta = 2;       // Incremental stderr text  
    ProgressUpdate progress = 3;      // Structured progress (percent, message)
    ExecuteResponse final = 4;        // Final result (same as Execute response)
  }
}

message OutputDelta {
  string text = 1;
  bool is_stderr = 2;
}

message ProgressUpdate {
  string message = 1;      // e.g., "Building plugin 3/12..."
  float percent = 2;       // 0.0-1.0, -1 if unknown
  map<string, string> metadata = 3;
}
```

### Orchestrator Changes

The plugin manager gains a `ExecuteStream` method:

```go
// ExecuteStream calls the streaming RPC if the plugin supports it,
// otherwise falls back to Execute() and synthesizes a single final event.
func (m *Manager) ExecuteStream(ctx context.Context, name string, input json.RawMessage, workDir string) (<-chan ToolStreamEvent, error)
```

The conversation agent consumes events and:
1. Forwards output deltas to the TUI via Bubble Tea messages
2. Accumulates full output for the final tool result
3. Records everything in the Inkwell

### TUI Changes

New message types for progressive tool rendering:

```go
type ToolOutputDeltaMsg struct {
    ToolUseID string
    Name      string
    Delta     string
    IsStderr  bool
}

type ToolProgressMsg struct {
    ToolUseID string
    Name      string
    Message   string
    Percent   float32
}
```

The `renderMessages()` function shows an active tool block:

```
⚡ bash_exec: make all
  ┃ Building plugin: cuttlebone-bash-exec
  ┃ Building plugin: cuttlebone-edit-file
  ┃ Building plugin: cuttlebone-git
  ┃ ...
```

This block:
- Shows the last N lines of output (configurable, default 5)
- Scrolls as new output arrives
- Has a distinct visual style (dimmer, monospace, indented)
- Collapses to a summary on completion

### bash_exec Streaming Implementation

The bash_exec plugin is the highest-value target for streaming since most commands produce incremental output. Implementation:

```go
func (t *bashExecTool) ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error {
    // ... setup ...
    
    cmd := exec.Command("bash", "-c", params.Command)
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()
    
    cmd.Start()
    
    // Stream stdout/stderr as deltas
    go scanAndStream(stdout, false, stream)
    go scanAndStream(stderr, true, stream)
    
    cmd.Wait()
    
    // Send final result
    stream.Send(&pb.ExecuteStreamEvent{
        Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{...}},
    })
}
```

## Comprehensive Inkwell Telemetry

### Enhanced InkEntry Structure

```go
type InkEntry struct {
    // Identity & Timing
    ID         string          `json:"id"`          // Unique entry ID (ink_<ulid>)
    Timestamp  time.Time       `json:"timestamp"`   // Start time (sub-second)
    EndTime    time.Time       `json:"end_time"`    // Completion time
    Turn       int             `json:"turn"`
    Step       int             `json:"step"`        // Step within turn (0-indexed)
    
    // Tool Execution
    ToolName   string          `json:"tool_name"`
    ToolUseID  string          `json:"tool_use_id"`
    Input      json.RawMessage `json:"input"`       // Full input JSON (never truncated)
    Output     string          `json:"output"`      // Full output (never truncated)
    Stderr     string          `json:"stderr"`      // Separated stderr when available
    ExitCode   *int            `json:"exit_code,omitempty"`
    DurationMs int64           `json:"duration_ms"`
    
    // Error Classification
    IsError    bool            `json:"is_error"`
    ErrorType  string          `json:"error_type,omitempty"`
    ErrorClass string          `json:"error_class,omitempty"` // From classifier
    
    // Context (what led to this tool call)
    ReasoningContext string    `json:"reasoning_context,omitempty"` // Model's reasoning before this call
    UserIntent       string    `json:"user_intent,omitempty"`       // Original user message this turn
    
    // Streaming telemetry
    OutputChunks []OutputChunk `json:"output_chunks,omitempty"` // Timestamped incremental output
    
    // Governance metadata
    PluginVersion string       `json:"plugin_version"`
    ModelID       string       `json:"model_id"`
    SessionID     string       `json:"session_id"`
    
    // Security flags
    WasBlocked       bool     `json:"was_blocked,omitempty"`       // Tool discipline blocked it
    WasOverridden    bool     `json:"was_overridden,omitempty"`    // Allowed after N blocks
    BlockReason      string   `json:"block_reason,omitempty"`
    RequiredApproval bool     `json:"required_approval,omitempty"` // Future: user-approved destructive op
}

type OutputChunk struct {
    Timestamp time.Time `json:"timestamp"`
    Text      string    `json:"text"`
    IsStderr  bool      `json:"is_stderr"`
}
```

### Session-Level Telemetry

The session file gains comprehensive metadata:

```go
type SessionState struct {
    Meta     SessionMeta    `json:"meta"`
    Messages []Message      `json:"messages"`
    Todos    []todo.Item    `json:"todos"`
    Inkwell  []InkEntry     `json:"inkwell"`     // Full execution trace
    
    // Governance additions
    Audit    AuditTrail     `json:"audit"`       // Session-level audit info
}

type AuditTrail struct {
    // Who
    AuthMethod    string `json:"auth_method"`    // "iam_role", "pat", "coder_oauth"
    AuthIdentity  string `json:"auth_identity"`  // Role ARN, username, etc.
    
    // What model
    ModelID       string `json:"model_id"`
    ModelVersion  string `json:"model_version"`
    
    // Token accounting (for cost attribution)
    TotalInputTokens      int64 `json:"total_input_tokens"`
    TotalOutputTokens     int64 `json:"total_output_tokens"`
    TotalCacheReadTokens  int64 `json:"total_cache_read_tokens"`
    TotalCacheWriteTokens int64 `json:"total_cache_write_tokens"`
    
    // Safety
    ToolDisciplineBlocks    int `json:"tool_discipline_blocks"`
    ToolDisciplineOverrides int `json:"tool_discipline_overrides"`
    DestructiveOpsAttempted int `json:"destructive_ops_attempted"`
    DestructiveOpsApproved  int `json:"destructive_ops_approved"`
    
    // Timing
    FirstToolCallAt time.Time `json:"first_tool_call_at"`
    LastToolCallAt  time.Time `json:"last_tool_call_at"`
    WallClockMs     int64     `json:"wall_clock_ms"`
}
```

### Telemetry Output Channels

Inkwell data is available via multiple channels:

1. **Session files** — Full JSON on disk (`~/.local/share/codecuttlectl/sessions/`)
2. **Structured logs** — One-line JSON per event to stderr (for external log aggregation)
3. **Future: OpenTelemetry** — Spans per tool call with attributes
4. **Future: Webhook** — POST events to an external security/audit endpoint

### What Gets Captured (for liability/insurance)

Every session file contains enough to reconstruct:

| Question | Answered by |
|----------|-------------|
| What did the user ask? | `messages[role=user]` |
| What did the model reason? | `inkwell[].reasoning_context` + reasoning messages |
| What tool was called and why? | `inkwell[].tool_name` + `input` + reasoning context |
| What was the exact command? | `inkwell[].input` (full JSON, never truncated) |
| What happened? | `inkwell[].output` (full, never truncated) + `output_chunks` |
| Was it dangerous? | `inkwell[].was_blocked` / `error_type` / safety flags |
| Did a human approve it? | `inkwell[].required_approval` + audit trail |
| How much did it cost? | `audit.total_*_tokens` |
| Who authenticated? | `audit.auth_method` + `auth_identity` |
| What model version? | `audit.model_id` + `model_version` |
| Exact timeline? | Timestamps on every entry + `output_chunks` |

## Implementation Plan

| Phase | What | Status |
|-------|------|--------|
| 1 | Enhanced InkEntry struct + capture reasoning context | ✅ Done |
| 2 | Separate stdout/stderr in bash_exec + full output (no truncation in Inkwell) | ✅ Done |
| 3 | Proto: `ExecuteStream` RPC + `ExecuteStreamEvent` message | ✅ Done |
| 4 | bash_exec streaming implementation | ✅ Done |
| 5 | Plugin manager `ExecuteStream` + fallback to `Execute` | ✅ Done |
| 6 | TUI: live tool output preview block | ✅ Done |
| 7 | AuditTrail in session + structured log output | ⬜ Future |
| 8 | Other plugins streaming (git, grep, glob) | ⬜ Future |
| 9 | User approval flow for destructive operations | ✅ Done |
| 10 | OpenTelemetry integration | ⬜ Future |

## TUI Preview Rendering

### During execution:
```
⚡ bash_exec: make all (12s)
  ┃ Building plugin: cuttlebone-glob
  ┃ Building plugin: cuttlebone-go-skills
  ┃ Building plugin: cuttlebone-grep
  ┃ Building plugin: cuttlebone-list-directory  
  ┃ Building plugin: cuttlebone-read-file
```

### After completion:
```
✓ bash_exec: make all (18s, exit 0)
```

### On error:
```
✗ bash_exec: go build ./... (3s, exit 1)
  ┃ internal/tui/app.go:42:5: undefined: foo
```

The preview shows:
- Last 5 lines of output (scrolling)
- Duration counter (live)
- Tool name + first arg for context
- Color coding: green border normal, red on error

## Design Principles

1. **Never truncate in Inkwell** — display can truncate, telemetry cannot
2. **Timestamps everywhere** — sub-second precision on every event
3. **Causal chain preserved** — reasoning → tool call → output → next reasoning
4. **Fail-open for UX, fail-closed for safety** — preview can lag, but telemetry must be complete
5. **Retroactively queryable** — session files are self-contained JSON, greppable, parseable
6. **No PII in tool names/metadata** — sensitive content is in the output field only
