package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"google.golang.org/grpc"
)

type captureStream struct {
	grpc.ServerStream
	ctx    context.Context
	mu     sync.Mutex
	events []*pb.ExecuteStreamEvent
}

func (s *captureStream) Context() context.Context { return s.ctx }
func (s *captureStream) Send(event *pb.ExecuteStreamEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func TestCommandOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, command, kind, code, stdout, stderr string
		cancel                                    bool
		cancelDuring                              bool
		deadline                                  bool
		badDir                                    bool
	}{
		{name: "success", command: "printf ok", code: "0", stdout: "ok"},
		{name: "stderr alone is not failure", command: "printf warning >&2", code: "0", stderr: "warning"},
		{name: "failure", command: "printf partial; printf failed >&2; exit 1", kind: "exit", code: "1", stdout: "partial", stderr: "failed"},
		{name: "command not found", command: "codecuttle_nonexistent_command_92831", kind: "exit", code: "127", stderr: "command not found"},
		{name: "signal", command: "kill -TERM $$", kind: "signal", code: "-1"},
		{name: "cancel before start", command: "printf must-not-run", kind: "cancelled", cancel: true},
		{name: "cancel during command", command: "printf partial; exec sleep 5", kind: "cancelled", cancelDuring: true, code: "-1", stdout: "partial"},
		{name: "deadline during command", command: "printf partial; exec sleep 5", kind: "timeout", deadline: true, stdout: "partial", code: "-1"},
		{name: "start failure", command: "printf must-not-run", kind: "start", badDir: true},
	} {
		for _, streaming := range []bool{false, true} {
			mode := "unary"
			if streaming {
				mode = "stream"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if tc.cancel {
					cancel()
				}
				if tc.cancelDuring {
					timer := time.AfterFunc(200*time.Millisecond, cancel)
					defer timer.Stop()
				}
				if tc.deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, 200*time.Millisecond)
					defer stop()
				}
				input, err := json.Marshal(bashExecInput{Command: tc.command})
				if err != nil {
					t.Fatal(err)
				}
				req := &pb.ExecuteRequest{Input: string(input), WorkingDirectory: t.TempDir()}
				if tc.badDir {
					req.WorkingDirectory += "/missing"
				}
				tool := &bashExecTool{}
				var response *pb.ExecuteResponse
				if streaming {
					stream := &captureStream{ctx: ctx}
					if err := tool.ExecuteStream(req, stream); err != nil {
						t.Fatal(err)
					}
					finals := 0
					for i, event := range stream.events {
						if final := event.GetFinal(); final != nil {
							response = final
							finals++
							if i != len(stream.events)-1 {
								t.Fatal("final event must be last")
							}
						}
					}
					if finals != 1 {
						t.Fatalf("finals=%d, want 1", finals)
					}
				} else {
					response, err = tool.Execute(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
				}
				if response.IsError != (tc.kind != "") {
					t.Errorf("IsError=%v, want %v: %+v", response.IsError, tc.kind != "", response)
				}
				if response.Metadata["error_kind"] != tc.kind || response.Metadata["exit_code"] != tc.code {
					t.Errorf("metadata=%v, want kind=%q code=%q", response.Metadata, tc.kind, tc.code)
				}
				if tc.kind != "" && response.ErrorMessage == "" {
					t.Error("failure needs a diagnostic message")
				}
				if !strings.Contains(response.Output, tc.stdout) || !strings.Contains(response.Metadata["stderr"], tc.stderr) {
					t.Errorf("output lost: %+v", response)
				}
				if tc.cancel && strings.Contains(response.Output, "must-not-run") {
					t.Fatal("cancelled command executed")
				}
			})
		}
	}
}
