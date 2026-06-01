# Writing Plugins

Cuttlebone plugins are standalone executables that communicate with the orchestrator via gRPC over Unix domain sockets using the HashiCorp go-plugin framework.

## Minimal Tool Plugin

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type myTool struct{}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "One-line description shown to the LLM",
        InputSchema: `{
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "The search query"}
            },
            "required": ["query"]
        }`,
        LlmContextHint: "Use my_tool when the user asks about X.",
        Version:         "1.0.0",
        Capabilities: &pb.ToolCapabilities{
            SupportsCancellation: true,
            MaxTimeoutSeconds:    60,
        },
    }, nil
}

func (t *myTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
    var params struct {
        Query string `json:"query"`
    }
    if err := json.Unmarshal([]byte(req.Input), &params); err != nil {
        return &pb.ExecuteResponse{IsError: true, ErrorMessage: err.Error()}, nil
    }

    result := fmt.Sprintf("Found results for: %s", params.Query)
    return &pb.ExecuteResponse{
        Output:   result,
        Metadata: map[string]string{"query": params.Query},
    }, nil
}

func main() {
    pluginkit.Serve(&myTool{})
}
```

## Plugin with Embedded Skills

Plugins can ship versioned knowledge alongside their tools:

```go
package main

import (
    "context"
    "embed"
    "encoding/json"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

//go:embed skills/*
var skillFS embed.FS

type myTool struct{}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "Does something useful",
        InputSchema: `{"type": "object", "properties": {"input": {"type": "string"}}, "required": ["input"]}`,
        Version:     "2.0.0",
        Skills: []*pb.Skill{
            pluginkit.EmbedSkill(skillFS, "skills/workflow.md",
                "my_workflow", "on_error:*|on_tool:my_tool", 50),
            pluginkit.EmbedSkill(skillFS, "skills/best_practices.md",
                "my_best_practices", "on_request", 30),
        },
    }, nil
}
```

Skills are Markdown files embedded at compile time. They're conditionally injected into the LLM context based on trigger expressions.

## Companion Knowledge Plugin (No Tool)

A plugin that only ships skills — no executable tool:

```go
package main

import (
    "context"
    "embed"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

//go:embed skills/*
var skillFS embed.FS

type knowledgePlugin struct{}

func (t *knowledgePlugin) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "domain_knowledge",
        Description: "Domain-specific knowledge (no tool)",
        InputSchema: `{"type": "object", "properties": {}}`,
        Version:     "1.0.0",
        Skills: []*pb.Skill{
            pluginkit.EmbedSkill(skillFS, "skills/debugging.md",
                "debugging_guide", "on_error:*", 60),
            pluginkit.EmbedSkill(skillFS, "skills/patterns.md",
                "design_patterns", "on_request", 40),
        },
    }, nil
}

func (t *knowledgePlugin) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
    return &pb.ExecuteResponse{
        Output: "This is a knowledge-only plugin. Use get_skill to browse its content.",
    }, nil
}

func main() {
    pluginkit.Serve(&knowledgePlugin{})
}
```

## Key Concepts

### Naming

Binary must be named `cuttlebone-<name>` (e.g., `cuttlebone-git`). The orchestrator discovers plugins by scanning the plugin directory for executables matching this prefix.

### The Two RPCs

| RPC | When Called | Purpose |
|-----|------------|---------|
| `Describe()` | Once at startup | Returns tool metadata, JSON schema, LLM hints, skills |
| `Execute()` | Each invocation | Runs the tool with JSON input, returns text output |

### DescribeResponse Fields

| Field | Required | Purpose |
|-------|----------|---------|
| `name` | Yes | Unique tool identifier (used in LLM tool_use blocks) |
| `description` | Yes | Shown to LLM alongside the tool definition |
| `input_schema` | Yes | JSON Schema string defining input parameters |
| `llm_context_hint` | No | Extra guidance injected into the system prompt |
| `version` | No | Semantic version for tracking |
| `capabilities` | No | Declares timeout, cancellation, confirmation support |
| `skills` | No | Embedded knowledge/workflows with trigger expressions |

### Skill Triggers

Triggers determine when a skill is injected into the LLM context:

| Trigger | Fires When |
|---------|-----------|
| `always` | Every Converse call (subject to token budget) |
| `on_request` | Only when agent explicitly asks via `get_skill` |
| `on_error:compile` | Inkwell detects compile error class |
| `on_error:*` | Any error |
| `on_tool:bash_exec` | A specific tool was used recently |
| `on_file:*.go` | A matching file was referenced |
| `on_language:python` | Language detected in output |
| `on_turn:first` | First turn of session only |
| `on_loop` | Agent is stuck in a failure loop |

Combine with `|` (OR): `on_error:compile|on_language:go`

### Flexible JSON Parsing

LLMs often emit numbers as strings (`"5"` instead of `5`). Handle this in your input parsing:

```go
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
    var i int
    if err := json.Unmarshal(data, &i); err == nil {
        *f = flexInt(i)
        return nil
    }
    var s string
    if err := json.Unmarshal(data, &s); err == nil {
        fmt.Sscanf(s, "%d", (*int)(f))
    }
    return nil
}
```

### Error Handling

Return errors via the response, not as Go errors:

```go
// Good: error in response (LLM sees the error and can adapt)
return &pb.ExecuteResponse{IsError: true, ErrorMessage: "file not found"}, nil

// Bad: Go error (causes gRPC failure, less informative)
return nil, fmt.Errorf("file not found")
```

### Plugin Robustness

The orchestrator provides:
- **Startup timeout** (10s): Plugins that hang during handshake are skipped
- **Execution timeout**: Per-plugin, declared in `Capabilities.MaxTimeoutSeconds`
- **Crash recovery**: If your plugin crashes, it's automatically restarted (up to 3 times)
- **Process isolation**: Panics/OOM in your plugin never crash the orchestrator

### Building and Installing

```bash
# Build
go build -trimpath -ldflags="-s -w" -o cuttlebone-my-tool ./plugins/cuttlebone-my-tool/

# Install
sudo cp cuttlebone-my-tool /usr/local/lib/codecuttlectl/plugins/

# Verify (next c3 invocation will discover it)
c3 -message "Use tool_info to list all tools"
```

### Language Agnosticism

Because the interface is gRPC, plugins can be written in **any language** that supports gRPC (Python, Rust, Java, TypeScript, etc.). They need to:

1. Implement the `ToolPlugin` service from `proto/cuttlebone.proto`
2. Use the go-plugin handshake protocol (magic cookie: `CUTTLEBONE_PLUGIN=codecuttle-v1`)
3. Serve on the negotiated Unix socket

For non-Go plugins, implement the handshake protocol directly. See [HashiCorp go-plugin docs](https://github.com/hashicorp/go-plugin) for cross-language examples.
