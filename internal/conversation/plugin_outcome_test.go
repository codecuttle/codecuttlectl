package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/audit"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/provider"
)

// Exercise the actual executable and gRPC boundary, not a fake ExecuteResponse.
func TestBuiltPluginFailurePropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	dir := t.TempDir()
	binary := filepath.Join(dir, "cuttlebone-bash-exec")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../plugins/cuttlebone-bash-exec")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
	manager := pluginhost.NewManager(false)
	defer manager.Shutdown()
	if err := manager.DiscoverPlugins(ctx, dir); err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"command":"printf './main.go:1:1: undefined: missingSymbol\\n' >&2; exit 1"}`)
	result, err := manager.ExecuteFull(ctx, "bash_exec", input, dir)
	if err == nil || !result.IsError || result.Metadata["exit_code"] != "1" || !strings.Contains(result.Metadata["stderr"], "undefined") {
		t.Fatalf("unary host lost failure: %+v, %v", result, err)
	}
	stream, err := manager.ExecuteStream(ctx, "bash_exec", input, dir)
	if err != nil {
		t.Fatal(err)
	}
	finals := 0
	for event := range stream {
		if event.Final != nil {
			finals++
			if !event.Final.IsError || event.Final.Metadata["exit_code"] != "1" || event.Final.ErrorMessage == "" {
				t.Fatalf("stream host lost failure: %+v", event.Final)
			}
		}
	}
	if finals != 1 {
		t.Fatalf("finals=%d, want 1", finals)
	}

	for _, streaming := range []bool{false, true} {
		var auditOutput bytes.Buffer
		model := &failureProvider{input: input}
		agent, err := NewAgent(Config{Provider: model, PluginMgr: manager, WorkDir: dir, MaxSteps: 3, AuditLogger: audit.NewLogger(&auditOutput, true)})
		if err != nil {
			t.Fatal(err)
		}
		if streaming {
			_, err = agent.StreamTurn(ctx, "Run the check", nil)
		} else {
			_, err = agent.Turn(ctx, "Run the check")
		}
		if err != nil {
			t.Fatal(err)
		}
		if !model.sawFailure {
			t.Fatalf("streaming=%v: model did not receive failed tool result", streaming)
		}
		if advice := agent.reconciler.Advise(agent.inkwell); advice.InjectPrompt == "" {
			t.Fatal("failed execution did not produce diagnostic advice")
		}
		if len(agent.inkwell) != 1 || !agent.inkwell[0].IsError || agent.inkwell[0].ErrorType == "" {
			t.Fatalf("Inkwell lost failure: %+v", agent.inkwell)
		}
		found := false
		for _, line := range strings.Split(strings.TrimSpace(auditOutput.String()), "\n") {
			var event audit.Event
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "tool_exec" {
				found = event.IsError && event.ToolName == "bash_exec"
			}
		}
		if !found {
			t.Fatalf("audit lost failure: %s", auditOutput.String())
		}
	}
}

type failureProvider struct {
	input      json.RawMessage
	calls      int
	sawFailure bool
}

func (p *failureProvider) ID() string   { return "test:failure" }
func (p *failureProvider) Name() string { return "failure fixture" }
func (p *failureProvider) Converse(_ context.Context, req provider.Request) (*provider.Response, error) {
	p.calls++
	if p.calls == 1 {
		return &provider.Response{ToolUses: []provider.ToolUseRequest{{ToolUseID: "check", Name: "bash_exec", Input: p.input}}}, nil
	}
	for _, block := range req.Messages[len(req.Messages)-1].Content {
		if result, ok := block.(provider.ToolResultBlock); ok {
			p.sawFailure = result.IsError && strings.Contains(result.Content, "undefined")
		}
	}
	return &provider.Response{Content: "The check failed."}, nil
}
func (p *failureProvider) ConverseStream(ctx context.Context, req provider.Request) <-chan provider.StreamEvent {
	response, _ := p.Converse(ctx, req)
	ch := make(chan provider.StreamEvent, 5)
	for _, tool := range response.ToolUses {
		ch <- provider.ToolUseStartEvent{ToolUseID: tool.ToolUseID, Name: tool.Name}
		ch <- provider.ToolInputDeltaEvent{Delta: string(tool.Input)}
		ch <- provider.ToolUseStopEvent{}
	}
	if response.Content != "" {
		ch <- provider.TextDeltaEvent{Text: response.Content}
	}
	ch <- provider.MessageStopEvent{StopReason: "end_turn"}
	close(ch)
	return ch
}
