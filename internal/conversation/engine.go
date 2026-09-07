package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/session"
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

// Engine serializes submissions against one Agent. It owns the Agent only when
// callers use Submit/StreamTurnAsync; legacy frontends still bypass it until R6/R8.
// Never mutate Agent directly or share it between Engines while turns are active.
type Engine struct {
	agent  *Agent
	mu     sync.RWMutex
	active *TurnHandle
	nextID uint64
	closed bool
	root   context.Context
	cancel context.CancelFunc
	latest EngineSnapshot
}

// NewEngine wraps a configured Agent. Context lifetime is explicit via Close.
func NewEngine(agent *Agent) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{agent: agent, root: ctx, cancel: cancel}
	if agent != nil {
		e.latest = snapshotAgent(agent)
	}
	return e
}

// Agent is a legacy escape hatch retained for the unmigrated frontends.
// Deprecated: do not use concurrently with Submit, Snapshot or Close. A returned
// pointer is not protected by the Engine lock. Configure before NewEngine instead.
func (e *Engine) Agent() *Agent { return e.agent }

var (
	ErrEngineBusy   = errors.New("engine already has an active turn")
	ErrEngineClosed = errors.New("engine is closed")
	ErrNoAgent      = errors.New("engine has no agent configured")
	ErrSlowConsumer = errors.New("engine event consumer fell behind; turn cancelled")
)

const engineMailboxSize = 128

// Submit admits exactly one turn. Rejected submissions do not touch Agent state.
// The independent handle completes even if the event consumer stops draining.
func (e *Engine) Submit(ctx context.Context, userPrompt string) (*TurnHandle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrEngineClosed
	}
	if e.agent == nil {
		return nil, ErrNoAgent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.active != nil {
		return nil, ErrEngineBusy
	}
	// Retain the caller's deadline and values; Close supplies the second
	// cancellation source without replacing the caller's context lineage.
	turnCtx, cancel := context.WithCancelCause(ctx)
	stopSession := context.AfterFunc(e.root, func() { cancel(context.Canceled) })
	e.nextID++
	h := &TurnHandle{id: e.nextID, cancel: cancel, done: make(chan struct{}), events: make(chan EngineEvent, engineMailboxSize)}
	e.active = h
	e.latest.Revision++
	go e.run(turnCtx, stopSession, h, userPrompt)
	return h, nil
}

// StreamTurnAsync is the event-only compatibility API. Prefer Submit when the
// caller may detach: its handle retains the terminal outcome independently.
func (e *Engine) StreamTurnAsync(ctx context.Context, userPrompt string) (<-chan EngineEvent, error) {
	h, err := e.Submit(ctx, userPrompt)
	if err != nil {
		return nil, err
	}
	return h.Events(), nil
}

func (e *Engine) run(ctx context.Context, stopSession func() bool, h *TurnHandle, prompt string) {
	// Reserve two slots for plan and terminal events. Overflow cancels the turn
	// explicitly instead of silently dropping content or blocking finalization.
	publish := func(event EngineEvent) {
		if ctx.Err() != nil {
			return
		}
		if len(h.events) >= cap(h.events)-2 {
			h.cancel(ErrSlowConsumer)
			return
		}
		h.events <- event // one producer; the reserved-capacity check cannot race a producer
	}
	var response string
	var err error
	if ctx.Err() != nil {
		err = context.Cause(ctx)
	} else {
		response, err = e.agent.StreamTurn(ctx, prompt, func(evt StreamEvent) {
			// Error callbacks are notifications; the return value finalizes once.
			switch evt.Type {
			case "text":
				publish(EventToken{Text: evt.Text})
			case "tool_start":
				publish(EventToolStart{ToolUseID: evt.ToolUseID, Name: evt.ToolName})
			}
		})
	}
	stopSession()

	// Only the worker inspects mutable Agent state. No lock spans provider/tool
	// I/O; Snapshot serves the last completed checkpoint while a turn runs.
	checkpoint := snapshotAgent(e.agent)
	e.mu.Lock()
	defer e.mu.Unlock()
	// Serialize cancellation with completion through the handle mutex.
	h.mu.Lock()
	defer h.mu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	} else if e.closed {
		// Close may win before its AfterFunc cancellation has propagated.
		err = context.Canceled
	}
	status := TurnCompleted
	if err != nil {
		status = TurnFailed
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSlowConsumer) {
			status = TurnCancelled
		}
	}
	h.result = TurnResult{ID: h.id, Status: status, Response: response, Error: err}
	h.finished = true
	h.cancel(nil)
	checkpoint.Revision = e.latest.Revision + 1
	checkpoint.LastTurn = h.result
	e.latest = checkpoint
	e.active = nil
	h.events <- EventPlanUpdate{Todos: append([]todo.Item(nil), checkpoint.Todos...)}
	h.events <- EventTurnDone{Response: response, Error: err}
	close(h.events)
	close(h.done)
}

// Snapshot returns a deep copy of the last completed Agent checkpoint plus live
// lifecycle metadata. It does not promise live partial history or disk durability.
func (e *Engine) Snapshot() EngineSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snapshot := cloneSnapshot(e.latest)
	snapshot.Closed = e.closed
	if e.active != nil {
		snapshot.ActiveTurn = e.active.id
	}
	return snapshot
}

// Close stops admission permanently and requests cancellation. A deadline does
// not pretend an uncooperative worker stopped: later Close calls may wait again.
// External provider/plugin resources remain owned by the caller in this slice.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		e.latest.Revision++
		e.cancel()
	}
	active := e.active
	e.mu.Unlock()
	if active != nil {
		active.Cancel() // synchronous propagation; no Engine lock spans cancellation
	}
	if active == nil {
		return nil
	}
	select {
	case <-active.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// snapshotAgent is called at construction or by the sole worker after StreamTurn
// returns. It intentionally uses existing serialization, not a new storage schema.
func snapshotAgent(a *Agent) EngineSnapshot {
	s := EngineSnapshot{SessionID: a.sessionID, Node: a.activeNode, System: a.systemPrompt, Workbench: a.workbench}
	if a.provider != nil {
		s.ProviderID = a.provider.ID()
		s.History, s.CheckpointError = session.MarshalProviderHistory(a.provHistory)
	} else {
		if a.client != nil {
			s.ProviderID = a.client.ModelID()
		}
		s.History, s.CheckpointError = session.MarshalHistory(a.history)
	}
	if a.todos != nil {
		s.Todos = a.todos.Items()
	}
	return cloneSnapshot(s)
}
