package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
)

func TestPrimaryProfileApplied(t *testing.T) {
	pm, err := prompt.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	morph := &swarm.Morphology{Nodes: map[string]swarm.Node{
		"lead": {IsPrimary: true, Provider: "fixture", Model: "fixture", SystemPrompt: "PRIMARY_PERSONA_SENTINEL", Workbench: []string{"todo_manage"}},
	}}
	a, err := NewAgent(Config{Provider: &mockEchoProvider{}, PluginMgr: pluginhost.NewManager(false), Morph: morph, PromptMgr: pm, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if a.activeNode != "lead" || len(a.workbench) != 1 || a.workbench[0] != "todo_manage" || !strings.Contains(a.systemPrompt, "PRIMARY_PERSONA_SENTINEL") {
		t.Fatalf("expected primary profile applied: activeNode=%q, workbench=%v, systemPrompt=%q", a.activeNode, a.workbench, a.systemPrompt)
	}
	names := make(map[string]bool)
	for _, tool := range a.allProviderToolDefs() {
		names[tool.Name] = true
	}
	if !names["todo_manage"] {
		t.Errorf("expected todo_manage in active tool defs")
	}
	// "handoff" shouldn't be in defs if workbench is strictly ["todo_manage"]
	if names["handoff"] {
		t.Errorf("handoff should not be allowed under strict todo_manage workbench")
	}
}

func TestHandoffWithoutPoolReturnsError(t *testing.T) {
	a, err := NewAgent(Config{Provider: &mockEchoProvider{}, Morph: &swarm.Morphology{Nodes: map[string]swarm.Node{"target": {}}}})
	if err != nil {
		t.Fatal(err)
	}
	msg, status := a.handleHandoff([]byte(`{"target":"target","instructions":"test"}`))
	if status != types.ToolResultStatusError {
		t.Fatalf("expected error status, got %v (msg: %s)", status, msg)
	}
	if !strings.Contains(msg, "Error:") {
		t.Fatalf("expected error message, got: %s", msg)
	}
}

type terminalErrorProvider struct{ mockEchoProvider }

func (*terminalErrorProvider) ConverseStream(context.Context, provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamErrorEvent{Err: errors.New("fixture permanent error")}
	close(ch)
	return ch
}

func TestEngineEmitsOneTerminalError(t *testing.T) {
	a, err := NewAgent(Config{Provider: &terminalErrorProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch, err := NewEngine(a).StreamTurnAsync(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	terminals := 0
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				if terminals != 1 {
					t.Fatalf("terminal errors=%d, want exactly one", terminals)
				}
				return
			}
			if done, ok := event.(EventTurnDone); ok {
				if done.Error == nil {
					t.Fatal("expected error terminal")
				}
				terminals++
			}
		case <-ctx.Done():
			t.Fatal("bounded Engine error scenario did not finish")
		}
	}
}
