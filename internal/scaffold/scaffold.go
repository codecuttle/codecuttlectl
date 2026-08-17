// Package scaffold generates Cuttlebone plugin stubs from structured specifications.
// It produces a buildable Go module with:
//   - Annotated input struct with json + jsonschema tags
//   - Describe() returning auto-derived JSON Schema via pluginkit/schema
//   - Stub Execute() that returns "not yet implemented"
//   - main() calling pluginkit.Serve()
//
// The generated plugin compiles and runs immediately, ready for implementation.
package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
)

// Spec defines what to generate.
type Spec struct {
	ToolName     string      // e.g., "json_query" — becomes cuttlebone-json-query binary
	Description  string      // One-line description for the LLM
	Params       []ParamSpec // Input parameters
	LLMHint      string      // Optional LLM context hint
	Capabilities CapSpec     // Optional capabilities
}

// ParamSpec defines a single input parameter.
type ParamSpec struct {
	Name        string   // snake_case JSON name (e.g., "max_results")
	Type        string   // "string", "integer", "boolean", "string_array"
	Description string   // Human/LLM-readable description
	Required    bool     // Whether this field is required
	EnumValues  []string // Optional: valid values (for enums)
}

// CapSpec defines plugin capabilities.
type CapSpec struct {
	RequiresConfirmation bool
	MaxTimeoutSeconds    int
	SupportsCancellation bool
}

// Result holds the generated output.
type Result struct {
	Dir        string // Directory containing generated files
	BinaryName string // Expected binary name (cuttlebone-<name>)
	MainGo     string // Path to generated main.go
	GoMod      string // Path to generated go.mod
}

// Generate produces a complete, buildable plugin stub in a temporary directory.
// The caller is responsible for building and installing the result.
func Generate(spec Spec, modulePath string) (*Result, error) {
	if err := validateSpec(spec); err != nil {
		return nil, fmt.Errorf("invalid spec: %w", err)
	}

	binaryName := "cuttlebone-" + strings.ReplaceAll(spec.ToolName, "_", "-")
	dir := filepath.Join(os.TempDir(), "scaffold-"+binaryName)

	// Clean up any previous scaffold attempt
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating scaffold dir: %w", err)
	}

	// Generate main.go
	mainContent, err := renderTemplate(spec)
	if err != nil {
		return nil, fmt.Errorf("rendering template: %w", err)
	}
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		return nil, fmt.Errorf("writing main.go: %w", err)
	}

	// Generate go.mod with replace directive
	goModContent := renderGoMod(binaryName, modulePath)
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(goModContent), 0644); err != nil {
		return nil, fmt.Errorf("writing go.mod: %w", err)
	}

	return &Result{
		Dir:        dir,
		BinaryName: binaryName,
		MainGo:     mainPath,
		GoMod:      goModPath,
	}, nil
}

func validateSpec(spec Spec) error {
	if spec.ToolName == "" {
		return fmt.Errorf("tool_name is required")
	}
	if spec.Description == "" {
		return fmt.Errorf("description is required")
	}
	// Validate tool name: lowercase, underscores, no spaces
	for _, r := range spec.ToolName {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return fmt.Errorf("tool_name must contain only letters, digits, and underscores: got %q", spec.ToolName)
		}
	}
	// Validate params
	for i, p := range spec.Params {
		if p.Name == "" {
			return fmt.Errorf("param %d: name is required", i)
		}
		switch p.Type {
		case "string", "integer", "boolean", "string_array":
			// valid
		default:
			return fmt.Errorf("param %d (%s): unsupported type %q (use string, integer, boolean, string_array)", i, p.Name, p.Type)
		}
	}
	return nil
}

// templateData is passed to the Go template.
type templateData struct {
	ToolName       string
	StructName     string // PascalCase struct name (e.g., "jsonQueryInput")
	Description    string
	LLMHint        string
	Capabilities   CapSpec
	Params         []templateParam
	HasFlexInt     bool
	HasStringArray bool
}

type templateParam struct {
	GoName      string // PascalCase Go field name
	GoType      string // Go type string
	JSONName    string // snake_case JSON name
	Tags        string // Complete struct tag string
	Description string
}

