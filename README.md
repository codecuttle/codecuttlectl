# codecuttlectl

`codecuttlectl` wraps foundation models with structured tool execution, diagnostic feedback loops, and conditional knowledge injection. It's the control binary for the Codecuttle meta-harness.

**Early alpha.** Phases 1-3 of the PoC plan are implemented. Routing engine, Optic Lobe memory, fleet telemetry, swarm orchestration, and self-evolving harness code are future work.

![codecuttlectl TUI](docs/tui-screenshot.png)

## Context

Lee et al. ("Meta-Harness: End-to-End Optimization of Model Harnesses", 2026) showed that the harness around a fixed LLM — context construction, tool orchestration, memory, state — produces a 6x performance gap on identical benchmarks. Giving an agent full access to prior execution traces enables iterative harness optimization.

We're building toward that. The Inkwell captures execution traces. The skills system ships versioned knowledge with tools. The proto contract defines a stable interface. The intent is for the system to eventually guide production of its own plugins in a versioned, iterative swarm loop.

## Install

```bash
make all
sudo cp bin/codecuttlectl /usr/local/bin/codecuttlectl
sudo mkdir -p /usr/local/lib/codecuttlectl/plugins
sudo cp bin/plugins/* /usr/local/lib/codecuttlectl/plugins/
echo 'alias c3="codecuttlectl -plugin-dir /usr/local/lib/codecuttlectl/plugins"' >> ~/.bashrc
```

## Usage

```bash
# Bedrock (default provider)
c3                              # TUI
c3 -no-tui                      # Streaming REPL
c3 -message "Fix the build"    # One-shot
c3 --session ses_abc123         # Resume
c3 --list-sessions              # Recent sessions
c3 -thinking                    # Extended reasoning

# Ollama (local models)
c3 --provider ollama --model gemma4:31b         # Explicit provider
c3 --model ollama:gemma4:31b                    # Auto-detect from prefix
c3 --provider ollama --model qwen3:32b          # Any Ollama model
c3 --provider ollama --ollama-url http://remote:11434 --model gemma4:31b  # Remote server

# Google AI (Gemini)
c3 --provider google --model gemini-2.5-pro     # Explicit provider
c3 --provider google --model gemini-2.5-flash   # Faster/cheaper
c3 --provider google --list-models              # List available models
```

## Providers

codecuttlectl supports multiple LLM providers through a unified interface:

| Provider | Flag | Models | Cost |
|----------|------|--------|------|
| **AWS Bedrock** (default) | `--provider bedrock` | Claude Opus 4.6, Sonnet, Haiku | Pay-per-token |
| **Google AI** | `--provider google` | Gemini 2.5 Pro, Flash | Pay-per-token |
| **OpenRouter** | `--provider openrouter` | Any model (Qwen, Claude, Llama) | Pay-per-token |
| **Ollama** | `--provider ollama` | gemma4, llama3, qwen3, any local model | Free (local) |

The provider is auto-detected from the model name prefix (e.g., `ollama:gemma4:31b`). See [`docs/providers.md`](docs/providers.md) for details.

## Architecture

Named after cephalopod neurology. A cuttlefish distributes 60% of its neurons into peripheral arm clusters that solve problems locally. The software mirrors this: tool execution happens in isolated plugin subprocesses, not the central model.

| Subsystem | Status |
|-----------|--------|
| **Cuttlebone Substrate** — protobuf + gRPC plugin interface | Done |
| **Inkwell** — error classification + reconciliation loop | Done |
| **Skills Registry** — conditional knowledge injection | Done |
| **Sessions** — persistence, resume, Inkwell capture | Done |
| **Typed Schema** — auto-derived JSON Schema from Go structs | Done |
| **Scaffold Generator** — plugin stub generation mid-session | Done |
| **Context Compaction** — heuristic tool result summarization | Done |
| **Multi-Provider** — Bedrock + Google + Ollama via provider interface | Done |
| **State Dictionary** — ground-truth injection for local models | Done |
| **Auto-Planning** — harness-managed task extraction from text | Done |
| **Swarm Orchestration** — multi-agent routing via YAML Morphologies | Done |
| **Swarm Backlog** — parallel async delegation via Headless Agents | Done |
| **Chromatophore Engine** — Chomsky hierarchy routing | Planned |
| **Optic Lobe** — PostgreSQL + pgvector + AGE memory | Planned |
| **Arm Nodes** — edge inference agents | Planned |

