package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/approval"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/compact"
	"github.com/codecuttle/codecuttlectl/internal/conversation"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	bedrockprov "github.com/codecuttle/codecuttlectl/internal/provider/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// Config holds the configuration needed to create the TUI app.
type Config struct {
	Client         *bedrock.Client
	Provider       provider.Provider  // Provider interface (used when Client is nil)
	PluginMgr      *pluginhost.Manager
	System         string // Rendered system prompt
	WorkDir        string
	Verbose        bool
	EnableThinking bool // Enable extended thinking (model must support it)
	ThinkingBudget int  // Token budget for thinking (default: 16000)
	AutoApprove    bool // When true, skip destructive operation confirmations

	// Session persistence
	Store     session.Store
	SessionID string // If set, resume this session
}

// Model is the main Bubble Tea application model.
type Model struct {
	// Sub-components
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	// Configuration
	client         *bedrock.Client
	llmProvider    provider.Provider // Provider interface (non-nil for Ollama, etc.)
	pluginMgr      *pluginhost.Manager
	system         string
	workDir        string
	enableThinking bool
	thinkingBudget int

	// Conversation state
	history   []types.Message
	messages  []chatMessage
	streaming bool
	streamBuf *strings.Builder
	streamCh  <-chan provider.StreamEvent // Active stream channel (provider-agnostic)
	streamStep int // Counts consecutive tool-use rounds in this turn (for grounding)
	stateDict  *stateDict // State dictionary for small-model grounding (nil for large models)
	lastExecutedTools []pendingTool // Tools from the last execution (for state dict tracking)

	// Interrupt state
	interruptPending bool // true when user pressed esc once, waiting for confirmation

	// Reasoning/thinking state
	reasoningBuf       *strings.Builder
	reasoningSignature string
	inReasoning        bool
	showThinking       bool // Toggle: show/hide thinking (default: true)

	// Current tool call accumulation
	currentToolID    string
	currentToolName  string
	currentToolInput *strings.Builder
	pendingToolCalls []pendingTool

	// Active tool execution streaming state
	activeToolName   string          // Name of currently executing tool
	activeToolOutput *strings.Builder // Rolling output buffer for live preview

	// Approval gate state
	approvalPending  *approval.Request // Non-nil when awaiting user approval
	approvalTool     *pendingTool      // The tool awaiting approval
	approvalRemaining []pendingTool    // Tools queued behind the approval gate
	autoApprove      bool              // When true, skip confirmation prompts

	// Todo state
	todos        *todo.List
	todoExpanded bool

	// Session persistence
	store     session.Store
	sessionID string

	// Stats
	totalInputTokens          int32
	totalOutputTokens         int32
	totalCacheReadInputTokens int32
	totalCacheWriteInputTokens int32

	// Per-call stats (most recent API call — used for context window %)
	lastCallInputTokens          int32
	lastCallCacheReadInputTokens int32
	lastCallCacheWriteInputTokens int32

	// Context window size for this provider (tokens)
	contextWindow int32

	spinnerColorIdx   int
	spinnerTickCount  int

	// Layout
	width  int
	height int
	ready  bool

	// Mouse mode toggle: when true, mouse is captured (scroll works).
	// When false, terminal handles mouse natively (text selection works).
	mouseEnabled bool

	// Markdown renderer
	mdRenderer *glamour.TermRenderer

	// Cache keepalive: track when the last API call was made so we can
	// decide whether a keepalive ping is needed. Pings fire every 4 minutes
	// but only send a request if we've been idle > 4 minutes since the last call.
	lastAPICallTime time.Time
}

type chatMessage struct {
	role    string // "user", "assistant", "reasoning", "tool_call", "tool_result"
	content string
	name    string
	isError bool
}

// contextWindowSize is the maximum input context window for Claude Opus 4.6 on Bedrock (1M tokens).
// This went GA in March 2026 — no beta header required.
// Used as default; overridden by provider-specific context window when available.
const defaultContextWindowSize = 1_000_000

// cacheKeepaliveInterval is how often the keepalive ticker fires. Set to 4 minutes
// to stay well within the 5-minute Bedrock cache TTL. If no real API call has been
// made in this interval, a minimal PingCache request refreshes the TTL.
const cacheKeepaliveInterval = 4 * time.Minute

type pendingTool struct {
	id    string
	name  string
	input json.RawMessage
}

// New creates a new TUI Model.
func New(cfg Config) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Prompt = "" // Remove the vertical bar cursor prompt
	ta.ShowLineNumbers = false

	// Dynamic height: starts at 1 row, grows up to 10 as user types multi-line
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	ta.SetHeight(1)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = SpinnerStyle

	thinkingBudget := cfg.ThinkingBudget
	if thinkingBudget <= 0 {
		thinkingBudget = 16000
	}

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(0), // Will be re-created on WindowSizeMsg with actual width
	)

	m := Model{
		input:            ta,
		spinner:          sp,
		client:           cfg.Client,
		llmProvider:      cfg.Provider,
		pluginMgr:        cfg.PluginMgr,
		system:           cfg.System,
		workDir:          cfg.WorkDir,
		enableThinking:   cfg.EnableThinking,
		thinkingBudget:   thinkingBudget,
		todos:            todo.NewList(),
		messages:         []chatMessage{},
		streamBuf:        &strings.Builder{},
		reasoningBuf:     &strings.Builder{},
		mouseEnabled:     true,
		currentToolInput: &strings.Builder{},
		activeToolOutput: &strings.Builder{},
		autoApprove:      cfg.AutoApprove,
		showThinking:     true,
		mdRenderer:       renderer,
		store:            cfg.Store,
		sessionID:        cfg.SessionID,
	}

	// If no bedrock client but we have a provider, create a wrapper for provider-aware streaming
	if m.llmProvider == nil && m.client != nil {
		m.llmProvider = bedrockprov.New(m.client)
	}

	// Discover context window size from provider
	if m.llmProvider != nil {
		if cwp, ok := m.llmProvider.(provider.ContextWindowProvider); ok {
			if cw := cwp.ContextWindow(); cw > 0 {
				m.contextWindow = cw
			}
		}
	}

	// If resuming a session, restore conversation history
	if cfg.Store != nil && cfg.SessionID != "" {
		m.restoreSession()
	} else if cfg.Store != nil {
		// Create a new session
		modelName := ""
		region := ""
		if cfg.Client != nil {
			modelName = cfg.Client.ModelID()
			region = cfg.Client.Region()
		} else if cfg.Provider != nil {
			modelName = cfg.Provider.ID()
		}
		meta := session.SessionMeta{
			Model:   modelName,
			Region:  region,
			WorkDir: cfg.WorkDir,
		}
		id, err := cfg.Store.Create(meta)
		if err == nil {
			m.sessionID = id
		}
	}

	return m
}

// SessionID returns the current session ID for display on exit.
func (m Model) SessionID() string {
	return m.sessionID
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, textarea.Blink, m.cacheKeepaliveTick())
}

// cacheKeepaliveTick returns a Cmd that fires CacheKeepaliveTickMsg after the keepalive interval.
func (m *Model) cacheKeepaliveTick() tea.Cmd {
	return tea.Tick(cacheKeepaliveInterval, func(t time.Time) tea.Msg {
		return CacheKeepaliveTickMsg{}
	})
}

