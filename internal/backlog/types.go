// Package backlog implements a persistent, cross-session intent queue for
// deferred work. Work items capture context across sessions — by the time
// work is eventually executed, the system has a rich record of why it was
// proposed, what was learned since, and what constraints have emerged.
//
// Storage is file-based (one JSON file per work item), with an interface
// abstraction for future cloud-sync implementations.
package backlog

import "time"

// Kind classifies the scale and nature of a work item.
type Kind string

const (
	KindTask         Kind = "task"         // Single action (trivial-small)
	KindPlugin       Kind = "plugin"       // New tool binary (small-medium)
	KindSkill        Kind = "skill"        // New knowledge doc (trivial-small)
	KindSubsystem    Kind = "subsystem"    // Package-level work (medium-large)
	KindArchitecture Kind = "architecture" // Cross-cutting design (large)
	KindResearch     Kind = "research"     // Discovery/evaluation (unbounded)
)

// ValidKinds contains all valid Kind values.
var ValidKinds = map[Kind]bool{
	KindTask:         true,
	KindPlugin:       true,
	KindSkill:        true,
	KindSubsystem:    true,
	KindArchitecture: true,
	KindResearch:     true,
}

// Status represents the lifecycle state of a work item.
type Status string

const (
	StatusProposed   Status = "proposed"
	StatusApproved   Status = "approved"
	StatusBlocked    Status = "blocked"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusRejected   Status = "rejected"
)

// ValidStatuses contains all valid Status values.
var ValidStatuses = map[Status]bool{
	StatusProposed:   true,
	StatusApproved:   true,
	StatusBlocked:    true,
	StatusInProgress: true,
	StatusDone:       true,
	StatusRejected:   true,
}

// Effort classifies the estimated scope of work.
type Effort string

const (
	EffortTrivial  Effort = "trivial"
	EffortSmall    Effort = "small"
	EffortMedium   Effort = "medium"
	EffortLarge    Effort = "large"
	EffortResearch Effort = "research"
)

// ValidEfforts contains all valid Effort values.
var ValidEfforts = map[Effort]bool{
	EffortTrivial:  true,
	EffortSmall:    true,
	EffortMedium:   true,
	EffortLarge:    true,
	EffortResearch: true,
}

// Trigger describes how a work item was identified.
type Trigger string

const (
	TriggerUserRequest      Trigger = "user_request"
	TriggerInkwellDiagnosis Trigger = "inkwell_diagnosis"
	TriggerAgentObservation Trigger = "agent_observation"
	TriggerDecomposition    Trigger = "decomposition"
)

// WorkItem represents a unit of deferred work in the backlog.
type WorkItem struct {
	// Identity
	ID        string    `json:"id"`         // wi_<8hex>
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`      // Short: "Structured JSON query tool"
	Kind      Kind      `json:"kind"`       // See Kind constants

	// Scope
	Project string `json:"project,omitempty"` // Auto-detected or explicit

	// Lifecycle
	Status     Status `json:"status"`                // See Status constants
	Priority   int    `json:"priority"`              // 0-100 (higher = more important)
	AssignedTo string `json:"assigned_to,omitempty"` // "human" | "agent" | "" (unassigned)

	// Origin: what triggered the realization
	Origin Origin `json:"origin"`

	// Context accumulation: grows across sessions
	Snapshots []Snapshot `json:"snapshots"`

	// Decomposition
	ParentID  string   `json:"parent_id,omitempty"`  // Hierarchy
	Children  []string `json:"children,omitempty"`   // Sub-item IDs
	DependsOn []string `json:"depends_on,omitempty"` // Blocking IDs

	// Execution hints
	Effort      Effort   `json:"effort"`      // See Effort constants
	Constraints []string `json:"constraints"` // e.g., "needs-human-approval", "modifies-proto"
	Tags        []string `json:"tags"`        // Free-form labels

	// Results (populated when status=done)
	Outcome   string   `json:"outcome,omitempty"`   // Summary of what was produced
	Artifacts []string `json:"artifacts,omitempty"` // Paths, commit SHAs, etc.
}

// Origin records what triggered the creation of a work item.
type Origin struct {
	SessionID   string `json:"session_id"`
	Turn        int    `json:"turn"`
	Trigger     Trigger `json:"trigger"`     // How it was identified
	Description string `json:"description"` // Natural language why
}

// Snapshot captures a point-in-time context addition to a work item.
// Each snapshot is lightweight (a paragraph or two), not a full session dump.
type Snapshot struct {
	SessionID  string    `json:"session_id"`
	Timestamp  time.Time `json:"timestamp"`
	Context    string    `json:"context"`    // Relevant excerpt
	Refinement string    `json:"refinement"` // What new information this adds
}

// ListFilter specifies criteria for listing work items.
type ListFilter struct {
	Status  string // Filter by status ("" = any)
	Kind    string // Filter by kind ("" = any)
	Tag     string // Filter by tag ("" = any)
	Project string // Filter by project ("" = current, "*" = all)
	Limit   int    // Max results (0 = default 20)
}

// WorkItemSummary is a lightweight view of a work item for list displays.
type WorkItemSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    Status    `json:"status"`
	Kind      Kind      `json:"kind"`
	Project   string    `json:"project"`
	Priority  int       `json:"priority"`
	Effort    Effort    `json:"effort"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks that the work item has valid required fields.
func (w *WorkItem) Validate() error {
	if w.Title == "" {
		return &ValidationError{Field: "title", Message: "title is required"}
	}
	if !ValidKinds[w.Kind] {
		return &ValidationError{Field: "kind", Message: "invalid kind: " + string(w.Kind)}
	}
	if !ValidStatuses[w.Status] {
		return &ValidationError{Field: "status", Message: "invalid status: " + string(w.Status)}
	}
	if w.Priority < 0 || w.Priority > 100 {
		return &ValidationError{Field: "priority", Message: "priority must be 0-100"}
	}
	if w.Effort != "" && !ValidEfforts[w.Effort] {
		return &ValidationError{Field: "effort", Message: "invalid effort: " + string(w.Effort)}
	}
	return nil
}

// Summary returns a lightweight summary of this work item.
func (w *WorkItem) Summary() WorkItemSummary {
	return WorkItemSummary{
		ID:        w.ID,
		Title:     w.Title,
		Status:    w.Status,
		Kind:      w.Kind,
		Project:   w.Project,
		Priority:  w.Priority,
		Effort:    w.Effort,
		UpdatedAt: w.UpdatedAt,
	}
}

// ValidationError represents a field validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
