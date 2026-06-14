package prompt

import (
	"encoding/json"
	"testing"
)

func TestSchemaToToolParams_BasicStruct(t *testing.T) {
	// Simulates what schema.MustSchema(&bashExecInput{}) produces.
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The bash command to execute"
			},
			"workdir": {
				"type": "string",
				"description": "Working directory for the command. Defaults to the session working directory."
			},
			"timeout": {
				"oneOf": [
					{"type": "integer"},
					{"type": "string", "pattern": "^-?[0-9]+$"}
				],
				"description": "Timeout in seconds. Default: 120"
			}
		},
		"required": ["command"]
	}`)

	params := SchemaToToolParams(schema)
	if len(params) != 3 {
		t.Fatalf("expected 3 params, got %d", len(params))
	}

	// First param should be the required one (command).
	if params[0].Name != "command" {
		t.Errorf("expected first param to be 'command', got %q", params[0].Name)
	}
	if !params[0].Required {
		t.Error("expected 'command' to be required")
	}
	if params[0].Type != "string" {
		t.Errorf("expected type 'string', got %q", params[0].Type)
	}
	if params[0].Description != "The bash command to execute" {
		t.Errorf("unexpected description: %q", params[0].Description)
	}

	// Find the timeout param — should have oneOf type.
	var timeoutParam *ToolParam
	for i := range params {
		if params[i].Name == "timeout" {
			timeoutParam = &params[i]
			break
		}
	}
	if timeoutParam == nil {
		t.Fatal("timeout param not found")
	}
	if timeoutParam.Type != "integer|string" {
		t.Errorf("expected timeout type 'integer|string', got %q", timeoutParam.Type)
	}
	if timeoutParam.Required {
		t.Error("expected 'timeout' to NOT be required")
	}
}

func TestSchemaToToolParams_EnumType(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "GitHub command to execute",
				"enum": ["pr_list", "pr_create", "issue_list"]
			}
		},
		"required": ["command"]
	}`)

	params := SchemaToToolParams(schema)
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}

	if params[0].Type != "string, enum: ['pr_list', 'pr_create', 'issue_list']" {
		t.Errorf("unexpected type: %q", params[0].Type)
	}
}

func TestSchemaToToolParams_ArrayType(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"labels": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Labels to apply"
			}
		}
	}`)

	params := SchemaToToolParams(schema)
	if len(params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(params))
	}

	if params[0].Type != "[]string" {
		t.Errorf("expected type '[]string', got %q", params[0].Type)
	}
	if params[0].Required {
		t.Error("expected 'labels' to NOT be required")
	}
}

func TestSchemaToToolParams_EmptySchema(t *testing.T) {
	params := SchemaToToolParams(nil)
	if params != nil {
		t.Errorf("expected nil for nil input, got %v", params)
	}

	params = SchemaToToolParams(json.RawMessage(`{}`))
	if params != nil {
		t.Errorf("expected nil for empty schema, got %v", params)
	}

	params = SchemaToToolParams(json.RawMessage(`{"type":"object","properties":{}}`))
	if params != nil {
		t.Errorf("expected nil for empty properties, got %v", params)
	}
}

func TestSchemaToToolParams_InvalidJSON(t *testing.T) {
	params := SchemaToToolParams(json.RawMessage(`not json`))
	if params != nil {
		t.Errorf("expected nil for invalid JSON, got %v", params)
	}
}

func TestSchemaToToolParams_SortOrder(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"zebra": {"type": "string", "description": "z field"},
			"alpha": {"type": "string", "description": "a field"},
			"mango": {"type": "string", "description": "m field, required"}
		},
		"required": ["mango"]
	}`)

	params := SchemaToToolParams(schema)
	if len(params) != 3 {
		t.Fatalf("expected 3 params, got %d", len(params))
	}

	// Required first.
	if params[0].Name != "mango" {
		t.Errorf("expected required param 'mango' first, got %q", params[0].Name)
	}
	// Then alphabetical among non-required.
	if params[1].Name != "alpha" {
		t.Errorf("expected 'alpha' second, got %q", params[1].Name)
	}
	if params[2].Name != "zebra" {
		t.Errorf("expected 'zebra' third, got %q", params[2].Name)
	}
}
