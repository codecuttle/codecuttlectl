package backlog

import "time"

// Store defines the interface for persisting and retrieving work items.
// The initial implementation uses local JSON files (FileStore).
// A future implementation will use S3/GCS for team-wide visibility.
type Store interface {
	// Create persists a new work item and returns its generated ID.
	// The item's ID, CreatedAt, and UpdatedAt fields are set automatically.
	Create(item *WorkItem) (string, error)

	// Save persists an updated work item. The item must already exist.
	// UpdatedAt is set automatically.
	Save(id string, item *WorkItem) error

	// Load retrieves a work item by ID.
	Load(id string) (*WorkItem, error)

	// List returns work item summaries matching the given filter.
	// Results are sorted by priority descending, then updated_at descending.
	List(filter ListFilter) ([]WorkItemSummary, error)

	// Delete removes a work item from storage.
	Delete(id string) error

	// Prune removes work items with status done or rejected that are older
	// than maxAge (based on updated_at). Returns the number of items deleted.
	Prune(maxAge time.Duration) (int, error)
}
