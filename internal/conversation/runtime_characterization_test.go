package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
)

func TestKnownDivergence_PrimaryProfileNotApplied(t *testing.T) {
	pm, err := prompt.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	morph := &swarm.Morphology{Nodes: map[string]swarm.Node{
		"lead": {IsPrimary: true, Provider: "fixture", Model: "fixture", SystemPrompt: "PRIMARY_PERSONA_SENTINEL", Workbench: []string{"read_file"}},
	}}
	a, err := NewAgent(Config{Provider: &mockEchoProvider{}, PluginMgr: pluginhost.NewManager(false), Morph: morph, PromptMgr: pm, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if a.activeNode != "lead" || len(a.workbench) != 0 || strings.Contains(a.systemPrompt, "PRIMARY_PERSONA_SENTINEL") {
		t.Fatal("baseline changed: primary identity is applied, but persona/workbench are not")
	}
	names := make(map[string]bool)
	for _, tool := range a.allProviderToolDefs() {
		names[tool.Name] = true
	}
	for _, name := range []string{"todo_manage", "tool_info", "get_skill", "scaffold_plugin", "reload_plugins", "handoff"} {
		if !names[name] {
			t.Fatalf("expected baseline native catalog to contain %s", name)
		}
	}
}

func TestKnownDivergence_HandoffWithoutPoolPanics(t *testing.T) {
	a, err := NewAgent(Config{Provider: &mockEchoProvider{}, Morph: &swarm.Morphology{Nodes: map[string]swarm.Node{"target": {}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("baseline changed: replace known panic with typed-error regression")
		}
	}()
	a.handleHandoff([]byte(`{"target":"target","instructions":"test"}`))
}

type terminalErrorProvider struct{ mockEchoProvider }

func (*terminalErrorProvider) ConverseStream(context.Context, provider.Request) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamErrorEvent{Err: errors.New("fixture permanent error")}
	close(ch)
	return ch
}

func TestKnownDivergence_EngineEmitsTwoTerminalErrors(t *testing.T) {
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
				if terminals != 2 {
					t.Fatalf("baseline terminal errors=%d, want 2 until R3 repair", terminals)
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
