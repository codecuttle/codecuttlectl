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
c3                              # TUI
c3 -no-tui                      # Streaming REPL
c3 -message "Fix the build"    # One-shot
c3 --session ses_abc123         # Resume
c3 --list-sessions              # Recent sessions
c3 -thinking                    # Extended reasoning
```

## Architecture

Named after cephalopod neurology. A cuttlefish distributes 60% of its neurons into peripheral arm clusters that solve problems locally. The software mirrors this: tool execution happens in isolated plugin subprocesses, not the central model.

| Subsystem | Status |
|-----------|--------|
| **Cuttlebone Substrate** — protobuf + gRPC plugin interface | Done |
| **Inkwell** — error classification + reconciliation loop | Done |
| **Skills Registry** — conditional knowledge injection | Done |
| **Sessions** — persistence, resume, Inkwell capture | Done |
| **Chromatophore Engine** — Chomsky hierarchy routing | Planned |
| **Optic Lobe** — PostgreSQL + pgvector + AGE memory | Planned |
| **Arm Nodes** — edge inference agents | Planned |

## Tools

12 total (9 plugin, 3 built-in):

`read_file` `write_file` `edit_file` `list_directory` `bash_exec` `grep` `glob` `git` `go_skills` `todo_manage` `tool_info` `get_skill`

## Plugins

Standalone gRPC binaries. Drop a `cuttlebone-*` binary in the plugin directory, discovered on next launch. Any language. Plugins ship embedded skills (versioned Markdown) that activate based on context triggers.

```go
pluginkit.EmbedSkill(fs, "skills/debugging.md", "go_debugging", "on_error:compile|on_language:go", 60)
```

Crash recovery, execution timeouts, auto-restart. See [`docs/writing-plugins.md`](docs/writing-plugins.md).

## Sessions + Inkwell

Conversations persist to `~/.local/share/codecuttlectl/sessions/`. Every tool execution recorded with timing, error classification, full I/O. The reconciler injects corrective prompts when failures are detected. Zero overhead when things work.

See [`docs/sessions-and-inkwell.md`](docs/sessions-and-inkwell.md).

## Not yet implemented

- Chomsky routing (dynamic complexity classification)
- Optic Lobe (cross-session semantic memory)
- Fleet telemetry (OpenTelemetry)
- Swarm orchestration
- Self-evolving harness (outer loop from execution traces)
- MicroVM isolation (Firecracker)
- Hot-reload plugins

## Development

```bash
make all     # Build orchestrator + 9 plugins
make test    # 69 tests, 5 packages
make proto   # Regenerate protobuf
```

## License

See [LICENSE](LICENSE).
