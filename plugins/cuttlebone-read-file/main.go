// cuttlebone-read-file is a Cuttlebone plugin that reads file contents.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type readFileTool struct{}

type readFileInput struct {
	Path   string        `json:"path" jsonschema:"required" jsonschema_description:"Absolute path to the file to read"`
	Offset types.FlexInt `json:"offset,omitempty" jsonschema_description:"Line number to start reading from (0-indexed). Default: 0"`
	Limit  types.FlexInt `json:"limit,omitempty" jsonschema_description:"Maximum number of lines to read. Default: 2000"`
}

func (t *readFileTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "read_file",
		Description: "Read the contents of a file at the given absolute path. Returns the file contents with line numbers prefixed. Use offset and limit to read specific sections of large files.",
		InputSchema: schema.MustSchema(&readFileInput{}),
		LlmContextHint: "Use read_file to inspect file contents before making edits. Always read a file before modifying it. Use offset/limit for large files to avoid overwhelming context.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

func (t *readFileTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params readFileInput
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
	limit := params.Limit.Int()
	if limit == 0 {
		limit = 2000
	}
	offset := params.Offset.Int()

	data, err := os.ReadFile(params.Path)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("reading file: %v", err),
		}, nil
	}

	lines := strings.Split(string(data), "\n")
	start := offset
	if start >= len(lines) {
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("(file has %d lines, offset %d is beyond end)", len(lines), start),
			Metadata: map[string]string{"total_lines": fmt.Sprintf("%d", len(lines))},
		}, nil
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}

	var result strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&result, "%d: %s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&result, "\n(showing lines %d-%d of %d total)", start+1, end, len(lines))
	}

	return &pb.ExecuteResponse{
		Output: result.String(),
		Metadata: map[string]string{
			"total_lines": fmt.Sprintf("%d", len(lines)),
			"shown_from":  fmt.Sprintf("%d", start+1),
			"shown_to":    fmt.Sprintf("%d", end),
		},
	}, nil
}

func main() {
	pluginkit.Serve(&readFileTool{})
}
