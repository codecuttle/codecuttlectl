> Status: **Superseded.** This design has been replaced by [Swarm Morphologies](swarm-morphologies.md).

# Multi-Model Support

## Overview

Enable codecuttlectl to use multiple Bedrock models within a single session, routing different tasks to the most cost-effective model. The primary agent loop continues to use Opus 4.6 (or user-specified model), while auxiliary tasks — summarization, title generation, classification, and future routing — use cheaper models like Haiku 4.5 or Sonnet 4.6.

## Motivation

Current state: every API call uses `us.anthropic.claude-opus-4-6-v1` at **$5/MTok input, $25/MTok output**.

Many internal operations don't need frontier intelligence:

| Task | Current cost (Opus) | With Haiku 4.5 | Savings |
|------|--------------------:|---------------:|--------:|
| Context compaction summary (3k in, 200 out) | $0.020 | $0.004 | 80% |
| Session title generation (500 in, 20 out) | $0.003 | $0.001 | 67% |
| Cache keepalive ping (20k in, 1 out) | $0.010 | $0.002 | 80% |
| Future: complexity classification | $0.005 | $0.001 | 80% |
| Future: tool result relevance scoring | $0.010 | $0.002 | 80% |

For a typical session with 5 compaction summaries + title + keepalive pings, multi-model saves **~$0.10-0.20/session** on auxiliary calls alone. More significantly, it unlocks cheap summarization for the LLM compaction feature (Phase 2).

## Available Models (Bedrock, June 2026)

| Model | ID (Geo inference) | Context | Input | Output | Cache Read | Cache Write | Sweet Spot |
|-------|-------------------|---------|-------|--------|------------|-------------|------------|
| **Opus 4.8** | `us.anthropic.claude-opus-4-8` | 1M | $5/MTok | $25/MTok | $0.50/MTok | $6.25/MTok | Complex reasoning, autonomous agent loop |
| **Opus 4.6** | `us.anthropic.claude-opus-4-6-v1` | 1M | $5/MTok | $25/MTok | $0.50/MTok | $6.25/MTok | Current default — proven stable |
| **Sonnet 4.6** | `us.anthropic.claude-sonnet-4-6` | 1M | $3/MTok | $15/MTok | $0.30/MTok | $3.75/MTok | Complex auxiliary tasks, planning |
| **Haiku 4.5** | `us.anthropic.claude-haiku-4-5-20251001-v1:0` | 200K | $1/MTok | $5/MTok | $0.10/MTok | $1.25/MTok | Summaries, classification, structured extraction |

## Architecture

### Model Pool

A `ModelPool` manages multiple pre-configured Bedrock clients, one per model:

```go
// ModelRole identifies the purpose a model serves in the system.
type ModelRole string

const (
    // RolePrimary is the main agent conversation model (Opus).
    RolePrimary ModelRole = "primary"

    // RoleAuxiliary is for cheap background tasks: summaries, titles, classification.
    RoleAuxiliary ModelRole = "auxiliary"

    // RolePlanning is for complex-but-not-frontier tasks (Sonnet tier).
    // Used when Haiku isn't sufficient but Opus is overkill.
    RolePlanning ModelRole = "planning"
)

// ModelPool holds pre-initialized clients for each configured model.
type ModelPool struct {
    mu      sync.RWMutex
    clients map[ModelRole]*Client
    configs map[ModelRole]ModelInfo
}

// ModelInfo describes one model in the pool.
type ModelInfo struct {
    Role          ModelRole
    ModelID       string
    ContextWindow int32  // Max input tokens
    MaxOutput     int32  // Max output tokens
    InputCost     float64 // $/MTok
    OutputCost    float64 // $/MTok
    CacheReadCost float64 // $/MTok
    CacheWriteCost float64 // $/MTok
    SupportsCache bool   // Whether this model supports prompt caching
    SupportTools  bool   // Whether this model supports tool use
}
```

### Pool Construction

