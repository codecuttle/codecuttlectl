package schema

import (
	"encoding/json"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
)

// testSimpleInput mimics a basic plugin input struct.
type testSimpleInput struct {
	Path string `json:"path" jsonschema:"required" jsonschema_description:"The file path to read"`
}

// testComplexInput mimics a plugin with multiple field types.
type testComplexInput struct {
	Command string        `json:"command" jsonschema:"required" jsonschema_description:"The bash command to execute"`
	WorkDir string        `json:"workdir,omitempty" jsonschema_description:"Working directory"`
	Timeout types.FlexInt `json:"timeout,omitempty" jsonschema_description:"Timeout in seconds"`
}

// testEnumInput tests enum support.
type testEnumInput struct {
	Subcommand string   `json:"subcommand" jsonschema:"required,enum=status,enum=log,enum=diff,enum=add,enum=commit" jsonschema_description:"Git subcommand to run"`
	Args       []string `json:"args,omitempty" jsonschema_description:"Arguments to pass"`
}

// testBoolInput tests FlexBool.
type testBoolInput struct {
	Path       string         `json:"path" jsonschema:"required" jsonschema_description:"File path"`
	ReplaceAll types.FlexBool `json:"replace_all,omitempty" jsonschema_description:"Replace all occurrences"`
}

func TestFromStruct_Simple(t *testing.T) {
	schema, err := FromStruct(&testSimpleInput{})
	if err != nil {
		t.Fatalf("FromStruct failed: %v", err)
	}

	// Parse and verify structure
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	// Must be type object
	if m["type"] != "object" {
		t.Errorf("expected type=object, got %v", m["type"])
	}

	// Must have properties
	props, ok := m["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties object, got %T", m["properties"])
	}

	// Must have path property
	pathProp, ok := props["path"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected path property, got %T", props["path"])
	}
	if pathProp["type"] != "string" {
		t.Errorf("expected path type=string, got %v", pathProp["type"])
	}
	if pathProp["description"] != "The file path to read" {
		t.Errorf("expected description, got %v", pathProp["description"])
	}

	// Must have required containing "path"
	required, ok := m["required"].([]interface{})
	if !ok {
		t.Fatalf("expected required array, got %T", m["required"])
	}
	found := false
	for _, r := range required {
		if r == "path" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'path' in required, got %v", required)
	}
}

func TestFromStruct_Complex(t *testing.T) {
	schema, err := FromStruct(&testComplexInput{})
	if err != nil {
		t.Fatalf("FromStruct failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})

	// command should be a string
	cmd := props["command"].(map[string]interface{})
	if cmd["type"] != "string" {
		t.Errorf("expected command type=string, got %v", cmd["type"])
	}

	// timeout should have oneOf (from FlexInt.JSONSchema())
	timeout := props["timeout"].(map[string]interface{})
	oneOf, ok := timeout["oneOf"].([]interface{})
	if !ok {
		t.Fatalf("expected timeout to have oneOf, got %v", timeout)
	}
	if len(oneOf) != 2 {
		t.Errorf("expected 2 oneOf entries, got %d", len(oneOf))
	}

	// required should only contain "command"
	required := m["required"].([]interface{})
	if len(required) != 1 || required[0] != "command" {
		t.Errorf("expected required=[command], got %v", required)
	}
}

func TestFromStruct_Enum(t *testing.T) {
	schema, err := FromStruct(&testEnumInput{})
	if err != nil {
		t.Fatalf("FromStruct failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})
	sub := props["subcommand"].(map[string]interface{})

	enumVals, ok := sub["enum"].([]interface{})
	if !ok {
		t.Fatalf("expected enum array on subcommand, got %v", sub)
	}
	if len(enumVals) != 5 {
		t.Errorf("expected 5 enum values, got %d: %v", len(enumVals), enumVals)
	}

	// args should be an array of strings
	args := props["args"].(map[string]interface{})
	if args["type"] != "array" {
		t.Errorf("expected args type=array, got %v", args["type"])
	}
}

func TestFromStruct_FlexBool(t *testing.T) {
	schema, err := FromStruct(&testBoolInput{})
	if err != nil {
		t.Fatalf("FromStruct failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})
	replaceAll := props["replace_all"].(map[string]interface{})

	// Should have oneOf from FlexBool.JSONSchema()
	oneOf, ok := replaceAll["oneOf"].([]interface{})
	if !ok {
		t.Fatalf("expected replace_all to have oneOf, got %v", replaceAll)
	}
	if len(oneOf) != 3 {
		t.Errorf("expected 3 oneOf entries (bool, int, string), got %d", len(oneOf))
	}
}

func TestMustSchema_Success(t *testing.T) {
	// Should not panic
	schema := MustSchema(&testSimpleInput{})
	if schema == "" {
		t.Error("expected non-empty schema")
	}
}

func TestMustSchema_NoSchemaAndID(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	if _, ok := m["$schema"]; ok {
		t.Error("schema should not contain $schema field")
	}
	if _, ok := m["$id"]; ok {
		t.Error("schema should not contain $id field")
	}
}

func TestValidate_Valid(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})
	input := []byte(`{"path": "/tmp/test.txt"}`)

	if err := Validate(schema, input); err != nil {
		t.Errorf("expected valid input, got error: %v", err)
	}
}

func TestValidate_MissingRequired(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})
	input := []byte(`{}`)

	err := Validate(schema, input)
	if err == nil {
		t.Error("expected validation error for missing required field")
	}
}

func TestValidate_WrongType(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})
	input := []byte(`{"path": 123}`)

	err := Validate(schema, input)
	if err == nil {
		t.Error("expected validation error for wrong type")
	}
}

