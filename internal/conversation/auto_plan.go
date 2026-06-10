package conversation

// auto_plan.go implements harness-managed planning: the agent automatically
// extracts planned steps from the model's text output and maintains the todo
// list without requiring the model to call todo_manage with perfect JSON.
//
// This addresses the core insight from agentic planning research: smaller models
// "know" they should plan (evidenced by their text output) but fail to translate
// that knowledge into the correct structured tool call. The harness bridges this
// gap by parsing natural-language plans from the model's responses.

import (
	"regexp"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/todo"
)

// planPatterns are regex patterns that detect when a model is expressing a plan.
var planPatterns = []*regexp.Regexp{
	// Numbered lists: "1. Do X", "2. Do Y"
	regexp.MustCompile(`(?m)^\s*(\d+)\.\s+(.+)$`),
	// Bullet lists with action verbs: "- Read the file", "- Check the directory"
	regexp.MustCompile(`(?m)^\s*[-•*]\s+((?:Read|Write|Edit|Check|List|Search|Run|Build|Test|Fix|Create|Add|Remove|Update|Implement|Review|Explore|Investigate|Look|Find|Open|Fetch|Install|Deploy|Configure|Set up|Verify)\b.+)$`),
}

// planIntroPatterns detect introductory phrases that signal a plan follows.
var planIntroPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:I will|I'll|Let me|I'm going to|I need to|My plan is to|Here's (?:my|the) plan|Steps?:)`),
}

// extractPlanFromText attempts to extract actionable steps from the model's
// text output. Returns nil if no plan structure is detected.
func extractPlanFromText(text string) []string {
	if text == "" {
		return nil
	}

	// Only attempt extraction if the text contains plan-like language
	hasIntro := false
	for _, p := range planIntroPatterns {
		if p.MatchString(text) {
			hasIntro = true
			break
		}
	}
	if !hasIntro {
		return nil
	}

	// Try numbered list first (more structured = higher confidence)
	numbered := planPatterns[0].FindAllStringSubmatch(text, -1)
	if len(numbered) >= 2 {
		var steps []string
		for _, match := range numbered {
			step := strings.TrimSpace(match[2])
			if len(step) > 5 && len(step) < 200 { // Sanity bounds
				steps = append(steps, step)
			}
		}
		if len(steps) >= 2 {
			return steps
		}
	}

	// Try bullet list with action verbs
	bullets := planPatterns[1].FindAllStringSubmatch(text, -1)
	if len(bullets) >= 2 {
		var steps []string
		for _, match := range bullets {
			step := strings.TrimSpace(match[1])
			if len(step) > 5 && len(step) < 200 {
				steps = append(steps, step)
			}
		}
		if len(steps) >= 2 {
			return steps
		}
	}

	return nil
}

// maybeUpdatePlanFromText checks if the model's response contains a plan and
// automatically updates the todo list if so. Only updates if the todo list
// is currently empty (doesn't override explicit model-managed plans).
func (a *Agent) maybeUpdatePlanFromText(text string) {
	// Don't override if the model has already set up todos explicitly
	if !a.todos.IsEmpty() {
		return
	}

	steps := extractPlanFromText(text)
	if steps == nil || len(steps) < 2 {
		return
	}

	// Cap at 7 steps to avoid overwhelming the UI
	if len(steps) > 7 {
		steps = steps[:7]
	}

	// Build todo items: first is in_progress, rest are pending
	items := make([]todo.Item, len(steps))
	for i, step := range steps {
		status := todo.StatusPending
		if i == 0 {
			status = todo.StatusInProgress
		}
		items[i] = todo.Item{
			Content:  step,
			Status:   status,
			Priority: todo.PriorityMedium,
		}
	}

	a.todos.Replace(items)
}

// maybeAdvancePlan marks the current in-progress item as completed and
// advances to the next pending item. Called after a successful tool execution
// sequence (when the model doesn't manage todos itself).
func (a *Agent) maybeAdvancePlan() {
	if a.todos.IsEmpty() {
		return
	}

	items := a.todos.Items()
	// Find current in-progress item
	for i, item := range items {
		if item.Status == todo.StatusInProgress {
			items[i].Status = todo.StatusCompleted
			// Advance next pending to in_progress
			for j := i + 1; j < len(items); j++ {
				if items[j].Status == todo.StatusPending {
					items[j].Status = todo.StatusInProgress
					break
				}
			}
			a.todos.Replace(items)
			return
		}
	}
}
