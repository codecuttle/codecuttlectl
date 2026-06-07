// Package inkwell implements the diagnostic reconciliation loop for the Codecuttle
// meta-harness. Named after the cuttlefish ink defense mechanism — the system's
// local cache where diagnostic "ink" is gathered, analyzed, and fed back into
// corrective prompts to enable rapid self-healing.
//
// The Inkwell operates on three principles:
//   1. Classify: Determine the nature and severity of each error
//   2. Advise: Generate targeted corrective guidance based on error patterns
//   3. Escalate: Detect looping failures and force strategy changes
package inkwell

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/session"
)

// ErrorClass represents a high-level error category.
type ErrorClass string

const (
	ClassCompile    ErrorClass = "compile"
	ClassSyntax     ErrorClass = "syntax"
	ClassType       ErrorClass = "type"
	ClassImport     ErrorClass = "import"
	ClassNotFound   ErrorClass = "not_found"
	ClassPermission ErrorClass = "permission"
	ClassTimeout    ErrorClass = "timeout"
	ClassNetwork    ErrorClass = "network"
	ClassRuntime    ErrorClass = "runtime"
	ClassUnknown    ErrorClass = "unknown"
)

// ClassifiedError holds a parsed error with structural metadata.
type ClassifiedError struct {
	Class       ErrorClass
	Language    string // Detected language (go, python, typescript, rust, etc.)
	File        string // File path if extractable
	Line        int    // Line number if extractable
	Message     string // Core error message
	RawOutput   string // Full stderr/output
	Suggestion  string // Machine-generated hint for the corrective prompt
}

// Classify analyzes tool output and returns a structured error classification.
// This is the expanded version of the simple classifyError function.
func Classify(toolName string, output string, isError bool) ClassifiedError {
	if !isError {
		return ClassifiedError{Class: ClassUnknown}
	}

	ce := ClassifiedError{
		RawOutput: output,
	}

	// Detect language from tool output patterns
	ce.Language = detectLanguage(output)

	// Try structured parsing based on language
	switch ce.Language {
	case "go":
		parseGoError(output, &ce)
	case "python":
		parsePythonError(output, &ce)
	case "typescript", "javascript":
		parseTSError(output, &ce)
	default:
		parseGenericError(output, &ce)
	}

	// If structured parsing didn't classify, fall back to heuristics
	if ce.Class == "" {
		ce.Class = classifyByHeuristic(output)
	}

	return ce
}

// --- Language detection ---

func detectLanguage(output string) string {
	switch {
	case goErrorPattern.MatchString(output):
		return "go"
	case pythonTracebackPattern.MatchString(output):
		return "python"
	case tsErrorPattern.MatchString(output):
		return "typescript"
	case rustErrorPattern.MatchString(output):
		return "rust"
	default:
		return ""
	}
}

var (
	goErrorPattern        = regexp.MustCompile(`(?m)^.+\.go:\d+:\d+:`)
	pythonTracebackPattern = regexp.MustCompile(`(?m)^Traceback \(most recent call last\)|File ".+\.py", line \d+`)
	tsErrorPattern        = regexp.MustCompile(`(?m)^.+\.tsx?:\d+:\d+ - error TS\d+:`)
	rustErrorPattern      = regexp.MustCompile(`(?m)^error\[E\d+\]:`)
)

// --- Go error parsing ---

var goFileLinePattern = regexp.MustCompile(`^(.+\.go):(\d+):\d+: (.+)$`)

func parseGoError(output string, ce *ClassifiedError) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if m := goFileLinePattern.FindStringSubmatch(line); m != nil {
			ce.File = m[1]
			fmt.Sscanf(m[2], "%d", &ce.Line)
			ce.Message = m[3]
			break
		}
	}

	switch {
	case strings.Contains(output, "undefined:"):
		ce.Class = ClassImport
		ce.Suggestion = "A variable or function is used but not defined. Check imports and spelling."
	case strings.Contains(output, "cannot find package") || strings.Contains(output, "no required module provides"):
		ce.Class = ClassImport
		ce.Suggestion = "A Go package is missing. Run 'go get' or check the import path."
	case strings.Contains(output, "cannot use") || strings.Contains(output, "cannot convert"):
		ce.Class = ClassType
		ce.Suggestion = "Type mismatch. Check the expected vs actual types carefully."
	case strings.Contains(output, "syntax error") || strings.Contains(output, "expected"):
		ce.Class = ClassSyntax
		ce.Suggestion = "Syntax error in Go code. Check braces, parentheses, and statement termination."
	case strings.Contains(output, "declared and not used") || strings.Contains(output, "imported and not used"):
		ce.Class = ClassCompile
		ce.Suggestion = "Unused declaration. Remove the unused import/variable or use it."
	default:
		ce.Class = ClassCompile
		ce.Suggestion = "Go compilation error. Read the error message carefully and fix the indicated file/line."
	}
}

// --- Python error parsing ---

var pythonFileLinePattern = regexp.MustCompile(`File "(.+)", line (\d+)`)

