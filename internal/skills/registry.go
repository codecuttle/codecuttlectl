package skills

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
)

// DefaultBudget is the default total token budget for skill injection.
const DefaultBudget = 24000

// DefaultMaxPerSkill is the maximum tokens a single skill can consume.
const DefaultMaxPerSkill = 8000

// RegisteredSkill is a skill associated with its source plugin and parsed trigger.
type RegisteredSkill struct {
	PluginName string
	PluginVer  string
	Skill      *pb.Skill
	Trigger    TriggerExpr
	TokenCost  int // Estimated token cost
}

// Registry manages all skills from all loaded plugins.
type Registry struct {
	mu     sync.RWMutex
	skills []RegisteredSkill
	budget int
}

// NewRegistry creates a skill registry with the given token budget.
func NewRegistry(budget int) *Registry {
	if budget <= 0 {
		budget = DefaultBudget
	}
	return &Registry{
		budget: budget,
	}
}

// Register adds skills from a plugin to the registry.
func (r *Registry) Register(pluginName, pluginVersion string, skills []*pb.Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range skills {
		tokenCost := int(s.EstimatedTokens)
		if tokenCost <= 0 {
			tokenCost = len(s.Content) / 4 // Rough estimate: 4 chars per token
		}
		if tokenCost > DefaultMaxPerSkill {
			tokenCost = DefaultMaxPerSkill // Cap individual skills
		}

		r.skills = append(r.skills, RegisteredSkill{
			PluginName: pluginName,
			PluginVer:  pluginVersion,
			Skill:      s,
			Trigger:    ParseTrigger(s.Trigger),
			TokenCost:  tokenCost,
		})
	}
}

// ScoredSkill pairs a registered skill with its computed relevance score.
type ScoredSkill struct {
	RegisteredSkill
	Score int // Combined relevance + priority score
}

// Evaluate returns skills whose triggers match the given context,
// sorted by relevance (descending) then priority (descending).
func (r *Registry) Evaluate(ctx Context) []ScoredSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var matched []ScoredSkill

	for _, rs := range r.skills {
		relevance := rs.Trigger.Relevance(ctx)
		if relevance == 0 {
			continue
		}

		// Combined score: relevance (0-100) weighted 2x + priority (0-100)
		score := relevance*2 + int(rs.Skill.Priority)

		matched = append(matched, ScoredSkill{
			RegisteredSkill: rs,
			Score:           score,
		})
	}

	// Sort: highest score first
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Score > matched[j].Score
	})

	return matched
}

// Render takes matched skills and produces the injection text within the token budget.
// Returns empty string if no skills should be injected.
func (r *Registry) Render(matched []ScoredSkill) string {
	if len(matched) == 0 {
		return ""
	}

	var sb strings.Builder
	remaining := r.budget
	injected := 0

	for _, s := range matched {
		cost := s.TokenCost
		if cost > remaining {
			continue // Skip skills that don't fit in remaining budget
		}

		if injected == 0 {
			sb.WriteString("\n\n## Active Skills\n")
		}

		sb.WriteString(fmt.Sprintf("\n### %s (from %s v%s)\n\n",
			s.Skill.Name, s.PluginName, s.PluginVer))
		sb.WriteString(s.Skill.Content)
		sb.WriteString("\n")

		remaining -= cost
		injected++
	}

	return sb.String()
}

// GetByName retrieves a specific skill by name (for on_request/get_skill).
func (r *Registry) GetByName(name string) (*RegisteredSkill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := range r.skills {
		if r.skills[i].Skill.Name == name {
			return &r.skills[i], true
		}
	}
	return nil, false
}

// List returns all registered skills (for --list-skills and tool_info).
func (r *Registry) List() []RegisteredSkill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]RegisteredSkill, len(r.skills))
	copy(result, r.skills)
	return result
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// SetBudget updates the token budget.
func (r *Registry) SetBudget(budget int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.budget = budget
}
