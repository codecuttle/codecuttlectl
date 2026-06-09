# LLM-Generated Compaction Summaries

## Overview

Replace the heuristic head/tail truncation in context compaction with LLM-generated summaries. Instead of showing "4 lines from the top + 4 from the bottom + omission marker", we produce semantic summaries that capture what the content *means* — enabling the model to make better decisions about whether to re-read without the full text.

## Status

**Phase**: Design (this document)  
**Depends on**: Context compaction (Phase 1 — ✅ implemented)  
**Blocked by**: Nothing — can be implemented with the current single-model architecture

## Problem with Heuristic Compaction

The current head/tail summary works but loses semantic content:

```
line 1: package tui
line 2: 
line 3: import (
line 4:     "context"

[... 1592 lines omitted — use read_file with offset/limit to retrieve specific sections ...]

line 1597: func sanitizeModelText(text string) string {
line 1598:     cleaned := xmlTagPattern.ReplaceAllString(text, "")
line 1599:     for strings.Contains(cleaned, "\n\n\n") {
line 1600:         cleaned = strings.ReplaceAll(cleaned, "\n\n\n", "\n\n")
```

This tells the model *where* the file starts and ends, but nothing about what's *in* it. A semantic summary would capture:

```
[read_file: internal/tui/app.go — 1614 lines, Bubble Tea TUI application]
Key structures: Model{} struct (line 44) with viewport, input, spinner, streaming state.
Token tracking: totalInputTokens, lastCallInputTokens (lines 99-107).
Status bar: renderStatusBar() at line 1270 with cache%, ctx%, cost.
Stream management: launchStream() at line 795 calls maybeCompact() then ConverseStream.
Tool execution: executePendingTools() at line 943, approval flow at line 969.
Cache keepalive: 4-min tick refreshes Bedrock TTL (lines 668-700).
```

## Design Constraints

### Single-Model Reality (Current State)

We currently run only `us.anthropic.claude-opus-4-6-v1`. Using Opus for summarization is expensive:
- $5.00/MTok input + $25.00/MTok output
- A 10k-token file read → ~$0.05 to summarize + ~$0.03 for the summary output
- At 5 compactions per session → ~$0.40 extra per session

This is too expensive for inline summarization of every tool result.

### Options Evaluated

| Approach | Cost/summary | Latency | Quality | Feasibility today |
|----------|-------------|---------|---------|-------------------|
| **Opus 4.6 (same model)** | ~$0.08 | 2-5s | Excellent | ✅ Works now |
| **Haiku/Sonnet via Bedrock** | ~$0.001-0.005 | 0.3-1s | Good | ❌ Requires multi-model |
| **Local model (e.g., Phi-3)** | ~$0 | 0.5-2s | Moderate | ❌ Deployment complexity |
| **Batch offline summarization** | ~$0.08 | Async | Excellent | ✅ Works now |
| **Conditional Opus summarization** | ~$0.02 avg | 2-5s | Excellent | ✅ Works now |

### Chosen Approach: Conditional Opus Summarization

Use the *same model* (Opus 4.6) but **only summarize when the payoff is high enough**. The key insight: a single cache-write penalty from context growing too large costs ~$0.12, and we pay it every time compaction busts the cache. A one-time $0.08 summary that prevents 10+ cache misses over a session is profitable.

**When to summarize (not compact heuristically):**
- Tool result is >5000 chars (significant content worth summarizing)
- The result has been in context for >3 turns (not likely to be re-read soon)
- Context window usage is >30% (we have enough history to benefit)

**When to use heuristic compaction (current approach):**
- Tool result is 1000-5000 chars (not worth an API call)
- We're near the context limit and need immediate relief (>80% usage)
- Summarization would delay the next user-facing response

This creates a two-tier compaction system: fast heuristic for small/urgent cases, LLM summary for large/stable content.

## Architecture

### Summarization Request

```go
// SummarizeRequest describes content to be summarized for compaction.
type SummarizeRequest struct {
    ToolName   string // Which tool produced this (read_file, grep, etc.)
    ToolInput  string // What was requested (file path, search query, etc.)
    Content    string // The full tool result to summarize
    MaxTokens  int    // Target summary length in tokens (default: 200)
}

// SummarizeResult holds the generated summary.
type SummarizeResult struct {
    Summary      string        // The LLM-generated summary text
    InputTokens  int32         // Tokens consumed for this summarization
    OutputTokens int32         // Tokens generated
    Cost         float64       // Estimated cost of this summarization call
    Duration     time.Duration // How long the summarization took
}
```

