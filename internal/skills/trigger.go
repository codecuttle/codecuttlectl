// Package skills implements the conditional skill injection system for the
// Codecuttle meta-harness. Skills are versioned knowledge, workflows, and
// behavioral guidance that plugins embed and ship alongside their tools.
//
// Skills are conditionally injected into the LLM context window based on
// trigger expressions evaluated against the current session state. This
// enables context-sensitive guidance without bloating every request.
package skills

import (
	"path/filepath"
	"strings"
)

// TriggerKind identifies the type of trigger condition.
type TriggerKind int

const (
	TriggerAlways    TriggerKind = iota // Always inject (subject to budget)
	TriggerOnRequest                    // Only when explicitly asked via get_skill
	TriggerOnError                      // On a specific error class (or * for any)
	TriggerOnTool                       // When a specific tool was used recently
	TriggerOnFile                       // When a matching file was referenced
	TriggerOnLang                       // When a language was detected
	TriggerOnTurn                       // On a specific turn (e.g., "first")
	TriggerOnLoop                       // When looping is detected
)

// TriggerCondition is a single parsed trigger predicate.
type TriggerCondition struct {
	Kind    TriggerKind
	Value   string // The argument: error class, tool name, file pattern, language, etc.
}

// TriggerExpr is a disjunction (OR) of conditions — any one matching means the trigger fires.
type TriggerExpr []TriggerCondition

// ParseTrigger parses a trigger expression string into a structured form.
// Expressions are '|'-separated conditions like: "on_error:compile|on_language:go"
func ParseTrigger(expr string) TriggerExpr {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return TriggerExpr{{Kind: TriggerOnRequest}} // Default: on_request only
	}

	parts := strings.Split(expr, "|")
	var conditions []TriggerCondition

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		conditions = append(conditions, parseCondition(part))
	}

	if len(conditions) == 0 {
		return TriggerExpr{{Kind: TriggerOnRequest}}
	}

	return TriggerExpr(conditions)
}

func parseCondition(s string) TriggerCondition {
	switch {
	case s == "always":
		return TriggerCondition{Kind: TriggerAlways}
	case s == "on_request":
		return TriggerCondition{Kind: TriggerOnRequest}
	case s == "on_loop":
		return TriggerCondition{Kind: TriggerOnLoop}
	case strings.HasPrefix(s, "on_error:"):
		return TriggerCondition{Kind: TriggerOnError, Value: strings.TrimPrefix(s, "on_error:")}
	case strings.HasPrefix(s, "on_tool:"):
		return TriggerCondition{Kind: TriggerOnTool, Value: strings.TrimPrefix(s, "on_tool:")}
	case strings.HasPrefix(s, "on_file:"):
		return TriggerCondition{Kind: TriggerOnFile, Value: strings.TrimPrefix(s, "on_file:")}
	case strings.HasPrefix(s, "on_language:"):
		return TriggerCondition{Kind: TriggerOnLang, Value: strings.TrimPrefix(s, "on_language:")}
	case strings.HasPrefix(s, "on_turn:"):
		return TriggerCondition{Kind: TriggerOnTurn, Value: strings.TrimPrefix(s, "on_turn:")}
	default:
		// Unrecognized → treat as on_request (safe default, won't fire automatically)
		return TriggerCondition{Kind: TriggerOnRequest}
	}
}

// Context holds the current session state used to evaluate triggers.
type Context struct {
	RecentErrors    []string // Error class strings from recent Inkwell entries
	RecentTools     []string // Tool names used in recent steps
	RecentFiles     []string // File paths referenced in recent tool calls
	DetectedLang    string   // Language detected in recent outputs
	IsFirstTurn     bool
	IsLooping       bool
	TurnNumber      int
	ExplicitRequest string // If the agent explicitly requested a skill by name
}

// Matches evaluates whether this trigger expression fires given the context.
// Returns true if ANY condition in the disjunction matches.
func (te TriggerExpr) Matches(ctx Context) bool {
	for _, cond := range te {
		if cond.matches(ctx) {
			return true
		}
	}
	return false
}

// IsOnRequestOnly returns true if the trigger only fires on explicit request.
func (te TriggerExpr) IsOnRequestOnly() bool {
	for _, cond := range te {
		if cond.Kind != TriggerOnRequest {
			return false
		}
	}
	return true
}

func (tc TriggerCondition) matches(ctx Context) bool {
	switch tc.Kind {
	case TriggerAlways:
		return true

	case TriggerOnRequest:
		return ctx.ExplicitRequest != ""

	case TriggerOnLoop:
		return ctx.IsLooping

	case TriggerOnError:
		if tc.Value == "*" {
			return len(ctx.RecentErrors) > 0
		}
		for _, errClass := range ctx.RecentErrors {
			if errClass == tc.Value {
				return true
			}
		}
		return false

	case TriggerOnTool:
		for _, tool := range ctx.RecentTools {
			if tool == tc.Value {
				return true
			}
		}
		return false

	case TriggerOnFile:
		for _, file := range ctx.RecentFiles {
			// Support glob matching on the filename
			base := filepath.Base(file)
			if matched, _ := filepath.Match(tc.Value, base); matched {
				return true
			}
			// Also try matching against the full relative path
			if matched, _ := filepath.Match(tc.Value, file); matched {
				return true
			}
		}
		return false

	case TriggerOnLang:
		return ctx.DetectedLang == tc.Value

	case TriggerOnTurn:
		switch tc.Value {
		case "first":
			return ctx.IsFirstTurn
		}
		return false

	default:
		return false
	}
}

// Relevance scores how relevant a trigger match is given the context.
// Higher score = more relevant. Used for weighted injection ordering.
// Returns 0 if not matching. Range: 1-100.
func (te TriggerExpr) Relevance(ctx Context) int {
	if !te.Matches(ctx) {
		return 0
	}

	maxScore := 0
	for _, cond := range te {
		if !cond.matches(ctx) {
			continue
		}
		score := cond.relevanceScore(ctx)
		if score > maxScore {
			maxScore = score
		}
	}
	return maxScore
}

func (tc TriggerCondition) relevanceScore(ctx Context) int {
	switch tc.Kind {
	case TriggerAlways:
		return 10 // Low relevance — always on
	case TriggerOnRequest:
		return 100 // Highest — explicitly asked for
	case TriggerOnLoop:
		return 90 // Very high — agent is stuck
	case TriggerOnError:
		if tc.Value == "*" {
			return 50 // Generic error match
		}
		return 70 // Specific error class match
	case TriggerOnTool:
		return 40 // Tool was used recently
	case TriggerOnFile:
		return 60 // Specific file context
	case TriggerOnLang:
		return 55 // Language match
	case TriggerOnTurn:
		return 30 // Structural match
	default:
		return 0
	}
}
