// cuttlebone-git is a Cuttlebone plugin that provides git operations.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

type gitTool struct{}

//go:embed skills/*
var skillFS embed.FS

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
		Skills: []*pb.Skill{
			pluginkit.EmbedSkill(skillFS, "skills/commit_workflow.md",
				"git_commit_workflow", "on_tool:git|on_request", 50),
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
// NOTE: These are now checked per-argument (not via substring of joined args)
// to prevent false positives from commit messages containing these strings.

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

	// Safety: check for forbidden patterns in arguments.
	// Only match standalone arguments, not substrings of other args or commit messages.
	for _, arg := range params.Args {
		trimmed := strings.TrimSpace(arg)

		// --force is forbidden (except --force-with-lease which is safe)
		if trimmed == "--force" || trimmed == "-f" {
			// Allow -f for checkout and branch (checkout -f, branch -f are safe)
			if trimmed == "-f" && (params.Subcommand == "checkout" || params.Subcommand == "branch") {
				continue
			}
			// Allow --force-with-lease
			if trimmed == "--force" {
				continue // standalone --force without --with-lease caught below
			}
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("forbidden argument %q detected for %q. This operation is considered destructive and requires manual execution.", trimmed, params.Subcommand),
			}, nil
		}

		// Catch push --force (but not --force-with-lease)
		if params.Subcommand == "push" && trimmed == "--force" {
			// Check if --force-with-lease is also present
			hasLease := false
			for _, a := range params.Args {
				if strings.TrimSpace(a) == "--force-with-lease" {
					hasLease = true
					break
				}
			}
			if !hasLease {
				return &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: "forbidden: 'git push --force' is destructive. Use --force-with-lease for safer force push, or execute manually.",
				}, nil
			}
		}

		// --hard (reset --hard)
		if trimmed == "--hard" {
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: "forbidden: 'git reset --hard' is destructive and discards uncommitted changes. Execute manually if intended.",
			}, nil
		}
	}

	// Forbidden subcommand + arg combinations (multi-arg patterns)
	if params.Subcommand == "clean" {
		for _, arg := range params.Args {
			trimmed := strings.TrimSpace(arg)
			if trimmed == "-fd" || trimmed == "-f" || trimmed == "-fx" || trimmed == "-fxd" {
				return &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: "forbidden: 'git clean -f' removes untracked files permanently. Execute manually if intended.",
				}, nil
			}
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
