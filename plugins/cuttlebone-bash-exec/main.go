// cuttlebone-bash-exec is a Cuttlebone plugin that executes bash commands.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

type bashExecTool struct{}

// misuseTracker tracks repeated tool discipline violations per command pattern.
// The plugin process persists for the session, so this state accumulates.
var misuseTracker = struct {
	counts map[string]int
}{
	counts: make(map[string]int),
}

const maxMisuseBlocks = 3 // Allow through after 3 blocked attempts

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

CRITICAL TOOL DISCIPLINE: DO NOT use this tool for git commands (use 'git'), reading files (use 'read_file'), editing files (use 'edit_file'), grepping (use 'grep'), or listing directories (use 'list_directory'). Your request will be blocked if you attempt to bypass dedicated tools.

bash_exec is for: make, go build, go test, apt install, pip install, npm, docker, and other build/run/install operations with no dedicated tool.

CRITICAL TIMEOUT GUIDANCE: The default timeout is 120s. For long-running commands (e.g., docker build, large dependency installations, heavy test suites), you MUST proactively set the 'timeout' parameter to 300, 600, or higher. Do not wait for a timeout failure to increase it.`,
		Version: "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			SupportsStreaming:    true,
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

	// Dynamic tool discipline: check if this command should be handled by
	// a dedicated tool, using command_patterns from the available_tools registry.
	if warning := detectToolMisuseDynamic(params.Command, req.AvailableTools); warning != "" {
		patternKey := warning
		misuseTracker.counts[patternKey]++
		attempts := misuseTracker.counts[patternKey]

		if attempts <= maxMisuseBlocks {
			return &pb.ExecuteResponse{
				IsError: true,
				ErrorMessage: fmt.Sprintf("TOOL DISCIPLINE (attempt %d/%d): %s\n\nThe command was NOT executed. Use the appropriate tool instead. After %d failed attempts, the command will be allowed through.",
					attempts, maxMisuseBlocks, warning, maxMisuseBlocks),
				Metadata: map[string]string{
					"tool_misuse":  "true",
					"blocked_cmd":  params.Command,
					"attempt":      fmt.Sprintf("%d", attempts),
					"max_attempts": fmt.Sprintf("%d", maxMisuseBlocks),
				},
			}, nil
		}

		// After maxMisuseBlocks attempts, allow through but emit telemetry
		fmt.Fprintf(os.Stderr, "[TELEMETRY] TOOL_DISCIPLINE_OVERRIDE: command=%q warning=%q attempts=%d\n",
			params.Command, warning, attempts)
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

	// Capture stdout and stderr separately for telemetry.
	// Combined output is still returned to the model, but the plugin response
	// metadata carries separated stderr for Inkwell auditing.
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

	resp := commandOutcome(timeoutCtx, cmd.ProcessState, err, stdoutBuf.String(), stderrBuf.String())
	if resp.IsError {
		return resp, nil
	}
	metadata := resp.Metadata
	result := resp.Output

	// If this command was allowed through after repeated blocks, tag the output
	if warning := detectToolMisuseDynamic(params.Command, req.AvailableTools); warning != "" {
		metadata["tool_misuse_override"] = "true"
		metadata["override_reason"] = warning
		metadata["total_attempts"] = fmt.Sprintf("%d", misuseTracker.counts[warning])
		return &pb.ExecuteResponse{
			Output: fmt.Sprintf("⚠️ TOOL DISCIPLINE OVERRIDE (after %d blocked attempts): %s\n\n%s",
				misuseTracker.counts[warning], warning, result),
			Metadata: metadata,
		}, nil
	}

	return &pb.ExecuteResponse{
		Output:   result,
		Metadata: metadata,
	}, nil
}

// detectToolMisuseDynamic checks if a bash command matches any command_patterns
// declared by other loaded tools. This enables fully dynamic tool discipline —
// no hardcoded tool names or patterns in bash_exec itself.
// Returns a warning message if a match is found, empty string otherwise.
func detectToolMisuseDynamic(command string, tools []*pb.ToolInfo) string {
	if len(tools) == 0 {
		// Fallback to legacy detection if no tool registry provided.
		return detectToolMisuse(command)
	}

	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	for _, tool := range tools {
		if tool.Name == "bash_exec" {
			continue // Don't match against ourselves
		}
		for _, pattern := range tool.CommandPatterns {
			if matchCommandPattern(lower, strings.ToLower(pattern)) {
				return fmt.Sprintf("Use the '%s' tool instead of bash_exec for this operation. The '%s' tool provides safety checks and proper error handling.",
					tool.Name, tool.Name)
			}
		}
	}

	return ""
}

// matchCommandPattern checks if a command matches a glob-style pattern.
// Supported syntax:
//
//	"git *"              → prefix match (anything starting with "git ")
//	"*api.github.com*"  → contains match
//	"curl *"            → prefix match
//
// Patterns with leading and trailing * are treated as "contains".
// Patterns with only trailing * are treated as "starts with".
// Patterns with only leading * are treated as "ends with".
// Exact patterns (no *) are treated as exact prefix match.
func matchCommandPattern(command, pattern string) bool {
	// Handle *...*  (contains)
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 2 {
		inner := pattern[1 : len(pattern)-1]
		return strings.Contains(command, inner)
	}

	// Handle ...* (starts with)
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(command, prefix)
	}

	// Handle *... (ends with)
	if strings.HasPrefix(pattern, "*") {
		suffix := pattern[1:]
		return strings.HasSuffix(command, suffix)
	}

	// Exact match (or prefix if you want loose matching)
	return strings.HasPrefix(command, pattern)
}

// detectToolMisuse is the legacy hardcoded detection used as fallback when
// no available_tools registry is provided (e.g., during testing or if the
// orchestrator hasn't been updated yet).
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

// ExecuteStream implements the streaming execution RPC.
// Streams stdout/stderr incrementally as the command runs, then sends the final result.
func (t *bashExecTool) ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error {
	var params bashExecInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("parsing input: %v", err),
			}},
		})
	}

	if params.Command == "" {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: "command is required",
			}},
		})
	}

	// Tool discipline check (same as Execute)
	if warning := detectToolMisuseDynamic(params.Command, req.AvailableTools); warning != "" {
		patternKey := warning
		misuseTracker.counts[patternKey]++
		attempts := misuseTracker.counts[patternKey]
		if attempts <= maxMisuseBlocks {
			return stream.Send(&pb.ExecuteStreamEvent{
				Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: fmt.Sprintf("TOOL DISCIPLINE (attempt %d/%d): %s\n\nThe command was NOT executed.", attempts, maxMisuseBlocks, warning),
					Metadata:     map[string]string{"tool_misuse": "true", "attempt": fmt.Sprintf("%d", attempts)},
				}},
			})
		}
		fmt.Fprintf(os.Stderr, "[TELEMETRY] TOOL_DISCIPLINE_OVERRIDE: command=%q attempts=%d\n", params.Command, attempts)
	}

	timeout := params.Timeout.Int()
	if timeout == 0 {
		timeout = 120
	}

	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}

	ctx := stream.Context()
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", params.Command)
	if workDir != "" {
		cmd.Dir = workDir
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("creating stdout pipe: %v", err),
			}},
		})
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("creating stderr pipe: %v", err),
			}},
		})
	}

	if err := cmd.Start(); err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: commandOutcome(timeoutCtx, cmd.ProcessState, err, "", "")},
		})
	}

	// Stream stdout and stderr concurrently
	var stdoutBuf, stderrBuf strings.Builder
	done := make(chan struct{})

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				text := string(buf[:n])
				stdoutBuf.WriteString(text)
				stream.Send(&pb.ExecuteStreamEvent{
					Event: &pb.ExecuteStreamEvent_OutputDelta{OutputDelta: &pb.OutputDelta{Text: text}},
				})
			}
			if readErr != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				text := string(buf[:n])
				stderrBuf.WriteString(text)
				stream.Send(&pb.ExecuteStreamEvent{
					Event: &pb.ExecuteStreamEvent_ErrorDelta{ErrorDelta: &pb.OutputDelta{Text: text}},
				})
			}
			if readErr != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	// Wait for both readers to finish
	<-done
	<-done

	cmdErr := cmd.Wait()

	resp := commandOutcome(timeoutCtx, cmd.ProcessState, cmdErr, stdoutBuf.String(), stderrBuf.String())
	return stream.Send(&pb.ExecuteStreamEvent{
		Event: &pb.ExecuteStreamEvent_Final{Final: resp},
	})
}

func main() {
	pluginkit.Serve(&bashExecTool{})
}
