# Dependencies

## Why These Libraries

### Core: AWS Bedrock

| Package | Why |
|---------|-----|
| `github.com/aws/aws-sdk-go-v2` | Official AWS SDK. Native credential chain (env, profile, IMDS, ECS, web identity). SigV4 signing handled transparently. |
| `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | Bedrock Runtime: `Converse` (sync) and `ConverseStream` (streaming). Typed event stream with proper binary framing. |

**Why not use a third-party LLM SDK (LangChain, etc.)?** Direct SDK gives us full control over the Converse API, streaming event handling, and extended thinking — without abstraction layers that add latency or hide failures.

### Core: Plugin System

| Package | Why |
|---------|-----|
| `github.com/hashicorp/go-plugin` | Battle-tested (powers Terraform, Vault, Nomad). True process isolation via subprocess + gRPC. Auto-TLS on Unix sockets. Supports any gRPC-capable language. |
| `github.com/hashicorp/go-hclog` | Structured logging used by go-plugin internally. |
| `github.com/hashicorp/yamux` | Connection multiplexing for go-plugin's socket communication. |
| `google.golang.org/grpc` | gRPC runtime for plugin RPC calls. |
| `google.golang.org/protobuf` | Protocol Buffer runtime for schema enforcement. |

**Why go-plugin over WASM?** WASM (via Wasmtime/WASI) provides memory safety but struggles with native multi-threading, arbitrary network connections, and debugging. go-plugin gives us true OS process isolation with microsecond-latency Unix socket IPC — better for tools that need to hit filesystems, networks, and shells.

**Why not MCP (Model Context Protocol)?** MCP standardizes tool discovery via JSON-RPC but operates as a heavy abstraction layer. The Cuttlebone Substrate binds tool schemas directly into compiled protobuf contracts — the tool interface is the source of truth, not a remote registry.

### Core: TUI

| Package | Why |
|---------|-----|
| `charm.land/bubbletea/v2` | Elm Architecture TUI. Functional state management prevents mutation bugs. Supports inline, full-screen, and mixed rendering. Native mouse/keyboard. |
| `charm.land/lipgloss/v2` | CSS-like terminal styling. Adaptive color downsampling (truecolor → 256 → 16 → mono). |
| `charm.land/bubbles/v2` | Reusable components: viewport (scrollable), textarea (input), spinner (loading). |
| `github.com/charmbracelet/glamour` | Markdown rendering in the terminal. Renders code blocks, tables, headings with color. |

**Why Bubble Tea over tview?** tview is widget-based (OOP/callbacks). Bubble Tea's functional approach (Model-Update-View) is predictable, testable, and composes well. The Elm Architecture eliminates an entire class of state-mutation bugs that are common in TUI development.

### Internal Packages (no external deps)

| Package | Purpose |
|---------|---------|
| `internal/session` | Session persistence — FileStore with atomic writes, message serialization, XDG paths. Pure Go, no external deps. |
| `internal/inkwell` | Error classification (multi-language parser) + reconciliation loop. Pure Go regex-based parsing. |
| `internal/skills` | Trigger expression parser + skill registry + budget management. Pure Go. |
| `internal/todo` | In-memory task list. Pure Go. |
| `internal/prompt` | Embedded Markdown templates via `embed.FS` + `text/template`. Pure Go stdlib. |

### Indirect (pulled in by direct deps)

| Package | Pulled by | Notes |
|---------|-----------|-------|
| `github.com/fatih/color` | go-hclog | Terminal colors for structured logs. |
| `github.com/oklog/run` | go-plugin | Actor/run-group coordination. |
| `golang.org/x/net`, `x/sys`, `x/text` | grpc | Official Go extended libraries. |
| `github.com/golang/protobuf` | grpc (transitional) | Deprecated compatibility shim. |

### Test-only (in go.sum, NOT compiled into binary)

| Package | Why in go.sum |
|---------|--------------|
| `github.com/stretchr/testify` | Test dep of go-plugin |
| `github.com/pmezard/go-difflib` | Test dep of testify |
| `github.com/davecgh/go-spew` | Test dep of testify |

These never appear in the compiled `codecuttlectl` binary.

## Version Policy

- Direct dependencies: update to latest stable when new features are needed or security patches land.
- Indirect dependencies: managed by upstream.
- Internal packages: no external deps, no version coordination needed.
