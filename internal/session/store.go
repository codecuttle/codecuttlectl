package session

import "time"

// Store defines the interface for persisting and retrieving sessions.
// The initial implementation uses local JSON files (FileStore).
// A future implementation will use PostgreSQL with pgvector + Apache AGE
// for the full Optic Lobe architecture.
type Store interface {
	// Create initializes a new session with the given metadata.
	// Returns the generated session ID.
	Create(meta SessionMeta) (string, error)

	// Save persists the full session state to storage.
	// This is called after each tool execution and at the end of each turn.
	Save(id string, state *SessionState) error

	// Load retrieves the full session state by ID.
	Load(id string) (*SessionState, error)

	// List returns recent session metadata, sorted by updated_at descending.
	// The limit parameter controls the maximum number of results.
	List(limit int) ([]SessionMeta, error)

	// Delete removes a session from storage.
	Delete(id string) error

	// Prune removes sessions older than maxAge (based on updated_at).
	// Returns the number of sessions deleted.
	Prune(maxAge time.Duration) (int, error)
}