### Summarizer Interface

```go
// Summarizer generates semantic summaries of tool results for context compaction.
// The current implementation uses the same Bedrock client (Opus 4.6).
// Future: a multi-model implementation could use Haiku for cheaper summaries.
type Summarizer interface {
    Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error)
}

// OpusSummarizer uses the primary Bedrock client for summarization.
// This is expensive but high-quality and requires no additional infrastructure.
type OpusSummarizer struct {
    client *bedrock.Client
}

func NewOpusSummarizer(client *bedrock.Client) *OpusSummarizer {
    return &OpusSummarizer{client: client}
}
```

### Summarization Prompt

```go
const summarizeSystemPrompt = `You are a context compaction assistant. Your job is to 
summarize tool output so a coding agent can decide whether it needs to re-read the 
full content. Produce a concise structural summary that captures:

1. What the content IS (file type, package, test output, etc.)
2. Key structures, functions, or data points with line numbers when available
3. Anything that would be relevant to ongoing coding tasks
4. What to search for if the agent needs specific content back

Rules:
- Maximum 5-8 lines
- Include line numbers for key landmarks
- Mention file paths, function names, type names
- For error output: capture the error message and location
- For search results: summarize what was found and where
- Never invent content that wasn't in the original
- Format as a compact reference card, not prose`
```

### Integration with Compact Package

```go
// Config gains a Summarizer field for LLM-based compaction.
type Config struct {
    // ... existing fields ...

    // Summarizer, if set, enables LLM-generated summaries for large tool results.
    // When nil, falls back to heuristic head/tail compaction for everything.
    Summarizer Summarizer

    // SummaryMinSize is the minimum result size (chars) to use LLM summarization
    // instead of heuristic compaction. Results between MinResultSize and SummaryMinSize
    // use heuristic compaction; results above SummaryMinSize use LLM summarization.
    // Default: 5000.
    SummaryMinSize int

    // SummaryMinTurnsStale is how many turns old a result must be before it's
    // eligible for LLM summarization (vs. heuristic compaction). Newer results
    // might still be re-read and aren't worth the summarization cost.
    // Default: 3.
    SummaryMinTurnsStale int
}
```

### Flow

```
Tool result arrives (e.g., read_file output)
    │
    ▼
Result stays in context (full fidelity) for N turns
    │
    ▼
Compaction pass triggers (on every API call):
    │
    ├─ Result < 1000 chars → leave alone
    ├─ Result 1000-5000 chars → heuristic head/tail (existing)
    └─ Result > 5000 chars AND > 3 turns old:
         │
         ├─ Summarizer available AND not in "urgent" mode?
         │    ├── YES → LLM summarize (async, ~2-5s)
         │    └── NO → heuristic head/tail (immediate)
         │
         ▼
    Summary cached in-memory for this result (keyed by content hash).
    If the same content appears again (re-read), the cached summary is reused.
```

### Async Summarization

The key UX challenge: summarization takes 2-5 seconds and shouldn't block the user's next interaction.

**Strategy: Lazy summarization on compaction boundary**

