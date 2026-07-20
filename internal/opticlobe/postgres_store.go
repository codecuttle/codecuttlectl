package opticlobe

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/audit"
	_ "github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

// PostgresOpticStore implements OpticStore using PostgreSQL 16 + pgvector + Recursive CTEs (polyfilling SQL/PGQ).
type PostgresOpticStore struct {
	db *sql.DB
}

func NewPostgresOpticStore(connStr string) (*PostgresOpticStore, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresOpticStore{db: db}, nil
}

func (s *PostgresOpticStore) IngestCommit(ctx context.Context, repoID string, commit CommitData) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Insert Commit
	var commitEmb interface{}
	if len(commit.MessageEmbedding) > 0 {
		commitEmb = pgvector.NewVector(commit.MessageEmbedding)
	}
	
	_, err = tx.ExecContext(ctx, `
		INSERT INTO commits (hash, repository_id, author_id, message, message_embedding, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (hash) DO NOTHING
	`, commit.Hash, repoID, commit.AuthorID, commit.Message, commitEmb, commit.Timestamp)
	if err != nil {
		return fmt.Errorf("failed inserting commit: %w", err)
	}

	// 2. Insert Nodes
	for _, n := range commit.Nodes {
		var contentEmb interface{}
		if len(n.ContentEmbedding) > 0 {
			contentEmb = pgvector.NewVector(n.ContentEmbedding)
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO code_nodes (id, repository_id, file_path, symbol_name, node_type, content, content_embedding, valid_from_commit, valid_to_commit)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				valid_to_commit = EXCLUDED.valid_to_commit
		`, n.ID, repoID, n.FilePath, n.SymbolName, n.NodeType, n.Content, contentEmb, n.ValidFromCommit, n.ValidToCommit)
		if err != nil {
			return fmt.Errorf("failed inserting code_node %s: %w", n.ID, err)
		}
	}

	// 3. Insert Edges (EVOLVED_FROM)
	for _, e := range commit.Edges {
		if e.Label == "EVOLVED_FROM" {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO code_evolutions (from_node_id, to_node_id, commit_hash)
				VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING
			`, e.FromNodeID, e.ToNodeID, e.CommitHash)
			if err != nil {
				return fmt.Errorf("failed inserting code_evolution edge %s->%s: %w", e.FromNodeID, e.ToNodeID, err)
			}
		}
	}

	return tx.Commit()
}

func (s *PostgresOpticStore) AddInsight(ctx context.Context, workspaceID string, insight InsightData) (string, error) {
	var id string
	var emb interface{}
	if len(insight.Embedding) > 0 {
		emb = pgvector.NewVector(insight.Embedding)
	}

	err := s.db.QueryRowContext(ctx, `
		INSERT INTO insights (workspace_id, author_id, content, embedding)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, workspaceID, insight.AuthorID, insight.Content, emb).Scan(&id)
	
	if err != nil {
		return "", fmt.Errorf("failed to add insight: %w", err)
	}
	return id, nil
}

func (s *PostgresOpticStore) RecallContext(ctx context.Context, query string, queryEmbedding []float32, filter RecallFilter) ([]ContextChunk, error) {
	// HybridRAG Implementation via Recursive CTEs (PG 16 Fallback for SQL/PGQ)
	// We do a fast vector retrieval, then traverse EVOLVED_FROM edges up to MaxHops.
	
	q := `
	WITH RECURSIVE semantic_matches AS (
		SELECT id, content, 
		       1.0 - (content_embedding <=> $1::vector) AS semantic_similarity
		FROM code_nodes
		WHERE repository_id = $2
		ORDER BY content_embedding <=> $1::vector
		LIMIT $3
	),
	evolution_graph AS (
		-- Base Step
		SELECT sm.id, sm.content, sm.semantic_similarity, 0 AS hop_depth
		FROM semantic_matches sm

		UNION ALL

		-- Recursive Step
		SELECT cn.id, cn.content, eg.semantic_similarity, eg.hop_depth + 1
		FROM code_nodes cn
		JOIN code_evolutions ce ON cn.id = ce.from_node_id
		JOIN evolution_graph eg ON ce.to_node_id = eg.id
		WHERE eg.hop_depth < $4
	)
	SELECT id, content, semantic_similarity
	FROM evolution_graph
	ORDER BY semantic_similarity DESC, hop_depth ASC
	LIMIT $3;
	`
	
	vec := pgvector.NewVector(queryEmbedding)
	rows, err := s.db.QueryContext(ctx, q, vec, filter.RepositoryID, filter.Limit, filter.MaxHops)
	if err != nil {
		return nil, fmt.Errorf("failed to query graph: %w", err)
	}
	defer rows.Close()

	var chunks []ContextChunk
	for rows.Next() {
		var chunk ContextChunk
		if err := rows.Scan(&chunk.ID, &chunk.Content, &chunk.Confidence); err != nil {
			return nil, err
		}
		chunk.Type = "raw_code"
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *PostgresOpticStore) RecordAudit(logger *audit.Logger, sessionID string, action string, metadata map[string]interface{}) error {
	if logger == nil || !logger.Enabled() {
		return nil
	}
	
	msgBytes, _ := json.Marshal(metadata)
	logger.Emit(audit.Event{
		Level:     "security",
		Type:      "optic_lobe_" + action,
		SessionID: sessionID,
		Message:   string(msgBytes),
	})
	return nil
}

func (s *PostgresOpticStore) IsAvailable() bool {
	if s.db == nil {
		return false
	}
	err := s.db.Ping()
	return err == nil
}

func (s *PostgresOpticStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

