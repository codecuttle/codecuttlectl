package conversation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/codecuttle/codecuttlectl/internal/pluginhost"
	"github.com/codecuttle/codecuttlectl/internal/swarm"
	"github.com/codecuttle/codecuttlectl/internal/todo"
)

func TestBuiltinOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		failed      bool
	}{
		{"todo_manage", `{`, true},
		{"todo_manage", `{"todos":[{"content":"bad","status":"invalid"}]}`, true},
		{"todo_manage", `{"todos":[{"content":"bad","status":"pending","assignee":"missing"}]}`, true},
		{"todo_manage", `{"todos":[]}`, false},
		{"tool_info", `{`, true},
		{"tool_info", `{"name":"missing"}`, true},
		{"tool_info", `{}`, false},
		{"tool_info", `{"name":"todo_manage"}`, false},
		{"get_skill", `{`, true},
		{"get_skill", `{"name":"missing"}`, true},
		{"get_skill", `{}`, false},
	} {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			agent := &Agent{pluginMgr: pluginhost.NewManager(false), todos: todo.NewList(), morph: &swarm.Morphology{Nodes: map[string]swarm.Node{"primary": {IsPrimary: true}}}}
			output, status := agent.executeTool(context.Background(), tc.name, json.RawMessage(tc.input))
			if (status == types.ToolResultStatusError) != tc.failed {
				t.Fatalf("status=%s want error=%v; output=%s", status, tc.failed, output)
			}
		})
	}
}
