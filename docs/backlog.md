# Work Backlog

## Overview

The Work Backlog is a persistent, cross-session intent queue that captures work the system or user recognizes as needed but defers for later execution. It generalizes beyond plugin generation to encompass any deferred task: subsystem design, research, architecture work, multi-session R&D, or coordination with external systems.

Work items accumulate context across sessions. When a session touches a work item (refines it, adds observations, encounters related failures), a context snapshot is appended. By the time work is eventually executed, the system has a rich record of *why* it was proposed, *what was learned since*, and *what constraints have emerged*.

## Storage

**Location:** `~/.local/share/codecuttlectl/backlog/` (respects `$XDG_DATA_HOME`)

Each work item is a single JSON file: `{work_item_id}.json`. Writes are atomic (same pattern as sessions: write `.tmp`, rename to `.json`).

**Future:** The file-based store sits behind a `BacklogStore` interface. A future cloud-sync implementation (S3, GCS, or a shared API) enables team-wide backlog visibility with permissioned access. The sync strategy is append-only snapshots, making conflict resolution straightforward across distributed agents.

```go
type BacklogStore interface {
    Create(item *WorkItem) (string, error)
    Save(id string, item *WorkItem) error
    Load(id string) (*WorkItem, error)
    List(filter ListFilter) ([]WorkItemSummary, error)
    Delete(id string) error
    Prune(maxAge time.Duration) (int, error)
}

type ListFilter struct {
    Status  string // Filter by status ("" = any)
    Kind    string // Filter by kind ("" = any)
    Tag     string // Filter by tag ("" = any)
    Project string // Filter by project ("" = current, "*" = all)
    Limit   int    // Max results (0 = default 20)
}

type WorkItemSummary struct {
    ID        string    `json:"id"`
    Title     string    `json:"title"`
    Status    string    `json:"status"`
    Kind      string    `json:"kind"`
    Project   string    `json:"project"`
    Priority  int       `json:"priority"`
    Effort    string    `json:"effort"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

## Work Item Structure

```go
type WorkItem struct {
    // Identity
    ID        string    `json:"id"`         // wi_<8hex>
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Title     string    `json:"title"`      // Short: "Structured JSON query tool"
    Kind      string    `json:"kind"`       // See Kind taxonomy below

    // Scope
    Project   string    `json:"project,omitempty"` // Auto-detected or explicit (see Project Scoping)

    // Lifecycle
    Status    string    `json:"status"`     // See Status lifecycle below
    Priority  int       `json:"priority"`   // 0-100 (higher = more important)
    AssignedTo string   `json:"assigned_to,omitempty"` // "human" | "agent" | "" (unassigned)

    // Origin: what triggered the realization
    Origin    Origin    `json:"origin"`

    // Context accumulation: grows across sessions
    Snapshots []Snapshot `json:"snapshots"`

    // Decomposition
    ParentID  string   `json:"parent_id,omitempty"`  // Hierarchy
    Children  []string `json:"children,omitempty"`   // Sub-item IDs
    DependsOn []string `json:"depends_on,omitempty"` // Blocking IDs

    // Execution hints
    Effort      string   `json:"effort"`      // "trivial"|"small"|"medium"|"large"|"research"
    Constraints []string `json:"constraints"` // e.g., "needs-human-approval", "modifies-proto"
    Tags        []string `json:"tags"`        // Free-form labels

    // Results (populated when status=done)
    Outcome   string   `json:"outcome,omitempty"`   // Summary of what was produced
    Artifacts []string `json:"artifacts,omitempty"` // Paths, commit SHAs, etc.
}
```

### Origin

```go
type Origin struct {
    SessionID   string `json:"session_id"`
    Turn        int    `json:"turn"`
    Trigger     string `json:"trigger"`     // How it was identified
    Description string `json:"description"` // Natural language why
}
```

Trigger values:

| Trigger | Meaning |
|---------|---------|
| `user_request` | User explicitly said "we need X" |
| `inkwell_diagnosis` | Inkwell detected a capability gap or pattern |
| `agent_observation` | Agent noticed a repeated workaround or missing tool |
| `decomposition` | Spawned as a sub-task of another work item |

