package opticlobe

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLocalOpticStore(t *testing.T) {
	store := NewLocalOpticStore()

	if !store.IsAvailable() {
		t.Error("Expected LocalOpticStore to be available")
	}

	ctx := context.Background()

	// Test IngestCommit
	err := store.IngestCommit(ctx, "repo_123", CommitData{})
	if err != nil {
		t.Errorf("IngestCommit failed: %v", err)
	}

	// Test AddInsight
	id, err := store.AddInsight(ctx, "workspace_123", InsightData{})
	if err != nil {
		t.Errorf("AddInsight failed: %v", err)
	}
	if id != "" {
		t.Errorf("Expected empty ID from local store stub, got %v", id)
	}

	// Test RecallContext
	chunks, err := store.RecallContext(ctx, "query", make([]float32, 1536), RecallFilter{})
	if err != nil {
		t.Errorf("RecallContext failed: %v", err)
	}
	if chunks != nil {
		t.Errorf("Expected nil chunks from local store stub, got %v", chunks)
	}

	// Test RecordAudit
	err = store.RecordAudit(nil, "session_123", "test_action", map[string]interface{}{})
	if err != nil {
		t.Errorf("RecordAudit failed: %v", err)
	}

	// Test Close
	err = store.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestPostgresOpticStore(t *testing.T) {
	// Skip if CI doesn't have Postgres, but for this dev environment, it should be running
	if os.Getenv("SKIP_POSTGRES_TESTS") == "1" {
		t.Skip("Skipping Postgres tests")
	}

	connStr := "host=localhost port=5439 user=codecuttle password=codecuttle_dev_pass dbname=optic_lobe sslmode=disable"
	store, err := NewPostgresOpticStore(connStr)
	
	if err != nil {
		// Just skip rather than fail if DB isn't fully up yet or not running in test env
		t.Logf("PostgreSQL not available or connection failed: %v. Skipping real DB tests.", err)
		t.Skip()
		return
	}
	defer store.Close()

	if !store.IsAvailable() {
		t.Error("Expected PostgresOpticStore to be available after successful connection")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test Stubs
	// Use valid UUIDs for PostgreSQL tests to avoid 22P02 errors
	dummyRepoUUID := "123e4567-e89b-12d3-a456-426614174000"
	dummyWorkspaceUUID := "223e4567-e89b-12d3-a456-426614174001"

	err = store.IngestCommit(ctx, dummyRepoUUID, CommitData{
		Hash: "mockhash123", 
		AuthorID: "323e4567-e89b-12d3-a456-426614174002", // Add dummy author id
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Errorf("IngestCommit failed: %v", err)
	}

	id, err := store.AddInsight(ctx, dummyWorkspaceUUID, InsightData{
		Content: "mock insight",
		AuthorID: "323e4567-e89b-12d3-a456-426614174002", // Add dummy author id
	})
	if err != nil {
		t.Errorf("AddInsight failed: %v", err)
	}
	if id == "" {
		t.Errorf("Expected valid UUID from postgres store, got empty")
	}

	chunks, err := store.RecallContext(ctx, "query", make([]float32, 1536), RecallFilter{
		RepositoryID: dummyRepoUUID,
		MaxHops: 2,
		Limit: 5,
	})
	if err != nil {
		t.Errorf("RecallContext failed: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("Expected empty slice of chunks from postgres store stub, got %v", chunks)
	}

	err = store.RecordAudit(nil, "session_123", "test_action", map[string]interface{}{})
	if err != nil {
		t.Errorf("RecordAudit failed: %v", err)
	}
}
