// Package conversation implements the agent conversation loop with tool calling.
package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// Agent orchestrates the conversation between the user, the LLM, and the tool system.
type Agent struct {
	client       *bedrock.Client
	promptMgr    *prompt.Manager
	pluginMgr    *pluginhost.Manager
	systemPrompt string
	workDir      string
	history      []types.Message
	todos        *todo.List
	maxSteps     int
	verbose      bool

	// Session persistence
	store     session.Store
	sessionID string
	inkwell   []session.InkEntry
	turn      int
	dirty     bool // true when state needs flushing to disk
}

// Config holds configuration for creating an Agent.
type Config struct {
	Client    *bedrock.Client
	PromptMgr *prompt.Manager
	PluginMgr *pluginhost.Manager
	WorkDir   string
	MaxSteps  int  // Maximum tool-use iterations per turn. Default: 25
	Verbose   bool // Print debug info

	// Session persistence (optional — nil disables persistence)
	Store     session.Store
	SessionID string // If set, resume this session
}

// NewAgent creates a new conversation agent.
func NewAgent(cfg Config) (*Agent, error) {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 25
	}

	var systemPrompt string
	if cfg.PromptMgr != nil {
		var promptTools []prompt.ToolDef
		for _, def := range cfg.PluginMgr.Definitions() {
			promptTools = append(promptTools, prompt.ToolDef{
				Name:        def.Name,
				Description: def.Description,
			})
		}

		var err error
		systemPrompt, err = cfg.PromptMgr.RenderSystem(cfg.WorkDir, cfg.Client.ModelID(), promptTools)
		if err != nil {
			return nil, fmt.Errorf("rendering system prompt: %w", err)
		}

		if hints := cfg.PluginMgr.LLMHints(); hints != "" {
			systemPrompt += "\n\n## Additional Tool Guidance\n" + hints
		}
	}

	agent := &Agent{
		client:       cfg.Client,
		promptMgr:    cfg.PromptMgr,
		pluginMgr:    cfg.PluginMgr,
		systemPrompt: systemPrompt,
		workDir:      cfg.WorkDir,
		todos:        todo.NewList(),
		maxSteps:     cfg.MaxSteps,
		verbose:      cfg.Verbose,
		store:        cfg.Store,
		sessionID:    cfg.SessionID,
	}

	// If resuming a session, restore state
	if cfg.Store != nil && cfg.SessionID != "" {
		if err := agent.loadSession(); err != nil {
			return nil, fmt.Errorf("loading session %s: %w", cfg.SessionID, err)
		}
	}

	return agent, nil
}

// SetSystemPrompt overrides the system prompt (used when prompt is pre-rendered externally).
func (a *Agent) SetSystemPrompt(s string) {
	a.systemPrompt = s
}

// SessionID returns the current session ID (empty if no session persistence).
func (a *Agent) SessionID() string {
	return a.sessionID
}

// InitSession creates a new session and returns its ID.
// Call this after agent creation if you want persistence but aren't resuming.
func (a *Agent) InitSession(model, region, workDir string) (string, error) {
	if a.store == nil {
		return "", nil
	}

	meta := session.SessionMeta{
		Model:   model,
		Region:  region,
		WorkDir: workDir,
	}

	id, err := a.store.Create(meta)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	a.sessionID = id
	return id, nil
}