### Snapshot (Context Accumulation)

```go
type Snapshot struct {
    SessionID  string    `json:"session_id"`
    Timestamp  time.Time `json:"timestamp"`
    Context    string    `json:"context"`    // Relevant excerpt (ink entries, conversation, etc.)
    Refinement string    `json:"refinement"` // What new information this adds
}
```

Each snapshot is lightweight (a paragraph or two of relevant context, not a full session dump). The system captures:
- The Inkwell entries that triggered the observation (if `inkwell_diagnosis`)
- The user's statement (if `user_request`)
- Relevant tool outputs or error patterns
- Any new constraints or requirements discovered since the last snapshot

## Kind Taxonomy

| Kind | Scale | Typical Effort | Example |
|------|-------|---------------|---------|
| `task` | Single action | trivial-small | "Fix that off-by-one in classifier.go" |
| `plugin` | New tool binary | small-medium | "Structured JSON query tool" |
| `skill` | New knowledge doc | trivial-small | "Add Python debugging guide" |
| `subsystem` | Package-level work | medium-large | "Python-specific Inkwell classifier" |
| `architecture` | Cross-cutting design | large | "Chromatophore routing engine" |
| `research` | Discovery/evaluation | unbounded | "Evaluate vector DBs for Optic Lobe" |

## Status Lifecycle

```
proposed --> approved --> in_progress --> done
    |           |            |
    v           v            v
 rejected    blocked    blocked --> in_progress --> done
```

| Status | Meaning |
|--------|---------|
| `proposed` | Identified but not yet approved. May still be refined. |
| `approved` | Human (or auto-approve flag) has greenlit this work. |
| `blocked` | Waiting on a dependency (another work item or external factor). |
| `in_progress` | Actively being worked on (by human or agent). |
| `done` | Completed. Outcome and artifacts recorded. |
| `rejected` | Decided against. Reason captured in outcome field. |

## Decomposition

When a work item is proposed, the agent attempts a first-pass decomposition into sub-tasks. This is speculative — a human can revise the decomposition at approval time.

Example:
```json
{
  "id": "wi_a3f8c2d1",
  "title": "Implement Chromatophore routing engine",
  "kind": "architecture",
  "children": ["wi_b4e9d3e2", "wi_c5f0e4f3", "wi_d6a1f5a4"],
  "effort": "large"
}
```

Children inherit `blocked` status until their `depends_on` items are `done`. The system does NOT auto-schedule or auto-execute decomposed items — each sub-item follows the same approval lifecycle.

## Agent Interaction (Built-in Tools)

### `propose_work`

Called by the agent when it identifies deferred work. Captures context automatically from the current session.

```json
{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "Short title (2-8 words)"},
    "kind": {"type": "string", "enum": ["task","plugin","skill","subsystem","architecture","research"]},
    "description": {"type": "string", "description": "Natural language description of what's needed and why"},
    "effort": {"type": "string", "enum": ["trivial","small","medium","large","research"]},
    "priority": {"type": "integer", "description": "0-100, higher = more important"},
    "tags": {"type": "array", "items": {"type": "string"}},
    "parent_id": {"type": "string", "description": "Parent work item ID if this is a sub-task"}
  },
  "required": ["title", "kind", "description"]
}
```

The agent also provides a first-pass decomposition as separate `propose_work` calls with `parent_id` set, if the item is `medium` effort or above.

### `list_work`

Browse the backlog with optional filters.

```json
{
  "type": "object",
  "properties": {
    "status": {"type": "string", "description": "Filter by status"},
    "kind": {"type": "string", "description": "Filter by kind"},
    "tag": {"type": "string", "description": "Filter by tag"},
    "project": {"type": "string", "description": "Filter by project. Default: current project. Use '*' for all projects."},
    "limit": {"type": "integer", "description": "Max results (default 20)"}
  }
}
```

