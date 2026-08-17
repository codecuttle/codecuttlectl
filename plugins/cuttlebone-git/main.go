// cuttlebone-git is a Cuttlebone plugin that provides git operations.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
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
		Name:           "git",
		Description:    "Execute git commands for version control. Supports: status, diff, log, add, commit, branch, checkout, stash. For safety, destructive operations (force push, reset --hard) are rejected.",
		InputSchema:    schema.MustSchema(&gitInput{}),
		LlmContextHint: "Use git for version control operations. Always check 'git status' before committing. Use 'git diff' to review changes. Allowed subcommands: status, diff, log, add, commit, branch, checkout, stash, show, rev-parse, remote, fetch, pull, push, tag, blame, merge, rebase, cherry-pick, init. Forbidden: push --force, reset --hard, clean -fd. Protected branches (main, master, production, prod): direct commits and pushes are blocked — always create a feature branch first. CRITICAL: Never invent branch names like 'pr-123' for PRs. When checking out PRs, fetch and checkout the actual source branch. Follow the conventions in skills/commit_workflow.md.",
		Version:        "1.0.0",
		CommandPatterns: []string{
			"git *",
		},
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: true,
			SupportsStreaming:    true,
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
	"status":      true,
	"diff":        true,
	"log":         true,
	"add":         true,
	"commit":      true,
	"branch":      true,
	"checkout":    true,
	"stash":       true,
	"show":        true,
	"rev-parse":   true,
	"remote":      true,
	"fetch":       true,
	"pull":        true,
	"push":        true,
	"tag":         true,
	"blame":       true,
	"merge":       true,
	"rebase":      true,
	"cherry-pick": true,
	"init":        true,
}

// Forbidden argument patterns
// NOTE: These are now checked per-argument (not via substring of joined args)
// to prevent false positives from commit messages containing these strings.

// Default protected branches. Direct commits and pushes to these branches are blocked.
// Override via CODECUTTLECTL_PROTECTED_BRANCHES env var (comma-separated).
var protectedBranches = getProtectedBranches()

func getProtectedBranches() map[string]bool {
	defaults := map[string]bool{
		"main":       true,
		"master":     true,
		"production": true,
		"prod":       true,
	}

	env := os.Getenv("CODECUTTLECTL_PROTECTED_BRANCHES")
	if env == "" {
		return defaults
	}

	// If env is set, it completely overrides defaults
	result := make(map[string]bool)
	for _, b := range strings.Split(env, ",") {
		b = strings.TrimSpace(b)
		if b != "" {
			result[b] = true
		}
	}
	return result
}

// getCurrentBranch returns the current git branch name for the given working directory.
func getCurrentBranch(workDir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getPushTarget extracts the target branch from push args.
// Returns the remote branch name if explicit, or empty string if implicit (uses current).
func getPushTarget(args []string) string {
	// git push origin main → "main"
	// git push origin feature:main → "main"
	// git push → "" (uses current branch)
	nonFlagArgs := []string{}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			nonFlagArgs = append(nonFlagArgs, a)
		}
	}
	// nonFlagArgs[0] = remote, nonFlagArgs[1] = refspec
	if len(nonFlagArgs) >= 2 {
		refspec := nonFlagArgs[1]
		// Handle src:dst refspec
		if idx := strings.Index(refspec, ":"); idx >= 0 {
			return refspec[idx+1:]
		}
		return refspec
	}
	return ""
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

	// Resolve working directory early (needed for branch detection)
	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}

	// Protected branch guard: block direct commits and pushes to protected branches.
	// The agent should always work on a feature branch and merge via PR.
	if params.Subcommand == "commit" {
		branch := getCurrentBranch(workDir)
		if protectedBranches[branch] {
			return &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("forbidden: direct commit to protected branch %q. Create a feature branch first with 'git checkout -b <branch-name>', then commit there.", branch),
			}, nil
		}
	}
	if params.Subcommand == "push" {
		// Check explicit push target, or fall back to current branch
		target := getPushTarget(params.Args)
		if target == "" {
			target = getCurrentBranch(workDir)
		}
		if protectedBranches[target] {
			// Allow pushing if we're on a feature branch pushing to its own remote
			currentBranch := getCurrentBranch(workDir)
			if protectedBranches[currentBranch] {
				return &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: fmt.Sprintf("forbidden: direct push to protected branch %q. Create a feature branch, commit there, and push the feature branch instead.", target),
				}, nil
			}
		}
	}

	// Build git command
	cmdArgs := append([]string{params.Subcommand}, params.Args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)

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

