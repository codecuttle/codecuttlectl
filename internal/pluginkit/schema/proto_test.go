package schema

import (
	"encoding/json"
	"testing"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

func TestFromProtoDescriptor_ExecuteRequest(t *testing.T) {
	// ExecuteRequest has: string input, string working_directory, string request_id
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	schema, err := FromProtoDescriptor(md)
	if err != nil {
		t.Fatalf("FromProtoDescriptor failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v\nschema: %s", err, schema)
	}

	if m["type"] != "object" {
		t.Errorf("expected type=object, got %v", m["type"])
	}

	props := m["properties"].(map[string]interface{})

	// Check input field (camelCase by default from protojson)
	inputProp, ok := props["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'input' property, got keys: %v", keys(props))
	}
	if inputProp["type"] != "string" {
		t.Errorf("expected input type=string, got %v", inputProp["type"])
	}

	// Check workingDirectory (camelCase from working_directory)
	wdProp, ok := props["workingDirectory"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'workingDirectory' property, got keys: %v", keys(props))
	}
	if wdProp["type"] != "string" {
		t.Errorf("expected workingDirectory type=string, got %v", wdProp["type"])
	}
}

func TestFromProtoDescriptor_ExecuteResponse(t *testing.T) {
	// ExecuteResponse has: string output, string error_message, bool is_error, map<string,string> metadata
	msg := &pb.ExecuteResponse{}
	md := msg.ProtoReflect().Descriptor()

	schema, err := FromProtoDescriptor(md)
	if err != nil {
		t.Fatalf("FromProtoDescriptor failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})

	// bool field
	isError, ok := props["isError"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'isError' property, got keys: %v", keys(props))
	}
	if isError["type"] != "boolean" {
		t.Errorf("expected isError type=boolean, got %v", isError["type"])
	}

	// map field
	metadata, ok := props["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'metadata' property, got keys: %v", keys(props))
	}
	if metadata["type"] != "object" {
		t.Errorf("expected metadata type=object, got %v", metadata["type"])
	}
	// additionalProperties should describe string values
	addProps, ok := metadata["additionalProperties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata additionalProperties, got %v", metadata)
	}
	if addProps["type"] != "string" {
		t.Errorf("expected metadata values type=string, got %v", addProps["type"])
	}
}

func TestFromProtoDescriptor_DescribeResponse(t *testing.T) {
	// DescribeResponse has nested messages (ToolCapabilities), repeated (Skill), strings
	msg := &pb.DescribeResponse{}
	md := msg.ProtoReflect().Descriptor()

	schema, err := FromProtoDescriptor(md)
	if err != nil {
		t.Fatalf("FromProtoDescriptor failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})

	// Nested message: capabilities should be type=object with its own properties
	caps, ok := props["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'capabilities' property, got keys: %v", keys(props))
	}
	if caps["type"] != "object" {
		t.Errorf("expected capabilities type=object, got %v", caps["type"])
	}
	capProps, ok := caps["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected capabilities to have properties, got %v", caps)
	}
	// ToolCapabilities has supports_streaming (bool), max_timeout_seconds (int32)
	if ss, ok := capProps["supportsStreaming"].(map[string]interface{}); !ok || ss["type"] != "boolean" {
		t.Errorf("expected supportsStreaming boolean in capabilities, got %v", capProps["supportsStreaming"])
	}
	if mts, ok := capProps["maxTimeoutSeconds"].(map[string]interface{}); !ok || mts["type"] != "integer" {
		t.Errorf("expected maxTimeoutSeconds integer in capabilities, got %v", capProps["maxTimeoutSeconds"])
	}

	// Repeated message: skills should be type=array with items
	skills, ok := props["skills"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'skills' property, got keys: %v", keys(props))
	}
	if skills["type"] != "array" {
		t.Errorf("expected skills type=array, got %v", skills["type"])
	}
	items, ok := skills["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected skills items, got %v", skills)
	}
	if items["type"] != "object" {
		t.Errorf("expected skills items type=object, got %v", items["type"])
	}
}