### Future tools (not in MVP)

- `refine_work(id, context)` — Add a snapshot with new context from the current session
- `claim_work(id)` — Mark as in_progress, assigned to agent
- `complete_work(id, outcome, artifacts)` — Mark done with results
- `approve_work(id)` — Agent-driven approval (only when auto-approve flag is set)

## CLI Interaction

```bash
# List work items for the current project (auto-detected from cwd)
codecuttlectl backlog list

# List all work items across all projects
codecuttlectl backlog list --all

# Filter by status/kind/project
codecuttlectl backlog list --status=proposed --kind=plugin
codecuttlectl backlog list --project=stocks-app
codecuttlectl backlog list --tag=needs-research --all

# Show full detail on a work item (with all snapshots)
codecuttlectl backlog show wi_a3f8c2d1

# Approve a proposed item (human gate)
codecuttlectl backlog approve wi_a3f8c2d1

# Reject with reason
codecuttlectl backlog reject wi_a3f8c2d1 --reason "Superseded by wi_b4e9d3e2"

# Edit (opens $EDITOR with the JSON)
codecuttlectl backlog edit wi_a3f8c2d1

# Generate from an approved plugin intent
codecuttlectl backlog generate wi_a3f8c2d1

# Prune old completed/rejected items
codecuttlectl backlog prune --older-than 90d
```

## Integration with Existing Subsystems

| Subsystem | Integration |
|-----------|-------------|
| **Inkwell** | `Diagnose()` gains a `CapabilityGap` field. When detected, the agent can auto-propose a work item. |
| **Skills** | A `on_turn:first` skill surfaces high-priority approved backlog items in the system prompt at session start. |
| **Sessions** | Session metadata gains `related_work_items []string` for traceability. |
| **Scaffold generator** | Consumes `kind=plugin` items with `status=approved` — pulls title, description, and accumulated snapshots as generation context. |
| **Future Optic Lobe** | Semantic search surfaces relevant backlog items when session context overlaps. |
| **Future swarm** | Swarm workers poll for `status=approved, assigned_to!=""` items. Background tasks are executed by Headless Agents (Phase 2), and results are injected safely back into the Orchestrator's context stream. |

## Capability Gap Detection (Inkwell to Backlog)

The Inkwell reconciler already detects looping failures. Extend `Diagnosis` to identify capability gaps:

```go
type Diagnosis struct {
    // ... existing fields ...
    CapabilityGap *CapabilityGap // Non-nil if a gap was detected
}

type CapabilityGap struct {
    Description   string   // "Repeated bash_exec calls for JSON transformation"
    Evidence      []string // Tool names, patterns, error classes
    SuggestedKind string   // "plugin", "skill", etc.
    Confidence    float64  // 0-1: how confident the diagnosis is
}
```

The agent sees this in the reconciler advice and can call `propose_work` if confidence is above a threshold. The human gate (default) ensures no runaway self-modification.

## Auto-Approve Flag

```bash
# Default: requires human approval for generation
codecuttlectl --backlog-auto-approve=false

# For automated pipelines: agent can approve its own proposals
codecuttlectl --backlog-auto-approve=true
```

When `auto-approve=true`:
- `propose_work` immediately sets status to `approved` (skips human gate)
- The scaffold generator can execute within the same session
- Appropriate for CI/automated pipelines where oversight is elsewhere

When `auto-approve=false` (default):
- `propose_work` sets status to `proposed`
- Human must run `codecuttlectl backlog approve <id>` before generation/execution
- The agent acknowledges the deferral and continues the session

## Cloud Sync (Future)

The `BacklogStore` interface abstracts storage. A future `CloudStore` implementation enables:

- **S3/GCS backend**: Work items synced to a shared bucket with per-user prefixes
- **Permissions**: IAM-based access control. Some items are user-private, others are team-visible.
- **Conflict resolution**: Snapshots are append-only; status transitions use optimistic locking
- **Notifications**: Webhook on status transitions (proposed to approved triggers a Slack message, etc.)
- **Cross-agent visibility**: Multiple `codecuttlectl` instances (different machines, different users) share the same backlog
- **Audit trail**: All mutations logged with actor, timestamp, and session context

