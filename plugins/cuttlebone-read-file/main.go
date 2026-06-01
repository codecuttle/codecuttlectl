// cuttlebone-read-file is a Cuttlebone plugin that reads file contents.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type readFileTool struct{}

func (t *readFileTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "read_file",
		Description: "Read the contents of a file at the given absolute path. Returns the file contents with line numbers prefixed. Use offset and limit to read specific sections of large files.",
		InputSchema: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Absolute path to the file to read"
				},
				"offset": {
					"type": "integer",
					"description": "Line number to start reading from (0-indexed). Default: 0"
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of lines to read. Default: 2000"
				}
			},
			"required": ["path"]
		}`,
		LlmContextHint: "Use read_file to inspect file contents before making edits. Always read a file before modifying it. Use offset/limit for large files to avoid overwhelming context.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

type readFileInput struct {
	Path   string      `json:"path"`
	Offset flexInt     `json:"offset,omitempty"`
	Limit  flexInt     `json:"limit,omitempty"`
}

// flexInt handles both integer and string-encoded integer values from LLM JSON.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	// Try integer first
	var i int
	if err := json.Unmarshal(data, &i); err == nil {
		*f = flexInt(i)
		return nil
	}
	// Try string-encoded integer
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		i, err := strconv.Atoi(s)
		if err == nil {
			*f = flexInt(i)
			return nil
		}
	}
	*f = 0
	return nil
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
	limit := int(params.Limit)
	if limit == 0 {
		limit = 2000
	}
	offset := int(params.Offset)

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
