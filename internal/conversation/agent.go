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
	"github.com/codecuttle/codecuttlectl/internal/approval"
	"github.com/codecuttle/codecuttlectl/internal/audit"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/inkwell"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/scaffold"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/skills"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// Agent orchestrates the conversation between the user, the LLM, and the tool system.
type Agent struct {
	client       *bedrock.Client   // Bedrock client (nil when using provider interface)
	pool         provider.Pool     // Multi-model pool (nil when using provider interface)
	provider     provider.Provider // Provider interface (used when client/pool is nil)
	promptMgr    *prompt.Manager
	pluginMgr    *pluginhost.Manager
	systemPrompt string
	workDir      string
	pluginDir    string
	history      []types.Message    // Bedrock SDK messages (used when client != nil)
	provHistory  []provider.Message // Provider-agnostic messages (used when provider != nil)
	todos        *todo.List
	maxSteps     int
	verbose      bool

	// Safety
	autoApprove  bool
	approvalFunc func(toolName, command, reason, risk string) bool

	// Audit trail
	auditLogger *audit.Logger
	auditTrail  session.AuditTrail

	// Inkwell reconciliation loop
	reconciler *inkwell.Reconciler

	// Session persistence
	store     session.Store
	sessionID string
	inkwell   []session.InkEntry
	turn      int
	dirty     bool // true when state needs flushing to disk

	// Sandbox
	workbench  []string
	morph      *swarm.Morphology
	activeNode string
	dispatcher swarm.EventDispatcher
}

// Config holds configuration for creating an Agent.
type Config struct {
	Client    *bedrock.Client   // Bedrock client (nil when using Provider)
	Pool      provider.Pool     // Multi-model pool (if set, Client is ignored)
	Provider  provider.Provider // Provider interface (takes precedence over Client when non-nil)
	Morph     *swarm.Morphology // Swarm configuration (enables handoff tool)
	PromptMgr *prompt.Manager
	PluginMgr *pluginhost.Manager
	WorkDir   string
	PluginDir string // Plugin binary directory (for reload)
	MaxSteps  int    // Maximum tool-use iterations per turn. Default: 25
	Verbose   bool   // Print debug info

	// Safety
	AutoApprove  bool                                              // When true, skip destructive op confirmation
	ApprovalFunc func(toolName, command, reason, risk string) bool // External approval callback (nil = deny)
	Workbench    []string                                          // List of allowed tools (nil/empty or ["*"] means all tools)

	// Audit
	AuditLogger *audit.Logger // Structured event logger (nil = no structured logs)

	// Session persistence (optional — nil disables persistence)
	Store     session.Store
	SessionID string // If set, resume this session

	// Swarm Dispatcher
	EventDispatcher swarm.EventDispatcher // Dispatches async events to TUI
}

// NewAgent creates a new conversation agent.
func NewAgent(cfg Config) (*Agent, error) {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 25
	}

	// Resolve client from pool if not provided directly
	if cfg.Client == nil && cfg.Pool != nil {
		if bp, ok := cfg.Pool.(interface{ BedrockPool() *bedrock.ModelPool }); ok {
			cfg.Client = bp.BedrockPool().Primary()
		} else if primary := cfg.Pool.Primary(); primary != nil {
			cfg.Provider = primary
		}
	}

	// Phase 2: Compute available Swarm Nodes for the prompt
	var swarmNodes []string
	if cfg.Morph != nil {
		for nodeID := range cfg.Morph.Nodes {
			// Don't include the current active node (defaults to "orchestrator" but we check all)
			if nodeID != "orchestrator" {
				swarmNodes = append(swarmNodes, nodeID)
			}
		}
	}

	var systemPrompt string
	if cfg.PromptMgr != nil && (cfg.Client != nil || cfg.Provider != nil) {
		var promptTools []prompt.ToolDef
		for _, def := range allToolDefs(cfg.PluginMgr, cfg.Workbench, cfg.Morph) {
			promptTools = append(promptTools, prompt.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  prompt.SchemaToToolParams(def.InputSchema),
			})
		}

		var err error
		provName := "bedrock"
		if cfg.Provider != nil && cfg.Client == nil {
			provName = "ollama"
		}

		var modelID string
		if cfg.Client != nil {
			modelID = cfg.Client.ModelID()
		} else {
			modelID = cfg.Provider.ID()
		}

		systemPrompt, err = cfg.PromptMgr.RenderSystem(cfg.WorkDir, modelID, provName, promptTools, swarmNodes)
		if err != nil {
			return nil, fmt.Errorf("rendering system prompt: %w", err)
		}

		if hints := cfg.PluginMgr.LLMHints(); hints != "" {
			systemPrompt += "\n\n## Additional Tool Guidance\n" + hints
		}
	}

	agent := &Agent{
		client:       cfg.Client,
		pool:         cfg.Pool,
		provider:     cfg.Provider,
		promptMgr:    cfg.PromptMgr,
		pluginMgr:    cfg.PluginMgr,
		systemPrompt: systemPrompt,
		workDir:      cfg.WorkDir,
		pluginDir:    cfg.PluginDir,
		todos:        todo.NewList(),
		maxSteps:     cfg.MaxSteps,
		verbose:      cfg.Verbose,
		autoApprove:  cfg.AutoApprove,
		approvalFunc: cfg.ApprovalFunc,
		auditLogger:  cfg.AuditLogger,
		reconciler:   inkwell.NewReconciler(),
		store:        cfg.Store,
		sessionID:    cfg.SessionID,
		workbench:    cfg.Workbench,
		morph:        cfg.Morph,
		activeNode:   "orchestrator", // default until handoff overrides
		dispatcher:   cfg.EventDispatcher,
	}

	// Initialize active node if morph is provided
	if agent.morph != nil {
		for nodeID, node := range agent.morph.Nodes {
			if node.IsPrimary {
				agent.activeNode = nodeID
				break
			}
		}
	}

	// Initialize audit trail with model info and session start time
	modelID := ""
	if cfg.Client != nil {
		modelID = cfg.Client.ModelID()
	} else if cfg.Provider != nil {
		modelID = cfg.Provider.ID()
	}
	agent.auditTrail = session.AuditTrail{
		ModelID:        modelID,
		SessionStartAt: time.Now().UTC(),
	}

	// If resuming a session, restore state
	if cfg.Store != nil && cfg.SessionID != "" {
		if err := agent.loadSession(); err != nil {
			return nil, fmt.Errorf("loading session %s: %w", cfg.SessionID, err)
		}
	}

	return agent, nil
}

