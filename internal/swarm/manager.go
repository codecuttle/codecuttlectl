package swarm

import (
	"sync"
)

// Manager orchestrates asynchronous background tasks for the Swarm.
// It manages node concurrency limits via a worker pool system to ensure
// we don't exhaust API rate limits or local resources.
type Manager struct {
	morph      *Morphology
	dispatcher EventDispatcher
	
	// worker semaphores per NodeID
	semaphores map[string]chan struct{}
	mu         sync.Mutex
}

// NewManager creates a new swarm manager configured with the given morphology.
func NewManager(morph *Morphology, dispatcher EventDispatcher) *Manager {
	semaphores := make(map[string]chan struct{})
	
	// Initialize a semaphore for each node based on its MaxConcurrency.
	if morph != nil {
		for nodeID, node := range morph.Nodes {
			// Default to 1 if not specified or invalid
			concurrency := node.MaxConcurrency
			if concurrency <= 0 {
				concurrency = 1
			}
			semaphores[nodeID] = make(chan struct{}, concurrency)
		}
	}
	
	return &Manager{
		morph:      morph,
		dispatcher: dispatcher,
		semaphores: semaphores,
	}
}

// Submit enqueues an asynchronous task for a specific node.
// It will execute the task as soon as a worker slot is available for that node.
func (m *Manager) Submit(taskID, assignee string, run func()) error {
	m.mu.Lock()
	sem, ok := m.semaphores[assignee]
	m.mu.Unlock()

	// If the node isn't defined in the morphology, we can't route to it.
	if !ok {
		return nil // Should handle this with a clear error in the next iteration
	}

	// Execute the background task
	go func() {
		// Acquire a worker token for this specific node
		sem <- struct{}{}

		defer func() {
			// Release the worker token when done
			<-sem
		}()
		
		// Let the main thread know we started
		if m.dispatcher != nil {
			m.dispatcher.Dispatch(TaskStartedMsg{
				TaskID:   taskID,
				Assignee: assignee,
			})
		}
		
		run()
	}()
	
	return nil
}
