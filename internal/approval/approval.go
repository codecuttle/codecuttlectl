// Package approval implements user confirmation gates for destructive operations.
//
// The approval system sits between tool dispatch and execution. When a tool
// invocation matches a destructive pattern, execution is paused until the user
// explicitly approves or denies it. This prevents the agent from accidentally
// running commands like `rm -rf /`, `DROP DATABASE`, or `git push --force`
// without human oversight.
//
// The system is designed to be:
//   - Composable: works in TUI, REPL, and one-shot modes
//   - Auditable: all approval decisions are recorded in the Inkwell
//   - Conservative: when in doubt, require confirmation
//   - Bypassable: `--auto-approve` flag for automated pipelines
package approval

import (
	"fmt"
	"regexp"
	"strings"
)

// Decision represents the user's response to an approval request.
type Decision int

const (
	// Pending means no decision has been made yet.
	Pending Decision = iota
	// Approved means the user explicitly allowed the operation.
	Approved
	// Denied means the user rejected the operation.
	Denied
	// AutoApproved means the operation was approved by policy (--auto-approve).
	AutoApproved
)

func (d Decision) String() string {
	switch d {
	case Pending:
		return "pending"
	case Approved:
		return "approved"
	case Denied:
		return "denied"
	case AutoApproved:
		return "auto_approved"
	default:
		return "unknown"
	}
}

// Request describes a destructive operation awaiting user confirmation.
type Request struct {
	ToolName  string // e.g., "bash_exec", "git"
	ToolUseID string // Unique tool invocation ID
	Command   string // The specific command or operation (human-readable)
	Reason    string // Why this requires confirmation
	Risk      Risk   // Severity level
}

// Risk classifies the severity of the destructive operation.
type Risk int

const (
	// RiskLow means the operation could cause minor inconvenience (e.g., overwriting a file).
	RiskLow Risk = iota
	// RiskMedium means the operation could cause data loss that is recoverable (e.g., git reset).
	RiskMedium
	// RiskHigh means the operation could cause significant, potentially unrecoverable damage.
	RiskHigh
	// RiskCritical means the operation targets system-level resources or could cause catastrophic damage.
	RiskCritical
)

func (r Risk) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Emoji returns a risk-level indicator for display.
func (r Risk) Emoji() string {
	switch r {
	case RiskLow:
		return "⚠️"
	case RiskMedium:
		return "⚠️"
	case RiskHigh:
		return "🔴"
	case RiskCritical:
		return "💀"
	default:
		return "❓"
	}
}

// Check examines a tool invocation and returns an approval Request if the
// operation is destructive. Returns nil if no confirmation is needed.
func Check(toolName string, inputJSON string) *Request {
	switch toolName {
	case "bash_exec":
		return checkBashExec(inputJSON)
	case "git":
		return checkGit(inputJSON)
	default:
		return nil
	}
}

// --- bash_exec destructive pattern detection ---

// destructivePattern groups a regex, risk level, and human-readable reason.
type destructivePattern struct {
	pattern *regexp.Regexp
	risk    Risk
	reason  string
}

var bashDestructivePatterns = []destructivePattern{
	// File/directory deletion
	{
		pattern: regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+|--recursive|--force).*(/|\$HOME|\$\{HOME\}|~)`),
		risk:    RiskCritical,
		reason:  "Recursive/forced file deletion targeting broad paths",
	},
	{
		pattern: regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+|--recursive|--force)`),
		risk:    RiskHigh,
		reason:  "Recursive or forced file deletion",
	},
	{
		pattern: regexp.MustCompile(`\brm\s+[^|;]+\*`),
		risk:    RiskMedium,
		reason:  "File deletion with wildcard pattern",
	},
	// Database destruction
	{
		pattern: regexp.MustCompile(`(?i)\bDROP\s+(DATABASE|TABLE|SCHEMA)\b`),
		risk:    RiskCritical,
		reason:  "Database/table/schema drop operation",
	},
	{
		pattern: regexp.MustCompile(`(?i)\bTRUNCATE\s+TABLE\b`),
		risk:    RiskHigh,
		reason:  "Table truncation (deletes all data)",
	},
	{
		pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\b.*\bWHERE\b.*=.*`),
		risk:    RiskMedium,
		reason:  "Targeted database record deletion",
	},
	{
		pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\S+\s*;?\s*$`),
		risk:    RiskHigh,
		reason:  "Unconditional DELETE FROM (no WHERE clause)",
	},
	// System commands
	{
		pattern: regexp.MustCompile(`\b(mkfs|fdisk|dd\s+if=.+\s+of=/dev/)\b`),
		risk:    RiskCritical,
		reason:  "Disk formatting or low-level write to device",
	},
	{
		pattern: regexp.MustCompile(`\b(shutdown|reboot|init\s+[06]|systemctl\s+(poweroff|reboot|halt))\b`),
		risk:    RiskCritical,
		reason:  "System shutdown or reboot",
	},
	{
		pattern: regexp.MustCompile(`\bchmod\s+(-R\s+)?[0-7]*[0-7][0-7][0-7]\s+/`),
		risk:    RiskHigh,
		reason:  "Recursive permission change on root-relative path",
	},
	{
		pattern: regexp.MustCompile(`\bchown\s+-R\s+`),
		risk:    RiskMedium,
		reason:  "Recursive ownership change",
	},
	// Container/infrastructure destruction
	{
		pattern: regexp.MustCompile(`\bdocker\s+(system\s+prune|rm\s+-f|rmi\s+-f|volume\s+rm)`),
		risk:    RiskMedium,
		reason:  "Docker resource removal",
	},
	{
		pattern: regexp.MustCompile(`\bkubectl\s+delete\s+(namespace|ns|deployment|pod|service)\b`),
		risk:    RiskHigh,
		reason:  "Kubernetes resource deletion",
	},
	// Cloud infrastructure
	{
		pattern: regexp.MustCompile(`\b(terraform|tofu)\s+destroy\b`),
		risk:    RiskCritical,
		reason:  "Infrastructure destruction (terraform/tofu destroy)",
	},
	{
		pattern: regexp.MustCompile(`\baws\s+.*\b(delete-|terminate-|remove-)\b`),
		risk:    RiskHigh,
		reason:  "AWS resource deletion",
	},
	// Package/environment
	{
		pattern: regexp.MustCompile(`\bpip\s+install\b.*--break-system-packages`),
		risk:    RiskMedium,
		reason:  "System Python package modification",
	},
	{
		pattern: regexp.MustCompile(`\bcurl\s+.*\|\s*(sudo\s+)?(ba)?sh\b`),
		risk:    RiskHigh,
		reason:  "Pipe remote content to shell execution",
	},
}

