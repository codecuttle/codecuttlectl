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
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type bashExecTool struct{}

type bashExecInput struct {
	Command string        `json:"command" jsonschema:"required" jsonschema_description:"The bash command to execute"`
	WorkDir string        `json:"workdir,omitempty" jsonschema_description:"Working directory for the command. Defaults to the session working directory."`
	Timeout types.FlexInt `json:"timeout,omitempty" jsonschema_description:"Timeout in seconds. Default: 120"`
}

func (t *bashExecTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "bash_exec",
		Description: "Execute a bash command and return its stdout and stderr. Use for running builds, tests, installing dependencies, or any shell operation.",
		InputSchema: schema.MustSchema(&bashExecInput{}),
		LlmContextHint: `Use bash_exec for system commands: building, testing, installing packages, running programs. Check exit codes in output for errors. Never run destructive commands (rm -rf /, DROP DATABASE) without explicit user permission.

IMPORTANT: Do NOT use bash_exec for operations that have a dedicated tool:
- Git operations → use the 'git' tool (not 'git commit' via bash)
- GitHub API → use the 'github' tool (not curl to api.github.com)
- File read/write/edit → use read_file, write_file, edit_file
- File search → use grep, glob
- Directory listing → use list_directory
- Web search/fetch → use websearch, webfetch

bash_exec is for: make, go build, go test, apt install, pip install, npm, docker, and other build/run/install operations with no dedicated tool.`,
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    300,
		},
	}, nil
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

	// Self-monitoring: detect when bash_exec is being used for operations
	// that have dedicated tools. Warn in the response so the Inkwell/reconciler
	// can flag this pattern.
	if warning := detectToolMisuse(params.Command); warning != "" {
		// Still execute the command (don't block), but prepend a warning
		// that surfaces to the model for self-correction.
		defer func() {}() // placeholder for future Inkwell integration
		_ = warning       // Used below in response
	}
	timeout := params.Timeout.Int()
	if timeout == 0 {
		timeout = 120
	}

	// Use working directory from params, fall back to request context
	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
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
			Output:   fmt.Sprintf("Command timed out after %d seconds\n\n%s", timeout, result),
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

	// If tool misuse was detected, prepend warning to output
	if warning := detectToolMisuse(params.Command); warning != "" {
		metadata["tool_misuse_warning"] = warning
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("⚠️ TOOL DISCIPLINE WARNING: %s\n\n%s", warning, result),
			Metadata: metadata,
		}, nil
	}

	return &pb.ExecuteResponse{
		Output:   result,
		Metadata: metadata,
	}, nil
}

// detectToolMisuse checks if a bash command is doing something that a
// dedicated tool should handle. Returns a warning message if detected.
func detectToolMisuse(command string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	// Git operations (should use 'git' tool)
	gitPrefixes := []string{"git commit", "git add", "git push", "git pull",
		"git checkout", "git branch", "git merge", "git rebase", "git stash",
		"git diff", "git log", "git status", "git tag", "git fetch", "git remote"}
	for _, prefix := range gitPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return fmt.Sprintf("Use the 'git' tool instead of bash_exec for '%s'. The git tool provides safety checks and proper error handling.", prefix)
		}
	}

	// GitHub API calls (should use 'github' tool)
	if strings.Contains(lower, "api.github.com") || strings.Contains(lower, "gh pr ") ||
		strings.Contains(lower, "gh issue ") || strings.Contains(lower, "gh repo ") {
		return "Use the 'github' tool instead of bash_exec for GitHub API operations."
	}

	// curl for web fetching (should use 'webfetch' tool)
	if (strings.HasPrefix(lower, "curl ") || strings.HasPrefix(lower, "wget ")) &&
		!strings.Contains(lower, "localhost") && !strings.Contains(lower, "127.0.0.1") {
		return "Consider using the 'webfetch' tool for fetching URL content instead of curl/wget via bash_exec."
	}

	return ""
}

func main() {
	pluginkit.Serve(&bashExecTool{})
}
