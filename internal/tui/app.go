package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// Config holds the configuration needed to create the TUI app.
type Config struct {
	Client         *bedrock.Client
	PluginMgr      *pluginhost.Manager
	System         string // Rendered system prompt
	WorkDir        string
	Verbose        bool
	EnableThinking bool // Enable extended thinking (model must support it)
	ThinkingBudget int  // Token budget for thinking (default: 16000)

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
	streamCh  <-chan bedrock.StreamEvent // Active stream channel

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

	// Todo state
	todos        *todo.List
	todoExpanded bool

	// Session persistence
	store     session.Store
	sessionID string

	// Stats
	totalInputTokens  int32
	totalOutputTokens int32
	spinnerColorIdx   int
	spinnerTickCount  int

	// Layout
	width  int
	height int
	ready  bool

	// Markdown renderer
	mdRenderer *glamour.TermRenderer
}

type chatMessage struct {
	role    string // "user", "assistant", "reasoning", "tool_call", "tool_result"
	content string
	name    string
	isError bool
}

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
		pluginMgr:        cfg.PluginMgr,
		system:           cfg.System,
		workDir:          cfg.WorkDir,
		enableThinking:   cfg.EnableThinking,
		thinkingBudget:   thinkingBudget,
		todos:            todo.NewList(),
		messages:         []chatMessage{},
		streamBuf:        &strings.Builder{},
		reasoningBuf:     &strings.Builder{},
		currentToolInput: &strings.Builder{},
		showThinking:     true,
		mdRenderer:       renderer,
		store:            cfg.Store,
		sessionID:        cfg.SessionID,
	}

	// If resuming a session, restore conversation history
	if cfg.Store != nil && cfg.SessionID != "" {
		m.restoreSession()
	} else if cfg.Store != nil {
		// Create a new session
		meta := session.SessionMeta{
			Model:   cfg.Client.ModelID(),
			Region:  cfg.Client.Region(),
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
	return tea.Batch(m.spinner.Tick, textarea.Blink)
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
		case "enter":
			if !m.streaming {
				text := strings.TrimSpace(m.input.Value())
				if text != "" {
					m.input.Reset()
					return m, m.submitMessage(text)
				}
			}
			return m, nil
		case "esc":
			if m.todoExpanded {
				m.todoExpanded = false
				m.recalcLayout()
				return m, nil
			}
		}

	// --- Stream event handling ---

	case StreamTextMsg:
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
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
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
			return m, m.executePendingTools()
		}

		// No tool calls — finalize streamed text as the assistant response
		if m.streamBuf.Len() > 0 {
			content := m.streamBuf.String()
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
		m.streaming = false
		m.streamCh = nil
		m.viewport.SetContent(m.renderMessages())
		return m, nil

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

	case ToolExecResultMsg:
		m.messages = append(m.messages, chatMessage{
			role:    "tool_result",
			content: truncateToolResult(msg.Output, 500),
			name:    msg.Name,
			isError: msg.IsError,
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

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
		return m, m.launchStream()

	case TodoUpdatedMsg:
		m.todos.Replace(msg.Items)
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
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

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
	view.MouseMode = tea.MouseModeCellMotion
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

	ctx := context.Background()
	var streamCfg bedrock.StreamConfig
	if m.enableThinking {
		streamCfg.EnableThinking = true
		streamCfg.ThinkingBudget = m.thinkingBudget
	}
	ch := m.client.ConverseStream(ctx, m.system, m.history, m.pluginMgr.Definitions(), streamCfg)
	m.streamCh = ch

	// Return a cmd that reads the first event from the stream
	return tea.Batch(m.spinner.Tick, m.readNextStreamEvent())
}

// readNextStreamEvent returns a Cmd that reads the next event from the active stream.
func (m *Model) readNextStreamEvent() tea.Cmd {
	ch := m.streamCh
	if ch == nil {
		return func() tea.Msg { return StreamDoneMsg{StopReason: "no_channel"} }
	}
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return StreamDoneMsg{StopReason: "end_turn"}
		}
		switch e := event.(type) {
		case bedrock.TextDeltaEvent:
			return StreamTextMsg{Text: e.Text}
		case bedrock.ReasoningDeltaEvent:
			return StreamReasoningMsg{Text: e.Text}
		case bedrock.ReasoningSignatureEvent:
			return StreamReasoningDoneMsg{Signature: e.Signature}
		case bedrock.ToolUseStartEvent:
			return StreamToolStartMsg{ToolUseID: e.ToolUseID, Name: e.Name}
		case bedrock.ToolInputDeltaEvent:
			return StreamToolInputMsg{Delta: e.Delta}
		case bedrock.ToolUseStopEvent:
			return StreamToolStopMsg{}
		case bedrock.MessageStopEvent:
			return StreamDoneMsg{StopReason: e.StopReason}
		case bedrock.UsageEvent:
			return StreamUsageMsg{InputTokens: e.InputTokens, OutputTokens: e.OutputTokens}
		case bedrock.StreamErrorEvent:
			return StreamErrorMsg{Err: e.Err}
		default:
			return StreamDoneMsg{StopReason: "unknown"}
		}
	}
}

