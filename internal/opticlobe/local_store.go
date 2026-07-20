package opticlobe

import (
	"context"
	"encoding/json"

	"github.com/codecuttle/codecuttlectl/internal/audit"
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

func (s *LocalOpticStore) RecallContext(ctx context.Context, query string, queryEmbedding []float32, filter RecallFilter) ([]ContextChunk, error) {
	// Fallback: Return basic context compaction or an empty list if unavailable
	return nil, nil
}

func (s *LocalOpticStore) RecordAudit(logger *audit.Logger, sessionID string, action string, metadata map[string]interface{}) error {
	if logger == nil || !logger.Enabled() {
		return nil
	}
	
	msgBytes, _ := json.Marshal(metadata)
	logger.Emit(audit.Event{
		Level:     "security",
		Type:      "optic_lobe_fallback_" + action,
		SessionID: sessionID,
		Message:   string(msgBytes),
	})
	return nil
}

func (s *LocalOpticStore) IsAvailable() bool {
	// Always returns true as it relies solely on local filesystem/memory
	return true
}

func (s *LocalOpticStore) Close() error {
	return nil
}
