package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/swarm"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

func TestHandleTodoTool_InvalidAssignee(t *testing.T) {
	morph := &swarm.Morphology{
		Nodes: map[string]swarm.Node{
			"orchestrator": {IsPrimary: true},
			"planner":      {},
		},
	}

	agent := &Agent{
		morph: morph,
		todos: todo.NewList(),
	}

	inputJSON := `{"todos":[{"id":"1","title":"test","content":"do work","status":"pending","priority":"high","assignee":"researcher","async":true}]}`

	result := agent.handleTodoTool(json.RawMessage(inputJSON))

	if !strings.Contains(result, "Error: Cannot assign task to \"researcher\"") {
		t.Errorf("expected error indicating invalid node, got: %v", result)
	}

	if len(agent.todos.Items()) != 0 {
		t.Errorf("expected todo list to remain empty upon validation failure, got %d items", len(agent.todos.Items()))
	}
}

func TestHandleTodoTool_ValidAssignee(t *testing.T) {
	morph := &swarm.Morphology{
		Nodes: map[string]swarm.Node{
			"orchestrator": {IsPrimary: true},
			"planner":      {},
		},
	}

	agent := &Agent{
		morph: morph,
		todos: todo.NewList(),
	}

	inputJSON := `{"todos":[{"id":"1","title":"test","content":"do work","status":"pending","priority":"high","assignee":"planner","async":true}]}`

	result := agent.handleTodoTool(json.RawMessage(inputJSON))

	if strings.Contains(result, "Error") {
		t.Errorf("expected success, got error: %v", result)
	}

	if len(agent.todos.Items()) != 1 {
		t.Errorf("expected 1 item in todo list, got %d", len(agent.todos.Items()))
	}
}