// IsToolAllowed checks if a tool name is permitted by the workbench.
func IsToolAllowed(toolName string, workbench []string) bool {
	if len(workbench) == 0 {
		return true // Default: all tools allowed
	}
	for _, allowed := range workbench {
		if allowed == "*" || allowed == toolName {
			return true
		}
	}
	return false
}

// SetSystemPrompt overrides the system prompt (used when prompt is pre-rendered externally).
func (a *Agent) SetSystemPrompt(s string) {
	a.systemPrompt = s
}

// SessionID returns the current session ID (empty if no session persistence).
func (a *Agent) SessionID() string {
	return a.sessionID
}

// SetApprovalFunc allows overriding the destructive tool approval handler.
func (a *Agent) SetApprovalFunc(fn func(toolName, command, reason, risk string) bool) {
	a.approvalFunc = fn
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
	// Use provider interface if available (Ollama, etc.)
	if a.provider != nil {
		return a.turnProvider(ctx, userMessage)
	}

	a.turn++
	a.history = append(a.history, bedrock.BuildUserTextMessage(userMessage))

	for step := 0; step < a.maxSteps; step++ {
		// Compute effective system prompt (base + reconciler injection)
		effectivePrompt := a.effectiveSystemPrompt()

		resp, err := a.client.Converse(ctx, effectivePrompt, a.history, a.allToolDefs())
		if err != nil {
			return "", fmt.Errorf("converse step %d: %w", step, err)
		}

		if a.verbose {
			log.Printf("[step %d] stop_reason=%s tools=%d tokens_in=%d tokens_out=%d cache_read=%d cache_write=%d",
				step, resp.StopReason, len(resp.ToolUses), resp.InputTokens, resp.OutputTokens,
				resp.CacheReadInputTokens, resp.CacheWriteInputTokens)
		}

		// Accumulate token usage in audit trail
		a.recordTokenUsage(resp.InputTokens, resp.OutputTokens, resp.CacheReadInputTokens, resp.CacheWriteInputTokens, step)

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

			if a.dispatcher != nil {
				a.dispatcher.Dispatch(swarm.TaskProgressMsg{
					Assignee: a.activeNode,
					Progress: fmt.Sprintf("Calling %s...", toolUse.Name),
				})
			}

			start := time.Now()
			result, status := a.executeTool(ctx, toolUse.Name, toolUse.Input)
			duration := time.Since(start)
			endTime := time.Now().UTC()

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool result] %s (%dms)", truncated, duration.Milliseconds())
			}

			// Record in Inkwell — full output, never truncated
			isErr := status == types.ToolResultStatusError
			errType := string(inkwell.Classify(toolUse.Name, result, isErr).Class)
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:        start.UTC(),
				EndTime:          endTime,
				Turn:             a.turn,
				Step:             step,
				ToolName:         toolUse.Name,
				ToolUseID:        toolUse.ToolUseID,
				Input:            toolUse.Input,
				Output:           result, // Full output — NEVER truncate in Inkwell
				DurationMs:       duration.Milliseconds(),
				IsError:          isErr,
				ErrorType:        errType,
				ReasoningContext: resp.Content, // Model's text reasoning this step
				UserIntent:       userMessage,
			})

			// Emit structured audit log event
			a.recordToolExec(toolUse.Name, toolUse.ToolUseID, duration.Milliseconds(), isErr, errType, step)

			toolResults = append(toolResults, bedrock.ToolResult{
				ToolUseID: toolUse.ToolUseID,
				Content:   result,
				Status:    status,
			})
		}

		a.history = append(a.history, bedrock.BuildToolResultMessage(toolResults))
		a.dirty = true
		a.flushSession()

		// Check if the reconciler recommends aborting
		advice := a.reconciler.Advise(a.inkwell)
		if advice.ShouldAbort {
			if a.verbose {
				log.Printf("[inkwell] ABORT recommended: %s", advice.AbortReason)
			}
			// Don't actually abort — let the model handle it via the injected prompt.
			// The escalation prompt tells the model to stop retrying and explain the failure.
		}
		if advice.InjectPrompt != "" {
			if a.verbose {
				log.Printf("[inkwell] injecting corrective prompt (%d chars)", len(advice.InjectPrompt))
			}
			// Append the inkwell advice as a text block to the last tool_result message
			lastIdx := len(a.history) - 1
			if lastIdx >= 0 && a.history[lastIdx].Role == types.ConversationRoleUser {
				a.history[lastIdx].Content = append(a.history[lastIdx].Content, &types.ContentBlockMemberText{
					Value: advice.InjectPrompt,
				})
			}
		}
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
	// Use provider interface if available (Ollama, etc.)
	if a.provider != nil {
		return a.streamTurnProvider(ctx, userMessage, cb)
	}

	a.turn++
	a.history = append(a.history, bedrock.BuildUserTextMessage(userMessage))

	for step := 0; step < a.maxSteps; step++ {
		var textBuf strings.Builder
		var toolCalls []pendingToolCall
		var currentToolInput strings.Builder
		var currentToolID, currentToolName string

		// Retry transient stream failures (mid-stream InternalServerException,
		// ThrottlingException / 429 "Too many tokens", etc.) with error-aware
		// backoff. The AWS SDK's built-in retryer only covers the initial
		// handshake with millisecond-scale delays — useless for token-bucket
		// throttling and blind to mid-stream faults. Partial output from a
		// failed attempt is discarded so history is never polluted.
		const maxStreamRetries = 5
		for attempt := 0; ; attempt++ {
			textBuf.Reset()
			toolCalls = nil
			currentToolInput.Reset()
			currentToolID, currentToolName = "", ""

			var streamErr error
			ch := a.client.ConverseStream(ctx, a.effectiveSystemPrompt(), a.history, a.allToolDefs())

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
					// Track token usage in audit trail
					a.recordTokenUsage(e.InputTokens, e.OutputTokens, e.CacheReadInputTokens, e.CacheWriteInputTokens, step)

				case bedrock.StreamErrorEvent:
					// Record and keep draining until the channel closes so the
					// producer goroutine is not leaked.
					streamErr = e.Err

				case bedrock.MessageStopEvent:
					// Stream complete for this round
				}
			}

			if streamErr == nil {
				break
			}

			if attempt < maxStreamRetries && ctx.Err() == nil && provider.IsTransientStreamError(streamErr) {
				delay := provider.RetryDelay(attempt, streamErr)
				if a.verbose {
					log.Printf("[stream] transient error (attempt %d/%d), retrying in %s: %v",
						attempt+1, maxStreamRetries, delay.Round(time.Second), streamErr)
				}
				if cb != nil {
					reason := "stream interrupted"
					if provider.IsThrottleError(streamErr) {
						reason = "rate limited"
					}
					cb(StreamEvent{Type: "text", Text: fmt.Sprintf(
						"\n[%s — retrying %d/%d in %s]\n", reason, attempt+1, maxStreamRetries, delay.Round(time.Second))})
				}
				select {
				case <-ctx.Done():
					return textBuf.String(), ctx.Err()
				case <-time.After(delay):
				}
				continue
			}

			if cb != nil {
				cb(StreamEvent{Type: "error", Error: streamErr})
			}
			return textBuf.String(), streamErr
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
			endTime := time.Now().UTC()

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool] %s (%dms): %s", tc.name, duration.Milliseconds(), truncated)
			}

			isErr := status == types.ToolResultStatusError
			errType := string(inkwell.Classify(tc.name, result, isErr).Class)
			a.inkwell = append(a.inkwell, session.InkEntry{
				Timestamp:        start.UTC(),
				EndTime:          endTime,
				Turn:             a.turn,
				Step:             step,
				ToolName:         tc.name,
				ToolUseID:        tc.id,
				Input:            tc.input,
				Output:           result, // Full output — NEVER truncate in Inkwell
				DurationMs:       duration.Milliseconds(),
				IsError:          isErr,
				ErrorType:        errType,
				ReasoningContext: textBuf.String(), // Model's reasoning text this round
				UserIntent:       userMessage,
			})

			// Emit structured audit log event
			a.recordToolExec(tc.name, tc.id, duration.Milliseconds(), isErr, errType, step)

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
	id               string
	name             string
	input            json.RawMessage
	thoughtSignature string
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
	// Security Gate: Workbench Sandboxing
	if !IsToolAllowed(name, a.workbench) {
		errorMsg := fmt.Sprintf("Error: Tool %q is not authorized in this node's workbench. Allowed tools: %v", name, a.workbench)
		return errorMsg, types.ToolResultStatusError
	}

	// Built-in: todo_manage
	if name == "todo_manage" {
		result := a.handleTodoTool(input)
		return result, types.ToolResultStatusSuccess
	}

	// Built-in: tool_info
	if name == "tool_info" {
		result := a.handleToolInfo(input)
		return result, types.ToolResultStatusSuccess
	}

	// Built-in: get_skill
	if name == "get_skill" {
		result := a.handleGetSkill(input)
		return result, types.ToolResultStatusSuccess
	}

	// Built-in: scaffold_plugin
	if name == "scaffold_plugin" {
		result, status := a.handleScaffoldPlugin(input)
		return result, status
	}

	// Built-in: reload_plugins
	if name == "reload_plugins" {
		result, status := a.handleReloadPlugins(ctx)
		return result, status
	}

	// Built-in: handoff (Swarm routing)
	if name == "handoff" {
		result, status := a.handleHandoff(input)
		return result, status
	}

	// Approval gate: check if this invocation is destructive
	if req := approval.Check(name, string(input)); req != nil {
		if !a.autoApprove {
			if a.approvalFunc != nil {
				allowed := a.approvalFunc(name, req.Command, req.Reason, req.Risk.String())
				if !allowed {
					// Record the denied operation in inkwell and audit trail
					a.inkwell = append(a.inkwell, session.InkEntry{
						Timestamp:        time.Now().UTC(),
						EndTime:          time.Now().UTC(),
						Turn:             a.turn,
						ToolName:         name,
						Input:            input,
						Output:           "Operation denied by user.",
						IsError:          true,
						RequiredApproval: true,
						ApprovalDecision: "denied",
					})
					a.recordApproval(name, req.Risk.String(), "denied")
					return "Operation denied by user. The destructive command was NOT executed. Choose a safer alternative or ask the user for guidance.", types.ToolResultStatusError
				}
				// Approved: record and continue to execution below
				a.recordApproval(name, req.Risk.String(), "approved")
			} else {
				// No approval function and not auto-approve: deny by default
				a.recordApproval(name, req.Risk.String(), "denied")
				return fmt.Sprintf("Operation requires user approval (risk: %s). Reason: %s. The command was NOT executed. Use --auto-approve for automated pipelines or provide an interactive approval handler.", req.Risk, req.Reason), types.ToolResultStatusError
			}
		} else {
			// Auto-approve: record and continue
			a.recordApproval(name, req.Risk.String(), "auto_approved")
			if a.verbose {
				log.Printf("[approval] auto-approved destructive op: %s (%s)", req.Command, req.Risk)
			}
		}
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

func (a *Agent) handleHandoff(input json.RawMessage) (string, types.ToolResultStatus) {
	if a.morph == nil {
		return "Error: Morphology is not enabled. Cannot handoff.", types.ToolResultStatusError
	}

	var payload struct {
		Target       string `json:"target"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Sprintf("Error parsing handoff input: %v", err), types.ToolResultStatusError
	}

	targetNode, ok := a.morph.Nodes[payload.Target]
	if !ok {
		return fmt.Sprintf("Error: Node %q does not exist in the active morphology.", payload.Target), types.ToolResultStatusError
	}

	targetProv, ok := a.pool.GetNode(payload.Target)
	if !ok {
		return fmt.Sprintf("Error: Provider for node %q failed to initialize.", payload.Target), types.ToolResultStatusError
	}

	// Update Agent State
	a.activeNode = payload.Target
	a.provider = targetProv
	a.client = nil // Ensure we only use the provider interface, avoiding fork logic
	a.workbench = targetNode.Workbench

	// Re-render System Prompt for the new Persona
	if a.promptMgr != nil {
		promptTools := []prompt.ToolDef{}
		for _, def := range a.pluginMgr.Definitions() {
			if !IsToolAllowed(def.Name, a.workbench) {
				continue
			}
			promptTools = append(promptTools, prompt.ToolDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  prompt.SchemaToToolParams(def.InputSchema),
			})
		}

		var swarmNodes []string
		for nodeID := range a.morph.Nodes {
			if nodeID != payload.Target {
				swarmNodes = append(swarmNodes, nodeID)
			}
		}

		newSysPrompt, err := a.promptMgr.RenderSystem(a.workDir, targetNode.Model, targetNode.Provider, promptTools, swarmNodes)
		if err != nil {
			return fmt.Sprintf("Error rendering new system prompt for %q: %v", payload.Target, err), types.ToolResultStatusError
		}

		if hints := a.pluginMgr.LLMHints(); hints != "" {
			newSysPrompt += "\n\n## Additional Tool Guidance\n" + hints
		}
		// If the node has its own static prompt from the YAML, append it
		if targetNode.SystemPrompt != "" {
			newSysPrompt += "\n\n## Persona Instructions\n" + targetNode.SystemPrompt
		}
		a.systemPrompt = newSysPrompt
	}

	if a.verbose {
		log.Printf("[swarm] handoff successful: %s -> %s", a.activeNode, payload.Target)
	}

	successMsg := fmt.Sprintf("Handoff successful. You are now operating as the %q node. Instructions from caller: %s", payload.Target, payload.Instructions)
	return successMsg, types.ToolResultStatusSuccess
}

func (a *Agent) handleTodoTool(input json.RawMessage) string {
	var payload struct {
		Todos []todo.Item `json:"todos"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return fmt.Sprintf("Error parsing todo input: %v", err)
	}

	// Prevent assigning tasks to nodes that do not exist
	if a.morph != nil {
		for _, item := range payload.Todos {
			if item.Assignee != "" {
				if _, ok := a.morph.Nodes[item.Assignee]; !ok {
					var availableNodes []string
					for n := range a.morph.Nodes {
						availableNodes = append(availableNodes, n)
					}
					return fmt.Sprintf("Error: Cannot assign task to %q. Node does not exist in the active morphology. Available nodes: %s", item.Assignee, strings.Join(availableNodes, ", "))
				}
			}
		}
	}

	if err := a.todos.Replace(payload.Todos); err != nil {
		return fmt.Sprintf("Error updating todos: %v", err)
	}
	return fmt.Sprintf("Todo list updated: %s", a.todos.Summary())
}

func (a *Agent) handleToolInfo(input json.RawMessage) string {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return fmt.Sprintf("Error parsing input: %v", err)
	}

	defs := a.allToolDefs()

	// If no name specified, list all tools
	if params.Name == "" {
		var sb strings.Builder
		sb.WriteString("Available tools:\n\n")
		for _, d := range defs {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", d.Name, d.Description))
		}
		sb.WriteString(fmt.Sprintf("\nTotal: %d tools. Use tool_info with a specific name to see full parameter schema.", len(defs)))
		return sb.String()
	}

	// Look up specific tool
	for _, d := range defs {
		if d.Name == params.Name {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("## %s\n\n", d.Name))
			sb.WriteString(fmt.Sprintf("%s\n\n", d.Description))
			sb.WriteString("### Input Schema\n\n```json\n")
			// Pretty-print the schema
			var pretty json.RawMessage
			if err := json.Unmarshal(d.InputSchema, &pretty); err == nil {
				formatted, fmtErr := json.MarshalIndent(pretty, "", "  ")
				if fmtErr == nil {
					sb.Write(formatted)
				} else {
					sb.Write(d.InputSchema)
				}
			} else {
				sb.Write(d.InputSchema)
			}
			sb.WriteString("\n```\n")
			return sb.String()
		}
	}

	return fmt.Sprintf("Tool %q not found. Use tool_info without a name to list all available tools.", params.Name)
}