// ExecuteStream implements the streaming execution RPC for git.
// Streams stdout/stderr incrementally as the git command runs.
func (t *gitTool) ExecuteStream(req *pb.ExecuteRequest, stream pb.ToolPlugin_ExecuteStreamServer) error {
	var params gitInput
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("parsing input: %v", err),
			}},
		})
	}

	if params.Subcommand == "" {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: "subcommand is required",
			}},
		})
	}

	// Safety: check subcommand whitelist
	if !allowedSubcommands[params.Subcommand] {
		return stream.Send(&pb.ExecuteStreamEvent{
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("subcommand %q is not allowed. Allowed: %s", params.Subcommand, allowedList()),
			}},
		})
	}

	// Safety: same arg checks as Execute
	for _, arg := range params.Args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "--force" || trimmed == "-f" {
			if trimmed == "-f" && (params.Subcommand == "checkout" || params.Subcommand == "branch") {
				continue
			}
			if trimmed == "--force" {
				continue
			}
			return stream.Send(&pb.ExecuteStreamEvent{
				Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: fmt.Sprintf("forbidden argument %q detected for %q.", trimmed, params.Subcommand),
				}},
			})
		}
		if params.Subcommand == "push" && trimmed == "--force" {
			hasLease := false
			for _, a := range params.Args {
				if strings.TrimSpace(a) == "--force-with-lease" {
					hasLease = true
					break
				}
			}
			if !hasLease {
				return stream.Send(&pb.ExecuteStreamEvent{
					Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
						IsError:      true,
						ErrorMessage: "forbidden: 'git push --force' is destructive.",
					}},
				})
			}
		}
		if trimmed == "--hard" {
			return stream.Send(&pb.ExecuteStreamEvent{
				Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: "forbidden: 'git reset --hard' is destructive.",
				}},
			})
		}
	}

	// Resolve working directory (needed for branch detection)
	workDir := params.WorkDir
	if workDir == "" {
		workDir = req.WorkingDirectory
	}

	// Protected branch guard (same as Execute)
	if params.Subcommand == "commit" {
		branch := getCurrentBranch(workDir)
		if protectedBranches[branch] {
			return stream.Send(&pb.ExecuteStreamEvent{
				Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
					IsError:      true,
					ErrorMessage: fmt.Sprintf("forbidden: direct commit to protected branch %q. Create a feature branch first.", branch),
				}},
			})
		}
	}
	if params.Subcommand == "push" {
		target := getPushTarget(params.Args)
		if target == "" {
			target = getCurrentBranch(workDir)
		}
		if protectedBranches[target] {
			currentBranch := getCurrentBranch(workDir)
			if protectedBranches[currentBranch] {
				return stream.Send(&pb.ExecuteStreamEvent{
					Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
						IsError:      true,
						ErrorMessage: fmt.Sprintf("forbidden: direct push to protected branch %q. Create a feature branch and push that instead.", target),
					}},
				})
			}
		}
	}

	// Build and run git command with pipes
	cmdArgs := append([]string{params.Subcommand}, params.Args...)
	ctx := stream.Context()
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)

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
			Event: &pb.ExecuteStreamEvent_Final{Final: &pb.ExecuteResponse{
				IsError:      true,
				ErrorMessage: fmt.Sprintf("starting git: %v", err),
			}},
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

	<-done
	<-done

	cmdErr := cmd.Wait()

	// Build final response
	result := stdoutBuf.String()
	stderrStr := stderrBuf.String()
	metadata := map[string]string{"subcommand": params.Subcommand}
	if stderrStr != "" {
		metadata["stderr"] = stderrStr
	}

	resp := &pb.ExecuteResponse{Metadata: metadata}

	if cmdErr != nil {
		combined := result
		if stderrStr != "" {
			if combined != "" {
				combined += "\n"
			}
			combined += stderrStr
		}
		resp.Output = fmt.Sprintf("git %s failed (exit: %v)\n\n%s", params.Subcommand, cmdErr, combined)
		metadata["exit_error"] = cmdErr.Error()
	} else {
		if result == "" {
			result = "(no output)"
		}
		resp.Output = result
	}

	return stream.Send(&pb.ExecuteStreamEvent{
		Event: &pb.ExecuteStreamEvent_Final{Final: resp},
	})
}

func main() {
	pluginkit.Serve(&gitTool{})
}
