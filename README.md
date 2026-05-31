# codecuttlectl

> *500 million neurons. 60% outside the brain. Zero wasted compute.*

**Codecuttle** is a meta-harness for autonomous agent orchestration. Like its namesake — the cuttlefish, which distributes cognition across peripheral neural clusters in its arms while reserving its central brain for complex reasoning — `codecuttlectl` routes tasks to the right computational tier: trivial syntax checks to lightweight edge agents, complex architectural work to heavy inference models.

The binary is `codecuttlectl`. The alias is `c3`.

## Install

```bash
make all
sudo cp bin/codecuttlectl /usr/local/bin/codecuttlectl
sudo ln -sf /usr/local/bin/codecuttlectl /usr/local/bin/c3
sudo mkdir -p /usr/local/lib/codecuttlectl/plugins
sudo cp bin/plugins/* /usr/local/lib/codecuttlectl/plugins/
```

## Usage

```bash
# Full-screen TUI (default)
c3 -plugin-dir /usr/local/lib/codecuttlectl/plugins

# Plain REPL (for non-TTY environments)
c3 -no-tui -plugin-dir /usr/local/lib/codecuttlectl/plugins

# One-shot (scripting, CI)
c3 -plugin-dir /usr/local/lib/codecuttlectl/plugins -message "Fix the build"

# With extended thinking
c3 -plugin-dir /usr/local/lib/codecuttlectl/plugins -thinking
```

## Keybindings

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Ctrl+R` | Toggle thinking visibility |
| `Ctrl+T` | Toggle todo panel |
| `Ctrl+C` | Quit |
| `↑/↓` | Scroll history |

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for diagrams and detailed explanation.

```
codecuttlectl (the orchestrator)
├── Cuttlebone Substrate     — Protobuf + gRPC tool interface (go-plugin)
├── ConverseStream           — Real-time token streaming from AWS Bedrock
├── Todo Manager             — Built-in task tracking (in-memory, LLM-driven)
└── Bubble Tea TUI           — Full-screen terminal UI with streaming + keybindings
```

### Cuttlebone Substrate

Tools run as **isolated subprocesses** via HashiCorp go-plugin. Each tool is a standalone binary communicating over gRPC on Unix domain sockets. A crash in a tool cannot destabilize the orchestrator.

```
proto/cuttlebone.proto       Canonical tool interface
plugins/
  cuttlebone-bash-exec       Shell command execution
  cuttlebone-list-directory  Directory listing
  cuttlebone-read-file       File reading
  cuttlebone-write-file      File writing
```

Write a new plugin: implement `Describe()` + `Execute()`, call `pluginkit.Serve()`, compile, drop in the plugins directory. See [`docs/writing-plugins.md`](docs/writing-plugins.md).

## AWS Bedrock

Uses the **default credential chain** — env vars, profiles, EC2 IMDS, ECS task roles, web identity. No API keys needed on EC2 with an appropriate IAM role.

| Flag | Default | Description |
|------|---------|-------------|
| `-model` | `us.anthropic.claude-opus-4-6-v1` | Bedrock model ID |
| `-region` | `$AWS_REGION` or `us-east-1` | AWS region |
| `-profile` | `$AWS_PROFILE` | Credentials profile |
| `-thinking` | off | Enable extended thinking |

## Development

```bash
make all            # Build orchestrator + plugins
make test           # Unit tests
make proto          # Regenerate protobuf code
make vet            # Static analysis
python3 scripts/tui-test.py  # E2E TUI tests via tmux
```

## Project Structure

```
cmd/codecuttlectl/       CLI entrypoint (TUI / REPL / one-shot)
internal/
  bedrock/               AWS Bedrock Converse + ConverseStream
  conversation/          Agent loop, tool dispatch, todo management
  cuttlebone/v1/         Generated protobuf + gRPC stubs
  pluginhost/            go-plugin host (discovery, lifecycle, gRPC)
  pluginkit/             Plugin authoring helper
  prompt/                Embedded system prompts (go:embed + text/template)
  todo/                  In-memory task list state
  tui/                   Full-screen Bubble Tea UI
plugins/                 Built-in plugin sources
proto/                   Protobuf schema definitions
scripts/                 Test automation (tmux-based E2E)
docs/                    Architecture, dependencies, design decisions
```

## License

See [LICENSE](LICENSE).
