package swarm

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Morphology defines a declarative multi-agent swarm configuration.
type Morphology struct {
	Name         string          `yaml:"name"`
	Version      string          `yaml:"version"`
	Description  string          `yaml:"description"`
	Presentation string          `yaml:"presentation"` // single_agent, progressive_disclosure
	Nodes        map[string]Node `yaml:"nodes"`
	Topology     Topology        `yaml:"topology"`
}

// Node represents a single agent within the swarm.
type Node struct {
	Provider     string   `yaml:"provider"`
	Model        string   `yaml:"model"`
	SystemPrompt string   `yaml:"system_prompt"`
	Workbench    []string `yaml:"workbench"`
	IsPrimary    bool     `yaml:"is_primary"`
	Fallbacks    []struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
	} `yaml:"fallbacks,omitempty"`
}

// Topology defines the routing rules for the swarm.
type Topology struct {
	Type  string              `yaml:"type"` // e.g., "handoff"
	Rules map[string][]string `yaml:"rules"`
}

// ParseMorphology reads and parses a morphology YAML configuration from the given reader.
func ParseMorphology(r io.Reader) (*Morphology, error) {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true) // strict parsing

	var m Morphology
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to parse morphology: %w", err)
	}

	if err := validateMorphology(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

// LoadMorphology reads and parses a morphology YAML configuration from a file path.
func LoadMorphology(path string) (*Morphology, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open morphology file: %w", err)
	}
	defer f.Close()

	return ParseMorphology(f)
}

func validateMorphology(m *Morphology) error {
	if m.Name == "" {
		return fmt.Errorf("morphology name is required")
	}
	if len(m.Nodes) == 0 {
		return fmt.Errorf("morphology must define at least one node")
	}

	primaryCount := 0
	for name, node := range m.Nodes {
		if node.Provider == "" {
			return fmt.Errorf("node %q is missing a provider", name)
		}
		if node.Model == "" {
			return fmt.Errorf("node %q is missing a model", name)
		}
		if node.IsPrimary {
			primaryCount++
		}
	}

	if primaryCount == 0 {
		return fmt.Errorf("morphology must define exactly one primary node")
	}
	if primaryCount > 1 {
		return fmt.Errorf("morphology defines multiple primary nodes")
	}

	return nil
}
