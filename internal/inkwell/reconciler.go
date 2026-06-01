package inkwell

import (
	"fmt"
	"strings"

	"github.com/codecuttle/codecuttlectl/internal/session"
)

// Advice represents the reconciler's recommendation for the next model call.
type Advice struct {
	// InjectPrompt is additional text to prepend to the system prompt.
	// Empty string means no injection.
	InjectPrompt string

	// ShouldAbort means the reconciler recommends stopping the current approach.
	ShouldAbort bool
	AbortReason string
}

// Reconciler analyzes Inkwell state and generates corrective guidance.
// It sits between tool execution and the next model call, providing
// the "Inkwell Reconciliation Loop" described in the architecture.
type Reconciler struct {
	// LookbackWindow is how many recent entries to analyze. Default: 10
	LookbackWindow int
	// MaxConsecutiveFailures before escalation. Default: 5
	MaxConsecutiveFailures int
}

// NewReconciler creates a reconciler with default settings.
func NewReconciler() *Reconciler {
	return &Reconciler{
		LookbackWindow:         10,
		MaxConsecutiveFailures: 5,
	}
}

// Advise examines the current Inkwell state and returns corrective guidance.
// Call this after each tool execution failure to get prompt injection recommendations.
func (r *Reconciler) Advise(entries []session.InkEntry) Advice {
	if len(entries) == 0 {
		return Advice{}
	}

	// Only run diagnosis if the most recent entry was an error
	lastEntry := entries[len(entries)-1]
	if !lastEntry.IsError {
		return Advice{}
	}

	diag := Diagnose(entries, r.LookbackWindow)

	// If escalation is needed, recommend aborting the current strategy
	if diag.ShouldEscalate {
		return Advice{
			InjectPrompt: r.buildEscalationPrompt(diag),
			ShouldAbort:  diag.LoopCount >= r.MaxConsecutiveFailures+2,
			AbortReason:  diag.EscalationReason,
		}
	}

	// If looping detected, inject stronger corrective guidance
	if diag.IsLooping {
		return Advice{
			InjectPrompt: r.buildLoopingPrompt(diag),
		}
	}

	// Standard single-error correction
	if len(diag.RecentErrors) > 0 {
		lastError := diag.RecentErrors[len(diag.RecentErrors)-1]
		return Advice{
			InjectPrompt: r.buildCorrectionPrompt(lastError, diag),
		}
	}

	return Advice{}
}

// --- Prompt builders ---

func (r *Reconciler) buildCorrectionPrompt(err ClassifiedError, diag Diagnosis) string {
	var sb strings.Builder

	sb.WriteString("\n\n## Inkwell Diagnostic Alert\n\n")
	sb.WriteString("Your last tool execution failed. Analyze the error carefully before retrying.\n\n")

	if err.Language != "" {
		sb.WriteString(fmt.Sprintf("**Language:** %s\n", err.Language))
	}
	if err.File != "" {
		sb.WriteString(fmt.Sprintf("**File:** %s", err.File))
		if err.Line > 0 {
			sb.WriteString(fmt.Sprintf(":%d", err.Line))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("**Error Class:** %s\n", err.Class))

	if err.Message != "" {
		sb.WriteString(fmt.Sprintf("**Message:** %s\n", err.Message))
	}

	if err.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\n**Guidance:** %s\n", err.Suggestion))
	}

	sb.WriteString("\n**Required actions:**\n")
	sb.WriteString("1. Read the error output carefully\n")
	sb.WriteString("2. If the error is in a file, re-read that file to see current state\n")
	sb.WriteString("3. Fix the root cause, not just the symptom\n")
	sb.WriteString("4. Verify your fix compiles/runs before moving on\n")

	return sb.String()
}

func (r *Reconciler) buildLoopingPrompt(diag Diagnosis) string {
	var sb strings.Builder

	sb.WriteString("\n\n## Inkwell Escalation Warning\n\n")
	sb.WriteString(fmt.Sprintf("**LOOP DETECTED:** Tool `%s` has failed %d times in the recent execution window.\n\n",
		diag.FailedTool, diag.LoopCount))

	if diag.FailedFile != "" {
		sb.WriteString(fmt.Sprintf("**Repeatedly failing on:** %s\n\n", diag.FailedFile))
	}

	sb.WriteString("You are repeating the same failing approach. **STOP and change strategy.**\n\n")
	sb.WriteString("**Required actions:**\n")
	sb.WriteString("1. Do NOT retry the same tool call with similar parameters\n")
	sb.WriteString("2. Step back and re-read the relevant files to understand the current state\n")
	sb.WriteString("3. Consider a fundamentally different approach:\n")

	switch diag.DominantClass {
	case ClassCompile, ClassType:
		sb.WriteString("   - Read the entire file to understand the type system context\n")
		sb.WriteString("   - Check import statements and package boundaries\n")
		sb.WriteString("   - Look at how similar code is structured elsewhere in the project\n")
	case ClassImport:
		sb.WriteString("   - List available packages/modules to find the correct import path\n")
		sb.WriteString("   - Check if the dependency needs to be installed first\n")
		sb.WriteString("   - Verify the module/package name spelling\n")
	case ClassNotFound:
		sb.WriteString("   - List the directory to see what actually exists\n")
		sb.WriteString("   - Check if you need to create the file/directory first\n")
		sb.WriteString("   - Verify the path is absolute and correctly spelled\n")
	case ClassSyntax:
		sb.WriteString("   - Re-read the file around the error location\n")
		sb.WriteString("   - Pay attention to matching brackets, quotes, and statement terminators\n")
		sb.WriteString("   - Consider rewriting the problematic section from scratch\n")
	default:
		sb.WriteString("   - Use bash_exec to gather more diagnostic information\n")
		sb.WriteString("   - Read documentation or similar working code for reference\n")
		sb.WriteString("   - Simplify your approach — do less in each step\n")
	}

	return sb.String()
}

func (r *Reconciler) buildEscalationPrompt(diag Diagnosis) string {
	var sb strings.Builder

	sb.WriteString("\n\n## CRITICAL: Inkwell Reconciliation Abort\n\n")
	sb.WriteString(fmt.Sprintf("**FAILURE LIMIT REACHED:** %s\n\n", diag.EscalationReason))
	sb.WriteString("The current approach has failed too many times. You MUST:\n\n")
	sb.WriteString("1. **STOP** attempting the failing operation\n")
	sb.WriteString("2. **REPORT** what you were trying to do and why it's failing\n")
	sb.WriteString("3. **EXPLAIN** what alternative approaches might work\n")
	sb.WriteString("4. **ASK** the user for guidance if you cannot resolve this autonomously\n\n")
	sb.WriteString("Do NOT retry the same tool call. Acknowledge the failure and propose alternatives.\n")

	return sb.String()
}
