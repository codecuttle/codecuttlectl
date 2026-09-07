package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

// commandOutcome is shared by unary and streaming execution. Command failures
// are tool outcomes, not RPC failures; preserve partial output and a diagnostic.
// Exit code -1 means the process was signaled (Go's ProcessState convention).
// A command that never started has no exit_code, rather than a fabricated zero.
func commandOutcome(ctx context.Context, state *os.ProcessState, err error, stdout, stderr string) *pb.ExecuteResponse {
	result := stdout
	if stderr != "" {
		if result != "" {
			result += "\n"
		}
		result += stderr
	}
	resp := &pb.ExecuteResponse{Output: result, Metadata: map[string]string{}}
	if stderr != "" {
		resp.Metadata["stderr"] = stderr
	}
	if state != nil {
		resp.Metadata["exit_code"] = strconv.Itoa(state.ExitCode())
	}
	if err == nil {
		return resp
	}

	resp.IsError = true
	resp.Metadata["exit_error"] = err.Error()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		resp.Metadata["error_kind"] = "timeout"
		resp.Metadata["timeout"] = "true"
		resp.ErrorMessage = "Command deadline exceeded"
	case ctx.Err() == context.Canceled:
		resp.Metadata["error_kind"] = "cancelled"
		resp.Metadata["cancelled"] = "true"
		resp.ErrorMessage = "Command cancelled"
	case state == nil:
		resp.Metadata["error_kind"] = "start"
		resp.ErrorMessage = fmt.Sprintf("Starting command: %v", err)
	case state.ExitCode() < 0:
		resp.Metadata["error_kind"] = "signal"
		resp.ErrorMessage = fmt.Sprintf("Command terminated: %v", err)
	default:
		resp.Metadata["error_kind"] = "exit"
		resp.ErrorMessage = fmt.Sprintf("Command exited with code %d: %v", state.ExitCode(), err)
	}
	return resp
}