func TestValidate_FlexInt_AcceptsBoth(t *testing.T) {
	schema := MustSchema(&testComplexInput{})

	// Integer form
	input := []byte(`{"command": "ls", "timeout": 30}`)
	if err := Validate(schema, input); err != nil {
		t.Errorf("expected integer timeout to be valid: %v", err)
	}

	// String form
	input = []byte(`{"command": "ls", "timeout": "30"}`)
	if err := Validate(schema, input); err != nil {
		t.Errorf("expected string timeout to be valid: %v", err)
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})
	input := []byte(`not json at all`)

	err := Validate(schema, input)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidate_ExtraFieldsAllowed(t *testing.T) {
	schema := MustSchema(&testSimpleInput{})
	// LLMs sometimes add extra fields — this should still be valid
	input := []byte(`{"path": "/tmp/test.txt", "extra_field": "should be ok"}`)

	if err := Validate(schema, input); err != nil {
		t.Errorf("expected extra fields to be allowed, got error: %v", err)
	}
}

func TestValidate_FlexBool_AcceptsCasings(t *testing.T) {
	schema := MustSchema(&testBoolInput{})

	// All of these are valid according to FlexBool.UnmarshalJSON
	valid := []string{
		`{"path": "/tmp/x", "replace_all": true}`,
		`{"path": "/tmp/x", "replace_all": false}`,
		`{"path": "/tmp/x", "replace_all": "true"}`,
		`{"path": "/tmp/x", "replace_all": "True"}`,
		`{"path": "/tmp/x", "replace_all": "TRUE"}`,
		`{"path": "/tmp/x", "replace_all": "false"}`,
		`{"path": "/tmp/x", "replace_all": "False"}`,
		`{"path": "/tmp/x", "replace_all": "FALSE"}`,
		`{"path": "/tmp/x", "replace_all": "yes"}`,
		`{"path": "/tmp/x", "replace_all": "Yes"}`,
		`{"path": "/tmp/x", "replace_all": "YES"}`,
		`{"path": "/tmp/x", "replace_all": "no"}`,
		`{"path": "/tmp/x", "replace_all": "No"}`,
		`{"path": "/tmp/x", "replace_all": "NO"}`,
		`{"path": "/tmp/x", "replace_all": "1"}`,
		`{"path": "/tmp/x", "replace_all": "0"}`,
		`{"path": "/tmp/x", "replace_all": 1}`,
		`{"path": "/tmp/x", "replace_all": 0}`,
	}

	for _, input := range valid {
		if err := Validate(schema, []byte(input)); err != nil {
			t.Errorf("expected valid: %s\n  got error: %v", input, err)
		}
	}

	// These should be rejected by the schema (garbage strings)
	invalid := []string{
		`{"path": "/tmp/x", "replace_all": "maybe"}`,
		`{"path": "/tmp/x", "replace_all": "2"}`,
	}

	for _, input := range invalid {
		if err := Validate(schema, []byte(input)); err == nil {
			t.Errorf("expected rejection: %s", input)
		}
	}
}
