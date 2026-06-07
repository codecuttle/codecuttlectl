// cuttlebone-write-file is a Cuttlebone plugin that writes content to files.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

type writeFileTool struct{}

type writeFileInput struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"Absolute path to the file to write"`
	Content string `json:"content" jsonschema:"required" jsonschema_description:"The full content to write to the file"`
}

func (t *writeFileTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "write_file",
		Description: "Write content to a file at the given absolute path. Creates the file and any parent directories if they do not exist. Overwrites the file if it already exists.",
		InputSchema: schema.MustSchema(&writeFileInput{}),
		LlmContextHint: "Use write_file to create or overwrite files. Always use absolute paths. Parent directories are created automatically. You must read a file before overwriting it to understand existing content.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			RequiresConfirmation: false,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

func (t *writeFileTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params writeFileInput
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

	dir := filepath.Dir(params.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("creating directories: %v", err),
		}, nil
	}

	if err := os.WriteFile(params.Path, []byte(params.Content), 0644); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("writing file: %v", err),
		}, nil
	}

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), params.Path),
		Metadata: map[string]string{
			"bytes_written": fmt.Sprintf("%d", len(params.Content)),
			"path":          params.Path,
		},
	}, nil
}

func main() {
	pluginkit.Serve(&writeFileTool{})
}
