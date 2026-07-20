package opticlobe

import (
	"context"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/audit"
)

// OpticStore defines the interface for Codecuttle's hierarchical memory system.
// It abstracts away the complexity of the underlying storage mechanism, ensuring
// graceful degradation if PostgreSQL (Optic Lobe) is unavailable.
type OpticStore interface {
	// IngestCommit processes a new Git commit, generating temporal code nodes
	// and EVOLVED_FROM edges (Interaction Graph).
	IngestCommit(ctx context.Context, repoID string, commit CommitData) error

	// AddInsight adds a global rule or heuristic to the Insight Graph.
	AddInsight(ctx context.Context, workspaceID string, insight InsightData) (string, error)

	// RecallContext executes a HybridRAG query, fusing semantic vector search
	// with structural graph traversals, returning condensed narrative summaries.
	RecallContext(ctx context.Context, query string, queryEmbedding []float32, filter RecallFilter) ([]ContextChunk, error)

	// RecordAudit logs a graph retrieval or mutation event for enterprise governance.
	RecordAudit(logger *audit.Logger, sessionID string, action string, metadata map[string]interface{}) error

	// IsAvailable returns true if the backing store is fully functional (e.g., Postgres is online).
	IsAvailable() bool

	// Close terminates the connection to the store.
	Close() error
}

// CommitData represents the structural changes extracted from a git commit via JIT AST parsing.
type CommitData struct {
	Hash             string
	Message          string
	MessageEmbedding []float32
	AuthorID         string
	Timestamp        time.Time
	Nodes            []CodeNode
	Edges            []CodeEdge
}

// CodeNode represents a semantic symbol (Function, Class) in the Code Property Graph.
type CodeNode struct {
	ID               string
	FilePath         string
	SymbolName       string
	NodeType         string
	Content          string
	ContentEmbedding []float32
	ValidFromCommit  string
	ValidToCommit    *string // nil if still active
}

// CodeEdge represents a relationship between code nodes (e.g., EVOLVED_FROM, CALLS).
type CodeEdge struct {
	FromNodeID string
	ToNodeID   string
	Label      string // "EVOLVED_FROM" | "CALLS"
	CommitHash string // Only for EVOLVED_FROM
}

// InsightData represents a global rule or heuristic.
type InsightData struct {
	ID       string
	Content  string
	Embedding []float32
	AuthorID string
}

// RecallFilter defines constraints for memory retrieval to prevent semantic drift.
type RecallFilter struct {
	WorkspaceID  string
	RepositoryID string
	TargetCommit string // Ensure we query the graph at a specific point in time
	MaxHops      int    // Max depth for graph traversal
	Limit        int
}

// ContextChunk is a fused, LLM-ready narrative or code block extracted from the graph.
type ContextChunk struct {
	ID         string
	Content    string
	Type       string  // "raw_code" | "narrative_summary" | "insight"
	Confidence float64 // RRF (Reciprocal Rank Fusion) score
}
