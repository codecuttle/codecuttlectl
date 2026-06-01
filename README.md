# codecuttlectl

Autonomous agent orchestration. The harness matters as much as the model.

`codecuttlectl` is the control binary for the Codecuttle meta-harness — a system that wraps foundation models with structured tool execution, diagnostic feedback loops, and conditional knowledge injection to produce reliable autonomous agents. It's a harness in the literal sense: the code that determines what the model sees, what tools it can use, and how it recovers from failure.

Early alpha. Most of the architecture described in the design docs is not yet implemented. What exists today is a functional PoC covering Phases 1-3 of the implementation plan: embedded prompts, the Cuttlebone plugin substrate, session persistence, the Inkwell reconciliation loop, and the skills injection system. The routing engine, Optic Lobe memory, fleet-scale telemetry, and the swarm architecture are future work.

## Why "harness"

Recent work (Lee et al., "Meta-Harness: End-to-End Optimization of Model Harnesses", 2026) demonstrates that the harness around a fixed LLM — the code governing context construction, tool orchestration, memory, and state management — can produce a 6x performance gap on identical benchmarks. Their key finding: giving an optimization agent full filesystem access to prior execution traces enables it to diagnose failures and iteratively improve the harness code itself.

This is the trajectory we're building toward. Today, `codecuttlectl` is a hand-engineered harness. The intent is for the system to eventually guide the production of its own Go plugins — tools, skills, and workflows — in a versioned, iterative loop. The Inkwell captures execution traces. The skills system ships versioned knowledge alongside tools. The proto contract defines a stable interface. The pieces exist for a future outer loop (the "Chromatophore Engine") to propose, evaluate, and evolve harness components autonomously.

## What it does today

A single Go binary that connects to AWS Bedrock, launches tool plugins as gRPC subprocesses, streams responses, persists sessions, classifies errors, injects corrective prompts when stuck, and conditionally surfaces domain knowledge based on what the agent is doing.

```
codecuttlectl (alias: c3)
├── 9 tool plugins (file ops, shell, git, search, Go knowledge)
├── 3 built-in tools (todos, introspection, skill retrieval)
├── Inkwell diagnostic loop (error classification → corrective prompt injection)
├── Skills system (trigger-based knowledge injection from plugins)
├── Session persistence (resume, list, prune)
├── Streaming (real-time token output in REPL and TUI)
└── Full-screen TUI with markdown rendering
```

![codecuttlectl TUI](docs/tui-screenshot.png)

## Install

```bash
make all
sudo cp bin/codecuttlectl /usr/local/bin/codecuttlectl
sudo mkdir -p /usr/local/lib/codecuttlectl/plugins
sudo cp bin/plugins/* /usr/local/lib/codecuttlectl/plugins/

# Alias
echo 'alias c3="codecuttlectl -plugin-dir /usr/local/lib/codecuttlectl/plugins"' >> ~/.bashrc
```

## Usage

```bash
c3                              # Full-screen TUI
c3 -no-tui                      # Streaming REPL
c3 -message "Fix the build"    # One-shot (scripting/CI)
c3 --session ses_abc123         # Resume
c3 --list-sessions              # Show recent
c3 -thinking                    # Extended reasoning
```

## Architecture

The naming draws from cephalopod neurology. A cuttlefish distributes 60% of its neurons outside the central brain into peripheral arm clusters that solve problems locally. The software mirrors this: deterministic tool execution happens in isolated plugin subprocesses (arm nodes), not in the central model.

| Subsystem | Analog | Status |
|-----------|--------|--------|
| **Cuttlebone Substrate** | Internal shell (structural rigidity) | Implemented — protobuf + gRPC plugin interface |
| **Inkwell** | Ink sac (diagnostic defense) | Implemented — error classification + reconciliation loop |
| **Skills Registry** | Learned hunting patterns | Implemented — conditional knowledge injection |
| **Chromatophore Engine** | Pigment cells (dynamic scaling) | Planned — Chomsky hierarchy routing |
| **Optic Lobe** | Visual processing center | Planned — PostgreSQL + pgvector + AGE memory |
| **Stellate Ganglion** | Peripheral motor reflexes | Partial — bash fallback when tools fail |
| **Arm Nodes** | Distributed neural clusters | Planned — edge inference agents |

