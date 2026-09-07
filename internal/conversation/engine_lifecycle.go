package conversation

import (
	"context"
	"sync"

	"github.com/codecuttle/codecuttlectl/internal/session"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// TurnStatus describes the final execution outcome, independently of event delivery.
type TurnStatus string

const (
	TurnCompleted TurnStatus = "completed"
	TurnFailed    TurnStatus = "failed"
	TurnCancelled TurnStatus = "cancelled"
)

type TurnResult struct {
	ID       uint64 // unique within an Engine, not a globally durable identifier
	Status   TurnStatus
	Response string
	Error    error
}

// TurnHandle supports waiting/cancellation without requiring an event consumer.
// One consumer owns Events; any number may wait on Done or call Wait/Cancel.
type TurnHandle struct {
	id       uint64
	cancel   context.CancelCauseFunc
	done     chan struct{}
	events   chan EngineEvent
	mu       sync.Mutex
	finished bool
	result   TurnResult
}

func (h *TurnHandle) ID() uint64                 { return h.id }
func (h *TurnHandle) Done() <-chan struct{}      { return h.done }
func (h *TurnHandle) Events() <-chan EngineEvent { return h.events }

// Cancel is idempotent and affects only this turn, never a later admitted turn.
func (h *TurnHandle) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.finished {
		h.cancel(context.Canceled)
	}
}

// Wait's error concerns the wait context only; execution errors are in Result.
// Timing out a waiter does not cancel execution; call Cancel explicitly for that.
func (h *TurnHandle) Wait(ctx context.Context) (TurnResult, error) {
	select {
	case <-h.done:
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result, nil
}

// EngineSnapshot combines a completed domain checkpoint with admission state.
// History/Todos/Workbench are copies. Revision changes on admission, completion
// and close; an active turn's domain mutations are not exposed until completion.
type EngineSnapshot struct {
	Revision        uint64
	ActiveTurn      uint64 // zero means no active worker
	Closed          bool
	LastTurn        TurnResult
	SessionID       string
	ProviderID      string
	Node            string
	System          string
	Workbench       []string
	History         []session.Message
	Todos           []todo.Item
	CheckpointError error
}

func cloneSnapshot(s EngineSnapshot) EngineSnapshot {
	s.Workbench = append([]string(nil), s.Workbench...)
	s.Todos = append([]todo.Item(nil), s.Todos...)
	s.History = append([]session.Message(nil), s.History...)
	for i := range s.History {
		s.History[i].Blocks = append([]session.ContentItem(nil), s.History[i].Blocks...)
		for j := range s.History[i].Blocks {
			block := &s.History[i].Blocks[j]
			block.Input = append([]byte(nil), block.Input...)
		}
	}
	return s
}