func (a *Agent) handleGetSkill(input json.RawMessage) string {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return fmt.Sprintf("Error parsing input: %v", err)
	}

	registry := a.pluginMgr.Skills()

	// If no name, list all skills
	if params.Name == "" {
		allSkills := registry.List()
		if len(allSkills) == 0 {
			return "No skills are currently registered. Skills are shipped by plugins as embedded knowledge, workflows, and guidance."
		}

		var sb strings.Builder
		sb.WriteString("Available skills:\n\n")
		for _, s := range allSkills {
			sb.WriteString(fmt.Sprintf("- **%s** (from %s v%s) [%s]\n  Trigger: `%s` | Type: %s | ~%d tokens\n",
				s.Skill.Name, s.PluginName, s.PluginVer,
				s.Skill.ContentType, s.Skill.Trigger,
				s.Skill.ContentType, s.TokenCost))
		}
		sb.WriteString("\nUse get_skill with a name to retrieve the full content.")
		return sb.String()
	}

	// Look up specific skill
	skill, ok := registry.GetByName(params.Name)
	if !ok {
		return fmt.Sprintf("Skill %q not found. Use get_skill without a name to list available skills.", params.Name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Skill: %s\n\n", skill.Skill.Name))
	sb.WriteString(fmt.Sprintf("**Plugin:** %s v%s\n", skill.PluginName, skill.PluginVer))
	sb.WriteString(fmt.Sprintf("**Type:** %s\n", skill.Skill.ContentType))
	sb.WriteString(fmt.Sprintf("**Trigger:** `%s`\n\n", skill.Skill.Trigger))
	sb.WriteString("---\n\n")
	sb.WriteString(skill.Skill.Content)
	return sb.String()
}

func (a *Agent) handleScaffoldPlugin(input json.RawMessage) (string, types.ToolResultStatus) {
	var params struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Params      []struct {
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Required    bool     `json:"required"`
			EnumValues  []string `json:"enum_values,omitempty"`
		} `json:"params"`
		LLMHint              string `json:"llm_hint,omitempty"`
		RequiresConfirmation bool   `json:"requires_confirmation,omitempty"`
		MaxTimeoutSeconds    int    `json:"max_timeout_seconds,omitempty"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return fmt.Sprintf("Error parsing scaffold input: %v", err), types.ToolResultStatusError
	}

	// Build scaffold spec
	spec := scaffold.Spec{
		ToolName:    params.Title,
		Description: params.Description,
		LLMHint:     params.LLMHint,
		Capabilities: scaffold.CapSpec{
			RequiresConfirmation: params.RequiresConfirmation,
			MaxTimeoutSeconds:    params.MaxTimeoutSeconds,
			SupportsCancellation: true,
		},
	}
	for _, p := range params.Params {
		spec.Params = append(spec.Params, scaffold.ParamSpec{
			Name:        p.Name,
			Type:        p.Type,
			Description: p.Description,
			Required:    p.Required,
			EnumValues:  p.EnumValues,
		})
	}

	// Generate the scaffold
	result, err := scaffold.Generate(spec, a.workDir)
	if err != nil {
		return fmt.Sprintf("Scaffold generation failed: %v", err), types.ToolResultStatusError
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Plugin stub generated successfully.\n\n"))
	sb.WriteString(fmt.Sprintf("**Binary name:** %s\n", result.BinaryName))
	sb.WriteString(fmt.Sprintf("**Source directory:** %s\n", result.Dir))
	sb.WriteString(fmt.Sprintf("**Main file:** %s\n\n", result.MainGo))
	sb.WriteString("Next steps:\n")
	sb.WriteString(fmt.Sprintf("1. Review the generated code at %s\n", result.MainGo))
	sb.WriteString("2. Implement the Execute() function body\n")
	sb.WriteString(fmt.Sprintf("3. Build: `cd %s && go build -o %s .`\n", result.Dir, result.BinaryName))
	sb.WriteString("4. Install the binary to the plugin directory\n")
	sb.WriteString("5. Call reload_plugins to discover the new tool\n")

	return sb.String(), types.ToolResultStatusSuccess
}

// effectiveSystemPrompt returns the system prompt with any skill injections appended.
func (a *Agent) effectiveSystemPrompt() string {
	prompt := a.systemPrompt

	// Skill injection (knowledge/workflows based on context)
	if a.pluginMgr != nil && a.pluginMgr.Skills() != nil {
		skillCtx := a.buildSkillContext()
		matched := a.pluginMgr.Skills().Evaluate(skillCtx)
		if len(matched) > 0 {
			skillContent := a.pluginMgr.Skills().Render(matched)
			if skillContent != "" {
				prompt += skillContent
			}
		}
	}

	return prompt
}

// buildSkillContext assembles the current session state for skill trigger evaluation.
func (a *Agent) buildSkillContext() skills.Context {
	ctx := skills.Context{
		IsFirstTurn: a.turn <= 1,
		TurnNumber:  a.turn,
	}

	// Look at recent Inkwell entries (last 10)
	lookback := 10
	start := len(a.inkwell) - lookback
	if start < 0 {
		start = 0
	}
	recent := a.inkwell[start:]

	toolSet := make(map[string]bool)
	fileSet := make(map[string]bool)

	for _, entry := range recent {
		toolSet[entry.ToolName] = true

		if entry.IsError {
			ctx.RecentErrors = append(ctx.RecentErrors, entry.ErrorType)
			// Detect language from error classification
			ce := inkwell.Classify(entry.ToolName, entry.Output, true)
			if ce.Language != "" {
				ctx.DetectedLang = ce.Language
			}
			if ce.File != "" {
				fileSet[ce.File] = true
			}
		}

		// Extract file paths from tool inputs
		var inputMap map[string]interface{}
		if err := json.Unmarshal(entry.Input, &inputMap); err == nil {
			if path, ok := inputMap["path"].(string); ok {
				fileSet[path] = true
			}
		}
	}

	for tool := range toolSet {
		ctx.RecentTools = append(ctx.RecentTools, tool)
	}
	for file := range fileSet {
		ctx.RecentFiles = append(ctx.RecentFiles, file)
	}

	// Check if looping
	diag := inkwell.Diagnose(a.inkwell, lookback)
	ctx.IsLooping = diag.IsLooping

	return ctx
}

// BuiltinToolDefs returns the bedrock.ToolDefinition for all builtin tools.
// If morph is not nil, it includes the handoff tool.
func BuiltinToolDefs(morph *swarm.Morphology) []bedrock.ToolDefinition {
	builtins := []bedrock.ToolDefinition{
		todoToolDefinition(),
		toolInfoDefinition(),
		getSkillDefinition(),
		scaffoldPluginDefinition(),
		reloadPluginsDefinition(),
	}

	if morph != nil {
		builtins = append(builtins, HandoffToolDefinition())
	}

	return builtins
}

// allToolDefs returns combined tool definitions from plugins + built-in tools,
// filtered by the given workbench sandbox.
func allToolDefs(pluginMgr *pluginhost.Manager, workbench []string, morph *swarm.Morphology) []bedrock.ToolDefinition {
	var filtered []bedrock.ToolDefinition
	if pluginMgr != nil {
		for _, def := range pluginMgr.Definitions() {
			if IsToolAllowed(def.Name, workbench) {
				filtered = append(filtered, def)
			}
		}
	}

	for _, def := range BuiltinToolDefs(morph) {
		if IsToolAllowed(def.Name, workbench) {
			filtered = append(filtered, def)
		}
	}

	return filtered
}

// allToolDefs returns combined tool definitions from plugins + built-in tools,
// filtered by the agent's workbench sandbox.
func (a *Agent) allToolDefs() []bedrock.ToolDefinition {
	return allToolDefs(a.pluginMgr, a.workbench, a.morph)
}

func HandoffToolDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "handoff",
		Description: "Yield conversational control and delegate execution to another specialized node in the Swarm Morphology. You will go to sleep and the target node will take over processing. Use this to route work to planners, researchers, or reviewers.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"target": {
					"type": "string",
					"description": "The Node ID to handoff to (e.g. 'planner', 'reviewer')."
				},
				"instructions": {
					"type": "string",
					"description": "Explicit instructions for the target node. They will see the entire conversation history, but this specifies exactly what you need them to do right now."
				}
			},
			"required": ["target", "instructions"]
		}`),
	}
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
							"priority": {"type": "string", "enum": ["high", "medium", "low"], "description": "Priority level"},
							"assignee": {"type": "string", "description": "(Swarm) The Node ID to assign this task to for execution."},
							"async": {"type": "boolean", "description": "(Swarm) If true, delegating to the assignee does not block the orchestrator."}
						},
						"required": ["content", "status", "priority"]
					}
				}
			},
			"required": ["todos"]
		}`),
	}
}

func toolInfoDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "tool_info",
		Description: "Introspect available tools. Call with no name to list all tools, or with a specific tool name to see its full parameter schema and usage details.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Name of the tool to get details for. Leave empty to list all available tools."
				}
			}
		}`),
	}
}

func getSkillDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "get_skill",
		Description: "Retrieve a skill, workflow, or knowledge document by name. Skills provide domain-specific guidance, step-by-step procedures, and best practices. Call with no name to list available skills.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Name of the skill to retrieve. Leave empty to list all available skills with their triggers."
				}
			}
		}`),
	}
}

func scaffoldPluginDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "scaffold_plugin",
		Description: "Generate a new Cuttlebone plugin stub. Produces a buildable Go module with typed input struct, auto-derived JSON Schema, and a stub Execute() ready for implementation. The generated plugin can be built and installed in the plugin directory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {
					"type": "string",
					"description": "Tool name using snake_case (e.g. 'json_query'). Will become cuttlebone-<name> binary."
				},
				"description": {
					"type": "string",
					"description": "One-line description of what the tool does (shown to the LLM)"
				},
				"params": {
					"type": "array",
					"description": "Input parameters for the tool",
					"items": {
						"type": "object",
						"properties": {
							"name": {"type": "string", "description": "Parameter name in snake_case"},
							"type": {"type": "string", "enum": ["string", "integer", "boolean", "string_array"], "description": "Parameter type"},
							"description": {"type": "string", "description": "Human-readable description of the parameter"},
							"required": {"type": "boolean", "description": "Whether this parameter is required"},
							"enum_values": {"type": "array", "items": {"type": "string"}, "description": "Optional: valid values for enum parameters"}
						},
						"required": ["name", "type", "description"]
					}
				},
				"llm_hint": {
					"type": "string",
					"description": "Optional guidance for the LLM about when and how to use this tool"
				},
				"requires_confirmation": {
					"type": "boolean",
					"description": "Whether the tool performs destructive operations requiring user confirmation"
				},
				"max_timeout_seconds": {
					"type": "integer",
					"description": "Maximum execution timeout in seconds (default 60)"
				}
			},
			"required": ["title", "description", "params"]
		}`),
	}
}