```go
// PoolConfig specifies which models to initialize.
type PoolConfig struct {
    Region  string
    Profile string

    // Primary model — used for main agent conversation.
    Primary string // Model ID, e.g. "us.anthropic.claude-opus-4-6-v1"

    // Auxiliary model — used for summaries, titles, classification.
    Auxiliary string // Model ID, e.g. "us.anthropic.claude-haiku-4-5-20251001-v1:0"

    // Planning model — mid-tier for complex auxiliary tasks.
    Planning string // Model ID, e.g. "us.anthropic.claude-sonnet-4-6"
}

// NewPool creates a ModelPool with clients for all three roles.
// All clients share the same AWS credentials and region.
func NewPool(ctx context.Context, cfg PoolConfig) (*ModelPool, error) {
    // Single AWS config load, shared across all clients
    awsCfg, err := loadAWSConfig(ctx, cfg.Region, cfg.Profile)
    if err != nil {
        return nil, err
    }

    pool := &ModelPool{
        clients: make(map[ModelRole]*Client),
        configs: make(map[ModelRole]ModelInfo),
    }

    // Initialize all three roles
    pool.clients[RolePrimary] = newClientFromAWSConfig(awsCfg, cfg.Primary)
    pool.configs[RolePrimary] = lookupModelInfo(cfg.Primary)

    pool.clients[RoleAuxiliary] = newClientFromAWSConfig(awsCfg, cfg.Auxiliary)
    pool.configs[RoleAuxiliary] = lookupModelInfo(cfg.Auxiliary)

    pool.clients[RolePlanning] = newClientFromAWSConfig(awsCfg, cfg.Planning)
    pool.configs[RolePlanning] = lookupModelInfo(cfg.Planning)

    return pool, nil
}

// Get returns the client for the given role. Falls back to Primary
// if the requested role has no dedicated model configured.
func (p *ModelPool) Get(role ModelRole) *Client {
    p.mu.RLock()
    defer p.mu.RUnlock()
    if c, ok := p.clients[role]; ok {
        return c
    }
    return p.clients[RolePrimary]
}

// Config returns the model config for the given role.
func (p *ModelPool) Config(role ModelRole) ModelInfo {
    p.mu.RLock()
    defer p.mu.RUnlock()
    if c, ok := p.configs[role]; ok {
        return c
    }
    return p.configs[RolePrimary]
}
```

### Model Registry

A built-in registry maps model IDs to their known capabilities and pricing:

```go
// registry.go

var knownModels = map[string]ModelInfo{
    "us.anthropic.claude-opus-4-8": {
        ModelID:        "us.anthropic.claude-opus-4-8",
        ContextWindow:  1_000_000,
        MaxOutput:      128_000,
        InputCost:      5.00,
        OutputCost:     25.00,
        CacheReadCost:  0.50,
        CacheWriteCost: 6.25,
        SupportsCache:  true,
        SupportTools:   true,
    },
    "us.anthropic.claude-opus-4-6-v1": {
        ModelID:        "us.anthropic.claude-opus-4-6-v1",
        ContextWindow:  1_000_000,
        MaxOutput:      128_000,
        InputCost:      5.00,
        OutputCost:     25.00,
        CacheReadCost:  0.50,
        CacheWriteCost: 6.25,
        SupportsCache:  true,
        SupportTools:   true,
    },
    "us.anthropic.claude-sonnet-4-6": {
        ModelID:        "us.anthropic.claude-sonnet-4-6",
        ContextWindow:  1_000_000,
        MaxOutput:      64_000,
        InputCost:      3.00,
        OutputCost:     15.00,
        CacheReadCost:  0.30,
        CacheWriteCost: 3.75,
        SupportsCache:  true,
        SupportTools:   true,
    },
    "us.anthropic.claude-haiku-4-5-20251001-v1:0": {
        ModelID:        "us.anthropic.claude-haiku-4-5-20251001-v1:0",
        ContextWindow:  200_000,
        MaxOutput:      64_000,
        InputCost:      1.00,
        OutputCost:     5.00,
        CacheReadCost:  0.10,
        CacheWriteCost: 1.25,
        SupportsCache:  true,
        SupportTools:   true,
    },
}

// LookupModel returns the known config for a model ID, or a zero-value
// config with just the ModelID set for unknown models.
func LookupModel(modelID string) ModelInfo {
    if cfg, ok := knownModels[modelID]; ok {
        return cfg
    }
    // Unknown model — return minimal config
    return ModelInfo{
        ModelID:       modelID,
        ContextWindow: 200_000, // Conservative default
        MaxOutput:     8_192,
        InputCost:     5.00,    // Assume expensive (safe default)
        OutputCost:    25.00,
        SupportsCache: false,
        SupportTools:  true,
    }
}
```

### Integration Points

