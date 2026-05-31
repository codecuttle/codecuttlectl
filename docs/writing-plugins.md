# Writing Plugins

Cuttlebone plugins are standalone executables that communicate with the orchestrator via gRPC over Unix domain sockets using the HashiCorp go-plugin framework.

## Minimal Example

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
        LlmContextHint: "Use my_tool when the user asks about X. Prefer this over bash_exec for Y.",
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

## Key Points

### Naming

Binary must be named `cuttlebone-<name>` (e.g., `cuttlebone-git-diff`). The orchestrator discovers plugins by scanning the plugin directory for executables matching this prefix.

### The Two RPCs

| RPC | When Called | Purpose |
|-----|------------|---------|
| `Describe()` | Once at startup | Returns tool metadata, JSON schema, LLM hints |
| `Execute()` | Each invocation | Runs the tool with JSON input, returns text output |

### DescribeResponse Fields

| Field | Required | Purpose |
|-------|----------|---------|
| `name` | Yes | Unique tool identifier (used in LLM tool_use blocks) |
| `description` | Yes | Shown to LLM alongside the tool definition |
| `input_schema` | Yes | JSON Schema string defining input parameters |
| `llm_context_hint` | No | Extra guidance injected into the system prompt |
| `version` | No | Semantic version for tracking |
| `capabilities` | No | Declares streaming, cancellation, confirmation support |

### LLM Context Hint

The `llm_context_hint` field solves the "missing context problem" — binary protocols strip human-readable metadata, so this field carries runtime guidance that gets injected into the system prompt alongside tool definitions. Use it for:

- When to prefer this tool over alternatives
- Important constraints or limitations
- Examples of good invocations

### Error Handling

Return errors via the response, not as Go errors:

```go
// Good: error in response (LLM sees the error and can retry)
return &pb.ExecuteResponse{IsError: true, ErrorMessage: "file not found"}, nil

// Bad: Go error (causes gRPC failure, less informative to the LLM)
return nil, fmt.Errorf("file not found")
```

### Working Directory

`req.WorkingDirectory` contains the session's working directory. Use it as the default for relative path resolution.

### Process Isolation

Your plugin runs as a separate OS process. This means:
- A panic/crash in your plugin does NOT crash the orchestrator
- You have full access to the OS (filesystem, network, shell)
- Memory usage is isolated
- The orchestrator kills your process on shutdown

### Building and Installing

```bash
# Build
go build -o cuttlebone-my-tool ./plugins/cuttlebone-my-tool/

# Install (copy to plugin directory)
cp cuttlebone-my-tool /usr/local/lib/codecuttlectl/plugins/

# Verify
c3 -plugin-dir /usr/local/lib/codecuttlectl/plugins -no-tui -message "Use my_tool"
```

### Language Agnosticism

Because the interface is gRPC, plugins can be written in **any language** that supports gRPC (Python, Rust, Java, TypeScript, etc.). They just need to:

1. Implement the `ToolPlugin` service from `proto/cuttlebone.proto`
2. Use the go-plugin handshake protocol (magic cookie: `CUTTLEBONE_PLUGIN=codecuttle-v1`)
3. Serve on the negotiated Unix socket

For non-Go plugins, you'll need to implement the handshake protocol directly. See [HashiCorp go-plugin docs](https://github.com/hashicorp/go-plugin) for cross-language examples.