// spinnerGradient is a smooth color transition: honey → green → teal → green → honey
// Each step is a small hue shift for seamless blending.
var spinnerGradient = []string{
	// honey → green
	"#f4a261", "#e0a964", "#ccb068", "#b5b56b", "#9ab96f",
	"#7fbc72", "#66bd76", "#52b788",
	// green → teal
	"#52b788", "#50b990", "#4ebb99", "#4cbda1", "#4abfaa",
	"#48c1b3", "#48c4bc", "#48c6c5", "#48cae4",
	// teal → green
	"#48cae4", "#48c6c5", "#48c4bc", "#48c1b3", "#4abfaa",
	"#4cbda1", "#4ebb99", "#50b990", "#52b788",
	// green → honey
	"#52b788", "#66bd76", "#7fbc72", "#9ab96f", "#b5b56b",
	"#ccb068", "#e0a964", "#f4a261",
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		m.ready = true
		// Render any pre-existing messages (e.g., restored from a session)
		if len(m.messages) > 0 {
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+t":
			m.todoExpanded = !m.todoExpanded
			m.recalcLayout()
			return m, nil
		case "ctrl+r":
			m.showThinking = !m.showThinking
			m.viewport.SetContent(m.renderMessages())
			return m, nil
		case "ctrl+m":
			// Toggle mouse mode: on = scroll wheel works, off = text selection works
			m.mouseEnabled = !m.mouseEnabled
			return m, nil
		case "y", "Y":
			// Handle approval confirmation
			if m.approvalPending != nil {
				req := m.approvalPending
				return m, func() tea.Msg {
					return ApprovalDecisionMsg{
						ToolUseID: req.ToolUseID,
						Approved:  true,
					}
				}
			}
		case "n", "N":
			// Handle approval denial
			if m.approvalPending != nil {
				req := m.approvalPending
				return m, func() tea.Msg {
					return ApprovalDecisionMsg{
						ToolUseID: req.ToolUseID,
						Approved:  false,
					}
				}
			}
		case "shift+enter", "alt+enter", "ctrl+j":
			// Insert a newline in the textarea (multi-line input)
			if !m.streaming {
				var cmd tea.Cmd
				// Forward an enter keypress to textarea (it handles newline insertion)
				enterMsg := tea.KeyPressMsg{Code: tea.KeyEnter}
				m.input, cmd = m.input.Update(enterMsg)
				m.recalcLayout()
				return m, cmd
			}
			return m, nil
		case "enter":
			if !m.streaming {
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.input.Reset()
					m.input.SetHeight(1)
					m.recalcLayout()
					return m, m.submitMessage(text)
				}
			}
			return m, nil
		case "esc":
			if m.streaming {
				if m.interruptPending {
					// Second esc: confirm interrupt — stop the stream
					m.streaming = false
					m.streamCh = nil
					m.interruptPending = false
					// Finalize any partial content as the response
					if m.streamBuf.Len() > 0 {
						content := m.streamBuf.String()
						m.messages = append(m.messages, chatMessage{
							role:    "assistant",
							content: sanitizeModelText(content) + "\n\n*(interrupted)*",
						})
						m.history = append(m.history, bedrock.BuildAssistantMessage(
							[]types.ContentBlock{
								&types.ContentBlockMemberText{Value: content},
							},
						))
						m.streamBuf.Reset()
					} else if m.reasoningBuf.Len() > 0 {
						m.messages = append(m.messages, chatMessage{
							role:    "reasoning",
							content: m.reasoningBuf.String(),
						})
						m.reasoningBuf.Reset()
					}
					m.inReasoning = false
					m.pendingToolCalls = nil
					m.messages = append(m.messages, chatMessage{
						role:    "assistant",
						content: "*(generation interrupted by user)*",
					})
					m.saveSession()
					m.viewport.SetContent(m.renderMessages())
					m.viewport.GotoBottom()
					return m, nil
				}
				// First esc: show confirmation prompt
				m.interruptPending = true
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()
				return m, nil
			}
			// Not streaming: clear interrupt state, handle other esc uses
			m.interruptPending = false
			if m.todoExpanded {
				m.todoExpanded = false
				m.recalcLayout()
				return m, nil
			}
		}

	// --- Stream event handling ---

	case StreamTextMsg:
		// Clear interrupt pending on new content (user didn't confirm)
		m.interruptPending = false
		// If we were in reasoning, finalize it before text starts
		if m.inReasoning && m.reasoningBuf.Len() > 0 {
			m.messages = append(m.messages, chatMessage{
				role:    "reasoning",
				content: m.reasoningBuf.String(),
			})
			m.reasoningBuf.Reset()
			m.inReasoning = false
		}
		m.streamBuf.WriteString(msg.Text)
		// Throttle viewport updates during streaming: only re-render when
		// we receive a newline (paragraph boundary) or every 200 bytes.
		// This prevents layout thrashing from partial markdown re-interpretation.
		if strings.HasSuffix(msg.Text, "\n") || m.streamBuf.Len()%200 < len(msg.Text) {
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
		}
		return m, m.readNextStreamEvent()

	case StreamReasoningMsg:
		m.inReasoning = true
		m.reasoningBuf.WriteString(msg.Text)
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.readNextStreamEvent()

	case StreamReasoningDoneMsg:
		m.reasoningSignature = msg.Signature
		// Finalize reasoning block
		if m.reasoningBuf.Len() > 0 {
			m.messages = append(m.messages, chatMessage{
				role:    "reasoning",
				content: m.reasoningBuf.String(),
			})
			m.reasoningBuf.Reset()
		}
		m.inReasoning = false
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.readNextStreamEvent()

	case StreamToolStartMsg:
		// Text emitted before a tool call is the model's internal reasoning
		// about what to do — treat it as thinking, not as a response
		if m.streamBuf.Len() > 0 {
			cleaned := sanitizeModelText(m.streamBuf.String())
			if strings.TrimSpace(cleaned) != "" {
				m.messages = append(m.messages, chatMessage{
					role:    "reasoning",
					content: strings.TrimSpace(cleaned),
				})
			}
			m.streamBuf.Reset()
		}
		m.currentToolID = msg.ToolUseID
		m.currentToolName = msg.Name
		m.currentToolInput.Reset()
		m.messages = append(m.messages, chatMessage{
			role:    "tool_call",
			content: fmt.Sprintf("Calling %s...", msg.Name),
			name:    msg.Name,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.readNextStreamEvent()

	case StreamToolInputMsg:
		m.currentToolInput.WriteString(msg.Delta)
		return m, m.readNextStreamEvent()

	case StreamToolStopMsg:
		if m.currentToolName != "" {
			input := bedrock.CollectToolInput([]string{m.currentToolInput.String()})
			m.pendingToolCalls = append(m.pendingToolCalls, pendingTool{
				id:    m.currentToolID,
				name:  m.currentToolName,
				input: input,
			})
			m.currentToolName = ""
			m.currentToolID = ""
			m.currentToolInput.Reset()
		}
		return m, m.readNextStreamEvent()

	case StreamDoneMsg:
		// If we have pending tool calls, build a single assistant message
		// containing any text AND the tool_use blocks, then execute tools.
		if len(m.pendingToolCalls) > 0 {
			var blocks []types.ContentBlock
			// Include any text that was streamed before/between tool calls
			if m.streamBuf.Len() > 0 {
				// Try to extract a plan from the model's text for the task panel
				m.maybeExtractPlan(m.streamBuf.String())
				blocks = append(blocks, &types.ContentBlockMemberText{Value: m.streamBuf.String()})
				m.streamBuf.Reset()
			}
			for _, tc := range m.pendingToolCalls {
				blocks = append(blocks, &types.ContentBlockMemberToolUse{
					Value: types.ToolUseBlock{
						ToolUseId: &tc.id,
						Name:      &tc.name,
						Input:     document.NewLazyDocument(jsonToMap(tc.input)),
					},
				})
			}
			m.history = append(m.history, bedrock.BuildAssistantMessage(blocks))
			m.lastExecutedTools = m.pendingToolCalls // Save for state dict tracking
			return m, m.executePendingTools()
		}

		// No tool calls — finalize streamed text as the assistant response
		if m.streamBuf.Len() > 0 {
			content := m.streamBuf.String()
			// Try to extract a plan from the model's final response
			m.maybeExtractPlan(content)
			m.messages = append(m.messages, chatMessage{
				role:    "assistant",
				content: sanitizeModelText(content),
			})
			m.history = append(m.history, bedrock.BuildAssistantMessage(
				[]types.ContentBlock{
					&types.ContentBlockMemberText{Value: content},
				},
			))
			m.streamBuf.Reset()
		}

		m.streaming = false
		m.streamCh = nil
		m.saveSession()
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case StreamUsageMsg:
		m.totalInputTokens += msg.InputTokens
		m.totalOutputTokens += msg.OutputTokens
		m.totalCacheReadInputTokens += msg.CacheReadInputTokens
		m.totalCacheWriteInputTokens += msg.CacheWriteInputTokens
		// Track per-call stats for context window % calculation
		m.lastCallInputTokens = msg.InputTokens
		m.lastCallCacheReadInputTokens = msg.CacheReadInputTokens
		m.lastCallCacheWriteInputTokens = msg.CacheWriteInputTokens
		// Usage arrives after MessageStop. Continue reading for the channel close
		// which will emit StreamDoneMsg to finalize the turn.
		m.viewport.SetContent(m.renderMessages())
		return m, m.readNextStreamEvent()

	case StreamErrorMsg:
		m.streaming = false
		m.streamCh = nil
		m.messages = append(m.messages, chatMessage{
			role:    "assistant",
			content: fmt.Sprintf("Error: %v", msg.Err),
			isError: true,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case ToolOutputDeltaMsg:
		// Live tool output streaming — update the active tool preview
		m.activeToolName = msg.Name
		m.activeToolOutput.WriteString(msg.Delta)
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case ToolExecResultMsg:
		// Tool finished — clear active preview, show final result
		m.activeToolName = ""
		m.activeToolOutput.Reset()
		m.messages = append(m.messages, chatMessage{
			role:    "tool_result",
			content: truncateToolResult(msg.Output, 500),
			name:    msg.Name,
			isError: msg.IsError,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case ApprovalRequestMsg:
		// A tool requires user confirmation before execution.
		// Show the approval prompt and pause tool execution.
		m.approvalPending = &approval.Request{
			ToolName:  msg.ToolName,
			ToolUseID: msg.ToolUseID,
			Command:   msg.Command,
			Reason:    msg.Reason,
		}
		// Store the tool awaiting approval
		m.approvalTool = &pendingTool{
			id:    msg.ToolUseID,
			name:  msg.ToolName,
			input: msg.Input,
		}
		// Convert remaining tools back to pendingTool for later execution
		m.approvalRemaining = nil
		for _, rt := range msg.RemainingTools {
			m.approvalRemaining = append(m.approvalRemaining, pendingTool{
				id:    rt.ID,
				name:  rt.Name,
				input: rt.Input,
			})
		}
		m.messages = append(m.messages, chatMessage{
			role:    "tool_call",
			content: fmt.Sprintf("⚠️  APPROVAL REQUIRED (%s risk)\n   Tool: %s\n   Command: %s\n   Reason: %s\n\n   Press 'y' to approve, 'n' to deny", msg.Risk, msg.ToolName, msg.Command, msg.Reason),
			name:    msg.ToolName,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case ApprovalDecisionMsg:
		// User made a decision on the pending approval.
		m.approvalPending = nil
		if msg.Approved {
			// Execute the approved tool
			m.messages = append(m.messages, chatMessage{
				role:    "tool_result",
				content: "✓ Approved — executing...",
			})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			tool := m.approvalTool
			m.approvalTool = nil
			remaining := m.approvalRemaining
			m.approvalRemaining = nil
			return m, m.executeApprovedTool(tool, remaining)
		}
		// Denied — return a tool error to the model
		m.messages = append(m.messages, chatMessage{
			role:    "tool_result",
			content: "✗ Denied by user — operation was not executed",
			isError: true,
		})
		tool := m.approvalTool
		m.approvalTool = nil
		remaining := m.approvalRemaining
		m.approvalRemaining = nil
		// Build result for the denied tool and execute remaining tools normally
		return m, m.executeDeniedToolAndRemaining(tool, remaining)


	case ContinueStreamMsg:
		// Process any todo updates safely in the Update handler (single-threaded)
		todoIdx := 0
		for i := range msg.Messages {
			if msg.Messages[i].Content == "" && todoIdx < len(msg.TodoInputs) {
				// This is a todo_manage placeholder — apply it now
				var payload struct {
					Todos []todo.Item `json:"todos"`
				}
				input := msg.TodoInputs[todoIdx]
				todoIdx++
				if err := json.Unmarshal(input, &payload); err != nil {
					msg.Messages[i].Content = fmt.Sprintf("Error parsing todo input: %v", err)
				} else if err := m.todos.Replace(payload.Todos); err != nil {
					msg.Messages[i].Content = fmt.Sprintf("Error updating todos: %v", err)
				} else {
					msg.Messages[i].Content = fmt.Sprintf("Todo list updated: %s", m.todos.Summary())
				}
			}
		}

		// Display tool results in the chat
		for _, r := range msg.Messages {
			isErr := r.Status == types.ToolResultStatusError
			m.messages = append(m.messages, chatMessage{
				role:    "tool_result",
				content: truncateToolResult(r.Content, 500),
				isError: isErr,
			})
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		// Add tool results to history and start new stream
		m.history = append(m.history, bedrock.BuildToolResultMessage(msg.Messages))
		m.saveSession()
		m.streamStep++ // Track consecutive tool-use rounds for grounding

		// Update state dictionary with tool execution results
		if m.stateDict != nil {
			for _, r := range msg.Messages {
				// Find the matching pending tool call to get the name/input
				for _, tc := range m.lastExecutedTools {
					if tc.id == r.ToolUseID {
						m.stateDict.recordToolResult(tc.name, tc.input, r.Status == types.ToolResultStatusError)
						break
					}
				}
			}
		}

		return m, m.launchStream()

	case TodoUpdatedMsg:
		m.todos.Replace(msg.Items)
		return m, nil

	case CacheKeepaliveTickMsg:
		// Only ping if we have history (something worth caching) and we're
		// not currently streaming (a stream already refreshes the cache).
		if !m.streaming && len(m.history) > 0 {
			elapsed := time.Since(m.lastAPICallTime)
			if elapsed >= cacheKeepaliveInterval {
				// Time to refresh — fire a ping in the background.
				client := m.client
				system := m.system
				history := m.history
				tools := m.pluginMgr.Definitions()
				cmd := func() tea.Msg {
					err := client.PingCache(context.Background(), system, history, tools)
					return CacheKeepaliveDoneMsg{Err: err}
				}
				return m, tea.Batch(cmd, m.cacheKeepaliveTick())
			}
		}
		// Reschedule the next tick regardless.
		return m, m.cacheKeepaliveTick()

	case CacheKeepaliveDoneMsg:
		// Keepalive completed. If it succeeded, update lastAPICallTime so we
		// don't send another ping until the next interval elapses.
		if msg.Err == nil {
			m.lastAPICallTime = time.Now()
		}
		// Errors are silently ignored — the worst case is a cache miss on the
		// next real call, which isn't worth surfacing to the user.
		return m, nil

	case spinner.TickMsg:
		if m.streaming {
			// Smooth color transition: cycle every 4th tick, interpolate between colors
			m.spinnerTickCount++
			if m.spinnerTickCount%4 == 0 {
				m.spinnerColorIdx = (m.spinnerColorIdx + 1) % len(spinnerGradient)
			}
			m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(spinnerGradient[m.spinnerColorIdx]))
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Pass remaining events to sub-components
	if !m.streaming {
		var cmd tea.Cmd
		prevHeight := m.input.Height()
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)

		// If textarea height changed (dynamic grow/shrink), recalculate layout
		if m.input.Height() != prevHeight {
			m.recalcLayout()
		}
	}

	// Only pass scroll-related events to the viewport.
	// Passing all key events causes the viewport to interfere with scroll
	// position while the user is typing. Mouse events (scroll wheel) are
	// safe to forward since they don't conflict with typing.
	switch msg := msg.(type) {
	case tea.MouseMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	case tea.WindowSizeMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	case tea.KeyMsg:
		// Only pass navigation keys to viewport (not typing keys)
		switch msg.String() {
		case "pgup", "pgdown", "home", "end", "ctrl+u", "ctrl+d":
			var vpCmd tea.Cmd
			m.viewport, vpCmd = m.viewport.Update(msg)
			cmds = append(cmds, vpCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing...")
	}

	statusBar := m.renderStatusBar()
	inputArea := m.renderInput()
	todoBar := m.renderTodoBar()
	helpBar := m.renderHelpBar()

	var todoPanel string
	if m.todoExpanded {
		todoPanel = m.renderTodoPanel()
	}

	sections := []string{statusBar}

	if m.todoExpanded && todoPanel != "" {
		sections = append(sections, m.viewport.View(), todoPanel)
	} else {
		sections = append(sections, m.viewport.View())
	}

	sections = append(sections, inputArea, todoBar, helpBar)

	view := tea.NewView(strings.Join(sections, "\n"))
	view.AltScreen = true
	if m.mouseEnabled {
		// Mouse captured: scroll wheel works, shift+drag for selection
		view.MouseMode = tea.MouseModeCellMotion
	}
	// When mouseEnabled is false, MouseMode defaults to None:
	// native text selection works, use pgup/pgdown/ctrl+u/ctrl+d to scroll
	return view
}

// --- Stream management ---

// launchStream starts a new ConverseStream and reads the first event.
func (m *Model) launchStream() tea.Cmd {
	m.streaming = true
	m.streamBuf.Reset()
	m.reasoningBuf.Reset()
	m.reasoningSignature = ""
	m.inReasoning = false
	m.currentToolInput.Reset()
	m.pendingToolCalls = nil

	// Record that a real API call is happening — resets the keepalive timer.
	m.lastAPICallTime = time.Now()

	// Apply context compaction if needed: replace old verbose tool results
	// with summaries to keep the context window lean. The full content remains
	// in the session file and Inkwell (never truncated there).
	m.maybeCompact()

	ctx := context.Background()

	// Determine the effective system prompt. For smaller/local models that
	// struggle with long agentic loops, append grounding context after several
	// consecutive tool-use rounds. This prevents the model from "resetting"
	// (re-introducing itself) when the original user message gets buried under
	// large tool results in the context window.
	//
	// Only applies to models that need it (small context window ≤ 512k).
	// Large frontier models (Opus 4.6 etc.) maintain their own world state.
	//
	// We inject on EVERY step after the first 2 (not just every 3rd) because
	// smaller models lose the thread extremely quickly once large tool results
	// accumulate. The system prompt is re-sent on every API call anyway, so
	// there's no context window cost to always including it.
	effectiveSystem := m.system
	if m.streamStep >= 2 && m.stateDict != nil {
		effectiveSystem += "\n\n" + m.stateDict.render()
	}

	// Use the provider interface for streaming
	if m.llmProvider != nil {
		req := provider.Request{
			System:   effectiveSystem,
			Messages: provider.MessagesToProvider(m.history),
			Tools:    m.providerToolDefs(),
			Config: provider.InferenceConfig{
				EnableThinking: m.enableThinking,
				ThinkingBudget: m.thinkingBudget,
			},
		}
		ch := m.llmProvider.ConverseStream(ctx, req)
		m.streamCh = ch
	} else {
		// Fallback: direct Bedrock client (shouldn't happen since we wrap it in New, but defensive)
		var streamCfg bedrock.StreamConfig
		if m.enableThinking {
			streamCfg.EnableThinking = true
			streamCfg.ThinkingBudget = m.thinkingBudget
		}
		bedrockCh := m.client.ConverseStream(ctx, effectiveSystem, m.history, m.pluginMgr.Definitions(), streamCfg)
		// Wrap bedrock channel into provider channel
		provCh := make(chan provider.StreamEvent, 64)
		go func() {
			defer close(provCh)
			for ev := range bedrockCh {
				switch e := ev.(type) {
				case bedrock.TextDeltaEvent:
					provCh <- provider.TextDeltaEvent{Text: e.Text}
				case bedrock.ReasoningDeltaEvent:
					provCh <- provider.ReasoningDeltaEvent{Text: e.Text}
				case bedrock.ReasoningSignatureEvent:
					provCh <- provider.ReasoningSignatureEvent{Signature: e.Signature}
				case bedrock.ToolUseStartEvent:
					provCh <- provider.ToolUseStartEvent{ToolUseID: e.ToolUseID, Name: e.Name}
				case bedrock.ToolInputDeltaEvent:
					provCh <- provider.ToolInputDeltaEvent{Delta: e.Delta}
				case bedrock.ToolUseStopEvent:
					provCh <- provider.ToolUseStopEvent{}
				case bedrock.MessageStopEvent:
					provCh <- provider.MessageStopEvent{StopReason: e.StopReason}
				case bedrock.UsageEvent:
					provCh <- provider.UsageEvent{
						InputTokens:      e.InputTokens,
						OutputTokens:     e.OutputTokens,
						CacheReadTokens:  e.CacheReadInputTokens,
						CacheWriteTokens: e.CacheWriteInputTokens,
					}
				case bedrock.StreamErrorEvent:
					provCh <- provider.StreamErrorEvent{Err: e.Err}
				}
			}
		}()
		m.streamCh = provCh
	}

	// Return a cmd that reads the first event from the stream
	return tea.Batch(m.spinner.Tick, m.readNextStreamEvent())
}

// maybeCompact applies heuristic compaction to the conversation history.
// This replaces old verbose tool results (read_file, grep, etc.) with concise
// summaries, freeing context space while preserving enough info for the model
// to know what was there.
//
// Compaction always runs for results older than PreserveRecentTurns — there's
// no reason to keep 8k tokens of a file read from 5 turns ago when a 200-byte
// summary suffices. The MaxContextPercent threshold controls *aggressive*
// compaction (reducing PreserveRecentTurns to 1), not whether compaction
// happens at all.
//
// The full content is always preserved in the Inkwell and session file.
func (m *Model) maybeCompact() {
	cfg := compact.DefaultConfig()

	// For small models, use aggressive compaction settings — these models lose
	// the thread when large tool results accumulate in context.
	if m.needsGroundingAssist() {
		cfg = compact.SmallModelConfig()
	} else {
		// If context usage is high on large models, compact more aggressively
		lastCallTotal := m.lastCallInputTokens + m.lastCallCacheReadInputTokens + m.lastCallCacheWriteInputTokens
		if compact.ShouldCompact(lastCallTotal, m.contextWindowSize(), cfg) {
			cfg.PreserveRecentTurns = 3
		}
	}

	// Count user text messages to determine current turn
	turn := 0
	for _, msg := range m.history {
		if msg.Role == types.ConversationRoleUser {
			for _, block := range msg.Content {
				if _, ok := block.(*types.ContentBlockMemberText); ok {
					turn++
					break
				}
			}
		}
	}

	// Always compact stale results (older than PreserveRecentTurns)
	result := compact.Compact(m.history, turn, cfg)
	if result.Compacted > 0 {
		m.history = result.Messages
	}
}

// readNextStreamEvent returns a Cmd that reads the next event from the active stream.
func (m *Model) readNextStreamEvent() tea.Cmd {
	ch := m.streamCh
	if ch == nil {
		return func() tea.Msg { return StreamDoneMsg{StopReason: "no_channel"} }
	}
	return func() tea.Msg {
		for {
			event, ok := <-ch
			if !ok {
				return StreamDoneMsg{StopReason: "end_turn"}
			}
			switch e := event.(type) {
			case provider.TextDeltaEvent:
				return StreamTextMsg{Text: e.Text}
			case provider.ReasoningDeltaEvent:
				return StreamReasoningMsg{Text: e.Text}
			case provider.ReasoningSignatureEvent:
				return StreamReasoningDoneMsg{Signature: e.Signature}
			case provider.ToolUseStartEvent:
				return StreamToolStartMsg{ToolUseID: e.ToolUseID, Name: e.Name}
			case provider.ToolInputDeltaEvent:
				return StreamToolInputMsg{Delta: e.Delta}
			case provider.ToolUseStopEvent:
				return StreamToolStopMsg{}
			case provider.MessageStopEvent:
				// Don't return yet — keep reading to consume the UsageEvent
				// that may follow. StreamDoneMsg will be emitted when the
				// channel closes (handled by !ok above).
				continue
			case provider.UsageEvent:
				return StreamUsageMsg{
					InputTokens:           e.InputTokens,
					OutputTokens:          e.OutputTokens,
					CacheReadInputTokens:  e.CacheReadTokens,
					CacheWriteInputTokens: e.CacheWriteTokens,
				}
			case provider.StreamErrorEvent:
				return StreamErrorMsg{Err: e.Err}
			default:
				return StreamDoneMsg{StopReason: "unknown"}
			}
		}
	}
}

// --- Layout ---

func (m *Model) recalcLayout() {
	// Fixed height elements:
	// - Status bar: 1 line
	// - Input area: textarea height + 2 (border)
	// - Todo bar: 1 line
	// - Help bar: 1 line
	inputHeight := m.input.Height() + 2
	headerFooter := 1 + inputHeight + 1 + 1 // status + input + todo + help
	if m.todoExpanded && !m.todos.IsEmpty() {
		todoLines := min(len(m.todos.Items())+2, 8)
		headerFooter += todoLines
	}

	vpHeight := m.height - headerFooter
	if vpHeight < 3 {
		vpHeight = 3
	}

	if !m.ready {
		m.viewport = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(vpHeight))
	} else {
		oldHeight := m.viewport.Height()
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(vpHeight)

		// When the viewport shrinks (textarea grew), adjust scroll position
		// so the same content remains visible. Instead of jumping to bottom,
		// scroll down by exactly the number of lines lost — this preserves
		// the user's scroll position relative to what they were reading.
		if vpHeight < oldHeight {
			linesLost := oldHeight - vpHeight
			m.viewport.ScrollDown(linesLost)
		}
	}

	m.input.SetWidth(m.width - 4)

	// Re-create the markdown renderer with the correct word-wrap width.
	// Subtract 6 for padding/margins that glamour applies internally.
	wrapWidth := m.width - 6
	if wrapWidth < 40 {
		wrapWidth = 40
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(wrapWidth),
	)
	if err == nil {
		m.mdRenderer = renderer
	}
}

// --- Actions ---

func (m *Model) submitMessage(text string) tea.Cmd {
	if text == "/reset" {
		m.history = nil
		m.messages = nil
		m.todos = todo.NewList()
		m.viewport.SetContent("")
		return nil
	}
	if text == "/plugins" {
		names := m.pluginMgr.PluginNames()
		m.messages = append(m.messages, chatMessage{
			role:    "assistant",
			content: "Loaded plugins: " + strings.Join(names, ", "),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return nil
	}

	m.messages = append(m.messages, chatMessage{role: "user", content: text})
	m.history = append(m.history, bedrock.BuildUserTextMessage(text))
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()

	m.streamStep = 0 // Reset step counter for new user turn
	// Initialize state dictionary for small-model grounding
	if m.needsGroundingAssist() {
		m.stateDict = newStateDict(text)
	} else {
		m.stateDict = nil
	}
	return m.launchStream()
}

func (m *Model) executePendingTools() tea.Cmd {
	tools := m.pendingToolCalls
	m.pendingToolCalls = nil
	pluginMgr := m.pluginMgr
	workDir := m.workDir
	autoApprove := m.autoApprove

	return func() tea.Msg {
		var results []bedrock.ToolResult
		var todoInputs []json.RawMessage
		ctx := context.Background()

		for i, tool := range tools {
			if tool.name == "todo_manage" {
				// Defer todo mutation to the Update handler (thread-safe).
				// Put a placeholder result; the Update handler will replace it.
				todoInputs = append(todoInputs, tool.input)
				results = append(results, bedrock.ToolResult{
					ToolUseID: tool.id,
					Content:   "", // placeholder, filled in by Update
					Status:    types.ToolResultStatusSuccess,
				})
				continue
			}

			// Check if this tool invocation requires user approval
			if req := approval.Check(tool.name, string(tool.input)); req != nil {
				req.ToolUseID = tool.id

				if autoApprove {
					// Auto-approve mode: proceed without prompting
				} else {
					// Build remaining tools list
					var remaining []pendingToolForApproval
					for _, rt := range tools[i+1:] {
						remaining = append(remaining, pendingToolForApproval{
							ID:    rt.id,
							Name:  rt.name,
							Input: rt.input,
						})
					}
					// Return an approval request to the TUI — execution pauses here.
					return ApprovalRequestMsg{
						ToolIndex:        i,
						ToolName:         tool.name,
						ToolUseID:        tool.id,
						Input:            tool.input,
						Command:          req.Command,
						Reason:           req.Reason,
						Risk:             req.Risk.String(),
						CompletedResults: results,
						CompletedTodos:   todoInputs,
						RemainingTools:   remaining,
					}
				}
			}

			output, err := pluginMgr.Execute(ctx, tool.name, tool.input, workDir)
			status := types.ToolResultStatusSuccess
			if err != nil {
				if output == "" {
					output = fmt.Sprintf("Error: %s", err.Error())
				}
				status = types.ToolResultStatusError
			}

			results = append(results, bedrock.ToolResult{
				ToolUseID: tool.id,
				Content:   output,
				Status:    status,
			})
		}

		return ContinueStreamMsg{Messages: results, TodoInputs: todoInputs}
	}
}

// executeApprovedTool runs the approved tool and then continues with the remaining tools.
func (m *Model) executeApprovedTool(tool *pendingTool, remaining []pendingTool) tea.Cmd {
	pluginMgr := m.pluginMgr
	workDir := m.workDir
	autoApprove := m.autoApprove

	return func() tea.Msg {
		ctx := context.Background()
		var results []bedrock.ToolResult
		var todoInputs []json.RawMessage

		// Execute the approved tool
		output, err := pluginMgr.Execute(ctx, tool.name, tool.input, workDir)
		status := types.ToolResultStatusSuccess
		if err != nil {
			if output == "" {
				output = fmt.Sprintf("Error: %s", err.Error())
			}
			status = types.ToolResultStatusError
		}
		results = append(results, bedrock.ToolResult{
			ToolUseID: tool.id,
			Content:   output,
			Status:    status,
		})

		// Execute remaining tools (they may also need approval)
		for i, rt := range remaining {
			if rt.name == "todo_manage" {
				todoInputs = append(todoInputs, rt.input)
				results = append(results, bedrock.ToolResult{
					ToolUseID: rt.id,
					Content:   "",
					Status:    types.ToolResultStatusSuccess,
				})
				continue
			}

			if req := approval.Check(rt.name, string(rt.input)); req != nil {
				req.ToolUseID = rt.id
				if !autoApprove {
					var furtherRemaining []pendingToolForApproval
					for _, fr := range remaining[i+1:] {
						furtherRemaining = append(furtherRemaining, pendingToolForApproval{
							ID:    fr.id,
							Name:  fr.name,
							Input: fr.input,
						})
					}
					return ApprovalRequestMsg{
						ToolIndex:        i,
						ToolName:         rt.name,
						ToolUseID:        rt.id,
						Input:            rt.input,
						Command:          req.Command,
						Reason:           req.Reason,
						Risk:             req.Risk.String(),
						CompletedResults: results,
						CompletedTodos:   todoInputs,
						RemainingTools:   furtherRemaining,
					}
				}
			}

			rtOutput, rtErr := pluginMgr.Execute(ctx, rt.name, rt.input, workDir)
			rtStatus := types.ToolResultStatusSuccess
			if rtErr != nil {
				if rtOutput == "" {
					rtOutput = fmt.Sprintf("Error: %s", rtErr.Error())
				}
				rtStatus = types.ToolResultStatusError
			}
			results = append(results, bedrock.ToolResult{
				ToolUseID: rt.id,
				Content:   rtOutput,
				Status:    rtStatus,
			})
		}

		return ContinueStreamMsg{Messages: results, TodoInputs: todoInputs}
	}
}

// executeDeniedToolAndRemaining returns a denial result for the tool and continues with remaining.
func (m *Model) executeDeniedToolAndRemaining(tool *pendingTool, remaining []pendingTool) tea.Cmd {
	pluginMgr := m.pluginMgr
	workDir := m.workDir
	autoApprove := m.autoApprove

	return func() tea.Msg {
		ctx := context.Background()
		var results []bedrock.ToolResult
		var todoInputs []json.RawMessage

		// Denied tool gets an error result
		results = append(results, bedrock.ToolResult{
			ToolUseID: tool.id,
			Content:   "Operation denied by user. The destructive command was NOT executed. Choose a safer alternative or ask the user for guidance.",
			Status:    types.ToolResultStatusError,
		})

		// Execute remaining tools normally
		for i, rt := range remaining {
			if rt.name == "todo_manage" {
				todoInputs = append(todoInputs, rt.input)
				results = append(results, bedrock.ToolResult{
					ToolUseID: rt.id,
					Content:   "",
					Status:    types.ToolResultStatusSuccess,
				})
				continue
			}

			if req := approval.Check(rt.name, string(rt.input)); req != nil {
				req.ToolUseID = rt.id
				if !autoApprove {
					var furtherRemaining []pendingToolForApproval
					for _, fr := range remaining[i+1:] {
						furtherRemaining = append(furtherRemaining, pendingToolForApproval{
							ID:    fr.id,
							Name:  fr.name,
							Input: fr.input,
						})
					}
					return ApprovalRequestMsg{
						ToolIndex:        i,
						ToolName:         rt.name,
						ToolUseID:        rt.id,
						Input:            rt.input,
						Command:          req.Command,
						Reason:           req.Reason,
						Risk:             req.Risk.String(),
						CompletedResults: results,
						CompletedTodos:   todoInputs,
						RemainingTools:   furtherRemaining,
					}
				}
			}

			rtOutput, rtErr := pluginMgr.Execute(ctx, rt.name, rt.input, workDir)
			rtStatus := types.ToolResultStatusSuccess
			if rtErr != nil {
				if rtOutput == "" {
					rtOutput = fmt.Sprintf("Error: %s", rtErr.Error())
				}
				rtStatus = types.ToolResultStatusError
			}
			results = append(results, bedrock.ToolResult{
				ToolUseID: rt.id,
				Content:   rtOutput,
				Status:    rtStatus,
			})
		}

		return ContinueStreamMsg{Messages: results, TodoInputs: todoInputs}
	}
}

// --- Session persistence ---

// restoreSession loads a saved session and rebuilds the display state.
func (m *Model) restoreSession() {
	if m.store == nil || m.sessionID == "" {
		return
	}

	state, err := m.store.Load(m.sessionID)
	if err != nil {
		return
	}

	// Restore conversation history for the model
	messages, err := session.UnmarshalHistory(state.Messages)
	if err != nil {
		return
	}
	m.history = messages

	// Restore token stats (so resumed sessions accumulate correctly)
	m.totalInputTokens = state.Meta.Stats.InputTokens
	m.totalOutputTokens = state.Meta.Stats.OutputTokens
	m.totalCacheReadInputTokens = state.Meta.Stats.CacheReadInputTokens
	m.totalCacheWriteInputTokens = state.Meta.Stats.CacheWriteInputTokens

	// Restore todos
	if len(state.Todos) > 0 {
		m.todos.Replace(state.Todos)
	}

	// Rebuild display messages from the serialized history
	for _, msg := range state.Messages {
		for _, block := range msg.Blocks {
			switch block.Type {
			case "text":
				if msg.Role == "user" {
					m.messages = append(m.messages, chatMessage{role: "user", content: block.Text})
				} else {
					m.messages = append(m.messages, chatMessage{role: "assistant", content: block.Text})
				}
			case "tool_use":
				m.messages = append(m.messages, chatMessage{
					role:    "tool_call",
					content: fmt.Sprintf("Called %s", block.Name),
					name:    block.Name,
				})
			case "tool_result":
				m.messages = append(m.messages, chatMessage{
					role:    "tool_result",
					content: truncateToolResult(block.Content, 200),
					isError: block.Status == "error",
				})
			}
		}
	}
}

// saveSession persists current TUI state to disk.
func (m *Model) saveSession() {
	if m.store == nil || m.sessionID == "" {
		return
	}

	serialized, err := session.MarshalHistory(m.history)
	if err != nil {
		return
	}

	// Load existing to preserve metadata
	existing, loadErr := m.store.Load(m.sessionID)
	var meta session.SessionMeta
	if loadErr == nil {
		meta = existing.Meta
	} else {
		meta.ID = m.sessionID
	}

	meta.Stats.InputTokens = m.totalInputTokens
	meta.Stats.OutputTokens = m.totalOutputTokens
	meta.Stats.CacheReadInputTokens = m.totalCacheReadInputTokens
	meta.Stats.CacheWriteInputTokens = m.totalCacheWriteInputTokens
	meta.Stats.EstimatedCostUSD = m.estimateCost()

	state := &session.SessionState{
		Meta:     meta,
		Messages: serialized,
		Todos:    m.todos.Items(),
		Inkwell:  []session.InkEntry{}, // TUI inkwell tracking can be added later
	}

	m.store.Save(m.sessionID, state)
}

// --- Rendering ---

func (m *Model) renderStatusBar() string {
	label := StatusLabelStyle.Render("codecuttlectl")
	modelName := ""
	region := ""
	if m.client != nil {
		modelName = m.client.ModelID()
		region = m.client.Region()
	} else if m.llmProvider != nil {
		modelName = m.llmProvider.Name()
	}
	model := StatusModelStyle.Render(modelName)
	regionStr := StatusDimStyle.Render(region)
	plugins := StatusDimStyle.Render(fmt.Sprintf("%dp", m.pluginMgr.Count()))

	// Token display: show total input (including cache), output, cache hit rate, and estimated cost
	totalIn := m.totalInputTokens + m.totalCacheReadInputTokens + m.totalCacheWriteInputTokens
	cacheHitPct := 0
	if totalIn > 0 {
		cacheHitPct = int(float64(m.totalCacheReadInputTokens) / float64(totalIn) * 100)
	}
	cost := m.estimateCost()

	// Context window usage: based on the most recent API call's total input tokens.
	// This represents how full the context window is RIGHT NOW (not cumulative).
	// Claude Opus 4.x on Bedrock: 200k input context window.
	ctxUsed := m.lastCallInputTokens + m.lastCallCacheReadInputTokens + m.lastCallCacheWriteInputTokens
	ctxPct := 0
	if ctxUsed > 0 {
		ctxPct = int(float64(ctxUsed) / float64(m.contextWindowSize()) * 100)
	}

	// Color the ctx% indicator based on usage level
	ctxStr := fmt.Sprintf("%d%% ctx", ctxPct)
	var ctxStyled string
	switch {
	case ctxPct >= 90:
		ctxStyled = ErrorStyle.Render(ctxStr)
	case ctxPct >= 75:
		ctxStyled = lipgloss.NewStyle().Foreground(colorHoney).Render(ctxStr)
	default:
		ctxStyled = StatusDimStyle.Render(ctxStr)
	}

	// Build right-side status: tokens in/out, ctx%, and optionally cost
	var right string
	if m.client != nil {
		// Bedrock: show cache hit % and cost
		tokenStr := fmt.Sprintf("%s in %s out %d%% cache ",
			formatTokenCount(totalIn), formatTokenCount(m.totalOutputTokens), cacheHitPct)
		tokens := StatusTokenStyle.Render(tokenStr) + ctxStyled + StatusTokenStyle.Render(fmt.Sprintf(" ~$%.2f", cost))
		right = tokens + " "
	} else {
		// Local provider (Ollama): show tokens and ctx%, no cost
		tokenStr := fmt.Sprintf("%s in %s out ",
			formatTokenCount(totalIn), formatTokenCount(m.totalOutputTokens))
		tokens := StatusTokenStyle.Render(tokenStr) + ctxStyled
		right = tokens + " "
	}

	left := " " + label + "  " + model + "  " + regionStr + "  " + plugins

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW
	if gap < 1 {
		// Terminal too narrow — drop some info
		left = " " + label + "  " + model
		leftW = lipgloss.Width(left)
		gap = m.width - leftW - rightW
		if gap < 1 {
			gap = 1
		}
	}

	return StatusBarStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

// formatTokenCount formats a token count as a human-readable string (e.g., "12.5k", "1.2M").
func formatTokenCount(tokens int32) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d", tokens)
	}
	if tokens < 1000000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
}

// estimateCost calculates an estimated dollar cost for the session's token usage.
// Returns 0 for local providers (Ollama) since they have no API cost.
// Uses Claude Opus 4.x pricing on Bedrock (as of 2026):
//   - Input tokens (uncached): $5.00 / 1M tokens
//   - Output tokens: $25.00 / 1M tokens
//   - Cache write (5m TTL): $6.25 / 1M tokens (1.25x input)
//   - Cache read: $0.50 / 1M tokens (0.1x input)
func (m *Model) estimateCost() float64 {
	// Local providers have no API cost — detect by: provider set but no bedrock client
	if m.client == nil && m.llmProvider != nil {
		return 0
	}

	const (
		inputPer1M      = 5.00
		outputPer1M     = 25.00
		cacheWritePer1M = 6.25
		cacheReadPer1M  = 0.50
	)

	input := float64(m.totalInputTokens) / 1_000_000 * inputPer1M
	output := float64(m.totalOutputTokens) / 1_000_000 * outputPer1M
	cacheWrite := float64(m.totalCacheWriteInputTokens) / 1_000_000 * cacheWritePer1M
	cacheRead := float64(m.totalCacheReadInputTokens) / 1_000_000 * cacheReadPer1M

	return input + output + cacheWrite + cacheRead
}

func (m *Model) renderInput() string {
	if m.streaming {
		// Use the current gradient color for the border
		borderColor := lipgloss.Color(spinnerGradient[m.spinnerColorIdx])
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)
		var content string
		if m.interruptPending {
			content = ErrorStyle.Render("Press esc again to interrupt, or wait...")
		} else {
			content = SpinnerStyle.Render(m.spinner.View()) + " thinking..."
		}
		return style.Width(m.width - 4).Render(content)
	}
	return InputActiveStyle.Width(m.width - 4).Render(m.input.View())
}

func (m *Model) renderTodoBar() string {
	var content string
	if m.todos.IsEmpty() {
		content = StatusDimStyle.Render("no active tasks")
	} else {
		content = m.todos.Summary()
	}

	toggle := HelpKeyStyle.Render("ctrl+t") + " " + HelpDescStyle.Render("tasks")

	leftW := lipgloss.Width(content) + 2 // " " prefix + content
	rightW := lipgloss.Width(toggle) + 1
	gap := m.width - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	line := " " + content + strings.Repeat(" ", gap) + toggle
	return TodoBarStyle.Width(m.width).Render(line)
}

func (m *Model) renderTodoPanel() string {
	if m.todos.IsEmpty() {
		return ""
	}

	var lines []string
	for _, item := range m.todos.Items() {
		var icon, styled string
		switch item.Status {
		case todo.StatusPending:
			icon = "○"
			styled = TodoPendingStyle.Render(item.Content)
		case todo.StatusInProgress:
			icon = "●"
			styled = TodoInProgressStyle.Render(item.Content)
		case todo.StatusCompleted:
			icon = "✓"
			styled = TodoCompletedStyle.Render(item.Content)
		case todo.StatusCancelled:
			icon = "✗"
			styled = TodoCancelledStyle.Render(item.Content)
		}

		var priorityBadge string
		switch item.Priority {
		case todo.PriorityHigh:
			priorityBadge = TodoHighPriorityStyle.Render("[H]")
		case todo.PriorityMedium:
			priorityBadge = TodoMedPriorityStyle.Render("[M]")
		case todo.PriorityLow:
			priorityBadge = TodoLowPriorityStyle.Render("[L]")
		}

		lines = append(lines, fmt.Sprintf("  %s %s %s", icon, priorityBadge, styled))
	}

	content := strings.Join(lines, "\n")
	return PanelBorderStyle.Width(m.width - 2).Render(content)
}

func (m *Model) renderHelpBar() string {
	keys := []struct{ key, desc string }{
		{"enter", "send"},
		{"shift+enter", "newline"},
		{"ctrl+m", "mouse"},
		{"ctrl+r", "thinking"},
		{"ctrl+t", "tasks"},
		{"ctrl+c", "quit"},
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, HelpKeyStyle.Render(k.key)+" "+HelpDescStyle.Render(k.desc))
	}

	content := " " + strings.Join(parts, "   ")
	// Truncate if wider than terminal
	if lipgloss.Width(content) > m.width {
		content = content[:m.width]
	}
	gap := m.width - lipgloss.Width(content)
	if gap < 0 {
		gap = 0
	}
	return HelpBarStyle.Width(m.width).Render(content + strings.Repeat(" ", gap))
}

func (m *Model) renderMessages() string {
	var lines []string

	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			prefix := UserPrefixStyle.Render(" ❯ ")
			// Wrap user message to viewport width
			maxW := m.width - 6
			if maxW < 20 {
				maxW = 20
			}
			body := UserBodyStyle.Width(maxW).Render(msg.content)
			lines = append(lines, prefix+body, "")

		case "reasoning":
			if m.showThinking {
				header := ReasoningPrefixStyle.Render("  ◇ thinking")
				lines = append(lines, header)
				content := strings.TrimSpace(msg.content)
				maxW := m.width - 8 // 4 indent + padding
				if maxW < 20 {
					maxW = 20
				}
				for _, line := range strings.Split(content, "\n") {
					if strings.TrimSpace(line) != "" {
						lines = append(lines, "    "+ReasoningBodyStyle.Width(maxW).Render(line))
					}
				}
				lines = append(lines, "")
			} else {
				wordCount := len(strings.Fields(msg.content))
				lines = append(lines, ReasoningCollapsedStyle.Render(
					fmt.Sprintf("  ◇ thinking (%d words) — ctrl+r to show", wordCount),
				))
			}

		case "assistant":
			prefix := AssistantPrefixStyle.Render(" ◆ ")
			lines = append(lines, prefix+"codecuttle")
			// Render markdown for completed messages
			rendered := m.renderMarkdown(msg.content)
			lines = append(lines, rendered, "")

		case "tool_call":
			lines = append(lines, ToolCallStyle.Render("  ⚡ "+msg.content))

		case "tool_result":
			if msg.isError {
				lines = append(lines, ToolResultErrorStyle.Width(m.width-6).Render("  ✗ "+truncateToolResult(msg.content, 200)), "")
			} else {
				lines = append(lines, ToolResultSuccessStyle.Width(m.width-6).Render("  ✓ "+truncateToolResult(msg.content, 200)), "")
			}
		}
	}

	// Active tool execution preview (live streaming output)
	if m.activeToolName != "" && m.activeToolOutput.Len() > 0 {
		output := m.activeToolOutput.String()
		outputLines := strings.Split(output, "\n")
		// Show last 5 lines as a rolling preview
		maxPreviewLines := 5
		start := len(outputLines) - maxPreviewLines
		if start < 0 {
			start = 0
		}
		previewLines := outputLines[start:]

		maxW := m.width - 8
		if maxW < 20 {
			maxW = 20
		}

		for _, line := range previewLines {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, ToolCallStyle.Width(maxW).Render("  ┃ "+line))
			}
		}
	}

	// Active streaming state
	if m.streaming {
		if m.inReasoning && m.reasoningBuf.Len() > 0 && m.showThinking {
			header := ReasoningPrefixStyle.Render("  ◇ thinking...")
			lines = append(lines, header)
			content := m.reasoningBuf.String()
			maxW := m.width - 8 // 4 indent + padding
			if maxW < 20 {
				maxW = 20
			}
			for _, line := range strings.Split(content, "\n") {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, "    "+ReasoningBodyStyle.Width(maxW).Render(line))
				}
			}
			lines = append(lines, StreamingCursorStyle.Render("    █"))
		} else if m.inReasoning && !m.showThinking {
			lines = append(lines, ReasoningCollapsedStyle.Render("  ◇ thinking..."))
		} else if m.streamBuf.Len() > 0 {
			prefix := AssistantPrefixStyle.Render(" ◆ ")
			lines = append(lines, prefix+"codecuttle")
			// During streaming, show plain text (not markdown-rendered) to avoid
			// layout jumps as partial markdown is re-interpreted each frame.
			// Markdown rendering happens once the message is finalized.
			content := sanitizeModelText(m.streamBuf.String())
			if strings.TrimSpace(content) != "" {
				lines = append(lines, m.wrapText(content))
			}
			lines = append(lines, StreamingCursorStyle.Render("█"))
		} else if !m.inReasoning && m.streamBuf.Len() == 0 && m.reasoningBuf.Len() == 0 && len(m.pendingToolCalls) == 0 {
			// Only show the "thinking..." spinner if no tool calls or results
			// have appeared yet in this streaming round. Otherwise, the model
			// is processing tool results — the tool_call/result messages are
			// already visible and sufficient feedback.
			hasToolActivity := false
			for i := len(m.messages) - 1; i >= 0; i-- {
				if m.messages[i].role == "user" {
					break
				}
				if m.messages[i].role == "tool_call" || m.messages[i].role == "tool_result" {
					hasToolActivity = true
					break
				}
			}
			if !hasToolActivity {
				lines = append(lines, "", SpinnerStyle.Render("  "+m.spinner.View()+" thinking..."))
			}
		}
	}

	return strings.Join(lines, "\n")
}

// renderMarkdown renders markdown text using glamour.
func (m *Model) renderMarkdown(text string) string {
	if m.mdRenderer == nil || strings.TrimSpace(text) == "" {
		return m.wrapText(text)
	}

	rendered, err := m.mdRenderer.Render(text)
	if err != nil {
		return m.wrapText(text)
	}

	// Glamour output includes trailing newlines, trim them
	return strings.TrimRight(rendered, "\n")
}

// wrapText manually wraps text lines that exceed the viewport width.
// This is the fallback when glamour isn't used or for non-markdown content.
func (m *Model) wrapText(text string) string {
	if m.width <= 0 {
		return text
	}
	maxW := m.width - 4 // Leave margin for padding
	if maxW < 20 {
		maxW = 20
	}

	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= maxW {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}
		// Hard-wrap long lines at maxW characters
		for len(line) > 0 {
			end := maxW
			if end > len(line) {
				end = len(line)
			}
			result.WriteString(line[:end])
			result.WriteString("\n")
			line = line[end:]
		}
	}
	return strings.TrimRight(result.String(), "\n")
}

func truncateToolResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func jsonToMap(data json.RawMessage) map[string]interface{} {
	var m map[string]interface{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

// xmlTagPattern matches XML-like tool call markup that models sometimes emit in text.
var xmlTagPattern = regexp.MustCompile(`</?(?:function_calls|invoke|antml:invoke|antml:function_calls|parameter|tool_call|tool_result|thinking|function|result)[^>]*>`)

// sanitizeModelText removes XML tool-call markup that the model sometimes emits
// in its text stream alongside native tool_use content blocks.
func sanitizeModelText(text string) string {
	// Remove XML tags
	cleaned := xmlTagPattern.ReplaceAllString(text, "")
	// Collapse multiple blank lines into at most two
	for strings.Contains(cleaned, "\n\n\n") {
		cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
	}
	return cleaned
}

// providerToolDefs returns tool definitions in provider-agnostic format.
func (m *Model) providerToolDefs() []provider.ToolDefinition {
	defs := m.pluginMgr.Definitions()
	var result []provider.ToolDefinition
	for _, d := range defs {
		result = append(result, provider.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}
	return result
}

// maybeExtractPlan checks if the model's text output contains a plan and
// automatically populates the todo list if the list is currently empty.
// This enables harness-managed planning for all providers (Bedrock + Ollama).
func (m *Model) maybeExtractPlan(text string) {
	if !m.todos.IsEmpty() {
		return
	}

	steps := conversation.ExtractPlanFromText(text)
	if steps == nil || len(steps) < 2 {
		return
	}

	// Cap at 7 steps
	if len(steps) > 7 {
		steps = steps[:7]
	}

	items := make([]todo.Item, len(steps))
	for i, step := range steps {
		status := todo.StatusPending
		if i == 0 {
			status = todo.StatusInProgress
		}
		items[i] = todo.Item{
			Content:  step,
			Status:   status,
			Priority: todo.PriorityMedium,
		}
	}

	m.todos.Replace(items)
}

// contextWindowSize returns the effective context window size for the current provider.
func (m *Model) contextWindowSize() int32 {
	if m.contextWindow > 0 {
		return m.contextWindow
	}
	return defaultContextWindowSize
}

// needsGroundingAssist returns true if the current model benefits from periodic
// grounding reminders. Large frontier models don't need this; smaller/local models do.
func (m *Model) needsGroundingAssist() bool {
	// If we have a bedrock client (non-nil), it's a frontier model — skip grounding
	if m.client != nil && m.llmProvider != nil {
		// Check if it's the bedrockprov wrapper (frontier model)
		if m.contextWindow > 512_000 {
			return false
		}
	}

	// Check provider's context window
	if m.contextWindow > 512_000 {
		return false
	}

	return true // Small/local model — inject grounding
}

// extractLastUserText finds the most recent user text message in history.
func (m *Model) extractLastUserText() string {
	// Walk backward to find the last user message with text content
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].Role == types.ConversationRoleUser {
			for _, block := range m.history[i].Content {
				if text, ok := block.(*types.ContentBlockMemberText); ok {
					return text.Value
				}
			}
		}
	}
	return ""
}
