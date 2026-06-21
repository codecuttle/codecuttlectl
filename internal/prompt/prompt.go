// Package prompt manages embedded system prompts and dynamic template hydration.
package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"runtime"
	"text/template"
)

//go:embed all:system all:tools
var promptFS embed.FS

// Context holds the runtime values injected into prompt templates.
type Context struct {
	WorkingDirectory string
	Platform         string
	Model            string
	Provider         string
	Tools            []ToolDef
	SwarmNodes       []string // Available Node IDs in the current morphology
}

// ToolDef describes a tool available to the agent for template rendering.
type ToolDef struct {
	Name        string
	Description string
	Parameters  []ToolParam
}

// ToolParam describes a single parameter of a tool.
type ToolParam struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

// Manager loads and renders embedded prompt templates.
type Manager struct {
	templates *template.Template
}

// NewManager parses all embedded prompt files into a template set.
func NewManager() (*Manager, error) {
	tmpl := template.New("prompts")

	err := fs.WalkDir(promptFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := promptFS.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading embedded file %s: %w", path, readErr)
		}
		_, parseErr := tmpl.New(path).Parse(string(data))
		if parseErr != nil {
			return fmt.Errorf("parsing template %s: %w", path, parseErr)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("loading prompt templates: %w", err)
	}

	return &Manager{templates: tmpl}, nil
}

// Render executes a named template with the given context and returns the result.
func (m *Manager) Render(name string, ctx Context) (string, error) {
	t := m.templates.Lookup(name)
	if t == nil {
		return "", fmt.Errorf("prompt template %q not found", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("executing template %q: %w", name, err)
	}
	return buf.String(), nil
}

// DefaultContext builds a Context with standard runtime information.
func DefaultContext(workDir, model, providerName string, tools []ToolDef) Context {
	return Context{
		WorkingDirectory: workDir,
		Platform:         runtime.GOOS + "/" + runtime.GOARCH,
		Model:            model,
		Provider:         providerName,
		Tools:            tools,
	}
}

// RenderSystem renders the main system prompt with environment context.
func (m *Manager) RenderSystem(workDir, model, providerName string, tools []ToolDef, swarmNodes []string) (string, error) {
	ctx := DefaultContext(workDir, model, providerName, tools)
	ctx.SwarmNodes = swarmNodes
	return m.Render("system/coding.md", ctx)
}

// RenderToolDefs renders the tool definitions prompt.
func (m *Manager) RenderToolDefs(tools []ToolDef) (string, error) {
	ctx := Context{Tools: tools}
	return m.Render("tools/tool_definitions.md", ctx)
}

// ListPrompts returns all available prompt template names.
func (m *Manager) ListPrompts() []string {
	var names []string
	for _, t := range m.templates.Templates() {
		if t.Name() != "prompts" {
			names = append(names, t.Name())
		}
	}
	return names
}
