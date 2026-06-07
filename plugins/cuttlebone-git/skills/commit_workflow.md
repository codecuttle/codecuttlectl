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

```
[Feat] / [Fix] / [Docs] — Short description
```

Examples:
- `[Feat] Add web search and URL fetch plugins via Exa MCP`
- `[Fix] TUI text overflow — wrap content to terminal width`
- `[Feat] TUI QoL: multi-line input, scroll/select toggle, esc interrupt`

### Body

Descriptive but laconic. Cover:
- **What** was done (1-2 sentence summary)
- **Key changes** (bullet points for each major item)
- **Numbers** when relevant: tool count, test count, lines changed
- **Breaking changes** if any

Don't repeat what the diff shows. Focus on the why and the what-at-a-glance.

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
