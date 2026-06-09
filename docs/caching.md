# Prompt Caching & Cost Optimization

## Overview

codecuttlectl uses a 3-tier incremental extension caching strategy for AWS Bedrock to minimize API costs. When working correctly, cache hit rates should exceed 70-90% after the first turn, reducing per-turn input costs by 80-90%.

## How Bedrock Prompt Caching Works

Bedrock stores computed KV tensor state of a contiguous prefix. On subsequent calls, if the prefix matches **exactly byte-for-byte**, the cached state is reused at 10% of the standard input price.

**Key constraints:**
- Maximum 4 cache checkpoints per request (across tools, system, and messages)
- Minimum 4,096 tokens per checkpoint for Claude Opus 4.x
- Cache TTL: 5 minutes (refreshed on every cache read)
- Evaluation order: `tools → system → messages` (changes in earlier sections invalidate later caches)
- Any byte-level change in a cached prefix = complete cache miss

## 3-Tier Cache Strategy

### Tier 1: Tool Definitions (~12k tokens, Checkpoint 1)

Tool definitions are 100% stable per session. A cache checkpoint at the end of the `toolConfig` array ensures these tokens are written once and read continuously.

Since Bedrock evaluates tools **first** in the hierarchy, this is the foundational prefix that all other cached content builds upon.

```
toolConfig: [tool1, tool2, ..., tool17, CACHE_POINT]
```

### Tier 2: System Prompt (~6k tokens, Checkpoint 2)

The system prompt is split into:
- **Stable base** (identity, tool guidance, environment) — CACHED
- **Dynamic injections** (skills, reconciler advice) — NOT cached, varies per turn

```
system: [stable_base_text, CACHE_POINT, dynamic_injections_text]
```

Dynamic content changes don't invalidate the cached prefix because they come *after* the checkpoint.

### Tier 3: Messages (Incremental Extension, Checkpoint 3)

The cache checkpoint is placed on the **last message** (most recent content). This implements incremental extension:

```
Turn 1: [tools✓][system✓][user1✓]          → Write entire prefix to cache
Turn 2: [tools✓][system✓][user1][asst1][user2✓]  → Read prefix, compute delta
Turn 3: [tools✓][system✓][...][asst2][user3✓]    → Read more, compute less
```

The prefix grows monotonically (messages are append-only), guaranteeing byte-for-byte prefix match on every call.

**Critical insight:** The old strategy placed the checkpoint on the *second-to-last* message and shifted it backward every call. This violated prefix matching and guaranteed 0% cache hits.

## Pricing (Claude Opus 4.x on Bedrock, 2026)

| Token Type | Rate | When |
|-----------|------|------|
| Input (uncached) | $5.00 / MTok | Tokens not in cache |
| Cache Write | $6.25 / MTok | First time prefix is stored (1.25x input) |
| Cache Read | $0.50 / MTok | Subsequent calls that hit cache (0.1x input) |
| Output | $25.00 / MTok | All generated tokens (never cached) |

### Cost math for a typical turn:

Without caching (all 50k context tokens as uncached input):
- 50k × $5/MTok = **$0.25 per API call**
- 15 tool calls per turn = **$3.75 per user turn**

With caching at 90% hit rate:
- 5k uncached × $5/MTok = $0.025
- 45k cache read × $0.50/MTok = $0.0225
- Per API call: **$0.048**
- 15 tool calls per turn = **$0.72 per user turn** (81% reduction)

## TUI Status Bar

The status bar shows live cache metrics:

```
codecuttlectl  claude-opus-4  us-west-2  12p    45.2k in  8.1k out  87% cache  ~$0.42
```

- **in**: Total input tokens (uncached + cache_read + cache_write)
- **out**: Output tokens generated
- **cache %**: `cache_read_tokens / total_input_tokens × 100`
- **~$X.XX**: Estimated session cost (accumulated across resumes)

## Diagnosing Cache Issues

### 0% cache hit

If you see 0% cache, check:
1. **Dynamic content in system prompt before checkpoint** — skills/reconciler text must come AFTER the cache point
2. **Tool definitions changing mid-session** — adding/removing tools busts the tools cache
3. **JSON serialization non-determinism** — Go map iteration order varies; tool schemas must be built deterministically

### Low cache hit (< 50%)

1. **Session idle > 5 minutes** — cache TTL expires, next call is a full write
2. **Very short conversations** — first call is always a write; cache value increases with turn count

### How to verify caching works

Run with `--audit-log` and check the structured events:
```bash
codecuttlectl --audit-log 2>&1 | grep token_usage | jq .
```

Look for `cache_read_tokens > 0` after the first API call in a turn.

## Session Cost Tracking

Token usage and estimated cost are persisted in the session file:

```json
{
  "meta": {
    "stats": {
      "input_tokens": 15000,
      "output_tokens": 8000,
      "cache_read_input_tokens": 200000,
      "cache_write_input_tokens": 18000,
      "estimated_cost_usd": 1.84
    }
  }
}
```

View per-session costs with:
```bash
codecuttlectl --list-sessions
```

## CLI Flags

| Flag | Purpose |
|------|---------|
| `--audit-log` | Emit structured JSON events (including token usage) to stderr |
| `--list-sessions` | Show all sessions with token counts and cost estimates |

## Future: 1-Hour TTL

Bedrock supports `"ttl": "1h"` on cache checkpoints for Claude Opus 4.5+. This costs $10/MTok for writes (vs $6.25 for 5m) but survives longer idle periods. Currently not implemented — the 5-minute TTL is sufficient for continuous agent loops where each call refreshes the timer.

Consider enabling for CDE environments where human review causes >5-minute gaps between turns.

## Future: Keepalive Pings

If cache expiration becomes a problem (e.g., sessions that idle while builds run), a background keepalive ping at 4.5-minute intervals can refresh the TTL. A minimal cache-read request costs ~$0.01 and prevents the ~$0.12 cache rebuild penalty.

## References

- [AWS Bedrock Prompt Caching docs](https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html)
- [How Claude Code uses prompt caching](https://code.claude.com/docs/en/prompt-caching)
- [Agent Loop Caching: The Missing Optimization](https://pub.towardsai.net/agent-loop-caching-the-missing-optimization-for-agent-workflows-230cc530eb72)
- [5 Things I Learned About Prompt Caching the Hard Way](https://builder.aws.com/content/3ElydDhkvqaHao2TrGxd3Z76BQq)
- [Claude API Pricing](https://platform.claude.com/docs/en/about-claude/pricing)
