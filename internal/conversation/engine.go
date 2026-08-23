package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// EngineEvent is the sealed marker interface for domain events emitted by Engine.
type EngineEvent interface {
	isEngineEvent()
}

// EventToken carries an incremental text chunk from the model stream.
type EventToken struct {
	Text string
}

func (EventToken) isEngineEvent() {}

// EventReasoningToken carries an incremental thinking/reasoning token delta.
type EventReasoningToken struct {
	Text      string
	Signature string
}

func (EventReasoningToken) isEngineEvent() {}

// EventToolStart signals that the model is beginning a tool execution.
type EventToolStart struct {
	ToolUseID        string
	Name             string
	Input            json.RawMessage
	ThoughtSignature string
}

func (EventToolStart) isEngineEvent() {}

// EventToolOutputDelta carries streaming output chunks from an active tool execution.
type EventToolOutputDelta struct {
	ToolUseID string
	Name      string
	Delta     string
	IsStderr  bool
}

func (EventToolOutputDelta) isEngineEvent() {}

// EventToolResult carries the completed result of a tool execution.
type EventToolResult struct {
	ToolUseID string
	Name      string
	Output    string
	IsError   bool
}

func (EventToolResult) isEngineEvent() {}

// EventPlanUpdate signals that the agent's task/todo plan was modified.
type EventPlanUpdate struct {
	Todos []todo.Item
}

func (EventPlanUpdate) isEngineEvent() {}

// EventDiagnostic carries Inkwell error classifications or self-healing notices.
type EventDiagnostic struct {
	Message string
}

func (EventDiagnostic) isEngineEvent() {}

// EventUsage carries token usage and prompt cache metrics for the turn.
type EventUsage struct {
	Usage provider.Usage
}

func (EventUsage) isEngineEvent() {}

// EventTurnDone signals that the model turn and tool loop completed.
type EventTurnDone struct {
	Response string
	Usage    provider.Usage
	Error    error
}

func (EventTurnDone) isEngineEvent() {}

// Engine orchestrates multi-turn agent execution independently of any attached UI.
// It runs execution on decoupled background goroutines, dispatching typed domain events
// to subscribers without stalling if UI rendering or terminal I/O hangs.
type Engine struct {
	agent *Agent
	mu    sync.RWMutex
}

// NewEngine wraps an existing Agent in an autonomous, event-driven Engine.
func NewEngine(agent *Agent) *Engine {
	return &Engine{
		agent: agent,
	}
}

// Agent returns the underlying conversation Agent.
func (e *Engine) Agent() *Agent {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.agent
}

// StreamTurnAsync executes an agent turn on a background goroutine, streaming
// typed domain events to the returned channel. The channel is closed when the turn ends.
func (e *Engine) StreamTurnAsync(ctx context.Context, userPrompt string) (<-chan EngineEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.agent == nil {
		return nil, fmt.Errorf("engine has no agent configured")
	}

	eventCh := make(chan EngineEvent, 128)

	// Launch execution in a decoupled background goroutine
	go func() {
		defer close(eventCh)

		// Create a turn-specific event adapter or run via provider StreamTurn
		resp, err := e.agent.StreamTurn(ctx, userPrompt, func(evt StreamEvent) {
			switch evt.Type {
			case "text":
				select {
				case eventCh <- EventToken{Text: evt.Text}:
				case <-ctx.Done():
				default:
					// Non-blocking drop to prevent PTY/UI stall from blocking engine goroutine
				}
			case "tool_start":
				select {
				case eventCh <- EventToolStart{ToolUseID: evt.ToolUseID, Name: evt.ToolName}:
				case <-ctx.Done():
				default:
				}
			case "error":
				select {
				case eventCh <- EventTurnDone{Error: evt.Error}:
				case <-ctx.Done():
				default:
				}
			}
		})

		if err != nil {
			eventCh <- EventTurnDone{Error: err}
			return
		}

		// Emit plan updates if todos exist
		if e.agent.todos != nil {
			eventCh <- EventPlanUpdate{Todos: e.agent.todos.Items()}
		}

		eventCh <- EventTurnDone{
			Response: resp,
		}
	}()

	return eventCh, nil
}