#### 1. Context Compaction (LLM Summarization)

The primary consumer. The `Summarizer` interface from the [LLM compaction design](docs/llm-compaction-design.md) gains a cheap implementation:

```go
// HaikuSummarizer uses the auxiliary model for cheap summarization.
type HaikuSummarizer struct {
    pool *ModelPool
}

func (s *HaikuSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error) {
    client := s.pool.Get(RoleAuxiliary) // Haiku 4.5

    // No tools, no caching — just a simple one-shot summarization
    resp, err := client.Converse(ctx, summarizeSystemPrompt, []types.Message{
        BuildUserTextMessage(formatSummarizeInput(req)),
    }, nil)
    if err != nil {
        return nil, err
    }

    return &SummarizeResult{
        Summary:      resp.Content,
        InputTokens:  resp.InputTokens,
        OutputTokens: resp.OutputTokens,
        Cost:         estimateCost(resp, s.pool.Config(RoleAuxiliary)),
    }, nil
}
```

Cost per summary with Haiku: **~$0.004** (vs $0.08 with Opus). This makes the per-session budget effectively irrelevant — you could summarize 250 results for $1.00.

#### 2. Session Title Generation

Currently uses Opus to generate a 5-word session title. With multi-model:

```go
func (a *Agent) generateTitle(ctx context.Context) (string, error) {
    client := a.pool.Get(RoleAuxiliary) // Haiku is plenty for titling
    resp, err := client.Converse(ctx, "", titleMessages, nil)
    // ...
}
```

#### 3. Cache Keepalive

The keepalive ping must hit the **primary model's cache** — it can't use a different model. This stays on the primary client. No change needed.

#### 4. Future: Complexity Classification (Chomsky Routing)

A fast Haiku call classifies the user's message complexity, routing simple tasks to Sonnet and complex ones to Opus:

```go
// Not implemented yet — this is future work that multi-model enables.
type ComplexityClassifier struct {
    pool *ModelPool
}

func (c *ComplexityClassifier) Classify(ctx context.Context, userMsg string) (ModelRole, error) {
    client := c.pool.Get(RoleAuxiliary) // Haiku for classification
    resp, err := client.Converse(ctx, classifyPrompt, []types.Message{
        BuildUserTextMessage(userMsg),
    }, nil)
    // Parse response: "simple" → RolePlanning (Sonnet), "complex" → RolePrimary (Opus)
    // ...
}
```

#### 5. Future: Tool Result Relevance Scoring

Before compacting, ask Haiku whether a tool result is likely to be needed again:

```go
func (s *RelevanceScorer) Score(ctx context.Context, toolResult string, recentContext string) (float64, error) {
    client := s.pool.Get(RoleAuxiliary)
    // Returns 0.0-1.0 relevance score
    // High-relevance results are preserved; low-relevance ones are compacted aggressively
}
```

## CLI Interface

The pool is **always active** with all three roles populated by default. The `--model` flag continues to control the primary model; auxiliary and planning models are automatically selected based on the primary model's family:

```bash
# Default: Opus 4.6 primary, Haiku 4.5 auxiliary, Sonnet 4.6 planning
codecuttlectl

# Override primary — auxiliary/planning auto-selected from same family
codecuttlectl --model us.anthropic.claude-opus-4-8

# Override everything (rare, for testing/experimentation)
codecuttlectl --model opus-4-8 --aux-model haiku-4-5 --plan-model sonnet-4-6
```

### Automatic Role Assignment

Given a primary model, the pool auto-populates auxiliary and planning:

```go
// DefaultRoles returns the standard role assignment for a given primary model.
// The system always initializes all three roles — no opt-in required.
func DefaultRoles(primaryID string) PoolConfig {
    switch {
    case strings.Contains(primaryID, "opus"):
        return PoolConfig{
            Primary:   primaryID,
            Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
            Planning:  "us.anthropic.claude-sonnet-4-6",
        }
    case strings.Contains(primaryID, "sonnet"):
        return PoolConfig{
            Primary:   primaryID,
            Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
            Planning:  primaryID, // Sonnet IS the planning tier
        }
    case strings.Contains(primaryID, "haiku"):
        return PoolConfig{
            Primary:   primaryID,
            Auxiliary: primaryID, // Haiku IS the auxiliary tier
            Planning:  primaryID, // Single-model mode
        }
    default:
        return PoolConfig{
            Primary:   primaryID,
            Auxiliary: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
            Planning:  "us.anthropic.claude-sonnet-4-6",
        }
    }
}
```

