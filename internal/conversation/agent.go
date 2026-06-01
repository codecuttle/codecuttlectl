// Package conversation implements the agent conversation loop with tool calling.
package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/bedrock"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
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
}

// Config holds configuration for creating an Agent.
type Config struct {
	Client    *bedrock.Client
	PromptMgr *prompt.Manager
	PluginMgr *pluginhost.Manager
	WorkDir   string
	MaxSteps  int  // Maximum tool-use iterations per turn. Default: 25
	Verbose   bool // Print debug info
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

	return &Agent{
		client:       cfg.Client,
		promptMgr:    cfg.PromptMgr,
		pluginMgr:    cfg.PluginMgr,
		systemPrompt: systemPrompt,
		workDir:      cfg.WorkDir,
		todos:        todo.NewList(),
		maxSteps:     cfg.MaxSteps,
		verbose:      cfg.Verbose,
	}, nil
}

// SetSystemPrompt overrides the system prompt (used when prompt is pre-rendered externally).
func (a *Agent) SetSystemPrompt(s string) {
	a.systemPrompt = s
}

// Turn sends a user message and processes the model's response, executing tools as needed.
// Returns the final text response from the model. (Synchronous, for one-shot/plain mode.)
func (a *Agent) Turn(ctx context.Context, userMessage string) (string, error) {
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
			return resp.Content, nil
		}

		// Execute all tool calls and collect results
		var toolResults []bedrock.ToolResult
		for _, toolUse := range resp.ToolUses {
			if a.verbose {
				log.Printf("[tool] %s id=%s input=%s", toolUse.Name, toolUse.ToolUseID, string(toolUse.Input))
			}

			result, status := a.executeTool(ctx, toolUse.Name, toolUse.Input)

			if a.verbose {
				truncated := result
				if len(truncated) > 500 {
					truncated = truncated[:500] + "..."
				}
				log.Printf("[tool result] %s", truncated)
			}

			toolResults = append(toolResults, bedrock.ToolResult{
				ToolUseID: toolUse.ToolUseID,
				Content:   result,
				Status:    status,
			})
		}

		a.history = append(a.history, bedrock.BuildToolResultMessage(toolResults))
	}

	return "", fmt.Errorf("exceeded maximum steps (%d) without completing", a.maxSteps)
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

// HistoryLen returns the current conversation history length.
func (a *Agent) HistoryLen() int {
	return len(a.history)
}

// Reset clears the conversation history.
func (a *Agent) Reset() {
	a.history = nil
	a.todos = todo.NewList()
}

// SystemPrompt returns the rendered system prompt for debugging.
func (a *Agent) SystemPrompt() string {
	return a.systemPrompt
}

// Todos returns the current todo list.
func (a *Agent) Todos() *todo.List {
	return a.todos
}