func reloadPluginsDefinition() bedrock.ToolDefinition {
	return bedrock.ToolDefinition{
		Name:        "reload_plugins",
		Description: "Re-scan the plugin directory and load any new or updated plugins. Use after scaffold_plugin has generated and built a new tool, or after manually installing a plugin binary.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func (a *Agent) handleReloadPlugins(ctx context.Context) (string, types.ToolResultStatus) {
	if a.pluginDir == "" {
		return "Plugin directory not configured. Cannot reload.", types.ToolResultStatusError
	}

	beforeCount := a.pluginMgr.Count()
	beforeNames := a.pluginMgr.PluginNames()

	// Discover new plugins (existing ones are already loaded and won't be duplicated
	// because LoadPlugin checks if the name already exists via the plugins map)
	if err := a.pluginMgr.DiscoverPlugins(ctx, a.pluginDir); err != nil {
		return fmt.Sprintf("Error reloading plugins: %v", err), types.ToolResultStatusError
	}

	afterCount := a.pluginMgr.Count()
	afterNames := a.pluginMgr.PluginNames()

	// Find newly discovered plugins
	beforeSet := make(map[string]bool, len(beforeNames))
	for _, n := range beforeNames {
		beforeSet[n] = true
	}
	var newPlugins []string
	for _, n := range afterNames {
		if !beforeSet[n] {
			newPlugins = append(newPlugins, n)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Plugin reload complete. %d plugins loaded (was %d).\n", afterCount, beforeCount))
	if len(newPlugins) > 0 {
		sb.WriteString(fmt.Sprintf("New plugins discovered: %s\n", strings.Join(newPlugins, ", ")))
	} else {
		sb.WriteString("No new plugins found.\n")
	}

	return sb.String(), types.ToolResultStatusSuccess
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

	// If using the provider interface (Ollama, etc.), also restore into provHistory.
	// Use the direct provider deserialization to avoid lossy Bedrock SDK round-trip.
	if a.provider != nil && len(state.Messages) > 0 {
		a.provHistory = session.UnmarshalProviderHistory(state.Messages)
	}

	// Restore todos
	if len(state.Todos) > 0 {
		a.todos.Replace(state.Todos)
	}

	// Restore inkwell
	a.inkwell = state.Inkwell

	// Restore audit trail (accumulate on resumed sessions)
	a.auditTrail = state.Audit
	// Keep original session start time; update model if changed
	if a.auditTrail.ModelID == "" {
		if a.client != nil {
			a.auditTrail.ModelID = a.client.ModelID()
		} else if a.provider != nil {
			a.auditTrail.ModelID = a.provider.ID()
		}
	}

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
		Audit:    a.AuditTrail(),
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
	// For provider-based sessions
	if a.provider != nil {
		return a.generateTitleProvider(ctx)
	}

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

// ActiveNode returns the currently active Swarm node ID.
func (a *Agent) ActiveNode() string {
	return a.activeNode
}

// SetActiveNode overrides the currently active Swarm node ID.
func (a *Agent) SetActiveNode(nodeID string) {
	a.activeNode = nodeID
}

// --- Audit trail helpers ---

// recordTokenUsage accumulates token usage and emits a structured log event.
func (a *Agent) recordTokenUsage(inputTokens, outputTokens, cacheRead, cacheWrite int32, step int) {
	a.auditTrail.TotalInputTokens += int64(inputTokens)
	a.auditTrail.TotalOutputTokens += int64(outputTokens)
	a.auditTrail.TotalCacheReadTokens += int64(cacheRead)
	a.auditTrail.TotalCacheWriteTokens += int64(cacheWrite)

	modelID := ""
	if a.client != nil {
		modelID = a.client.ModelID()
	} else if a.provider != nil {
		modelID = a.provider.ID()
	}

	if a.auditLogger != nil {
		a.auditLogger.TokenUsage(a.sessionID, modelID, inputTokens, outputTokens, cacheRead, cacheWrite, a.turn, step)
	}

	if a.dispatcher != nil {
		a.dispatcher.Dispatch(swarm.TokenUsageMsg{
			Assignee:              a.activeNode,
			InputTokens:           inputTokens,
			OutputTokens:          outputTokens,
			CacheReadInputTokens:  cacheRead,
			CacheWriteInputTokens: cacheWrite,
		})
	}
}

// recordToolExec emits a structured tool execution event and updates audit timing.
func (a *Agent) recordToolExec(toolName, toolUseID string, durationMs int64, isError bool, errorType string, step int) {
	now := time.Now().UTC()
	if a.auditTrail.FirstToolCallAt == nil {
		a.auditTrail.FirstToolCallAt = &now
	}
	a.auditTrail.LastToolCallAt = &now

	if a.auditLogger != nil {
		a.auditLogger.ToolExec(a.sessionID, toolName, toolUseID, durationMs, isError, errorType, a.turn, step)
	}
}

// recordApproval records a destructive operation approval decision.
func (a *Agent) recordApproval(toolName, risk, decision string) {
	a.auditTrail.DestructiveOpsAttempted++
	switch decision {
	case "approved", "auto_approved":
		a.auditTrail.DestructiveOpsApproved++
	case "denied":
		a.auditTrail.DestructiveOpsDenied++
	}

	if a.auditLogger != nil {
		a.auditLogger.ApprovalEvent(a.sessionID, toolName, risk, decision, a.turn)
	}
}

// AuditTrail returns the current audit trail (for session persistence).
func (a *Agent) AuditTrail() session.AuditTrail {
	trail := a.auditTrail
	trail.WallClockMs = time.Since(a.auditTrail.SessionStartAt).Milliseconds()
	return trail
}
