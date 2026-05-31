// cuttlebone-list-directory is a Cuttlebone plugin that lists directory contents.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type listDirTool struct{}

func (t *listDirTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "list_directory",
		Description: "List the contents of a directory. Returns entries with a trailing / for subdirectories. Does not recurse into subdirectories.",
		InputSchema: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Absolute path to the directory to list"
				}
			},
			"required": ["path"]
		}`,
		LlmContextHint: "Use list_directory to understand project structure before navigating. Check directory layout before creating files to avoid placing them in the wrong location.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    10,
		},
	}, nil
}

type listDirInput struct {
	Path string `json:"path"`
}

func (t *listDirTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params listDirInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Path == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "path is required",
		}, nil
	}

	entries, err := os.ReadDir(params.Path)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("reading directory: %v", err),
		}, nil
	}

	var result strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		fmt.Fprintln(&result, name)
	}

	return &pb.ExecuteResponse{
		Output: result.String(),
		Metadata: map[string]string{
			"entry_count": fmt.Sprintf("%d", len(entries)),
		},
	}, nil
}

func main() {
	pluginkit.Serve(&listDirTool{})
}
