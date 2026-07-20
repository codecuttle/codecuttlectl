package main

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

type opticIngestTool struct{}

type opticIngestInput struct {
	RepoID        string `json:"repo_id" jsonschema:"required" jsonschema_description:"Repository identifier"`
	CommitHash    string `json:"commit_hash" jsonschema:"required" jsonschema_description:"The Git commit hash to ingest"`
	CommitMessage string `json:"commit_message,omitempty" jsonschema_description:"Commit message metadata"`
	AuthorID      string `json:"author_id,omitempty" jsonschema_description:"Author identifier metadata"`
}

func (t *opticIngestTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "optic_ingest",
		Description: "Extract AST structural changes from a Git commit using Tree-sitter and ingest them into the Optic Lobe graph database.",
		InputSchema: schema.MustSchema(&opticIngestInput{}),
		LlmContextHint: "Use this tool when Codecuttle detects new commits or receives a callback from a Git hook to keep the temporal codebase memory up to date.",
		Version:     "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    600, // Parsing large diffs might take time
		},
	}, nil
}

func (t *opticIngestTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params opticIngestInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.RepoID == "" || params.CommitHash == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "repo_id and commit_hash are required",
		}, nil
	}

	// TODO: Initialize connection to OpticStore, parse Tree-sitter diffs, and insert into DB.
	// For now, this is a stub.

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Successfully ingested commit %s into Optic Lobe for repo %s (STUB)", params.CommitHash, params.RepoID),
	}, nil
}

func main() {
	pluginkit.Serve(&opticIngestTool{})
}
