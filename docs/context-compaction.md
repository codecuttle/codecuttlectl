# Context Compaction

## Overview

Context compaction replaces old, verbose tool results in the conversation history with concise summaries. This keeps the context window lean without losing the model's awareness of what was there — it can always re-read files or re-run commands if it needs the full content again.

**Key invariant**: The Inkwell and session file always retain full-fidelity content. Compaction only affects what's sent to the model in subsequent API calls.

## Problem

A typical coding session accumulates large tool results:
- `read_file` on a 1600-line file: ~8k tokens
- `grep` across a project: ~2-4k tokens
- `bash_exec` (build output): ~1-3k tokens
- `websearch` results: ~3-5k tokens

After 10+ turns with file reads, 100-200k tokens of stale tool results sit in the context window. The model is paying (in latency and cost) to re-process content it looked at 15 minutes ago and doesn't need byte-for-byte.

## Current Implementation: Heuristic Compaction

### When It Triggers

Compaction runs automatically before each `ConverseStream` call when:
- The most recent API call's total input tokens exceed **50%** of the context window (500k of 1M for Opus 4.6)

### What It Does

For tool results older than the last 2 user turns:
- Results smaller than 1000 characters are left alone (not worth compacting)
- Larger results are replaced with a head/tail summary:

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

The model sees: "I read this file, here's what the beginning and end look like, and I can re-read specific sections with offset/limit if needed."

### What It Preserves

- **Last 2 user turns**: Everything from your most recent and previous messages stays intact
- **Small results**: Anything under 1000 chars passes through unchanged
- **Inkwell**: Full content, always — governance and audit are never compromised
- **Session file**: Full content persisted for resume — compaction is transient

### Cache Impact

Compaction mutates the message history, which invalidates the Tier 3 (messages) cache on the next call. This is a one-time cost:
- One cache write penalty (~$0.01-0.05 depending on history size)
- All subsequent calls benefit from the reduced context (lower latency, lower cost)
- Tier 1 (tools) and Tier 2 (system prompt) caches are unaffected

The math: if compaction frees 100k tokens, that saves ~$0.50/MTok × 100k = $0.05 per call in cache-read costs. After 1 call, the compaction has paid for itself.

## Configuration

```go
compact.Config{
    MaxContextPercent:   0.50,  // Trigger when > 50% of context window used
    PreserveRecentTurns: 2,    // Never compact last 2 user turns
    SummaryMaxLines:     8,    // Head + tail lines in summary
    MinResultSize:       1000, // Don't compact results under 1000 chars
}
```

These defaults are currently hardcoded. Future: expose via CLI flags or config file.

## Architecture

```
                    ┌─────────────────────────────┐
                    │     Context Window (1M)      │
                    │                              │
                    │  Tools [cached, 12k]         │
                    │  System [cached, 6k]         │
                    │  Messages:                   │
                    │    Turn 1: [COMPACTED]       │  ← Summary only
                    │    Turn 2: [COMPACTED]       │  ← Summary only
                    │    ...                       │
                    │    Turn N-1: [FULL]          │  ← Preserved
                    │    Turn N: [FULL]            │  ← Preserved (current)
                    └─────────────────────────────┘
                                 │
                    ┌────────────┴────────────┐
                    │                         │
            ┌───────▼───────┐        ┌───────▼───────┐
            │  Session File │        │    Inkwell    │
            │  (FULL always)│        │ (FULL always) │
            └───────────────┘        └───────────────┘
```

## Future Enhancements

### Phase 2: LLM-Generated Summaries

Replace the heuristic head/tail truncation with model-generated summaries. A lightweight API call (Haiku or a small local model) produces richer summaries:

```
[read_file: internal/tui/app.go — 1614 lines, Bubble Tea TUI]
Key structures: Model{} with viewport, input, spinner, streaming state.
Token tracking: totalInputTokens, lastCallInputTokens (for ctx%).
Status bar renders at line 1226 with cache%, ctx%, cost.
launchStream() at line 745 calls maybeCompact() then ConverseStream.
Tool execution in executePendingTools() at line 895.
```

This costs ~$0.001 per summarization (Haiku) but produces much better context for the model to decide whether it needs to re-read.

### Phase 3: Optic Lobe Integration (pgvector)

When compacting, store the full content in the Optic Lobe with:
- **pgvector embedding** of the content (for semantic retrieval)
- **Metadata**: file path, tool name, turn number, timestamp
- **Raw text** in PostgreSQL for exact retrieval

The compacted summary in the context window gains a retrieval reference:

```
[read_file: internal/tui/app.go — compacted, optic_ref=ol_a3f8c2d1]
TUI model with Bubble Tea. Key: renderStatusBar (line 1226), 
launchStream (line 745), executePendingTools (line 895).
```

A new built-in `recall` tool lets the model pull content back:

```json
{"type": "object", "properties": {
  "ref": {"type": "string", "description": "Optic Lobe reference ID"},
  "query": {"type": "string", "description": "What to retrieve (semantic search)"}
}}
```

### Phase 4: Cross-Session Retrieval

Once the Optic Lobe is populated across sessions, the model can retrieve relevant context from *previous sessions*:

- "Last time we worked on the status bar, we changed X at line Y"
- "The caching doc says Z about cache invalidation"
- Error patterns: "This build error was fixed in session ses_xyz by doing W"

This is where compaction, the Inkwell, and the Optic Lobe fully converge:

| Layer | What it stores | How it's accessed |
|-------|---------------|-------------------|
| **Context window** | Summaries + recent full content | Directly by the model |
| **Inkwell (session file)** | Full I/O for all tool calls this session | Resume, audit, governance |
| **Optic Lobe (pgvector)** | Embedded content from all sessions | `recall` tool, semantic search |
| **Apache AGE (graph)** | Causal relationships (error→fix, file→change) | Graph traversal queries |

### Phase 5: Adaptive Compaction

Instead of a fixed threshold, the system learns which content is worth keeping:
- Content that was re-read → increase its preservation priority
- Content from files that were later edited → higher relevance
- Content that was never referenced again → compact aggressively
- Error outputs that led to fixes → keep the error, compact the rest

This feeds back from Inkwell patterns: if the model re-reads a file within 3 turns of its last read, that file's content has high "temporal locality" and should be preserved longer.

## Design Principles

1. **Never lose data** — Compaction is a context window optimization, not a deletion. Full content lives in Inkwell and session files forever.
2. **Transparent to the model** — The omission marker tells the model exactly what was removed and how to get it back (`read_file with offset/limit`).
3. **Conservative by default** — 50% threshold, preserve 2 recent turns, 1000-char minimum. Better to over-preserve than to lose something the model needs.
4. **One cache miss is acceptable** — Compaction invalidates the message cache once, then all subsequent calls benefit from the smaller context.
5. **Composable with future systems** — Summaries become metadata for vector retrieval. The architecture layers cleanly without rewrites.