func renderTemplate(spec Spec) (string, error) {
	data := templateData{
		ToolName:     spec.ToolName,
		StructName:   toInputStructName(spec.ToolName),
		Description:  spec.Description,
		LLMHint:      spec.LLMHint,
		Capabilities: spec.Capabilities,
	}

	for _, p := range spec.Params {
		tp := templateParam{
			GoName:      toPascalCase(p.Name),
			GoType:      goType(p.Type),
			JSONName:    p.Name,
			Description: p.Description,
		}

		if p.Type == "integer" {
			data.HasFlexInt = true
		}
		if p.Type == "string_array" {
			data.HasStringArray = true
		}

		// Build struct tag
		tp.Tags = buildStructTag(p)
		data.Params = append(data.Params, tp)
	}

	tmpl, err := template.New("plugin").Parse(pluginTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

func renderGoMod(binaryName, modulePath string) string {
	return fmt.Sprintf(`module %s

go 1.25.0

require github.com/codecuttle/codecuttlectl v0.0.0

replace github.com/codecuttle/codecuttlectl => %s
`, binaryName, modulePath)
}

func goType(typ string) string {
	switch typ {
	case "string":
		return "string"
	case "integer":
		return "types.FlexInt"
	case "boolean":
		return "bool"
	case "string_array":
		return "[]string"
	default:
		return "string"
	}
}

func buildStructTag(p ParamSpec) string {
	var parts []string

	// json tag
	jsonTag := p.Name
	if !p.Required {
		jsonTag += ",omitempty"
	}
	parts = append(parts, fmt.Sprintf(`json:"%s"`, jsonTag))

	// jsonschema tag
	var schemaParts []string
	if p.Required {
		schemaParts = append(schemaParts, "required")
	}
	for _, v := range p.EnumValues {
		schemaParts = append(schemaParts, "enum="+v)
	}
	if len(schemaParts) > 0 {
		parts = append(parts, fmt.Sprintf(`jsonschema:"%s"`, strings.Join(schemaParts, ",")))
	}

	// description tag
	if p.Description != "" {
		parts = append(parts, fmt.Sprintf(`jsonschema_description:"%s"`, p.Description))
	}

	return "`" + strings.Join(parts, " ") + "`"
}

func toInputStructName(toolName string) string {
	return toCamelCase(toolName) + "Input"
}

func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := range parts {
		if len(parts[i]) > 0 {
			if i == 0 {
				parts[i] = strings.ToLower(parts[i][:1]) + parts[i][1:]
			} else {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
		}
	}
	return strings.Join(parts, "")
}

const pluginTemplate = `// Code generated by cuttlebone-scaffold. DO NOT EDIT (structure).
// Implementation in Execute() should be filled in manually or by an agent.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
{{- if .HasFlexInt}}
	"github.com/codecuttle/codecuttlectl/internal/pluginkit/types"
{{- end}}
)

type tool struct{}

type {{.StructName}} struct {
{{- range .Params}}
	{{.GoName}} {{.GoType}} {{.Tags}}
{{- end}}
}

func (t *tool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "{{.ToolName}}",
		Description: ` + "`" + `{{.Description}}` + "`" + `,
		InputSchema: schema.MustSchema(&{{.StructName}}{}),
{{- if .LLMHint}}
		LlmContextHint: ` + "`" + `{{.LLMHint}}` + "`" + `,
{{- end}}
		Version: "0.1.0",
		Capabilities: &pb.ToolCapabilities{
			SupportsCancellation: {{.Capabilities.SupportsCancellation}},
			RequiresConfirmation: {{.Capabilities.RequiresConfirmation}},
			MaxTimeoutSeconds:    {{if .Capabilities.MaxTimeoutSeconds}}{{.Capabilities.MaxTimeoutSeconds}}{{else}}60{{end}},
		},
	}, nil
}

func (t *tool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	var params {{.StructName}}
	if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
		return &pb.ExecuteResponse{
			IsError:      true,
			ErrorMessage: fmt.Sprintf("parsing input: %v", err),
		}, nil
	}

	// TODO: Implement tool logic here.
	// The input parameters are available in the 'params' struct.
	// Return results via pb.ExecuteResponse{Output: "..."}.
	_ = params
	return &pb.ExecuteResponse{
		Output:  "Tool {{.ToolName}} is not yet implemented. This is a generated stub.",
		IsError: true,
		ErrorMessage: "not implemented",
	}, nil
}

func main() {
	pluginkit.Serve(&tool{})
}
`
