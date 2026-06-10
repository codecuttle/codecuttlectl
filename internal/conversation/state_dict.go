package conversation

// state_dict.go implements the State Dictionary — a compact representation of
// the agent's current execution state that is injected after tool results to
// keep the model grounded in reality (per PDDL-INSTRUCT / "Code as Agent Harness"
// methodology). This prevents the model from relying on its own hallucinated
// internal world model.

import (
	"fmt"
	"strings"
)

// stateEntry tracks one observable fact about the environment.
type stateEntry struct {
	Key   string
	Value string
}

// stateDict maintains a compact dictionary of ground-truth facts about the
// current execution environment. It is updated after each tool call and
// injected into the context before the next model invocation.
type stateDict struct {
	entries   []stateEntry
	filesRead map[string]bool // paths the agent has read
	filesWritten map[string]bool // paths the agent has written/created
	dirsListed   map[string]bool // directories listed
	commandsRun  int
	toolCalls    int
	errors       int
	userGoal     string
}

func newStateDict(userGoal string) *stateDict {
	return &stateDict{
		filesRead:    make(map[string]bool),
		filesWritten: make(map[string]bool),
		dirsListed:   make(map[string]bool),
		userGoal:     userGoal,
	}
}

// recordToolResult updates the state dictionary based on a tool execution.
func (sd *stateDict) recordToolResult(toolName string, input string, output string, isError bool) {
	sd.toolCalls++
	if isError {
		sd.errors++
	}

	// Extract observable state changes from tool results
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
	case "bash_exec":
		sd.commandsRun++
	case "git":
		// Track git operations
		sd.commandsRun++
	}
}

// render produces a compact text representation of the current state for injection
// into the model's context. This replaces verbose historical reasoning with a
// crisp snapshot of reality.
func (sd *stateDict) render() string {
	var sb strings.Builder
	sb.WriteString("[STATE DICTIONARY]\n")
	sb.WriteString(fmt.Sprintf("Goal: %s\n", sd.userGoal))
	sb.WriteString(fmt.Sprintf("Progress: %d tool calls completed, %d errors\n", sd.toolCalls, sd.errors))

	if len(sd.filesRead) > 0 {
		sb.WriteString("Files examined: ")
		paths := mapKeys(sd.filesRead)
		if len(paths) > 8 {
			sb.WriteString(fmt.Sprintf("%s ... (%d total)", strings.Join(paths[:8], ", "), len(paths)))
		} else {
			sb.WriteString(strings.Join(paths, ", "))
		}
		sb.WriteString("\n")
	}

	if len(sd.filesWritten) > 0 {
		sb.WriteString("Files modified: ")
		sb.WriteString(strings.Join(mapKeys(sd.filesWritten), ", "))
		sb.WriteString("\n")
	}

	if len(sd.dirsListed) > 0 {
		sb.WriteString("Directories explored: ")
		dirs := mapKeys(sd.dirsListed)
		if len(dirs) > 5 {
			sb.WriteString(fmt.Sprintf("%s ... (%d total)", strings.Join(dirs[:5], ", "), len(dirs)))
		} else {
			sb.WriteString(strings.Join(dirs, ", "))
		}
		sb.WriteString("\n")
	}

	if sd.commandsRun > 0 {
		sb.WriteString(fmt.Sprintf("Commands executed: %d\n", sd.commandsRun))
	}

	sb.WriteString("[/STATE DICTIONARY]")
	return sb.String()
}

// extractJSONString is a quick-and-dirty JSON string field extractor.
// Avoids importing encoding/json for a simple key lookup in tool input.
func extractJSONString(jsonStr, key string) string {
	needle := fmt.Sprintf(`"%s"`, key)
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		return ""
	}
	// Find the value after the key
	rest := jsonStr[idx+len(needle):]
	// Skip colon and whitespace
	rest = strings.TrimLeft(rest, ": \t\n")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	// Find the closing quote
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
