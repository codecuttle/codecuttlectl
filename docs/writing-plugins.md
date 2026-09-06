# Writing Plugins

Cuttlebone plugins are standalone executables that communicate with the orchestrator via gRPC over Unix domain sockets using the HashiCorp go-plugin framework.

## Minimal Tool Plugin (Recommended: Typed Schema)

The recommended approach uses annotated Go structs for input definition. The JSON Schema is auto-derived via `schema.MustSchema()` — one source of truth, no schema/struct drift.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

type myTool struct{}

type myToolInput struct {
    Query string `json:"query" jsonschema:"required" jsonschema_description:"The search query"`
}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "One-line description shown to the LLM",
        InputSchema: schema.MustSchema(&myToolInput{}),
        LlmContextHint: "Use my_tool when the user asks about X.",
        Version:         "1.0.0",
        Capabilities: &pb.ToolCapabilities{
            SupportsCancellation: true,
            MaxTimeoutSeconds:    60,
        },
    }, nil
}

func (t *myTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
    var params myToolInput
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

## Execution outcomes

A command failure is a tool result, not a successful RPC result: set `IsError`
and a nonempty `ErrorMessage`, retain partial stdout/stderr in `Output`, and
return that same outcome in the final streaming event. Nonempty stderr alone
must not imply failure. Consumers must use the status, not parse an `Error:`
text prefix. RPC errors instead describe failures delivering/executing the RPC.

The bash plugin uses identical unary/streaming result classification:

- `exit_code`: decimal process exit code; `-1` means signaled (Go ProcessState
  convention), absent if no process started.
- `error_kind`: `exit`, `signal`, `start`, `timeout`, or `cancelled`; absent on
  success. Input/schema validation errors remain separate.
- `stderr`: separated stderr when present; `exit_error`: underlying execution
  error. `timeout=true` and `cancelled=true` retain explicit context causes.
- `Output` contains collected command output; `ErrorMessage` carries the failure
  diagnostic. Unary pluginhost combines them for the model; raw streaming callers
  must inspect both fields on the final response.

These metadata fields do not introduce retry permission or a sandbox. A deadline
or cancellation can prevent the final gRPC response from reaching the caller;
transport errors must not be interpreted as command success. Process-tree cleanup,
stream transport recovery and persistence of all metadata are separate concerns.

## Input Struct Tags

The schema derivation system uses these struct tags:

| Tag | Purpose | Example |
|-----|---------|---------|
| `json:"name"` | JSON field name | `json:"max_results,omitempty"` |
| `jsonschema:"required"` | Mark field as required | `jsonschema:"required"` |
| `jsonschema:"enum=a,enum=b"` | Enumerate valid values | `jsonschema:"enum=status,enum=log,enum=diff"` |
| `jsonschema_description:"..."` | Human/LLM-readable description | `jsonschema_description:"Timeout in seconds"` |

Fields with `,omitempty` in the json tag are treated as optional (not required).

## Shared Types for LLM Quirks

The `pluginkit/types` package provides types that handle common LLM JSON generation issues:

### `types.FlexInt`

LLMs frequently emit integers as strings (`"5"` instead of `5`). `FlexInt` accepts both forms transparently:

```go
import "github.com/codecuttle/codecuttlectl/internal/pluginkit/types"

type myInput struct {
    Timeout types.FlexInt `json:"timeout,omitempty" jsonschema_description:"Timeout in seconds"`
}

// In Execute():
timeout := params.Timeout.Int()  // Always returns int, regardless of JSON form
```

The generated schema accurately reflects this: `oneOf[integer, string{pattern: "^-?[0-9]+$"}]`

### `types.FlexBool`

Handles `true`, `"true"`, `"yes"`, `1`, etc.:

```go
type myInput struct {
    Force types.FlexBool `json:"force,omitempty" jsonschema_description:"Force the operation"`
}

// In Execute():
if params.Force.Bool() { ... }
```

## Plugin with Embedded Skills

Plugins can ship versioned knowledge alongside their tools:

```go
package main

import (
    "context"
    "embed"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

//go:embed skills/*
var skillFS embed.FS

type myTool struct{}

type myToolInput struct {
    Input string `json:"input" jsonschema:"required" jsonschema_description:"The input to process"`
}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "Does something useful",
        InputSchema: schema.MustSchema(&myToolInput{}),
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
    "github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
)

//go:embed skills/*
var skillFS embed.FS

type knowledgePlugin struct{}

func (t *knowledgePlugin) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "domain_knowledge",
        Description: "Domain-specific knowledge (no tool)",
        InputSchema: schema.MustSchema(&struct{}{}),
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
| `input_schema` | Yes | JSON Schema string (use `schema.MustSchema(&input{})`) |
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

### Input Validation

The orchestrator validates tool input against the declared JSON Schema **before** sending it to the plugin. If validation fails, the LLM receives a clear error message and can fix its input on the next iteration. This is enabled by default and can be controlled via `Manager.SetValidateInput(bool)`.

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
- **Input validation**: Schema validation before execution catches malformed LLM output early

### Building and Installing

```bash
# Build
go build -trimpath -ldflags="-s -w" -o cuttlebone-my-tool ./plugins/cuttlebone-my-tool/

# Install
sudo cp cuttlebone-my-tool /usr/local/lib/codecuttlectl/plugins/

# Verify (next c3 invocation will discover it)
c3 -message "Use tool_info to list all tools"
```

### Scaffold Generator

New plugins can be scaffolded via the `scaffold_plugin` built-in tool during a session. The agent calls it with a structured spec (tool name, description, parameters) and receives a buildable Go module with:
- Annotated input struct with proper tags
- `Describe()` using `schema.MustSchema()`
- Stub `Execute()` ready for implementation
- `go.mod` with the correct replace directive

After building and installing the scaffold output, call `reload_plugins` to discover the new tool within the same session.

### Language Agnosticism

Because the interface is gRPC, plugins can be written in **any language** that supports gRPC (Python, Rust, Java, TypeScript, etc.). They need to:

1. Implement the `ToolPlugin` service from `proto/cuttlebone.proto`
2. Use the go-plugin handshake protocol (magic cookie: `CUTTLEBONE_PLUGIN=codecuttle-v1`)
3. Serve on the negotiated Unix socket

For non-Go plugins, implement the handshake protocol directly. See [HashiCorp go-plugin docs](https://github.com/hashicorp/go-plugin) for cross-language examples.

## Proto-Defined Inputs (Cross-Language Path)

For plugins where **multiple languages share the same input definition**, or where you want a formal API contract, you can define your tool input as a protobuf message and derive the JSON Schema from it.

### Defining the Input Proto

```protobuf
// plugins/cuttlebone-my-tool/input.proto
syntax = "proto3";
package mytool.v1;
option go_package = "cuttlebone-my-tool/inputpb;inputpb";

message MyToolInput {
  // The search query to execute
  string query = 1;
  // Maximum number of results to return
  int32 max_results = 2;
  // Output format
  OutputFormat format = 3;
}

enum OutputFormat {
  OUTPUT_FORMAT_UNSPECIFIED = 0;
  OUTPUT_FORMAT_JSON = 1;
  OUTPUT_FORMAT_TABLE = 2;
  OUTPUT_FORMAT_CSV = 3;
}
```

### Using Proto Schema in Go

```go
package main

import (
    "context"
    "encoding/json"

    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit/schema"
    "google.golang.org/protobuf/encoding/protojson"

    inputpb "cuttlebone-my-tool/inputpb"
)

type myTool struct{}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    msg := &inputpb.MyToolInput{}
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "Search with structured input",
        InputSchema: schema.MustProtoSchemaWithOptions(
            msg.ProtoReflect().Descriptor(),
            schema.ProtoSchemaOptions{
                Required: []string{"query"},
                Descriptions: map[string]string{
                    "query":      "The search query to execute",
                    "maxResults": "Maximum number of results (default 10)",
                    "format":     "Output format: OUTPUT_FORMAT_JSON, OUTPUT_FORMAT_TABLE, or OUTPUT_FORMAT_CSV",
                },
            },
        ),
        Version: "1.0.0",
    }, nil
}