1. When a result becomes eligible for LLM summarization (stale enough, large enough), mark it as "pending summarization"
2. On the *next* API call (when we're about to send the compacted history), fire summarization requests in parallel
3. If summarization completes before the main API call returns, use the summary
4. If it doesn't complete in time (timeout 3s), fall back to heuristic compaction for this turn and try again next turn

```go
// pendingSummary tracks a result awaiting LLM summarization.
type pendingSummary struct {
    msgIdx     int    // Index in message history
    blockIdx   int    // Index in content blocks
    contentKey string // SHA256 of the content (for caching)
    content    string // The full content to summarize
    toolName   string // Tool that produced it
    toolInput  string // Input that was given to the tool
}
```

### Summary Cache

Summaries are cached in-memory (and optionally persisted to the session file) so we never summarize the same content twice:

```go
// SummaryCache stores generated summaries keyed by content hash.
// This prevents re-summarizing content that was already processed.
type SummaryCache struct {
    mu      sync.RWMutex
    entries map[string]string // contentHash → summary text
}
```

In the session file:
```json
{
  "meta": { ... },
  "messages": [ ... ],
  "summaries": {
    "sha256:a3f8c2d1...": "[read_file: internal/tui/app.go — 1614 lines] ...",
    "sha256:b4e9d3e2...": "[grep: 'TODO' across project — 12 matches] ..."
  }
}
```

### Cost Controls

To prevent runaway costs, the summarizer respects budget limits:

```go
// SummaryBudget controls how much we're willing to spend on summarization per session.
type SummaryBudget struct {
    MaxPerSession    float64 // Maximum $ for summarization per session (default: $1.00)
    MaxPerResult     float64 // Maximum $ for a single summarization call (default: $0.15)
    SpentThisSession float64 // Running total (tracked in session stats)
}
```

When the budget is exhausted, all new compaction falls back to heuristic mode. The budget is visible in the status bar when summarization is active.

## CLI Interface

```bash
# Enable LLM compaction (default: off until proven stable)
codecuttlectl --llm-compact

# Set per-session budget
codecuttlectl --llm-compact --compact-budget 2.00

# Disable (use heuristic only, current default)
codecuttlectl --no-llm-compact
```

## Metrics & Observability

The audit log emits structured events for summarization:

```json
{
  "type": "compaction_summary",
  "timestamp": "2026-06-09T14:23:01Z",
  "session_id": "ses_abc123",
  "tool_name": "read_file",
  "content_size": 12500,
  "summary_size": 450,
  "compression_ratio": 27.8,
  "input_tokens": 3200,
  "output_tokens": 120,
  "cost_usd": 0.019,
  "duration_ms": 2340
}
```

Session stats track summarization separately:
```json
{
  "stats": {
    "input_tokens": 150000,
    "output_tokens": 8000,
    "compact_summaries_generated": 7,
    "compact_summary_cost_usd": 0.13,
    "compact_tokens_freed": 42000
  }
}
```

## Future: Multi-Model Summarization

When multi-model support lands, the `Summarizer` interface enables a drop-in replacement:

```go
// HaikuSummarizer uses Claude Haiku for ultra-cheap summarization.
// Cost: ~$0.001 per summary (vs ~$0.08 for Opus)
// Quality: Good (not as rich as Opus, but sufficient for structural summaries)
type HaikuSummarizer struct {
    client *bedrock.Client // Separate client configured for Haiku model
}

// MultiModelSummarizer routes based on content type:
// - Code files → Haiku (structural summary is sufficient)
// - Error output → Opus (nuanced interpretation matters)
// - Search results → Haiku (list summarization)
type MultiModelSummarizer struct {
    haiku *bedrock.Client
    opus  *bedrock.Client
}
```

This is explicitly **out of scope** for this phase. The `Summarizer` interface gives us the seam; multi-model is a separate feature that provides a cheaper implementation behind the same interface.

## Implementation Plan

| Step | What | Effort |
|------|------|--------|
| 1 | `Summarizer` interface + `OpusSummarizer` implementation | Small |
| 2 | `SummaryCache` (in-memory, content-hash keyed) | Small |
| 3 | Two-tier compaction logic in `compact.Compact()` | Medium |
| 4 | Async summarization with timeout + fallback | Medium |
| 5 | `--llm-compact` flag + `SummaryBudget` | Small |
| 6 | Persist summary cache in session file | Small |
| 7 | Audit log events + session stats | Small |
| 8 | TUI: show summarization activity in status area | Small |

Total estimated effort: **Medium** (1-2 focused sessions)

## Design Decisions

1. **Same model for now** — Using Opus 4.6 for summarization is expensive but eliminates all multi-model complexity. The `Summarizer` interface means we can swap in Haiku later without touching compaction logic.

2. **Opt-in via flag** — The heuristic approach works well enough for most sessions. LLM compaction is a cost/quality tradeoff that users should control.

3. **Budget cap** — Prevent cost surprises. If summarization costs exceed $1/session, fall back to heuristic.

4. **Cache aggressively** — The same file content will often be read multiple times across turns. A content-hash cache ensures we never summarize the same text twice.

5. **Async with fallback** — Never block the user. If summarization is slow, use heuristic now and try LLM next turn.

6. **No Optic Lobe dependency** — This phase is pure context-window optimization. The Optic Lobe (Phase 3) will eventually *consume* these summaries as metadata for vector retrieval, but that's a separate system.

## References

- [Context Compaction (Phase 1)](docs/context-compaction.md) — Current heuristic implementation
- [Caching Design](docs/caching.md) — Cache TTL and cost implications
- [Bedrock Pricing](https://aws.amazon.com/bedrock/pricing/) — Model cost comparison