func TestFromProtoDescriptor_Skill(t *testing.T) {
	// Skill has: string name, string trigger, string content_type, string content, int32 priority, int32 estimated_tokens
	msg := &pb.Skill{}
	md := msg.ProtoReflect().Descriptor()

	schema, err := FromProtoDescriptor(md)
	if err != nil {
		t.Fatalf("FromProtoDescriptor failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	props := m["properties"].(map[string]interface{})

	// int32 field
	priority, ok := props["priority"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'priority' property")
	}
	if priority["type"] != "integer" {
		t.Errorf("expected priority type=integer, got %v", priority["type"])
	}

	// string field
	name, ok := props["name"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'name' property")
	}
	if name["type"] != "string" {
		t.Errorf("expected name type=string, got %v", name["type"])
	}
}

func TestFromProtoDescriptorWithOptions_UseProtoNames(t *testing.T) {
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	opts := ProtoSchemaOptions{
		UseProtoNames: true,
	}

	schema, err := FromProtoDescriptorWithOptions(md, opts)
	if err != nil {
		t.Fatalf("FromProtoDescriptorWithOptions failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal([]byte(schema), &m)
	props := m["properties"].(map[string]interface{})

	// Should use snake_case (working_directory) not camelCase (workingDirectory)
	if _, ok := props["working_directory"]; !ok {
		t.Errorf("expected 'working_directory' with UseProtoNames, got keys: %v", keys(props))
	}
	if _, ok := props["workingDirectory"]; ok {
		t.Error("did not expect camelCase 'workingDirectory' with UseProtoNames")
	}
}

func TestFromProtoDescriptorWithOptions_Descriptions(t *testing.T) {
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	opts := ProtoSchemaOptions{
		Descriptions: map[string]string{
			"input":            "JSON-encoded parameters",
			"workingDirectory": "Current working directory",
		},
	}

	schema, err := FromProtoDescriptorWithOptions(md, opts)
	if err != nil {
		t.Fatalf("FromProtoDescriptorWithOptions failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal([]byte(schema), &m)
	props := m["properties"].(map[string]interface{})

	inputProp := props["input"].(map[string]interface{})
	if inputProp["description"] != "JSON-encoded parameters" {
		t.Errorf("expected description on input, got %v", inputProp["description"])
	}

	wdProp := props["workingDirectory"].(map[string]interface{})
	if wdProp["description"] != "Current working directory" {
		t.Errorf("expected description on workingDirectory, got %v", wdProp["description"])
	}
}

func TestFromProtoDescriptorWithOptions_Required(t *testing.T) {
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	opts := ProtoSchemaOptions{
		Required: []string{"input"},
	}

	schema, err := FromProtoDescriptorWithOptions(md, opts)
	if err != nil {
		t.Fatalf("FromProtoDescriptorWithOptions failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal([]byte(schema), &m)

	required, ok := m["required"].([]interface{})
	if !ok {
		t.Fatal("expected required array")
	}
	if len(required) != 1 || required[0] != "input" {
		t.Errorf("expected required=[input], got %v", required)
	}
}

func TestMustProtoSchema(t *testing.T) {
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	// Should not panic
	schema := MustProtoSchema(md)
	if schema == "" {
		t.Error("expected non-empty schema")
	}

	// Verify it's valid JSON
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schema), &m); err != nil {
		t.Fatalf("MustProtoSchema produced invalid JSON: %v", err)
	}
}

func TestFromProtoDescriptor_ValidatesAgainstInput(t *testing.T) {
	// Generate schema from ExecuteRequest proto, then validate JSON against it
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	schema, err := FromProtoDescriptor(md)
	if err != nil {
		t.Fatalf("FromProtoDescriptor failed: %v", err)
	}

	// Valid input
	input := []byte(`{"input": "{\"query\": \"hello\"}", "workingDirectory": "/tmp"}`)
	if err := Validate(schema, input); err != nil {
		t.Errorf("expected valid input, got error: %v", err)
	}

	// Empty object should be valid (no required fields by default in proto3)
	emptyInput := []byte(`{}`)
	if err := Validate(schema, emptyInput); err != nil {
		t.Errorf("expected empty object to be valid (proto3 no required), got error: %v", err)
	}
}

func TestFromProtoDescriptorWithOptions_RequiredValidation(t *testing.T) {
	msg := &pb.ExecuteRequest{}
	md := msg.ProtoReflect().Descriptor()

	opts := ProtoSchemaOptions{
		Required: []string{"input"},
	}

	schema, err := FromProtoDescriptorWithOptions(md, opts)
	if err != nil {
		t.Fatalf("FromProtoDescriptorWithOptions failed: %v", err)
	}

	// Missing required field
	input := []byte(`{"workingDirectory": "/tmp"}`)
	err = Validate(schema, input)
	if err == nil {
		t.Error("expected validation error for missing required 'input'")
	}

	// With required field
	input = []byte(`{"input": "hello"}`)
	if err := Validate(schema, input); err != nil {
		t.Errorf("expected valid input with required field, got: %v", err)
	}
}

// Helper to get map keys for error messages
func keys(m map[string]interface{}) []string {
	var result []string
	for k := range m {
		result = append(result, k)
	}
	return result
}