The file format is intentionally simple JSON — easy to sync, diff, merge, and inspect manually.

## Design Principles

1. **Lightweight in-session**: Proposing work is a single tool call. No workflow interruption.
2. **Context-preserving**: Every touch accumulates context. Nothing is lost between sessions.
3. **Human-gated by default**: Self-modification requires explicit approval unless overridden.
4. **Store-agnostic**: File-based MVP, cloud-sync later, same interface.
5. **Decomposition is speculative**: The agent's first-pass breakdown is a suggestion, not a commitment.
6. **No auto-execution without approval**: The backlog is a *queue of intent*, not a *scheduler*. Execution is always triggered (by human, agent, or external system).
7. **Global store, project-scoped views**: All items live in one store with globally unique IDs. Default views filter to the current project; cross-project queries are explicit.

## Project Scoping

Work items live in a single global store but are scoped to projects for default filtering. This enables both project-focused workflows ("what's left for stocks-app?") and cross-project visibility ("show me all research items across everything").

### The `Project` Field

Every WorkItem has an optional `project` string. When the agent proposes work, it is auto-populated from the current workspace. When listing items, the default view filters to the current project.

### Project Detection (layered, first match wins)

1. Explicit `--project` flag or tool input parameter
2. Git remote origin → extract repo name (e.g., `github.com/codecuttle/codecuttlectl` → `codecuttlectl`)
3. Go module path from `go.mod` → last path segment
4. `package.json` `name` field
5. Basename of the working directory

### Filtering Behavior

| Context | What you see |
|---------|-------------|
| `list_work()` with no project filter | Items matching the detected current project |
| `list_work(project="*")` | All items across all projects |
| `list_work(project="stocks-app")` | Only stocks-app items (even if you're in another workspace) |
| `codecuttlectl backlog list` | Current project (detected from cwd) |
| `codecuttlectl backlog list --all` | All projects |
| `codecuttlectl backlog list --project=stocks-app` | Specific project |

### Cross-Project Dependencies

Since IDs are globally unique (`wi_<8hex>`), `depends_on` references work across projects naturally:

```json
{
  "id": "wi_a3f8c2d1",
  "project": "stocks-app",
  "title": "Add WebSocket streaming",
  "depends_on": ["wi_b4e9d3e2"]
}
```

Where `wi_b4e9d3e2` might be a `codecuttlectl` item ("Implement event bus plugin"). The backlog doesn't enforce project boundaries on dependencies.

### Tags vs. Project

- **Project**: structural grouping, auto-detected, one per item, used for default filtering
- **Tags**: cross-cutting labels, manually applied, many per item, used for ad-hoc queries

Examples:
- `project: "stocks-app", tags: ["performance", "needs-research"]`
- `project: "codecuttlectl", tags: ["needs-research", "optic-lobe"]`

Searching by tag surfaces items from any project: `list_work(tag="needs-research", project="*")`

## Implementation Plan

| Phase | What | Dependencies | Status |
|-------|------|-------------|--------|
| 1 | `internal/backlog/` package: WorkItem types, FileStore, ID generation, project detection | None | ✅ Done |
| 2 | `propose_work` + `list_work` built-in tools | Phase 1 | ⬜ |
| 3 | CLI subcommand: `codecuttlectl backlog list/show/approve/reject` | Phase 1 | ⬜ |
| 4 | Integration: Inkwell CapabilityGap detection | Phase 1, Inkwell | ⬜ |
| 5 | Integration: `on_turn:first` skill surfacing approved items | Phase 1, Skills | ⬜ |
| 6 | `refine_work` + `complete_work` tools | Phase 2 | ⬜ |
| 7 | `--backlog-auto-approve` flag + scaffold integration | Phase 2, Scaffold | ⬜ |
| 8 | CloudStore implementation (S3/GCS) | Phase 1 interface | ⬜ |
