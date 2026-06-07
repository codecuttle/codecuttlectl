package scaffold

import (
	"os"
	"strings"
	"testing"
)

func TestGenerate_Simple(t *testing.T) {
	spec := Spec{
		ToolName:    "json_query",
		Description: "Query JSON data using JSONPath expressions",
		Params: []ParamSpec{
			{Name: "query", Type: "string", Description: "JSONPath expression", Required: true},
			{Name: "input", Type: "string", Description: "JSON string to query", Required: true},
		},
	}

	result, err := Generate(spec, "/home/user/codecuttlectl")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	if result.BinaryName != "cuttlebone-json-query" {
		t.Errorf("expected binary name cuttlebone-json-query, got %s", result.BinaryName)
	}

	// Read and verify main.go
	content, err := os.ReadFile(result.MainGo)
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	src := string(content)

	// Must contain the struct
	if !strings.Contains(src, "type jsonQueryInput struct") {
		t.Error("expected jsonQueryInput struct definition")
	}

	// Must have proper struct tags
	if !strings.Contains(src, `json:"query"`) {
		t.Error("expected json tag for query field")
	}
	if !strings.Contains(src, `jsonschema:"required"`) {
		t.Error("expected required jsonschema tag")
	}
	if !strings.Contains(src, `jsonschema_description:"JSONPath expression"`) {
		t.Error("expected description tag")
	}

	// Must use schema.MustSchema
	if !strings.Contains(src, "schema.MustSchema(&jsonQueryInput{})") {
		t.Error("expected schema.MustSchema call")
	}

	// Must have tool name
	if !strings.Contains(src, `Name:        "json_query"`) {
		t.Error("expected tool name in Describe")
	}

	// Must have main calling Serve
	if !strings.Contains(src, "pluginkit.Serve(&tool{})") {
		t.Error("expected main() with pluginkit.Serve")
	}

	// Verify go.mod
	gomod, err := os.ReadFile(result.GoMod)
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), "module cuttlebone-json-query") {
		t.Error("expected module declaration in go.mod")
	}
	if !strings.Contains(string(gomod), "replace github.com/codecuttle/codecuttlectl") {
		t.Error("expected replace directive in go.mod")
	}
}

func TestGenerate_WithFlexInt(t *testing.T) {
	spec := Spec{
		ToolName:    "file_search",
		Description: "Search files with pagination",
		Params: []ParamSpec{
			{Name: "pattern", Type: "string", Description: "Search pattern", Required: true},
			{Name: "max_results", Type: "integer", Description: "Max results", Required: false},
		},
	}

	result, err := Generate(spec, "/tmp/test")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	content, err := os.ReadFile(result.MainGo)
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	src := string(content)

	// Must import types package for FlexInt
	if !strings.Contains(src, `"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"`) {
		t.Error("expected types import for FlexInt")
	}

	// Must use types.FlexInt
	if !strings.Contains(src, "types.FlexInt") {
		t.Error("expected types.FlexInt type")
	}

	// Optional field should have omitempty
	if !strings.Contains(src, `json:"max_results,omitempty"`) {
		t.Error("expected omitempty for optional field")
	}
}

func TestGenerate_WithEnum(t *testing.T) {
	spec := Spec{
		ToolName:    "color_pick",
		Description: "Pick a color",
		Params: []ParamSpec{
			{Name: "color", Type: "string", Description: "Color choice", Required: true, EnumValues: []string{"red", "green", "blue"}},
		},
	}

	result, err := Generate(spec, "/tmp/test")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	content, err := os.ReadFile(result.MainGo)
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	src := string(content)
	if !strings.Contains(src, "enum=red,enum=green,enum=blue") {
		t.Error("expected enum values in jsonschema tag")
	}
}

func TestGenerate_WithCapabilities(t *testing.T) {
	spec := Spec{
		ToolName:    "db_query",
		Description: "Query a database",
		Params: []ParamSpec{
			{Name: "sql", Type: "string", Description: "SQL query", Required: true},
		},
		Capabilities: CapSpec{
			RequiresConfirmation: true,
			MaxTimeoutSeconds:    30,
			SupportsCancellation: true,
		},
		LLMHint: "Only use SELECT statements",
	}

	result, err := Generate(spec, "/tmp/test")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	defer os.RemoveAll(result.Dir)

	content, err := os.ReadFile(result.MainGo)
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}

	src := string(content)
	if !strings.Contains(src, "RequiresConfirmation: true") {
		t.Error("expected RequiresConfirmation")
	}
	if !strings.Contains(src, "MaxTimeoutSeconds:    30") {
		t.Error("expected MaxTimeoutSeconds")
	}
	if !strings.Contains(src, "Only use SELECT statements") {
		t.Error("expected LLM hint")
	}
}

func TestGenerate_InvalidSpec(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		err  string
	}{
		{"empty name", Spec{ToolName: "", Description: "x"}, "tool_name is required"},
		{"empty desc", Spec{ToolName: "x", Description: ""}, "description is required"},
		{"bad name chars", Spec{ToolName: "my tool", Description: "x"}, "tool_name must contain only"},
		{"bad param type", Spec{ToolName: "x", Description: "x", Params: []ParamSpec{{Name: "a", Type: "float"}}}, "unsupported type"},
		{"empty param name", Spec{ToolName: "x", Description: "x", Params: []ParamSpec{{Name: "", Type: "string"}}}, "name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Generate(tt.spec, "/tmp")
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.err) {
				t.Errorf("expected error containing %q, got %q", tt.err, err.Error())
			}
		})
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"max_results", "MaxResults"},
		{"path", "Path"},
		{"working_directory", "WorkingDirectory"},
		{"a", "A"},
	}
	for _, tt := range tests {
		got := toPascalCase(tt.input)
		if got != tt.want {
			t.Errorf("toPascalCase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToInputStructName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"json_query", "jsonQueryInput"},
		{"bash_exec", "bashExecInput"},
		{"read_file", "readFileInput"},
	}
	for _, tt := range tests {
		got := toInputStructName(tt.input)
		if got != tt.want {
			t.Errorf("toInputStructName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