// Turn sends a user message and processes the model's response, executing tools as needed.
// Returns the final text response from the model. (Synchronous, for one-shot/plain mode.)
func (a *Agent) Turn(ctx context.Context, userMessage string) (string, error) {
	a.turn++
	a.history = append(a.history, bedrock.BuildUserTextMessage(userMessage))

	for step := 0; step < a.maxSteps; step++ {
		resp, err := a.client.Converse(ctx, a.systemPrompt, a.history, a.allToolDefs())
		if err != nil {
			return "", fmt.Errorf("converse step %d: %w", step, err)
		}

		if a.verbose {
			log.Printf("[step %d] stop_reason=%s tools=%d tokens_in=%d tokens_out=%d",
				step, resp.StopReason, len(resp.ToolUses), resp.InputTokens, resp.OutputTokens)
		}

		// Record the assistant's full response
		if len(resp.RawContentBlocks) > 0 {
			a.history = append(a.history, bedrock.BuildAssistantMessage(resp.RawContentBlocks))
		}

		// If no tool calls, we're done
		if len(resp.ToolUses) == 0 {
			a.flushSession()
			return resp.Content, nil
		}

		// Execute all tool calls and collect results
		var toolResults []bedrock.ToolResult
		for _, toolUse := range resp.ToolUses {
			if a.verbose {
				log.Printf("[tool] %s id=%s input=%s", toolUse.Name, toolUse.ToolUseID, string(toolUse.Input))
			}

			start := time.Now()
			result, status := a.executeTool(ctx, toolUse.Name, toolUse.Input)
			duration := time.Since(start)

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool result] %s (%dms)", truncated, duration.Milliseconds())
			}

			// Record in Inkwell
			isErr := status == types.ToolResultStatusError
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:  time.Now().UTC(),
				Turn:       a.turn,
				ToolName:   toolUse.Name,
				ToolUseID:  toolUse.ToolUseID,
				Input:      toolUse.Input,
				Output:     result,
				DurationMs: duration.Milliseconds(),
				IsError:    isErr,
				ErrorType:  classifyError(isErr, toolUse.Name, result),
			})

			toolResults = append(toolResults, bedrock.ToolResult{
				ToolUseID: toolUse.ToolUseID,
				Content:   result,
				Status:    status,
			})
		}

		a.history = append(a.history, bedrock.BuildToolResultMessage(toolResults))
		a.dirty = true
		a.flushSession()
	}

	return "", fmt.Errorf("exceeded maximum steps (%d) without completing", a.maxSteps)
}

// StreamCallback is called for each event during a streaming turn.
type StreamCallback func(event StreamEvent)

// StreamEvent represents an event emitted during a streaming turn.
type StreamEvent struct {
	Type      string // "text", "tool_start", "tool_done", "error", "done"
	Text      string // For "text" events: the delta
	ToolName  string // For "tool_start" events
	ToolUseID string // For "tool_start" events
	Error     error  // For "error" events
}

// StreamTurn sends a user message and streams the model's response in real-time.
// The callback receives text deltas as they arrive. Tool calls are executed
// between streaming rounds. Returns the final accumulated text response.
func (a *Agent) StreamTurn(ctx context.Context, userMessage string, cb StreamCallback) (string, error) {
	a.turn++
	a.history = append(a.history, bedrock.BuildUserTextMessage(userMessage))

	for step := 0; step < a.maxSteps; step++ {
		ch := a.client.ConverseStream(ctx, a.systemPrompt, a.history, a.allToolDefs())

		var textBuf strings.Builder
		var toolCalls []pendingToolCall
		var currentToolInput strings.Builder
		var currentToolID, currentToolName string

		for event := range ch {
			switch e := event.(type) {
			case bedrock.TextDeltaEvent:
				textBuf.WriteString(e.Text)
				if cb != nil {
					cb(StreamEvent{Type: "text", Text: e.Text})
				}

			case bedrock.ToolUseStartEvent:
				currentToolID = e.ToolUseID
				currentToolName = e.Name
				currentToolInput.Reset()
				if cb != nil {
					cb(StreamEvent{Type: "tool_start", ToolName: e.Name, ToolUseID: e.ToolUseID})
				}

			case bedrock.ToolInputDeltaEvent:
				currentToolInput.WriteString(e.Delta)

			case bedrock.ToolUseStopEvent:
				if currentToolName != "" {
					input := bedrock.CollectToolInput([]string{currentToolInput.String()})
					toolCalls = append(toolCalls, pendingToolCall{
						id:    currentToolID,
						name:  currentToolName,
						input: input,
					})
					currentToolName = ""
					currentToolID = ""
					currentToolInput.Reset()
				}

			case bedrock.UsageEvent:
				// Track token usage (could record in stats)

			case bedrock.StreamErrorEvent:
				if cb != nil {
					cb(StreamEvent{Type: "error", Error: e.Err})
				}
				return textBuf.String(), e.Err

			case bedrock.MessageStopEvent:
				// Stream complete for this round
			}
		}

		// Build assistant message for history
		var blocks []types.ContentBlock
		if textBuf.Len() > 0 {
			blocks = append(blocks, &types.ContentBlockMemberText{Value: textBuf.String()})
		}
		for _, tc := range toolCalls {
			blocks = append(blocks, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: &tc.id,
					Name:      &tc.name,
					Input:     document.NewLazyDocument(jsonToMapAgent(tc.input)),
				},
			})
		}
		if len(blocks) > 0 {
			a.history = append(a.history, bedrock.BuildAssistantMessage(blocks))
		}

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			a.flushSession()
			if cb != nil {
				cb(StreamEvent{Type: "done"})
			}
			return textBuf.String(), nil
		}

		// Execute tool calls
		var toolResults []bedrock.ToolResult
		for _, tc := range toolCalls {
			start := time.Now()
			result, status := a.executeTool(ctx, tc.name, tc.input)
			duration := time.Since(start)

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool] %s (%dms): %s", tc.name, duration.Milliseconds(), truncated)
			}

			isErr := status == types.ToolResultStatusError
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:  time.Now().UTC(),
				Turn:       a.turn,
				ToolName:   tc.name,
				ToolUseID:  tc.id,
				Input:      tc.input,
				Output:     result,
				DurationMs: duration.Milliseconds(),
				IsError:    isErr,
				ErrorType:  classifyError(isErr, tc.name, result),
			})

			toolResults = append(toolResults, bedrock.ToolResult{
				ToolUseID: tc.id,
				Content:   result,
				Status:    status,
			})

			if cb != nil {
				cb(StreamEvent{Type: "tool_done", ToolName: tc.name, ToolUseID: tc.id})
			}
		}

		a.history = append(a.history, bedrock.BuildToolResultMessage(toolResults))
		a.dirty = true
		a.flushSession()

		// Clear text buffer for next round
		textBuf.Reset()
	}

	return "", fmt.Errorf("exceeded maximum steps (%d) without completing", a.maxSteps)
}

