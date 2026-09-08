package tui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// Verify that TUI executePendingTools properly respects workbench sandboxing and denies unauthorized todo_manage
	m.pendingToolCalls = []pendingTool{{id: "forged", name: "todo_manage", input: input}}
	result := m.executePendingTools()().(ContinueStreamMsg)
	if len(result.Messages) != 1 || !result.Messages[0].IsError || !strings.Contains(result.Messages[0].Content, "not authorized") {
		t.Fatalf("expected TUI to deny unauthorized todo_manage with error result, got: %+v", result.Messages)
	}
}

func TestKnownDivergence_TUIApprovalDeniesAgainAndLosesEarlierResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	dir := t.TempDir()
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(dir, "cuttlebone-runtime-fixture"), "../../testdata/runtime-plugin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	manager := pluginhost.NewManager(false)
	defer manager.Shutdown()
	if err := manager.DiscoverPlugins(ctx, dir); err != nil {
		t.Fatal(err)
	}
	gated := json.RawMessage(`{"subcommand":"rebase","args":["-i"]}`)
	for _, approve := range []bool{false, true} {
		work := t.TempDir()
		agent, err := conversation.NewAgent(conversation.Config{PluginMgr: manager, WorkDir: work})
		if err != nil {
			t.Fatal(err)
		}
		m := New(Config{Agent: agent, PluginMgr: manager, WorkDir: work})
		m.pendingToolCalls = []pendingTool{
			{id: "first", name: "git", input: json.RawMessage(`{"subcommand":"status"}`)},
			{id: "gated", name: "git", input: gated},
		}
		request := m.executePendingTools()().(ApprovalRequestMsg)
		if len(request.CompletedResults) != 1 || request.CompletedResults[0].ToolUseID != "first" {
			t.Fatal("fixture did not execute first tool before approval")
		}
		updated, _ := m.Update(request)
		m = updated.(Model)
		updated, execute := m.Update(ApprovalDecisionMsg{ToolUseID: "gated", Approved: approve})
		m = updated.(Model)
		results := execute().(ContinueStreamMsg)
		if len(results.Messages) != 1 || results.Messages[0].ToolUseID != "gated" || !results.Messages[0].IsError {
			t.Fatal("baseline changed: approval continuation should lose prior result and deny gated call")
		}
		if approve && !strings.Contains(results.Messages[0].Content, "requires user approval") {
			t.Fatalf("expected second Agent gate denial, got %q", results.Messages[0].Content)
		}
		calls, err := os.ReadFile(filepath.Join(work, "fixture-calls.log"))
		if err != nil || strings.Count(string(calls), "\n") != 1 {
			t.Fatalf("inert fixture should execute only initial status: %q %v", calls, err)
		}
	}
	// Control: the same inert gated tool DOES run once with an Agent approval callback.
	work := t.TempDir()
	decisions := 0
	agent, err := conversation.NewAgent(conversation.Config{PluginMgr: manager, WorkDir: work, ApprovalFunc: func(_, _, _, _ string) bool { decisions++; return true }})
	if err != nil {
		t.Fatal(err)
	}
	if _, status := agent.ExecuteTool(ctx, "git", gated); status != types.ToolResultStatusSuccess || decisions != 1 {
		t.Fatal("Agent control approval did not execute once")
	}
	calls, err := os.ReadFile(filepath.Join(work, "fixture-calls.log"))
	if err != nil || strings.Count(string(calls), "\n") != 1 {
		t.Fatal("Agent control side effect count != 1")
	}
}

func TestHandoffUpdatesTUIProviderAndPersona(t *testing.T) {
	manager := pluginhost.NewManager(false)
	source := &recordingRuntimeProvider{id: "bedrock:claude-3-5"}
	target := &recordingRuntimeProvider{id: "openrouter:deepseek"}
	pool := runtimePool{nodes: map[string]provider.Provider{"lead": source, "review": target}}
	morph := &swarm.Morphology{Nodes: map[string]swarm.Node{
		"lead":   {Provider: "bedrock", Model: source.id, IsPrimary: true},
		"review": {Provider: "openrouter", Model: target.id, SystemPrompt: "TARGET_PERSONA", Workbench: []string{"read_file", "handoff"}},
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
	// Test NodeChangedMsg listener re-arm
	updatedNodeMsg, nodeCmd := m.Update(swarm.NodeChangedMsg{Source: "lead", Target: "review", ProviderID: "openrouter:deepseek"})
	if updatedNodeMsg == nil || nodeCmd == nil {
		t.Errorf("expected NodeChangedMsg to return valid model and re-arm listener command")
	}

	// Test context window reset for provider without ContextWindowProvider
	if m.contextWindow != 0 {
		t.Errorf("expected contextWindow to be 0 for recordingRuntimeProvider without ContextWindowProvider interface, got %d", m.contextWindow)
	}

	// Test return handoff: target reviews and hands back to lead
	m.pendingToolCalls = []pendingTool{{id: "handoff_back", name: "handoff", input: json.RawMessage(`{"target":"lead","instructions":"completed review"}`)}}
	resultBack := m.executePendingTools()().(ContinueStreamMsg)
	if resultBack.Messages[0].IsError || agent.ActiveNode() != "lead" {
		t.Fatalf("fixture did not switch Agent back to lead: %+v", resultBack.Messages)
	}
	updatedBack, _ := m.Update(resultBack)
	mBack := updatedBack.(Model)
	defer mBack.stopModelStream()
	if len(source.requests) != 1 {
		t.Fatalf("expected next TUI request to route back to source provider, got source.requests=%d", len(source.requests))
	}
	if mBack.llmProvider.ID() != "bedrock:claude-3-5" {
		t.Fatalf("expected TUI llmProvider restored to lead, got %s", mBack.llmProvider.ID())
	}
}
