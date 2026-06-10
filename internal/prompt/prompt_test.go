package prompt

import (
	"strings"
	"testing"
)

func TestNewManager(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	prompts := mgr.ListPrompts()
	if len(prompts) == 0 {
		t.Fatal("expected at least one prompt template, got none")
	}

	// Check that known prompts are present
	found := map[string]bool{
		"system/default.md":        false,
		"system/coding.md":         false,
		"tools/tool_definitions.md": false,
	}
	for _, name := range prompts {
		if _, ok := found[name]; ok {
			found[name] = true
		}
	}
	for name, present := range found {
		if !present {
			t.Errorf("expected prompt %q not found in available prompts", name)
		}
	}
}

func TestRenderSystem(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	tools := []ToolDef{
		{Name: "read_file", Description: "Read a file"},
		{Name: "write_file", Description: "Write a file"},
	}

	result, err := mgr.RenderSystem("/tmp/test", "claude-sonnet", "bedrock", tools)
	if err != nil {
		t.Fatalf("RenderSystem() error: %v", err)
	}

	// Verify template variables were hydrated
	if !strings.Contains(result, "/tmp/test") {
		t.Error("expected working directory in rendered prompt")
	}
	if !strings.Contains(result, "Codecuttle") {
		t.Error("expected 'Codecuttle' identity in rendered prompt")
	}
	if !strings.Contains(result, "claude-sonnet") {
		t.Error("expected model name in rendered prompt")
	}
}

func TestRenderDefault(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	ctx := DefaultContext("/work", "test-model", "bedrock", nil)
	result, err := mgr.Render("system/default.md", ctx)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}

	if !strings.Contains(result, "Codecuttle") {
		t.Error("expected 'Codecuttle' in default prompt")
	}
	if !strings.Contains(result, "/work") {
		t.Error("expected working directory in rendered prompt")
	}
}

func TestRenderToolDefinitions(t *testing.T) {
	mgr, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	tools := []ToolDef{
		{
			Name:        "bash_exec",
			Description: "Execute a bash command",
			Parameters: []ToolParam{
				{Name: "command", Type: "string", Required: true, Description: "The command to run"},
			},
		},
	}

	result, err := mgr.RenderToolDefs(tools)
	if err != nil {
		t.Fatalf("RenderToolDefs() error: %v", err)
	}

	if !strings.Contains(result, "bash_exec") {
		t.Error("expected tool name in rendered output")
	}
	if !strings.Contains(result, "command") {
		t.Error("expected parameter name in rendered output")
	}
}
