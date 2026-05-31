// cuttlebone-bash-exec is a Cuttlebone plugin that executes bash commands.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type bashExecTool struct{}

func (t *bashExecTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "bash_exec",
		Description: "Execute a bash command and return its stdout and stderr. Use for running builds, tests, installing dependencies, or any shell operation.",
		InputSchema: `{
			"type": "object",
			"properties": {
				"command": {
					"type": "string",
					"description": "The bash command to execute"
				},
				"workdir": {
					"type": "string",
					"description": "Working directory for the command. Defaults to the session working directory."
				},
				"timeout": {
					"type": "integer",
					"description": "Timeout in seconds. Default: 120"
				}
			},
			"required": ["command"]
		}`,
		LlmContextHint: "Use bash_exec for system commands: building, testing, installing packages, running programs. Check exit codes in output for errors. Never run destructive commands (rm -rf /, DROP DATABASE) without explicit user permission.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    300,
		},
	}, nil
}

type bashExecInput struct {
	Command string `json:"command"`
	WorkDir string `json:"workdir,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

func (t *bashExecTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params bashExecInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Command == "" {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: "command is required",
		}, nil
	}
	if params.Timeout == 0 {
		params.Timeout = 120
	}

	// Use working directory from params, fall back to request context
	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", params.Command)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()

	result := output.String()
	metadata := map[string]string{}

	if timeoutCtx.Err() == context.DeadlineExceeded {
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("Command timed out after %d seconds\n\n%s", params.Timeout, result),
			IsError:  true,
			Metadata: map[string]string{"timeout": "true"},
		}, nil
	}

	if err != nil {
		metadata["exit_error"] = err.Error()
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("Exit code: %s\n\n%s", err.Error(), result),
			Metadata: metadata,
		}, nil
	}

	metadata["exit_code"] = "0"
	return &pb.ExecuteResponse{
		Output:   result,
		Metadata: metadata,
	}, nil
}

func main() {
	pluginkit.Serve(&bashExecTool{})
}