func checkBashExec(inputJSON string) *Request {
	// Extract command from the input JSON (quick parse without full unmarshal)
	command := extractJSONField(inputJSON, "command")
	if command == "" {
		return nil
	}

	for _, dp := range bashDestructivePatterns {
		if dp.pattern.MatchString(command) {
			return &Request{
				ToolName: "bash_exec",
				Command:  command,
				Reason:   dp.reason,
				Risk:     dp.risk,
			}
		}
	}

	return nil
}

// --- git destructive pattern detection ---

func checkGit(inputJSON string) *Request {
	subcommand := extractJSONField(inputJSON, "subcommand")
	// Note: The git plugin already blocks push --force and reset --hard,
	// but we add approval gates for other destructive git operations that
	// are allowed but risky.

	switch subcommand {
	case "push":
		// push with --force-with-lease is safer but still destructive
		if strings.Contains(inputJSON, "force-with-lease") {
			return &Request{
				ToolName: "git",
				Command:  "git push --force-with-lease",
				Reason:   "Force push (with lease) rewrites remote history",
				Risk:     RiskMedium,
			}
		}
	case "rebase":
		// Interactive rebase can rewrite history
		if strings.Contains(inputJSON, "-i") || strings.Contains(inputJSON, "interactive") {
			return &Request{
				ToolName: "git",
				Command:  "git rebase -i (interactive rebase)",
				Reason:   "Interactive rebase rewrites commit history",
				Risk:     RiskMedium,
			}
		}
	case "checkout":
		// checkout with -- discards uncommitted changes
		if strings.Contains(inputJSON, "-- ") || strings.Contains(inputJSON, "\".\"") {
			return &Request{
				ToolName: "git",
				Command:  "git checkout -- (discard changes)",
				Reason:   "Discards uncommitted changes to working tree files",
				Risk:     RiskMedium,
			}
		}
	}

	return nil
}

// --- Helper ---

// extractJSONField does a quick substring extraction for a known field name.
// This avoids importing encoding/json in the hot path (approval checks happen
// on every tool call). Falls back gracefully on malformed JSON.
func extractJSONField(jsonStr, field string) string {
	// Look for "field":"value" or "field": "value"
	key := fmt.Sprintf(`"%s"`, field)
	idx := strings.Index(jsonStr, key)
	if idx < 0 {
		return ""
	}

	// Skip past the key and colon
	rest := jsonStr[idx+len(key):]
	rest = strings.TrimLeft(rest, " \t\n\r:")

	if len(rest) == 0 {
		return ""
	}

	// Handle string value
	if rest[0] == '"' {
		rest = rest[1:]
		end := findUnescapedQuote(rest)
		if end < 0 {
			return ""
		}
		return unescapeJSON(rest[:end])
	}

	return ""
}

// findUnescapedQuote finds the index of the first unescaped double-quote.
func findUnescapedQuote(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++ // skip escaped char
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

// unescapeJSON handles basic JSON string escape sequences.
func unescapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	return s
}

// FormatConfirmation returns a user-facing confirmation message for the TUI/REPL.
func FormatConfirmation(req *Request) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n%s  DESTRUCTIVE OPERATION DETECTED (risk: %s)\n", req.Risk.Emoji(), req.Risk))
	sb.WriteString(fmt.Sprintf("   Tool: %s\n", req.ToolName))
	sb.WriteString(fmt.Sprintf("   Command: %s\n", req.Command))
	sb.WriteString(fmt.Sprintf("   Reason: %s\n", req.Reason))
	sb.WriteString("\n   Allow this operation? [y/N] ")
	return sb.String()
}