func (t *myTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
    var input inputpb.MyToolInput
    // Use protojson for lenient parsing (handles LLM quirks for int64, enums, etc.)
    if err := protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal([]byte(req.Input), &input); err != nil {
        return &pb.ExecuteResponse{IsError: true, ErrorMessage: err.Error()}, nil
    }

    // Use typed fields directly
    _ = input.Query
    _ = input.MaxResults
    _ = input.Format
    return &pb.ExecuteResponse{Output: "result"}, nil
}
```

### Key Differences from Go Struct Path

| Aspect | Go Struct Path | Proto Path |
|--------|---------------|-----------|
| Input definition | Go struct with tags | `.proto` message |
| Schema derivation | `schema.MustSchema(&s{})` | `schema.MustProtoSchema(md)` |
| JSON parsing | `json.Unmarshal` | `protojson.Unmarshal` |
| Cross-language | Go only | Any language with protoc |
| Field naming | Explicit via `json` tag | camelCase (proto default) or snake_case |
| Required fields | Via `jsonschema:"required"` tag | Via `ProtoSchemaOptions.Required` |
| Descriptions | Via `jsonschema_description` tag | Via `ProtoSchemaOptions.Descriptions` |
| Enums | Via `jsonschema:"enum=..."` tag | Auto-derived from proto enum values |
| LLM flexibility | `FlexInt` handles string/int | `protojson` handles int64-as-string natively |

### When to Use Proto Path

- Multiple languages implement the same tool (Python + Go + Rust)
- Formal API contract needed (internal systems, shared tooling)
- Existing `.proto` definitions you want to expose as tool inputs
- Enums with many values (auto-derived from proto enum definition)

### When to Use Go Struct Path

- Go-only plugins (simpler, fewer moving parts)
- Rapid iteration (no codegen step)
- `FlexInt`/`FlexBool` LLM tolerance needed (proto path is stricter)
- Plugin being scaffolded by `scaffold_plugin` (generates Go structs)

## Packages for Plugin Authors

| Package | Purpose |
|---------|---------|
| `internal/pluginkit` | `Serve()`, `EmbedSkill()`, `NewSkill()` |
| `internal/pluginkit/schema` | `MustSchema()`, `FromStruct()`, `MustProtoSchema()`, `FromProtoDescriptor()`, `Validate()` |
| `internal/pluginkit/types` | `FlexInt`, `FlexBool` (LLM-tolerant types) |
| `internal/cuttlebone/v1` | Generated protobuf types (`DescribeResponse`, etc.) |
