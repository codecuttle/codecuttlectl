package main

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
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

func (t *opticRecallTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params opticRecallInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Query == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "query is required",
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

	// TODO: Connect to OpticStore, execute HybridRAG, and return fused narrative chunks.
	// For now, this is a stub.

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Optic Lobe search for '%s' (repo: %s, max_hops: %d, limit: %d)\n\n[STUB: No results found or DB not connected]", params.Query, params.RepoID, maxHops, limit),
	}, nil
}

func main() {
	pluginkit.Serve(&opticRecallTool{})
}
