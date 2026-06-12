package tui

// state_dict.go implements a TUI-local state dictionary for small-model grounding.
// This tracks what the agent has actually done (files read, written, dirs explored)
// and renders a compact structured summary that gets appended to the system prompt.
//
// Only used when needsGroundingAssist() returns true (small/local models).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// stateDict maintains ground-truth about the agent's execution state.
type stateDict struct {
	filesRead    map[string]bool
	filesWritten map[string]bool
	dirsListed   map[string]bool
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
func (sd *stateDict) recordToolResult(toolName string, input json.RawMessage, isError bool) {
	sd.toolCalls++
	if isError {
		sd.errors++
	}

	// Extract path from common tool inputs
	var params struct {
		Path string `json:"path"`
	}
	json.Unmarshal(input, &params)

	switch toolName {
	case "read_file":
		if params.Path != "" {
			sd.filesRead[params.Path] = true
		}
	case "write_file":
		if params.Path != "" {
			sd.filesWritten[params.Path] = true
		}
	case "edit_file":
		if params.Path != "" {
			sd.filesRead[params.Path] = true
			sd.filesWritten[params.Path] = true
		}
	case "list_directory":
		if params.Path != "" {
			sd.dirsListed[params.Path] = true
		}
	case "bash_exec", "git":
		sd.commandsRun++
	case "glob", "grep":
		// These are read-like operations
		sd.toolCalls++ // already counted above, this is just a note
	}
}

// render produces a compact text representation for injection into the system prompt.
func (sd *stateDict) render() string {
	var sb strings.Builder
	sb.WriteString("## Current Task Context\n\n")
	sb.WriteString(fmt.Sprintf("**Goal:** %s\n", sd.userGoal))
	sb.WriteString(fmt.Sprintf("**Progress:** %d tool calls completed, %d errors\n", sd.toolCalls, sd.errors))

	if len(sd.filesRead) > 0 {
		sb.WriteString("**Files examined:** ")
		paths := mapKeysStr(sd.filesRead)
		if len(paths) > 8 {
			sb.WriteString(fmt.Sprintf("%s ... (%d total)", strings.Join(paths[:8], ", "), len(paths)))
		} else {
			sb.WriteString(strings.Join(paths, ", "))
		}
		sb.WriteString("\n")
	}

	if len(sd.filesWritten) > 0 {
		sb.WriteString("**Files modified:** ")
		sb.WriteString(strings.Join(mapKeysStr(sd.filesWritten), ", "))
		sb.WriteString("\n")
	}

	if len(sd.dirsListed) > 0 {
		sb.WriteString("**Directories explored:** ")
		dirs := mapKeysStr(sd.dirsListed)
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
	sb.WriteString("When making function calls, briefly state what you're doing and why before each tool call.")

	return sb.String()
}

func mapKeysStr(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
