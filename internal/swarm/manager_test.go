package swarm

import (
	"sync"
	"testing"
	"time"
)

type mockDispatcher struct {
	mu       sync.Mutex
	messages []any
}

func (m *mockDispatcher) Dispatch(msg any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
}

func (m *mockDispatcher) getMessages() []any {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]any, len(m.messages))
	copy(res, m.messages)
	return res
}

func TestSwarmManager_SubmitConcurrency(t *testing.T) {
	// Create a morphology with a single node restricted to 2 concurrent tasks
	morph := &Morphology{
		Nodes: map[string]Node{
			"worker": {MaxConcurrency: 2},
		},
	}

	dispatcher := &mockDispatcher{}
	mgr := NewManager(morph, dispatcher)

	// Keep track of how many tasks are actively running
	var active int
	var maxActive int
	var mu sync.Mutex

	// We will submit 5 tasks
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		err := mgr.Submit("task_id", "worker", func() {
			defer wg.Done()
			
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()

			// Simulate work
			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			active--
			mu.Unlock()
		})
		
		if err != nil {
			t.Fatalf("Submit error: %v", err)
		}
	}

	// Wait for all to complete
	wg.Wait()

	if maxActive > 2 {
		t.Errorf("Max concurrency breached: allowed 2, got %d", maxActive)
	}

	// Verify messages were dispatched
	msgs := dispatcher.getMessages()
	if len(msgs) != 5 {
		t.Errorf("Expected 5 TaskStartedMsg, got %d", len(msgs))
	}
	for _, m := range msgs {
		if _, ok := m.(TaskStartedMsg); !ok {
			t.Errorf("Expected TaskStartedMsg, got %T", m)
		}
	}
}

func TestSwarmManager_UnknownAssignee(t *testing.T) {
	morph := &Morphology{
		Nodes: map[string]Node{},
	}

	mgr := NewManager(morph, nil)
	
	// Submitting to an unknown node should just safely return nil and not panic or run the closure
	ran := false
	err := mgr.Submit("task", "unknown_node", func() {
		ran = true
	})
	
	if err != nil {
		t.Errorf("Expected nil error for unknown node, got %v", err)
	}
	
	if ran {
		t.Error("Closure ran for unknown node")
	}
}
