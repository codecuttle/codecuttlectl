package conversation

import (
	"testing"
)

func TestExtractPlanFromText_NumberedList(t *testing.T) {
	text := `I'll investigate the repository structure. Here's my plan:

1. List the directory to understand the project layout
2. Read the README for project overview
3. Check recent git commits for context
4. Look at open PRs on GitHub`

	steps := extractPlanFromText(text)
	if steps == nil {
		t.Fatal("expected steps, got nil")
	}
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d: %v", len(steps), steps)
	}
	if steps[0] != "List the directory to understand the project layout" {
		t.Errorf("step[0]=%q", steps[0])
	}
}

func TestExtractPlanFromText_BulletList(t *testing.T) {
	text := `Let me explore the codebase:

- Read the main.go file for entry point
- Check the internal directory structure
- Run the tests to verify current state`

	steps := extractPlanFromText(text)
	if steps == nil {
		t.Fatal("expected steps, got nil")
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d: %v", len(steps), steps)
	}
}

func TestExtractPlanFromText_NoIntro(t *testing.T) {
	// Without a plan-intro phrase, should not extract
	text := `Here are some files:

1. main.go
2. util.go
3. config.go`

	steps := extractPlanFromText(text)
	if steps != nil {
		t.Errorf("expected nil (no plan intro), got %v", steps)
	}
}

func TestExtractPlanFromText_TooFewSteps(t *testing.T) {
	text := `I will read the file.

1. Read the configuration`

	steps := extractPlanFromText(text)
	if steps != nil {
		t.Errorf("expected nil (only 1 step), got %v", steps)
	}
}

func TestExtractPlanFromText_Empty(t *testing.T) {
	steps := extractPlanFromText("")
	if steps != nil {
		t.Errorf("expected nil for empty string, got %v", steps)
	}
}

func TestExtractPlanFromText_ConversationalResponse(t *testing.T) {
	text := `The repository contains a Go project with the following structure. The main entry point is in cmd/codecuttlectl/main.go.`

	steps := extractPlanFromText(text)
	if steps != nil {
		t.Errorf("expected nil for conversational response, got %v", steps)
	}
}
