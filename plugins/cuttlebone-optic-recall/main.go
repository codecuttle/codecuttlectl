package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/opticlobe"
	"github.com/codecuttle/codecuttlectl/internal/embedding"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type opticRecallTool struct{}

type opticRecallInput struct {
	Query        string        `json:"query" jsonschema:"required" jsonschema_description:"The semantic natural language query to run against memory"`
	WorkspaceID  string        `json:"workspace_id,omitempty" jsonschema_description:"Workspace partition ID (filters insights/repos)"`
	RepoID       string        `json:"repo_id,omitempty" jsonschema_description:"Repository ID for precise meso-filtering"`
	TargetCommit string        `json:"target_commit,omitempty" jsonschema_description:"Optional commit hash to limit historical memory view to a specific point in time"`
	MaxHops      types.FlexInt `json:"max_hops,omitempty" jsonschema_description:"Maximum depth for SQL/PGQ graph traversal (default 2)"`
	Limit        types.FlexInt `json:"limit,omitempty" jsonschema_description:"Max results to return (default 5)"`
}

func (t *opticRecallTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "optic_recall",
		Description: "Retrieve semantic code history and architectural context using HybridRAG (pgvector + SQL/PGQ graph traversal).",
		InputSchema: schema.MustSchema(&opticRecallInput{}),
		LlmContextHint: "Use this tool to explore code provenance ('why did this function evolve?') or to check global architectural rules and user coding preferences.",
		Version:     "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    60,
		},
	}, nil
}

// createMockEmbedding returns a dummy vector representing the query embedding.
// In a full implementation, this calls out to Bedrock or Ollama.
func createMockEmbedding(ctx context.Context, query string) ([]float32, error) {
	return embedding.Generate(ctx, query)
}

func (t *opticRecallTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params opticRecallInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Query == "" || params.RepoID == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "query and repo_id are required",
		}, nil
	}

	maxHops := params.MaxHops.Int()
	if maxHops == 0 {
		maxHops = 2
	}
	limit := params.Limit.Int()
	if limit == 0 {
		limit = 5
	}

	connStr := "host=localhost port=5439 user=codecuttle password=codecuttle_dev_pass dbname=optic_lobe sslmode=disable"
	store, err := opticlobe.NewPostgresOpticStore(connStr)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("db connection failed: %v", err),
		}, nil
	}
	defer store.Close()

	emb, err := createMockEmbedding(ctx, params.Query)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("failed to generate query embedding: %v", err),
		}, nil
	}
	filter := opticlobe.RecallFilter{
		WorkspaceID:  params.WorkspaceID,
		RepositoryID: params.RepoID,
		TargetCommit: params.TargetCommit,
		MaxHops:      maxHops,
		Limit:        limit,
	}

	chunks, err := store.RecallContext(ctx, params.Query, emb, filter)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("Graph retrieval failed: %v", err),
		}, nil
	}

	if len(chunks) == 0 {
		return &pb.ExecuteResponse{
			Output: "No semantic context found in the Optic Lobe for this query.",
		}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Optic Lobe Retrieval Context (%d chunks retrieved):\n\n", len(chunks))
	for i, chunk := range chunks {
		fmt.Fprintf(&sb, "-- Chunk %d [ID: %s] (Confidence: %.4f) --\n%s\n\n", i+1, chunk.ID, chunk.Confidence, chunk.Content)
	}

	return &pb.ExecuteResponse{
		Output: sb.String(),
	}, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "test" {
		runTest()
		return
	}
	pluginkit.Serve(&opticRecallTool{})
}

