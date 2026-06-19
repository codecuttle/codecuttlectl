package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/todo"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestIsToolAllowed(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		workbench []string
		want      bool
	}{
		{"nil workbench allows all", "bash_exec", nil, true},
		{"empty workbench allows all", "read_file", []string{}, true},
		{"wildcard allows all", "write_file", []string{"*", "read_file"}, true},
		{"exact match allowed", "read_file", []string{"read_file", "list_directory"}, true},
		{"missing tool rejected", "bash_exec", []string{"read_file", "list_directory"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isToolAllowed(tc.tool, tc.workbench)
			if got != tc.want {
				t.Errorf("isToolAllowed(%q, %v) = %v, want %v", tc.tool, tc.workbench, got, tc.want)
			}
		})
	}
}

func TestExecuteTool_SandboxEnforcement(t *testing.T) {
	agent := &Agent{
		workbench: []string{"read_file", "list_directory"},
		todos:     todo.NewList(),
	}

	// Allowed tool (should pass sandbox, but fail further down because we didn't mock pluginMgr.
	// But let's check a built-in that IS allowed, or just observe the error message.
	// Actually, if it's rejected by sandbox, we get a specific error string.
	res, status := agent.executeTool(context.Background(), "bash_exec", json.RawMessage(`{}`))
	
	if status != types.ToolResultStatusError {
		t.Errorf("expected error status for unauthorized tool, got %v", status)
	}
	
	if !strings.Contains(res, "not authorized in this node's workbench") {
		t.Errorf("expected sandbox error message, got: %v", res)
	}

	// Try a tool that is allowed but doesn't exist in our mock (will panic or hit pluginMgr nil error,
	// but we just want to ensure it DOESN'T return the sandbox error).
	// Let's use a built-in tool that is allowed, like "todo_manage", by adding it to workbench.
	agent.workbench = append(agent.workbench, "todo_manage")
	res, status = agent.executeTool(context.Background(), "todo_manage", json.RawMessage(`{"todos":[]}`))
	
	if strings.Contains(res, "not authorized") {
		t.Errorf("did not expect sandbox error for authorized tool, got: %v", res)
	}
}