func parsePythonError(output string, ce *ClassifiedError) {
	// Find the last file/line reference (usually the most relevant)
	matches := pythonFileLinePattern.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		last := matches[len(matches)-1]
		ce.File = last[1]
		fmt.Sscanf(last[2], "%d", &ce.Line)
	}

	// Extract the final error line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 {
		ce.Message = lines[len(lines)-1]
	}

	switch {
	case strings.Contains(output, "SyntaxError"):
		ce.Class = ClassSyntax
		ce.Suggestion = "Python syntax error. Check indentation, colons, and parentheses."
	case strings.Contains(output, "ImportError") || strings.Contains(output, "ModuleNotFoundError"):
		ce.Class = ClassImport
		ce.Suggestion = "Python module not found. Install it with pip or check the import name."
	case strings.Contains(output, "TypeError"):
		ce.Class = ClassType
		ce.Suggestion = "Python type error. Check argument types and return values."
	case strings.Contains(output, "NameError"):
		ce.Class = ClassImport
		ce.Suggestion = "Python name not defined. Check variable/function spelling and scope."
	case strings.Contains(output, "FileNotFoundError") || strings.Contains(output, "No such file"):
		ce.Class = ClassNotFound
		ce.Suggestion = "File or directory does not exist. Check the path."
	default:
		ce.Class = ClassRuntime
		ce.Suggestion = "Python runtime error. Read the traceback from bottom to top."
	}
}

// --- TypeScript/JavaScript error parsing ---

func parseTSError(output string, ce *ClassifiedError) {
	ce.Class = ClassCompile

	switch {
	case strings.Contains(output, "Cannot find module"):
		ce.Class = ClassImport
		ce.Suggestion = "Module not found. Run 'npm install' or check the import path."
	case strings.Contains(output, "is not assignable to"):
		ce.Class = ClassType
		ce.Suggestion = "TypeScript type mismatch. Check the type annotations and interfaces."
	case strings.Contains(output, "Unexpected token"):
		ce.Class = ClassSyntax
		ce.Suggestion = "Syntax error. Check for missing brackets, commas, or semicolons."
	default:
		ce.Suggestion = "TypeScript compilation error. Read the error code and message."
	}
}

// --- Generic / fallback parsing ---

func parseGenericError(output string, ce *ClassifiedError) {
	ce.Class = classifyByHeuristic(output)
	ce.Message = firstMeaningfulLine(output)
}

func classifyByHeuristic(output string) ErrorClass {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "syntax error") || strings.Contains(lower, "unexpected token"):
		return ClassSyntax
	case strings.Contains(lower, "type error") || strings.Contains(lower, "cannot convert"):
		return ClassType
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no such file"):
		return ClassNotFound
	case strings.Contains(lower, "permission denied"):
		return ClassPermission
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return ClassTimeout
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "network"):
		return ClassNetwork
	case strings.Contains(lower, "import") || strings.Contains(lower, "module"):
		return ClassImport
	case strings.Contains(lower, "compile") || strings.Contains(lower, "build"):
		return ClassCompile
	default:
		return ClassRuntime
	}
}

func firstMeaningfulLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			if len(line) > 200 {
				return line[:200]
			}
			return line
		}
	}
	return ""
}

// --- Pattern analysis (looping detection) ---

// AnalyzeRecent examines recent Inkwell entries to detect problematic patterns.
// Returns a Diagnosis describing the overall health of the execution loop.
type Diagnosis struct {
	IsLooping        bool   // True if the agent is repeating the same failed action
	LoopCount        int    // How many times the same error has occurred
	FailedTool       string // The tool that's repeatedly failing
	FailedFile       string // The file being repeatedly targeted
	DominantClass    ErrorClass
	RecentErrors     []ClassifiedError
	ShouldEscalate   bool   // True if the reconciler should force a strategy change
	EscalationReason string
}

// Diagnose analyzes the most recent N inkwell entries for patterns.
func Diagnose(entries []session.InkEntry, lookback int) Diagnosis {
	if len(entries) == 0 {
		return Diagnosis{}
	}

	// Take last N entries
	start := len(entries) - lookback
	if start < 0 {
		start = 0
	}
	recent := entries[start:]

	diag := Diagnosis{}

	// Count errors by tool and classify them
	toolErrors := make(map[string]int)
	var recentErrors []ClassifiedError
	var lastFailedTool string

	for _, entry := range recent {
		if entry.IsError {
			toolErrors[entry.ToolName]++
			lastFailedTool = entry.ToolName
			ce := Classify(entry.ToolName, entry.Output, true)
			recentErrors = append(recentErrors, ce)
		}
	}

	diag.RecentErrors = recentErrors

	if len(recentErrors) == 0 {
		return diag
	}

	// Find the dominant error class
	classCounts := make(map[ErrorClass]int)
	for _, e := range recentErrors {
		classCounts[e.Class]++
	}
	maxCount := 0
	for class, count := range classCounts {
		if count > maxCount {
			maxCount = count
			diag.DominantClass = class
		}
	}

	// Detect looping: same tool failing 3+ times in the recent window
	for tool, count := range toolErrors {
		if count >= 3 {
			diag.IsLooping = true
			diag.LoopCount = count
			diag.FailedTool = tool
			break
		}
	}
	if !diag.IsLooping && lastFailedTool != "" {
		diag.FailedTool = lastFailedTool
	}

	// Detect repeated failures on the same file
	fileCounts := make(map[string]int)
	for _, e := range recentErrors {
		if e.File != "" {
			fileCounts[e.File]++
		}
	}
	for file, count := range fileCounts {
		if count >= 3 {
			diag.FailedFile = file
			diag.IsLooping = true
			break
		}
	}

	// Escalation logic
	if diag.LoopCount >= 5 {
		diag.ShouldEscalate = true
		diag.EscalationReason = fmt.Sprintf("tool %s has failed %d consecutive times", diag.FailedTool, diag.LoopCount)
	}

	return diag
}
