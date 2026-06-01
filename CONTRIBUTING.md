# Contributing to codecuttlectl

## Development Setup

```bash
# Prerequisites
# - Go 1.25+ (for grpc compatibility)
# - protoc (Protocol Buffers compiler)
# - protoc-gen-go, protoc-gen-go-grpc
# - tmux (for E2E tests)
# - Python 3 with pexpect (for E2E tests)

# Build everything
make all

# Run tests
make test                    # Unit tests
python3 scripts/tui-test.py  # E2E TUI tests
```

## Commit Style

Use conventional commits: `type(scope): summary`

Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`

Scopes: `tui`, `bedrock`, `plugin`, `proto`, `prompt`, `todo`

Examples:
- `feat(plugin): add git-diff plugin`
- `fix(tui): handle terminal resize during streaming`
- `docs: add plugin authoring guide`

## Writing Plugins

Plugins are standalone binaries that communicate via gRPC. See `docs/writing-plugins.md`.

Minimal plugin:

```go
package main

import (
    "context"
    pb "github.com/codecuttle/codecuttlectl/internal/cuttlebone/v1"
    "github.com/codecuttle/codecuttlectl/internal/pluginkit"
)

type myTool struct{}

func (t *myTool) Describe(ctx context.Context) (*pb.DescribeResponse, error) {
    return &pb.DescribeResponse{
        Name:        "my_tool",
        Description: "Does something",
        InputSchema: `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`,
        Version:     "1.0.0",
    }, nil
}

func (t *myTool) Execute(ctx context.Context, req *pb.ExecuteRequest) (*pb.ExecuteResponse, error) {
    return &pb.ExecuteResponse{Output: "done"}, nil
}

func main() { pluginkit.Serve(&myTool{}) }
```

Name your binary `cuttlebone-<name>` and place it in the plugins directory.

## Code Style

- Keep functions focused; extract helpers only when reused
- Avoid `try`/`catch` patterns — handle errors explicitly
- Use `context.Context` for cancellation propagation
- Pointer receivers for mutation, value receivers for read-only
- `strings.Builder` must be a pointer in Bubble Tea models (value types get copied)

## Testing

- Unit tests: `go test ./...`
- E2E tests: `python3 scripts/tui-test.py --verbose`
- Update golden files: `python3 scripts/tui-test.py --update-golden`
- Run from repo root (not package directories)

## Proto Changes

After editing `proto/cuttlebone.proto`:

```bash
make proto
```

This regenerates `internal/cuttlebone/v1/*.pb.go`.