This means `codecuttlectl` with zero flags already runs cost-optimized — Haiku handles summaries, titles, and classification; Sonnet handles planning tasks; Opus handles the agent conversation.

### Model Aliases

For convenience, short aliases map to full model IDs:

```go
var modelAliases = map[string]string{
    "opus":       "us.anthropic.claude-opus-4-6-v1",
    "opus-4-6":   "us.anthropic.claude-opus-4-6-v1",
    "opus-4-8":   "us.anthropic.claude-opus-4-8",
    "sonnet":     "us.anthropic.claude-sonnet-4-6",
    "sonnet-4-6": "us.anthropic.claude-sonnet-4-6",
    "haiku":      "us.anthropic.claude-haiku-4-5-20251001-v1:0",
    "haiku-4-5":  "us.anthropic.claude-haiku-4-5-20251001-v1:0",
}
```

## Cost Tracking Changes

The TUI status bar and session stats need to track costs per-model:

```go
// TokenStats tracks usage per model role.
type TokenStats struct {
    Role             ModelRole
    ModelID          string
    InputTokens      int64
    OutputTokens     int64
    CacheReadTokens  int64
    CacheWriteTokens int64
}

// SessionCost computes total cost across all models in the session.
func (p *ModelPool) SessionCost(stats []TokenStats) float64 {
    var total float64
    for _, s := range stats {
        cfg := p.Config(s.Role)
        total += float64(s.InputTokens) * cfg.InputCost / 1_000_000
        total += float64(s.OutputTokens) * cfg.OutputCost / 1_000_000
        total += float64(s.CacheReadTokens) * cfg.CacheReadCost / 1_000_000
        total += float64(s.CacheWriteTokens) * cfg.CacheWriteCost / 1_000_000
    }
    return total
}
```

Status bar format with multi-model:
```
opus-4-6 | us-west-2 | 12p | 45.2k in  8.1k out  87% cache  ~$0.42 | aux: haiku-4-5
```

## Haiku 4.5 Context Window Constraint

Haiku 4.5 has a **200K** context window (vs 1M for Opus/Sonnet). This matters for:

- **Summarization**: Tool results sent to Haiku for summarization must fit within 200K. Given our `SummaryMinSize` of 5000 chars (~1500 tokens), this is never a concern — we're sending individual results, not the full conversation.
- **Not suitable for**: Keepalive pings (need the full message history to match the primary cache) or agent conversation (history can exceed 200K).

The architecture handles this naturally: auxiliary calls are always self-contained (one-shot with a short system prompt + content to process). They never receive the full conversation history.

## Cache Considerations

Each model maintains its **own** cache namespace on Bedrock. Implications:

- **Primary model cache**: Continues to work exactly as today. The keepalive ping refreshes this cache.
- **Auxiliary model cache**: Not worth caching. Haiku calls are one-shot (new system prompt + new content each time). No prefix reuse between calls.
- **No cross-model cache sharing**: A cache built on Opus is not readable by Haiku (different model = different cache key).

This means multi-model doesn't affect our existing caching strategy at all. The primary model's 3-tier cache continues independently.

## Error Handling & Fallback

If an auxiliary or planning model is unavailable (throttled, region outage, etc.), fall back to primary:

```go
// GetWithFallback tries the requested role, then falls back to primary.
func (p *ModelPool) GetWithFallback(role ModelRole) *Client {
    p.mu.RLock()
    defer p.mu.RUnlock()
    if c, ok := p.clients[role]; ok {
        return c
    }
    return p.clients[RolePrimary]
}

// Example: Summarizer with automatic fallback
func (s *HaikuSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (*SummarizeResult, error) {
    client := s.pool.Get(RoleAuxiliary)
    resp, err := client.Converse(ctx, ...)
    if err != nil {
        // Auxiliary failed — fall back to primary (more expensive but available)
        s.logger.Warn("auxiliary model unavailable, falling back to primary", "err", err)
        client = s.pool.Get(RolePrimary)
        resp, err = client.Converse(ctx, ...)
        if err != nil {
            return nil, err // Both failed — propagate error
        }
    }
    return ...
}
```

Fallback is always silent from the user's perspective. The audit log records which model actually served each request.

## Audit & Observability

