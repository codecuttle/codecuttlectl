package conversation

// state_dict.go implements the State Dictionary — a compact representation of
// the agent's current execution state that is injected into the system prompt
// to keep the model grounded in reality (per PDDL-INSTRUCT / "Code as Agent Harness"
// methodology). This prevents smaller models from relying on their own hallucinated
// internal world model and losing track of the user's goal.
//
// Only used for small models (gated by needsGroundingAssist). Large frontier models
// like Opus 4.6 maintain their own internal state tracking effectively.

import (
	"fmt"
	"strings"
)

// stateDict maintains a compact dictionary of ground-truth facts about the
// current execution environment. It is updated after each tool call and
// rendered into the system prompt before the next model invocation.
type stateDict struct {
	filesRead    map[string]bool // paths the agent has read
	filesWritten map[string]bool // paths the agent has written/created
	dirsListed   map[string]bool // directories listed
	commandsRun  int
	toolCalls    int
	errors       int
	userGoal     string
	originalGoal string // Preserved across turns; the first substantive user message
}

func newStateDict(userGoal string) *stateDict {
	return &stateDict{
		filesRead:    make(map[string]bool),
		filesWritten: make(map[string]bool),
		dirsListed:   make(map[string]bool),
		userGoal:     userGoal,
		originalGoal: userGoal,
	}
}

// recordToolResult updates the state dictionary based on a tool execution.
func (sd *stateDict) recordToolResult(toolName string, input string, output string, isError bool) {
	sd.toolCalls++
	if isError {
		sd.errors++
	}

	switch toolName {
	case "read_file":
		if path := extractJSONString(input, "path"); path != "" {
			sd.filesRead[path] = true
		}
	case "write_file":
		if path := extractJSONString(input, "path"); path != "" {
			sd.filesWritten[path] = true
		}
	case "edit_file":
		if path := extractJSONString(input, "path"); path != "" {
			sd.filesRead[path] = true
			sd.filesWritten[path] = true
		}
	case "list_directory":
		if path := extractJSONString(input, "path"); path != "" {
			sd.dirsListed[path] = true
		}
	case "bash_exec", "git":
		sd.commandsRun++
	}
}

// render produces a compact text representation of the current state for injection
// into the system prompt. This replaces verbose historical context with a crisp
// snapshot of what the agent has actually done.
func (sd *stateDict) render() string {
	var sb strings.Builder
	sb.WriteString("## Current Task Context\n\n")

	// Use the original goal (first substantive user message) for grounding stability
	goal := sd.originalGoal
	if goal == "" {
		goal = sd.userGoal
	}
	sb.WriteString(fmt.Sprintf("**Goal:** %s\n", goal))
	sb.WriteString(fmt.Sprintf("**Progress:** %d tool calls completed, %d errors\n", sd.toolCalls, sd.errors))

	if len(sd.filesRead) > 0 {
		sb.WriteString("**Files examined:** ")
		paths := mapKeys(sd.filesRead)
		if len(paths) > 8 {
			sb.WriteString(fmt.Sprintf("%s ... (%d total)", strings.Join(paths[:8], ", "), len(paths)))
		} else {
			sb.WriteString(strings.Join(paths, ", "))
		}
		sb.WriteString("\n")
	}

	if len(sd.filesWritten) > 0 {
		sb.WriteString("**Files modified:** ")
		sb.WriteString(strings.Join(mapKeys(sd.filesWritten), ", "))
		sb.WriteString("\n")
	}

	if len(sd.dirsListed) > 0 {
		sb.WriteString("**Directories explored:** ")
		dirs := mapKeys(sd.dirsListed)
		if len(dirs) > 5 {
			sb.WriteString(fmt.Sprintf("%s ... (%d total)", strings.Join(dirs[:5], ", "), len(dirs)))
		} else {
			sb.WriteString(strings.Join(dirs, ", "))
		}
		sb.WriteString("\n")
	}

	if sd.commandsRun > 0 {
		sb.WriteString(fmt.Sprintf("**Commands executed:** %d\n", sd.commandsRun))
	}

	sb.WriteString("\nContinue making progress toward the goal. Do NOT re-introduce yourself or restart — you are mid-task.\n")
	sb.WriteString("When making function calls, briefly state what you're doing and why before each tool call.\n")
	sb.WriteString("IMPORTANT: Do NOT stop to explain your plan or ask for permission. Take action immediately with tool calls.\n")

	return sb.String()
}

// extractJSONString is a quick-and-dirty JSON string field extractor.
func extractJSONString(jsonStr, key string) string {
	needle := fmt.Sprintf(`"%s"`, key)
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(needle):]
	rest = strings.TrimLeft(rest, ": \t\n")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// mapKeys returns the keys of a map as a slice.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