## Tools

| Tool | Type | Description |
|------|------|-------------|
| `read_file` | plugin | Read with line numbers, offset/limit |
| `write_file` | plugin | Create or overwrite |
| `edit_file` | plugin | Find-and-replace with uniqueness checks |
| `list_directory` | plugin | Directory listing |
| `bash_exec` | plugin | Shell execution with timeout |
| `grep` | plugin | Regex search across files |
| `glob` | plugin | File pattern matching |
| `git` | plugin | Version control (safety whitelist) |
| `go_skills` | plugin | Go knowledge companion (no tool, skills only) |
| `todo_manage` | built-in | Task tracking |
| `tool_info` | built-in | Schema introspection |
| `get_skill` | built-in | On-demand skill retrieval |

## Plugin system

Tools are standalone binaries communicating over gRPC. Drop a new `cuttlebone-*` binary in the plugin directory and it's discovered on next launch. Write plugins in any gRPC-capable language. Plugins can ship embedded skills (versioned Markdown) that activate based on context:

```go
Skills: []*pb.Skill{
    pluginkit.EmbedSkill(fs, "skills/debugging.md",
        "go_debugging", "on_error:compile|on_language:go", 60),
}
```

See [`docs/writing-plugins.md`](docs/writing-plugins.md).

## Session management

Conversations persist to `~/.local/share/codecuttlectl/sessions/`. Every tool execution is recorded in the Inkwell with timing, error classification, and full I/O. Resume any session. Prune old ones.

```bash
c3 --list-sessions              # Table of recent sessions
c3 --session ses_abc123         # Resume
c3 --prune-sessions 30          # Delete >30 days old
```

See [`docs/sessions-and-inkwell.md`](docs/sessions-and-inkwell.md).

## Inkwell reconciliation

When the agent fails, the Inkwell analyzes recent execution traces and injects corrective guidance into the next model call:

- Single error → targeted fix advice (language-aware, file/line extracted)
- Loop detected (3+ same failure) → "stop, change strategy"
- Escalation (5+ failures) → "report failure, propose alternatives"

Zero overhead when things work. The reconciler does nothing if the last tool call succeeded.

## What's not implemented yet

- **Chomsky routing** — Dynamic complexity classification to route tasks to appropriate model tiers
- **Optic Lobe** — PostgreSQL + pgvector + Apache AGE for cross-session semantic memory
- **Fleet telemetry** — OpenTelemetry instrumentation for multi-agent observability
- **Swarm orchestration** — Multiple agents coordinating on decomposed tasks
- **Self-evolving harness** — The outer loop that optimizes harness code from execution traces (per Meta-Harness paper)
- **MicroVM isolation** — Firecracker sandboxing for untrusted code execution
- **Hot-reload** — Plugin discovery during a running session

## Development

```bash
make all            # Build orchestrator + 9 plugins
make test           # 69 tests across 5 packages
make proto          # Regenerate protobuf
make vet            # Static analysis
```

## Project structure

```
cmd/codecuttlectl/       CLI entrypoint
internal/
  bedrock/               AWS Bedrock Converse + ConverseStream
  conversation/          Agent loop, streaming, tool dispatch, sessions
  cuttlebone/v1/         Generated protobuf + gRPC stubs
  inkwell/               Error classification + reconciliation loop
  pluginhost/            Plugin lifecycle (discovery, crash recovery, restart)
  pluginkit/             Plugin authoring SDK
  prompt/                Embedded system prompts (go:embed + text/template)
  session/               Persistence (FileStore, message serialization)
  skills/                Conditional skill injection (triggers, registry, budget)
  todo/                  Task list state
  tui/                   Bubble Tea terminal UI
plugins/                 9 plugin sources
proto/                   Protobuf schema
docs/                    Architecture, guides
```

## License

See [LICENSE](LICENSE).
