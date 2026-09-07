package swarm

import (
	"slices"
	"strings"
	"testing"
)

func TestOpenRouterAstraSwarm(t *testing.T) {
	m, err := LoadMorphology("../../testdata/openrouter-astra-swarm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "openrouter-astra-swarm" || m.Presentation != "progressive_disclosure" || m.Topology.Type != "handoff" {
		t.Fatalf("unexpected morphology metadata: name=%q presentation=%q topology=%q", m.Name, m.Presentation, m.Topology.Type)
	}

	expected := map[string]int{
		"astra": 1, "fable": 1, "flash_coder": 5, "deep_coder": 1, "qwen_reviewer": 1,
	}
	if len(m.Nodes) != len(expected) || len(m.Topology.Rules) != len(expected) {
		t.Fatalf("expected five nodes and routing entries, got %d nodes and %d entries", len(m.Nodes), len(m.Topology.Rules))
	}
	for id, concurrency := range expected {
		node, ok := m.Nodes[id]
		if !ok {
			t.Fatalf("missing node %q", id)
		}
		if node.IsPrimary != (id == "astra") || node.MaxConcurrency != concurrency {
			t.Errorf("node %q: unexpected primary flag or concurrency", id)
		}
		if strings.Contains(node.SystemPrompt, "`orchestrator`") || strings.Contains(node.SystemPrompt, "k3_expert") {
			t.Errorf("node %q refers to an undefined node", id)
		}
		if !slices.Contains(node.Workbench, "handoff") {
			t.Errorf("node %q cannot use its handoff routes", id)
		}
		if id != "astra" && !slices.Equal(m.Topology.Rules[id], []string{"astra"}) {
			t.Errorf("node %q must return only to astra", id)
		}
		if id != "astra" && !slices.Contains(m.Topology.Rules["astra"], id) {
			t.Errorf("astra cannot handoff to %q", id)
		}
		if id == "astra" || id == "fable" || id == "qwen_reviewer" {
			for _, tool := range []string{"*", "write_file", "edit_file", "bash_exec"} {
				if slices.Contains(node.Workbench, tool) {
					t.Errorf("non-coding node %q exposes %q", id, tool)
				}
			}
		}
	}
	if !slices.Contains(m.Nodes["astra"].Workbench, "todo_manage") {
		t.Error("astra must be able to delegate async work")
	}
	for source, targets := range m.Topology.Rules {
		if _, ok := m.Nodes[source]; !ok {
			t.Errorf("undefined routing source %q", source)
		}
		for _, target := range targets {
			if _, ok := m.Nodes[target]; !ok {
				t.Errorf("undefined routing target %q from %q", target, source)
			}
		}
	}
}

func TestParseMorphology_Valid(t *testing.T) {
	yamlData := `
name: "senior-dev-committee"
version: "1.0.0"
description: "A fast, cheap planner combined with a powerful executor."
presentation: "progressive_disclosure"

nodes:
  orchestrator:
    provider: "bedrock"
    model: "us.anthropic.claude-opus-4-6-v1"
    system_prompt: "You are the lead developer. You synthesize plans from the planner and execute them."
    workbench: ["*"]
    is_primary: true
    fallbacks:
      - provider: "google"
        model: "gemini-3.1-pro"
    
  planner:
    provider: "google"
    model: "gemini-3.1-pro"
    system_prompt: "You are a software architect. Draft a step-by-step plan for the user's request."
    workbench: ["read_file", "list_directory", "glob", "grep"]
    
  reviewer:
    provider: "ollama"
    model: "qwen2.5-coder:32b"
    system_prompt: "Review code diffs and suggest optimizations."
    workbench: ["git"]

topology:
  type: "handoff"
  rules:
    orchestrator: ["planner", "reviewer"]
    planner: ["orchestrator"]
    reviewer: ["orchestrator"]
`

	m, err := ParseMorphology(strings.NewReader(yamlData))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if m.Name != "senior-dev-committee" {
		t.Errorf("expected name senior-dev-committee, got %q", m.Name)
	}
	if len(m.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(m.Nodes))
	}

	orch, ok := m.Nodes["orchestrator"]
	if !ok {
		t.Fatal("expected orchestrator node")
	}
	if !orch.IsPrimary {
		t.Errorf("expected orchestrator to be primary")
	}
	if orch.Provider != "bedrock" {
		t.Errorf("expected bedrock provider, got %q", orch.Provider)
	}
	if len(orch.Fallbacks) != 1 {
		t.Fatalf("expected 1 fallback, got %d", len(orch.Fallbacks))
	}
	if orch.Fallbacks[0].Provider != "google" {
		t.Errorf("expected google fallback provider, got %q", orch.Fallbacks[0].Provider)
	}

	planner, ok := m.Nodes["planner"]
	if !ok {
		t.Fatal("expected planner node")
	}
	if len(planner.Workbench) != 4 {
		t.Errorf("expected 4 workbench items, got %d", len(planner.Workbench))
	}
}

func TestParseMorphology_NoPrimary(t *testing.T) {
	yamlData := `
name: "no-primary"
nodes:
  node1:
    provider: "bedrock"
    model: "opus"
`
	_, err := ParseMorphology(strings.NewReader(yamlData))
	if err == nil {
		t.Fatal("expected error for no primary node")
	}
	if !strings.Contains(err.Error(), "exactly one primary node") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseMorphology_MultiplePrimary(t *testing.T) {
	yamlData := `
name: "multiple-primary"
nodes:
  node1:
    provider: "bedrock"
    model: "opus"
    is_primary: true
  node2:
    provider: "google"
    model: "gemini"
    is_primary: true
`
	_, err := ParseMorphology(strings.NewReader(yamlData))
	if err == nil {
		t.Fatal("expected error for multiple primary nodes")
	}
	if !strings.Contains(err.Error(), "multiple primary nodes") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseMorphology_MissingFields(t *testing.T) {
	tests := []struct {
		name     string
		yamlData string
		errMatch string
	}{
		{
			"missing name",
			`nodes:
  n1:
    provider: p
    model: m
    is_primary: true`,
			"morphology name is required",
		},
		{
			"no nodes",
			`name: "test"`,
			"morphology must define at least one node",
		},
		{
			"missing provider",
			`name: test
nodes:
  n1:
    model: m
    is_primary: true`,
			"missing a provider",
		},
		{
			"missing model",
			`name: test
nodes:
  n1:
    provider: p
    is_primary: true`,
			"missing a model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMorphology(strings.NewReader(tc.yamlData))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.errMatch) {
				t.Errorf("expected error containing %q, got: %v", tc.errMatch, err)
			}
		})
	}
}
