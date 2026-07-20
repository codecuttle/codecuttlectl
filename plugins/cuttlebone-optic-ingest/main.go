package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"

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

// extractSymbols is a scaffold for using tree-sitter to parse code into semantic blocks.
func extractSymbols(code []byte, lang string) ([]string, error) {
	parser := sitter.NewParser()
	
	switch lang {
	case "go":
		parser.SetLanguage(golang.GetLanguage())
	case "python":
		parser.SetLanguage(python.GetLanguage())
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}

	tree, err := parser.ParseCtx(context.Background(), nil, code)
	if err != nil {
		return nil, err
	}

	// This is a minimal scaffold. A full implementation would use Tree-sitter queries 
	// (*.scm files) to capture specific nodes like function_declaration or class_definition.
	var symbols []string
	root := tree.RootNode()
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if strings.Contains(child.Type(), "function") || strings.Contains(child.Type(), "class") {
			symbols = append(symbols, child.Type())
		}
	}

	return symbols, nil
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

	// TODO: Connect to OpticStore
	// 1. Run `git diff-tree -r --no-commit-id --name-only <commit_hash>` to find changed files
	// 2. Extract contents of changed files using `git show <commit_hash>:<file>`
	// 3. For each file, run `extractSymbols` using Tree-sitter to find semantic AST blocks
	// 4. Construct `opticlobe.CommitData` with AST `CodeNode`s and `EVOLVED_FROM` edges.
	// 5. Store.IngestCommit(ctx, params.RepoID, commitData)

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Successfully processed commit %s using Tree-sitter for repo %s (STUB: connection and diff algorithms pending)", params.CommitHash, params.RepoID),
	}, nil
}

func main() {
	pluginkit.Serve(&opticIngestTool{})
}
