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
	chunks, err := store.RecallContext(ctx, "query", RecallFilter{})
	if err != nil {
		t.Errorf("RecallContext failed: %v", err)
	}
	if chunks != nil {
		t.Errorf("Expected nil chunks from local store stub, got %v", chunks)
	}

	// Test RecordAudit
	err = store.RecordAudit(ctx, "test_action", map[string]interface{}{})
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
	err = store.IngestCommit(ctx, "repo_123", CommitData{})
	if err != nil {
		t.Errorf("IngestCommit failed: %v", err)
	}

	id, err := store.AddInsight(ctx, "workspace_123", InsightData{})
	if err != nil {
		t.Errorf("AddInsight failed: %v", err)
	}
	if id != "" {
		t.Errorf("Expected empty ID from postgres store stub, got %v", id)
	}

	chunks, err := store.RecallContext(ctx, "query", RecallFilter{})
	if err != nil {
		t.Errorf("RecallContext failed: %v", err)
	}
	if chunks == nil {
		t.Errorf("Expected empty slice of chunks from postgres store stub, got nil")
	}

	err = store.RecordAudit(ctx, "test_action", map[string]interface{}{})
	if err != nil {
		t.Errorf("RecordAudit failed: %v", err)
	}
}