// --- Layout ---

func (m *Model) recalcLayout() {
	// Fixed height elements:
	// - Status bar: 1 line
	// - Input area: 3 lines (1 content + 2 border)
	// - Todo bar: 1 line
	// - Help bar: 1 line
	// Total fixed: 6
	headerFooter := 6
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
		m.viewport.SetWidth(m.width)
		m.viewport.SetHeight(vpHeight)
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

	return m.launchStream()
}

func (m *Model) executePendingTools() tea.Cmd {
	tools := m.pendingToolCalls
	m.pendingToolCalls = nil
	pluginMgr := m.pluginMgr
	workDir := m.workDir

	return func() tea.Msg {
		var results []bedrock.ToolResult
		var todoInputs []json.RawMessage
		ctx := context.Background()

		for _, tool := range tools {
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
	model := StatusModelStyle.Render(m.client.ModelID())
	region := StatusDimStyle.Render(m.client.Region())
	plugins := StatusDimStyle.Render(fmt.Sprintf("%dp", m.pluginMgr.Count()))
	tokens := StatusTokenStyle.Render(fmt.Sprintf("%d/%d tok", m.totalInputTokens, m.totalOutputTokens))

	left := " " + label + "  " + model + "  " + region + "  " + plugins
	right := tokens + " "

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

func (m *Model) renderInput() string {
	if m.streaming {
		// Use the current gradient color for the border
		borderColor := lipgloss.Color(spinnerGradient[m.spinnerColorIdx])
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)
		content := SpinnerStyle.Render(m.spinner.View()) + " thinking..."
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
				for _, line := range strings.Split(content, "\n") {
					if strings.TrimSpace(line) != "" {
						lines = append(lines, "    "+ReasoningBodyStyle.Render(line))
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

	// Active streaming state
	if m.streaming {
		if m.inReasoning && m.reasoningBuf.Len() > 0 && m.showThinking {
			header := ReasoningPrefixStyle.Render("  ◇ thinking...")
			lines = append(lines, header)
			content := m.reasoningBuf.String()
			for _, line := range strings.Split(content, "\n") {
				if strings.TrimSpace(line) != "" {
					lines = append(lines, "    "+ReasoningBodyStyle.Render(line))
				}
			}
			lines = append(lines, StreamingCursorStyle.Render("    █"))
		} else if m.inReasoning && !m.showThinking {
			lines = append(lines, ReasoningCollapsedStyle.Render("  ◇ thinking..."))
		}

		if m.streamBuf.Len() > 0 {
			prefix := AssistantPrefixStyle.Render(" ◆ ")
			lines = append(lines, prefix+"codecuttle")
			// Progressive markdown rendering during streaming
			content := sanitizeModelText(m.streamBuf.String())
			if strings.TrimSpace(content) != "" {
				rendered := m.renderMarkdown(content)
				if rendered != "" {
					lines = append(lines, rendered)
				} else {
					lines = append(lines, content)
				}
			}
			lines = append(lines, StreamingCursorStyle.Render("█"))
		} else if !m.inReasoning && m.streamBuf.Len() == 0 && len(m.pendingToolCalls) == 0 {
			lines = append(lines, "", SpinnerStyle.Render("  "+m.spinner.View()+" thinking..."))
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
