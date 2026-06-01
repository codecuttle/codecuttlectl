// cuttlebone-go-skills is a companion knowledge plugin that provides Go-specific
// skills, workflows, and best practices to the agent. It registers no tools —
// only skills that are conditionally injected based on context.
package main

import (
	"context"
	"embed"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
	"github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

//go:embed skills/*
var skillFS embed.FS

type goSkills struct{}

func (t *goSkills) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
	return &pb.DescribeResponse{
		Name:        "go_skills",
		Description: "Go language knowledge, workflows, and best practices. This is a companion plugin that provides contextual guidance — it has no executable tool.",
		InputSchema: `{"type": "object", "properties": {}}`,
		Version:     "1.0.0",
		Skills: []*pb.Skill{
			pluginkit.EmbedSkill(skillFS, "skills/compile_errors.md",
				"go_compile_workflow", "on_error:compile|on_error:type|on_error:import|on_language:go", 60),
			pluginkit.EmbedSkill(skillFS, "skills/testing_patterns.md",
				"go_testing_patterns", "on_file:*_test.go|on_request", 40),
			pluginkit.EmbedSkill(skillFS, "skills/module_management.md",
				"go_module_management", "on_file:go.mod|on_file:go.sum|on_request", 40),
		},
	}, nil
}

func (t *goSkills) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
	// Knowledge-only plugin — no tool execution
	return &pb.ExecuteResponse{
		Output: "This is a knowledge-only plugin. It provides skills that are automatically injected into the agent's context when relevant. Use get_skill to browse available skills.",
	}, nil
}

func main() {
	pluginkit.Serve(&goSkills{})
}
