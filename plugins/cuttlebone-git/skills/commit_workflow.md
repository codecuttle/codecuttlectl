# Git Commit & PR Workflow

## Commit Messages

Write commits that are **short, descriptive, and laconic**. They should tell the narrative of the changes and provide context on development decisions.

### Format

```
<summary line: imperative, ≤72 chars>

<optional body: what and why, not how>
```

### Summary Line Rules

- Imperative mood: "Fix", "Add", "Remove", "Update" — not "Fixed" or "Fixes"
- No period at the end
- ≤72 characters (hard limit)
- Specific: "Fix viewport jumping during typing" not "Fix bug"
- If it touches one subsystem, name it: "pluginhost: kill orphaned process on re-register"

### Body Rules

- Wrap at 72 characters
- Explain **what** changed and **why**, not how (the diff shows how)
- Use bullet points with `-` for multiple related changes
- Reference evidence when available: frame counts, error messages, test results
- Separate from summary with a blank line

### Examples

```
Add websearch plugin with Exa MCP integration

Implements cuttlebone-web-search plugin exposing a `websearch` tool that
queries Exa's free MCP endpoint (https://mcp.exa.ai/mcp) via JSON-RPC.
No API key required for basic usage; EXA_API_KEY env var enables higher
rate limits.

Handles both direct JSON and SSE response formats from the MCP endpoint.
Includes a web_research_workflow skill for contextual guidance.
```

```
Fix viewport jumping during typing: stop passing keys to viewport

Root cause identified from video frame analysis (329 frames @ 10fps):
The viewport's Update() was receiving ALL events including every keystroke.
The viewport component internally handles key events (pgup/pgdn/arrows)
and was interfering with scroll position on each keypress.

Fix: Only pass MouseMsg and WindowSizeMsg to the viewport. All scroll
positioning is now controlled explicitly by our code (GotoBottom on new
content, ScrollDown on textarea grow) rather than the viewport reacting
to key events it shouldn't be handling.
```

```
Fix FlexBool schema validation rejecting valid casings; remove dead code
```

```
Update README for github plugin (12 plugins, 17 tools)
```

### Anti-patterns

- ❌ "WIP" / "wip" / "work in progress"
- ❌ "misc fixes" / "various changes" / "updates"
- ❌ "fix bug" (which bug?)
- ❌ "refactor" (refactor what? why?)
- ❌ Commit messages longer than the diff itself
- ❌ Squashing everything into one mega-commit with no narrative

## Branch Strategy

- Feature branches: `feat-<description>` (e.g., `feat-web-search-plugin`)
- Fix branches: `fix-<description>` (e.g., `fix-tui-text-overflow`)
- Branches tell a story via commit sequence — each commit should be a logical step
- Commits should compile individually (no "fix typo from last commit" unless genuinely needed)

## Pull Requests

### Title Format

Use conventional commit style matching the commit summary:
- `fix: description` — bug fixes
- `feat: description` — new features
- `test: description` — test additions/updates
- `docs: description` — documentation only

Examples:
- `fix: comprehensive bedrock prompt caching optimizations`
- `feat: auto-nudge small models that stop to ask permission`
- `test: update cache tests to match no-advance-mid-turn strategy`

### Body Structure

Follow this exact structure (use all sections that apply):

```markdown
## Problem

1-3 sentences describing what's broken or missing.

## Root Cause (if applicable)

Numbered list of contributing factors. Use when the fix addresses
multiple interacting issues.

## Fix

### `path/to/file.go`
- **Change description** — brief explanation

### `path/to/other_file.go`
- **Change description** — brief explanation

## Testing

\```
$ go build ./...  # Clean
$ go test ./...   # All pass
\```

Additional test coverage notes if new tests were added.

## Files Changed (optional, for larger PRs)

| File | Change |
|------|--------|
| `path/to/file.go` | Brief description |
```

### Body Rules

- Use real markdown — never pass literal `\n` escape sequences
- Em-dashes (—) for inline descriptions, not hyphens
- Code references in backticks: `functionName()`, `path/to/file.go`
- Tables for file change summaries on PRs touching 4+ files
- Keep it laconic — don't repeat what the diff shows
- Always include a Testing section with build/test commands

### CRITICAL: Markdown Formatting

When creating PR bodies programmatically (via API), the body MUST be
valid markdown with actual newline characters. Never use escaped `\n`
literals in the body string — they render as visible `\n` text on GitHub
instead of line breaks. Always use real newlines in the body content.

## Workflow

1. Create a branch from `main`
2. Commit as you go — each commit is a logical step in the narrative
3. Push and open a PR when ready for review
4. PR body summarizes the full branch's changes
5. Merge to main (squash or merge commit depending on narrative value)

## When to Commit

- After a feature/subfeature compiles and tests pass
- After a bug fix is verified
- After documentation is written for a completed piece
- After refactoring that changes behavior or structure
- NOT after every file save
- NOT with uncommitted work from other features mixed in