type pendingToolCall struct {
	id    string
	name  string
	input json.RawMessage
}

func jsonToMapAgent(data json.RawMessage) map[string]interface{} {
	var m map[string]interface{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

// executeTool dispatches a tool call to the appropriate handler.
func (a *Agent) executeTool(ctx context.Context, name string, input json.RawMessage) (string, types.ToolResultStatus) {
	// Built-in: todo_manage
	if name == "todo_manage" {
		result := a.handleTodoTool(input)
		return result, types.ToolResultStatusSuccess
	}

	// Plugin tools
	output, err := a.pluginMgr.Execute(ctx, name, input, a.workDir)
	if err != nil {
		// output already contains the error details from the plugin
		if output == "" {
			output = fmt.Sprintf("Error: %s", err.Error())
		}
		return output, types.ToolResultStatusError
	}
	return output, types.ToolResultStatusSuccess
}

func (a *Agent) handleTodoTool(input json.RawMessage) string {
	var payload struct {
		Todos []todo.Item `json:"todos"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Sprintf("Error parsing todo input: %v", err)
	}
	if err := a.todos.Replace(payload.Todos); err != nil {
		return fmt.Sprintf("Error updating todos: %v", err)
	}
	return fmt.Sprintf("Todo list updated: %s", a.todos.Summary())
}

// allToolDefs returns combined tool definitions from plugins + built-in tools.
func (a *Agent) allToolDefs() []bedrock.ToolDefinition {
	defs := a.pluginMgr.Definitions()
	defs = append(defs, todoToolDefinition())
	return defs
}

func todoToolDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "todo_manage",
		Description: "Create and maintain a structured task list for the current session. Tracks progress, organizes multi-step work, and surfaces status to the user.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"todos": {
					"type": "array",
					"description": "The complete updated todo list (full replacement)",
					"items": {
						"type": "object",
						"properties": {
							"content": {"type": "string", "description": "Brief description of the task"},
							"status": {"type": "string", "enum": ["pending", "in_progress", "completed", "cancelled"], "description": "Current status"},
							"priority": {"type": "string", "enum": ["high", "medium", "low"], "description": "Priority level"}
						},
						"required": ["content", "status", "priority"]
					}
				}
			},
			"required": ["todos"]
		}`),
	}
}

// --- Session persistence ---

// loadSession restores conversation state from a persisted session.
func (a *Agent) loadSession() error {
	state, err := a.store.Load(a.sessionID)
	if err != nil {
		return err
	}

	// Restore conversation history
	messages, err := session.UnmarshalHistory(state.Messages)
	if err != nil {
		return fmt.Errorf("restoring history: %w", err)
	}
	a.history = messages

	// Restore todos
	if len(state.Todos) > 0 {
		a.todos.Replace(state.Todos)
	}

	// Restore inkwell
	a.inkwell = state.Inkwell

	// Restore turn counter from stats
	a.turn = state.Meta.Stats.Turns

	return nil
}

// flushSession persists the current state to disk if a session store is configured.
func (a *Agent) flushSession() {
	if a.store == nil || a.sessionID == "" {
		return
	}

	// Marshal current history
	messages, err := session.MarshalHistory(a.history)
	if err != nil {
		if a.verbose {
			log.Printf("[session] warning: failed to marshal history: %v", err)
		}
		return
	}

	// Load existing state to preserve metadata (title, model, region, etc.)
	existing, loadErr := a.store.Load(a.sessionID)
	var meta session.SessionMeta
	if loadErr == nil {
		meta = existing.Meta
	} else {
		meta.ID = a.sessionID
	}

	// Update stats
	meta.Stats.Turns = a.turn
	meta.Stats.ToolCalls = len(a.inkwell)

	state := &session.SessionState{
		Meta:     meta,
		Messages: messages,
		Todos:    a.todos.Items(),
		Inkwell:  a.inkwell,
	}

	if err := a.store.Save(a.sessionID, state); err != nil {
		if a.verbose {
			log.Printf("[session] warning: failed to save session: %v", err)
		}
	}

	a.dirty = false
}

// GenerateTitle asks the model to generate a short title for the session.
// This is a lightweight call with no tools, used after the first turn.
func (a *Agent) GenerateTitle(ctx context.Context) string {
	if len(a.history) == 0 {
		return "Empty session"
	}

	// Extract the first user message
	var firstMsg string
	for _, msg := range a.history {
		if msg.Role == types.ConversationRoleUser {
			for _, block := range msg.Content {
				if text, ok := block.(*types.ContentBlockMemberText); ok {
					firstMsg = text.Value
					break
				}
			}
			break
		}
	}

	if firstMsg == "" {
		return "Untitled session"
	}

	// Make a lightweight model call to generate a title
	titlePrompt := "Generate a 2-5 word title for a coding session that started with this message. Reply with ONLY the title, no punctuation, nothing else."
	titleMessages := []types.Message{
		bedrock.BuildUserTextMessage(titlePrompt + "\n\nMessage: " + firstMsg),
	}

	resp, err := a.client.Converse(ctx, "", titleMessages, nil)
	if err != nil {
		// Fallback: truncate the first user message
		if len(firstMsg) > 50 {
			return firstMsg[:50]
		}
		return firstMsg
	}

	title := resp.Content
	if len(title) > 60 {
		title = title[:60]
	}
	return title
}

// HistoryLen returns the current conversation history length.
func (a *Agent) HistoryLen() int {
	return len(a.history)
}

// Reset clears the conversation history.
func (a *Agent) Reset() {
	a.history = nil
	a.todos = todo.NewList()
	a.inkwell = nil
	a.turn = 0
}

// SystemPrompt returns the rendered system prompt for debugging.
func (a *Agent) SystemPrompt() string {
	return a.systemPrompt
}

// Todos returns the current todo list.
func (a *Agent) Todos() *todo.List {
	return a.todos
}

// classifyError attempts to categorize a tool error for Inkwell analysis.
func classifyError(isErr bool, toolName string, output string) string {
	if !isErr {
		return ""
	}

	// Simple heuristic classification — Phase 3 will make this much smarter
	switch {
	case contains(output, "not found") || contains(output, "no such file"):
		return "not_found"
	case contains(output, "permission denied"):
		return "permission"
	case contains(output, "syntax error") || contains(output, "unexpected token"):
		return "syntax"
	case contains(output, "compile") || contains(output, "cannot find package") || contains(output, "undefined:"):
		return "compile"
	case contains(output, "timeout") || contains(output, "timed out"):
		return "timeout"
	case contains(output, "connection refused") || contains(output, "network"):
		return "network"
	default:
		return "runtime"
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsLower(s, substr))
}

func containsLower(s, substr string) bool {
	// Simple case-insensitive contains
	sl := toLower(s)
	return indexOf(sl, toLower(substr)) >= 0
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(s) < len(substr) {
		return -1
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
