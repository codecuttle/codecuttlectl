package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/conversation"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/prompt"
	"github.com/codecuttle/codecuttlectl/internal/provider"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
)

// KnownDivergence tests assert the measured baseline, NOT the desired contract.
// Replace these assertions with desired invariants in the linked repair PR.
type recordingRuntimeProvider struct {
	provider.Provider
	id       string
	requests []string // immutable JSON snapshots, not aliases of live requests
}

func (p *recordingRuntimeProvider) ID() string   { return p.id }
func (p *recordingRuntimeProvider) Name() string { return p.id }
func (p *recordingRuntimeProvider) ConverseStream(_ context.Context, req provider.Request) <-chan provider.StreamEvent {
	data, err := json.Marshal(req)
	if err != nil {
		panic(err) // fixture only: requests must be serializable
	}
	p.requests = append(p.requests, string(data))
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch
}

type runtimePool struct {
	provider.Pool
	nodes map[string]provider.Provider
}

func (p runtimePool) GetNode(id string) (provider.Provider, bool) { v, ok := p.nodes[id]; return v, ok }
func (p runtimePool) Primary() provider.Provider                  { return p.nodes["lead"] }

func TestTUICatalogAndTodoAuthorization(t *testing.T) {
	manager := pluginhost.NewManager(false)
	p := &recordingRuntimeProvider{id: "fixture:lead"}
	agent, err := conversation.NewAgent(conversation.Config{Provider: p, PluginMgr: manager, Workbench: []string{"read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{Provider: p, PluginMgr: manager, Agent: agent})
	m.launchStream()
	defer m.stopModelStream()
	if len(p.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(p.requests))
	}
	input := json.RawMessage(`{"todos":[{"content":"forged native task","status":"pending","priority":"medium"}]}`)
	if _, status := agent.ExecuteTool(context.Background(), "todo_manage", input); status != types.ToolResultStatusError {
		t.Fatal("restricted Agent should deny forged todo call")
	}
}

func TestHandoffUpdatesTUIProviderAndPersona(t *testing.T) {
	manager := pluginhost.NewManager(false)
	source := &recordingRuntimeProvider{id: "fixture:source"}
	target := &recordingRuntimeProvider{id: "fixture:target"}
	pool := runtimePool{nodes: map[string]provider.Provider{"lead": source, "review": target}}
	morph := &swarm.Morphology{Nodes: map[string]swarm.Node{
		"lead":   {Provider: "fixture", Model: source.id, IsPrimary: true},
		"review": {Provider: "fixture", Model: target.id, SystemPrompt: "TARGET_PERSONA", Workbench: []string{"read_file"}},
	}}
	pm, err := prompt.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	agent, err := conversation.NewAgent(conversation.Config{Provider: source, Pool: pool, PluginMgr: manager, PromptMgr: pm, Morph: morph, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{Provider: source, Pool: pool, Agent: agent, PluginMgr: manager, Morph: morph, System: "SOURCE_PERSONA"})
	m.pendingToolCalls = []pendingTool{{id: "handoff", name: "handoff", input: json.RawMessage(`{"target":"review","instructions":"review"}`)}}
	result := m.executePendingTools()().(ContinueStreamMsg)
	if result.Messages[0].IsError || agent.ActiveNode() != "review" || !strings.Contains(agent.SystemPrompt(), "TARGET_PERSONA") {
		t.Fatal("fixture did not switch Agent node/persona")
	}
	updated, _ := m.Update(result)
	m = updated.(Model)
	defer m.stopModelStream()
	if len(target.requests) != 1 || !strings.Contains(m.system, "TARGET_PERSONA") {
		t.Fatalf("expected next TUI request on target provider and updated persona, got target.requests=%d, m.system=%q", len(target.requests), m.system)
	}
}
