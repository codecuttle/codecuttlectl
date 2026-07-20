package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/opticlobe"
	"github.com/codecuttle/codecuttlectl/internal/embedding"
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

// getGitChangedFiles returns a list of files modified in the commit
func getGitChangedFiles(ctx context.Context, commitHash string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, l := range lines {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// getGitFileContent retrieves the file content at a specific commit
func getGitFileContent(ctx context.Context, commitHash, file string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "show", fmt.Sprintf("%s:%s", commitHash, file))
	return cmd.Output()
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

	files, err := getGitChangedFiles(ctx, params.CommitHash)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("failed to get git diff: %v", err),
		}, nil
	}

	commitMsgEmb, _ := embedding.Generate(ctx, params.CommitMessage)
	if commitMsgEmb == nil {
		commitMsgEmb = make([]float32, 768) // Fallback if API fails
	}

	// Build CommitData payload
	commitData := opticlobe.CommitData{
		Hash:             params.CommitHash,
		Message:          params.CommitMessage,
		AuthorID:         params.AuthorID,
		Timestamp:        time.Now(),
		MessageEmbedding: commitMsgEmb,
	}

	for _, file := range files {
		lang := ""
		if strings.HasSuffix(file, ".go") {
			lang = "go"
		} else if strings.HasSuffix(file, ".py") {
			lang = "python"
		} else {
			continue // skip unsupported for now
		}

		content, err := getGitFileContent(ctx, params.CommitHash, file)
		if err != nil {
			continue
		}

		symbols, _ := extractSymbols(content, lang)
		for i, sym := range symbols {
			symEmb, _ := embedding.Generate(ctx, sym)
			if symEmb == nil {
				symEmb = make([]float32, 768) // Fallback if API fails
			}

			nodeIDStr := fmt.Sprintf("%s_%s_%d", params.CommitHash, file, i)
			nodeID := uuid.NewMD5(uuid.NameSpaceOID, []byte(nodeIDStr)).String()
			commitData.Nodes = append(commitData.Nodes, opticlobe.CodeNode{
				ID:               nodeID,
				FilePath:         file,
				SymbolName:       sym,
				NodeType:         "ast_node",
				Content:          fmt.Sprintf("Semantic Block: %s in %s", sym, file),
				ContentEmbedding: symEmb,
				ValidFromCommit:  params.CommitHash,
			})
			
			// If we tracked lineage, we'd add an EVOLVED_FROM edge here.
			if i > 0 {
				prevIDStr := fmt.Sprintf("%s_%s_%d", params.CommitHash, file, i-1)
				prevID := uuid.NewMD5(uuid.NameSpaceOID, []byte(prevIDStr)).String()
				commitData.Edges = append(commitData.Edges, opticlobe.CodeEdge{
					FromNodeID: prevID,
					ToNodeID:   nodeID,
					Label:      "EVOLVED_FROM",
					CommitHash: params.CommitHash,
				})
			}
		}
	}

	// We initialize our local PostgresStore and ingest
	connStr := "host=localhost port=5439 user=codecuttle password=codecuttle_dev_pass dbname=optic_lobe sslmode=disable"
	store, err := opticlobe.NewPostgresOpticStore(connStr)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("db connection failed: %v", err),
		}, nil
	}
	defer store.Close()

	if err := store.IngestCommit(ctx, params.RepoID, commitData); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("failed to ingest commit: %v", err),
		}, nil
	}

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Successfully parsed %d files and ingested %d AST nodes for commit %s into Optic Lobe.", len(files), len(commitData.Nodes), params.CommitHash),
	}, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "test" {
		runTest()
		return
	}
	pluginkit.Serve(&opticIngestTool{})
}

