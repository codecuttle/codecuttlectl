// cuttlebone-edit-file is a Cuttlebone plugin that performs exact string replacements in files.
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

type editFileTool struct{}

func (t *editFileTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "edit_file",
		Description: "Perform an exact string replacement in a file. Finds the first occurrence of 'old_string' and replaces it with 'new_string'. Use 'replace_all' to replace every occurrence. The file must exist. Always read a file before editing it to ensure you have the correct content to match.",
		InputSchema: `{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Absolute path to the file to edit"
				},
				"old_string": {
					"type": "string",
					"description": "The exact string to find and replace. Must match file content exactly including whitespace and newlines."
				},
				"new_string": {
					"type": "string",
					"description": "The replacement string"
				},
				"replace_all": {
					"type": "boolean",
					"description": "If true, replace all occurrences. Default: false (replace first only)"
				}
			},
			"required": ["path", "old_string", "new_string"]
		}`,
		LlmContextHint: "Use edit_file for surgical modifications to existing files. Always read_file first to see current content. The old_string must match exactly — include sufficient surrounding context (3-5 lines) to ensure a unique match. Prefer edit_file over write_file when modifying existing files.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: false,
			MaxTimeoutSeconds:    10,
		},
	}, nil
}

type editFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *editFileTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params editFileInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Path == "" {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "path is required"}, nil
	}
	if params.OldString == "" {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "old_string is required"}, nil
	}
	if params.OldString == params.NewString {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "old_string and new_string are identical"}, nil
	}

	data, err := os.ReadFile(params.Path)
	if err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("reading file: %v", err),
		}, nil
	}

	content := string(data)

	// Check that old_string exists in the file
	count := strings.Count(content, params.OldString)
	if count == 0 {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "old_string not found in file content",
		}, nil
	}

	// Check for ambiguity when not using replace_all
	if !params.ReplaceAll && count > 1 {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("found %d matches for old_string — provide more surrounding context to make it unique, or set replace_all: true", count),
		}, nil
	}

	// Perform the replacement
	var newContent string
	if params.ReplaceAll {
		newContent = strings.ReplaceAll(content, params.OldString, params.NewString)
	} else {
		newContent = strings.Replace(content, params.OldString, params.NewString, 1)
	}

	// Write back
	if err := os.WriteFile(params.Path, []byte(newContent), 0644); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("writing file: %v", err),
		}, nil
	}

	replacements := 1
	if params.ReplaceAll {
		replacements = count
	}

	return &pb.ExecuteResponse{
		Output: fmt.Sprintf("Edit applied: %d replacement(s) made in %s", replacements, params.Path),
		Metadata: map[string]string{
			"replacements": fmt.Sprintf("%d", replacements),
			"path":         params.Path,
		},
	}, nil
}

func main() {
	pluginkit.Serve(&editFileTool{})
}