## Tools

17 total (12 plugin, 5 built-in):

`read_file` `write_file` `edit_file` `list_directory` `bash_exec` `grep` `glob` `git` `go_skills` `websearch` `webfetch` `github` `todo_manage` `tool_info` `get_skill` `scaffold_plugin` `reload_plugins`

## Plugins

Standalone gRPC binaries. Drop a `cuttlebone-*` binary in the plugin directory, discovered on next launch (or mid-session via `reload_plugins`). Any language. Plugins ship embedded skills (versioned Markdown) that activate based on context triggers.

Plugin inputs are defined as annotated Go structs with JSON Schema auto-derived at startup:

```go
type myInput struct {
    Query   string        `json:"query" jsonschema:"required" jsonschema_description:"Search query"`
    Limit   types.FlexInt `json:"limit,omitempty" jsonschema_description:"Max results"`
}

// In Describe():
InputSchema: schema.MustSchema(&myInput{}),
```

New plugins can be scaffolded mid-session via the `scaffold_plugin` tool — generates a buildable stub with typed inputs, schema derivation, and proper boilerplate.

Crash recovery, execution timeouts, input validation, auto-restart. See [`docs/writing-plugins.md`](docs/writing-plugins.md).

## Sessions + Inkwell

Conversations persist to `~/.local/share/codecuttlectl/sessions/`. Every tool execution recorded with timing, error classification, full I/O. The reconciler injects corrective prompts when failures are detected. Zero overhead when things work.

See [`docs/sessions-and-inkwell.md`](docs/sessions-and-inkwell.md).

## Prompt Caching & Cost Tracking

3-tier incremental extension caching for Bedrock minimizes API costs:

1. **Tools** (~12k tokens) — cached at end of `toolConfig` (never changes)
2. **System prompt** (~6k tokens) — cached after stable base, dynamic injections after
3. **Messages** — checkpoint on most recent message; prefix extends forward monotonically

Google AI context caching is also supported for the system prompt and tools, automatically engaging when token counts exceed the `--google-cache-threshold`.

The TUI status bar shows live metrics: `45.2k in  8.1k out  87% cache  ~$0.42`

For local models (Ollama), the status bar shows tokens and context window % without cost: `5.7k in  0.8k out  2% ctx`

Session cost tracking persists across resumes. `--list-sessions` shows per-session cost estimates. `--audit-log` emits structured JSON events for external cost monitoring.

See [`docs/caching.md`](docs/caching.md).

A background keepalive ping fires every 4 minutes during idle to prevent the 5-minute cache TTL from expiring. See [caching docs](docs/caching.md) for details.

## Not yet implemented

- Multi-model routing (Haiku/Sonnet for auxiliary tasks — [design doc](docs/multi-model-design.md), PR #25 ready)
- Chomsky routing (dynamic complexity classification — depends on multi-model)
- Optic Lobe (cross-session semantic memory)
- Work Backlog (cross-session deferred intent queue — [design doc](docs/backlog.md), Phase 1 done)
- Fleet telemetry (OpenTelemetry — Phase 10 of [streaming doc](docs/streaming-tool-telemetry.md))
- Self-evolving harness (outer loop from execution traces)
- Proto-based schema path (cross-language plugin inputs via .proto)
- MicroVM isolation (Firecracker)
- Hot-reload plugins (fsnotify-based auto-discovery)
- LLM-generated compaction summaries (Phase 2 — [design doc](docs/llm-compaction-design.md), depends on multi-model)
- Optic Lobe retrieval for compacted content (Phase 3 of [compaction doc](docs/context-compaction.md))

## Development

```bash
make all     # Build orchestrator + all plugins
make test    # Unit tests, all packages
make proto   # Regenerate protobuf
```

## License

See [LICENSE](LICENSE).