Every API call emits an audit event tagged with the model role and ID:

```json
{
  "type": "llm_call",
  "model_id": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
  "model_role": "auxiliary",
  "purpose": "compaction_summary",
  "input_tokens": 3200,
  "output_tokens": 120,
  "cost_usd": 0.004,
  "duration_ms": 340
}
```

Session persistence includes per-model cost breakdowns:

```json
{
  "stats": {
    "models": {
      "primary": {
        "model_id": "us.anthropic.claude-opus-4-6-v1",
        "input_tokens": 150000,
        "output_tokens": 8000,
        "cost_usd": 0.95
      },
      "auxiliary": {
        "model_id": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
        "input_tokens": 25000,
        "output_tokens": 1500,
        "cost_usd": 0.033
      }
    },
    "total_cost_usd": 0.983
  }
}
```

## Implementation Plan

| Phase | What | Effort | Depends on |
|-------|------|--------|------------|
| **1** | `ModelPool` + `ModelInfo` + registry | Small | Nothing |
| **2** | `--aux-model` / `--plan-model` CLI flags | Small | Phase 1 |
| **3** | Wire pool into TUI + Agent (replace single `*Client`) | Medium | Phase 1 |
| **4** | Move title generation to auxiliary | Tiny | Phase 3 |
| **5** | `HaikuSummarizer` for LLM compaction | Small | Phase 3 + LLM compaction design |
| **6** | Per-model cost tracking in status bar + session | Medium | Phase 3 |
| **7** | Model aliases + `--cost-optimized` shorthand | Tiny | Phase 2 |
| **8** | Audit events per-model | Small | Phase 3 |

**Total effort**: Medium-Large (~2-3 focused sessions)

**Phase 1-3 are the critical path** — once the pool is wired in, everything else is incremental.

## Migration Path

The change is **transparent to the user**:

1. `codecuttlectl` with default flags now initializes all three model roles automatically
2. The primary model is still controlled by `--model` (default: Opus 4.6)
3. Auxiliary (Haiku 4.5) and planning (Sonnet 4.6) are auto-selected based on the primary
4. `--aux-model` and `--plan-model` exist as overrides for testing/experimentation, not normal use
5. Cost tracking in the status bar now reflects the blended cost across all models
6. All internal operations (title gen, summaries, keepalive) automatically use the cheapest appropriate model

Users see lower costs immediately with no configuration changes.

## Design Decisions

1. **Always-on by default** — Multi-model is not opt-in. Every session initializes all three roles automatically. The cost savings are universally beneficial and there's no downside (fallback to primary handles failures gracefully).

2. **Role-based, not task-based routing** — Models are assigned roles (primary/auxiliary/planning), and tasks are mapped to roles. This is simpler than a per-task model config and makes the mental model clear: "Haiku does background work, Sonnet does planning, Opus does the real thinking."

3. **Automatic role assignment from primary** — Given a primary model, the system auto-selects appropriate auxiliary and planning models from the same family. No manual configuration needed.

4. **Shared AWS credentials** — All models use the same AWS config (region, profile, credentials). No need for separate auth per model.

5. **No runtime model switching for the primary loop** — The agent conversation always uses the primary model. Chomsky routing (dynamic switching based on complexity) is future work that builds on this foundation but doesn't change the architecture.

6. **Haiku for summarization, not Sonnet** — At $1/MTok vs $3/MTok, Haiku is 3x cheaper than Sonnet for the same task. Quality is sufficient for structural summaries. Sonnet is reserved for tasks that genuinely need more reasoning (planning tier).

7. **Registry is static** — Model capabilities and pricing are compiled in. We're not querying Bedrock for model metadata at runtime. This keeps startup fast and avoids API dependencies. The registry is updated when we update the codebase.

8. **Graceful degradation** — Auxiliary/planning model failures never break the session. Everything falls back to primary silently.

## References

- [LLM Compaction Design](docs/llm-compaction-design.md) — Primary consumer of auxiliary model
- [Caching Design](docs/caching.md) — Cache isolation between models
- [Bedrock Pricing](https://aws.amazon.com/bedrock/pricing/) — Cost data
- [Anthropic Pricing](https://platform.claude.com/docs/en/about-claude/pricing) — Canonical pricing table
- [Bedrock Model IDs](https://docs.aws.amazon.com/bedrock/latest/userguide/model-ids.html) — Model catalog
