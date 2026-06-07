// cuttlebone-git is a Cuttlebone plugin that provides git operations.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

type gitTool struct{}

type gitInput struct {
	Subcommand string   `json:"subcommand" jsonschema:"required,enum=status,enum=diff,enum=log,enum=add,enum=commit,enum=branch,enum=checkout,enum=stash,enum=show,enum=rev-parse,enum=remote,enum=fetch,enum=pull,enum=push,enum=tag,enum=blame,enum=merge,enum=rebase,enum=cherry-pick,enum=init" jsonschema_description:"Git subcommand to run"`
	Args       []string `json:"args,omitempty" jsonschema_description:"Arguments to pass to the git subcommand"`
	WorkDir    string   `json:"workdir,omitempty" jsonschema_description:"Working directory for the git command. Defaults to session working directory."`
}

func (t *gitTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "git",
		Description: "Execute git commands for version control. Supports: status, diff, log, add, commit, branch, checkout, stash. For safety, destructive operations (force push, reset --hard) are rejected.",
		InputSchema: schema.MustSchema(&gitInput{}),
		LlmContextHint: "Use git for version control operations. Always check 'git status' before committing. Use 'git diff' to review changes. Allowed subcommands: status, diff, log, add, commit, branch, checkout, stash, show, rev-parse, remote, fetch, pull, tag, blame. Forbidden: push --force, reset --hard, clean -fd.",
		Version:         "1.0.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			MaxTimeoutSeconds:    30,
		},
	}, nil
}

// Allowed git subcommands (safety whitelist)
var allowedSubcommands = map[string]bool{
	"status":    true,
	"diff":      true,
	"log":       true,
	"add":       true,
	"commit":    true,
	"branch":    true,
	"checkout":  true,
	"stash":     true,
	"show":      true,
	"rev-parse": true,
	"remote":    true,
	"fetch":     true,
	"pull":      true,
	"push":      true,
	"tag":       true,
	"blame":     true,
	"merge":     true,
	"rebase":    true,
	"cherry-pick": true,
	"init":      true,
}

// Forbidden argument patterns
var forbiddenPatterns = []string{
	"--force",
	"-f",
	"--hard",
	"clean -fd",
	"clean -f",
}

func (t *gitTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params gitInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	if params.Subcommand == "" {
		return &pb.ExecuteResponse{IsError: true, ErrorMessage: "subcommand is required"}, nil
	}

	// Safety: check subcommand whitelist
	if !allowedSubcommands[params.Subcommand] {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("subcommand %q is not allowed. Allowed: %s", params.Subcommand, allowedList()),
		}, nil
	}

	// Safety: check for forbidden patterns
	fullArgs := strings.Join(params.Args, " ")
	for _, forbidden := range forbiddenPatterns {
		if strings.Contains(fullArgs, forbidden) {
			// Allow --force-with-lease (safer alternative to --force)
			if forbidden == "--force" && strings.Contains(fullArgs, "--force-with-lease") {
				continue
			}
			// Allow -f for checkout (checkout -f is fine, push -f is not)
			if forbidden == "-f" && params.Subcommand == "checkout" {
				continue
			}
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("forbidden argument pattern %q detected. This operation is considered destructive and requires manual execution.", forbidden),
			}, nil
		}
	}

	// Build git command
	cmdArgs := append([]string{params.Subcommand}, params.Args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)

	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}
	if workDir != "" {
		cmd.Dir = workDir
	}

	output, err := cmd.CombinedOutput()
	result := string(output)

	if err != nil {
		// Non-zero exit from git — include the output which usually has the error message
		return &pb.ExecuteResponse{
			Output:   fmt.Sprintf("git %s failed (exit: %v)\n\n%s", params.Subcommand, err, result),
			Metadata: map[string]string{"exit_error": err.Error()},
		}, nil
	}

	if result == "" {
		result = "(no output)"
	}

	return &pb.ExecuteResponse{
		Output:   result,
		Metadata: map[string]string{"subcommand": params.Subcommand},
	}, nil
}

func allowedList() string {
	var list []string
	for k := range allowedSubcommands {
		list = append(list, k)
	}
	return strings.Join(list, ", ")
}

func main() {
	pluginkit.Serve(&gitTool{})
}
