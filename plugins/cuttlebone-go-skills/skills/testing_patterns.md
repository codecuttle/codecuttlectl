# Go Testing Patterns

## Writing Tests

- Test files: `*_test.go` in the same package
- Test functions: `func TestXxx(t *testing.T)`
- Table-driven tests are idiomatic Go:

```go
tests := []struct {
    name     string
    input    string
    expected string
}{
    {"empty", "", ""},
    {"basic", "hello", "HELLO"},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got := Transform(tt.input)
        if got != tt.expected {
            t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.expected)
        }
    })
}
```

## Running Tests

- `go test ./...` — run all tests
- `go test -v ./pkg/` — verbose, specific package
- `go test -run TestSpecific ./...` — run matching tests only
- `go test -race ./...` — enable race detector
- `go test -count=1 ./...` — disable test caching

## Test Helpers

- `t.Helper()` — mark function as test helper (better error locations)
- `t.TempDir()` — auto-cleaned temp directory
- `t.Cleanup(func())` — register cleanup function
- `t.Parallel()` — run test in parallel

## Assertions

Go stdlib has no assert library. Use `if got != want { t.Errorf(...) }`.
For complex comparisons: `reflect.DeepEqual()` or `cmp.Diff()` from google/go-cmp.
