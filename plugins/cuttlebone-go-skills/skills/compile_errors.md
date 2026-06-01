# Go Compile Error Resolution

When you encounter a Go compilation error, follow this systematic workflow:

## Step 1: Read the error carefully

Go compiler errors follow the format: `file.go:line:col: message`

Common patterns:
- `undefined: X` → missing import, typo in name, or function not exported
- `cannot use X as type Y` → type mismatch, check interfaces and conversions
- `imported and not used` → remove unused import or use the package
- `declared and not used` → remove unused variable or use it
- `cannot find package` → run `go get` or check import path

## Step 2: Identify the root cause

- Read the file at the indicated line
- Check 5 lines above and below for context
- If it's an import error, check go.mod for the dependency
- If it's a type error, trace the type definitions

## Step 3: Fix systematically

1. Fix ONE error at a time (later errors are often caused by earlier ones)
2. After each fix, recompile to check progress
3. If the same error persists after a fix, re-read the file — your edit may not have matched correctly

## Step 4: Verify

Always run `go build ./...` after fixes to confirm the error is resolved.
Do NOT assume a fix worked without verifying.

## Common Pitfalls

- Forgetting to add imports when using new packages
- Using the wrong package version (check go.mod)
- Mismatched function signatures when implementing interfaces
- Unexported types/functions (lowercase first letter) not visible across packages
