package opticlobe

import (
	"context"
)

// LocalOpticStore provides a fallback memory implementation when PostgreSQL is unavailable.
// It relies on local JSON files and basic in-memory tracking (Phase 2 context compaction).
type LocalOpticStore struct {
	// Add local file path or in-memory map references here
}

func NewLocalOpticStore() *LocalOpticStore {
	return &LocalOpticStore{}
}

func (s *LocalOpticStore) IngestCommit(ctx context.Context, repoID string, commit CommitData) error {
	// Fallback: Skip structural ingestion or write to a local JSON log
	return nil
}

func (s *LocalOpticStore) AddInsight(ctx context.Context, workspaceID string, insight InsightData) (string, error) {
	// Fallback: Append to local session state or skills markdown
	return "", nil
}

func (s *LocalOpticStore) RecallContext(ctx context.Context, query string, filter RecallFilter) ([]ContextChunk, error) {
	// Fallback: Return basic context compaction or an empty list if unavailable
	return nil, nil
}

func (s *LocalOpticStore) RecordAudit(ctx context.Context, action string, metadata map[string]interface{}) error {
	// Fallback: Write structured JSON logs to local audit file
	return nil
}

func (s *LocalOpticStore) IsAvailable() bool {
	// Always returns true as it relies solely on local filesystem/memory
	return true
}

func (s *LocalOpticStore) Close() error {
	return nil
}
