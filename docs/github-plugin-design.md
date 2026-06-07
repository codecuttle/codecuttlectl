# GitHub Plugin Design Notes

## Scope

The `cuttlebone-github` plugin wraps the GitHub REST API to enable the agent
to manage repositories, pull requests, issues, and releases. It uses a
`GITHUB_TOKEN` environment variable for authentication (fine-grained PAT
recommended).

## Priority Operations (MVP)

These are the operations most useful for an autonomous coding agent:

### Pull Requests
- `list` — List PRs (filterable by state, head, base)
- `create` — Create a PR (title, head, base, body, draft)
- `get` — Get PR details (including mergeable status)
- `merge` — Merge a PR (merge/squash/rebase)
- `close` — Close a PR
- `comment` — Add a comment to a PR

### Issues
- `list` — List issues (filterable by state, labels, assignee)
- `create` — Create an issue (title, body, labels, assignees)
- `get` — Get issue details
- `comment` — Add a comment to an issue
- `close` — Close an issue

### Repository
- `get` — Get repo info (description, default branch, visibility)
- `list_branches` — List branches
- `create_release` — Create a release/tag

### Generic
- `api` — Raw API call (method, path, body) for anything not covered above

## Authentication

Single env var: `GITHUB_TOKEN`

Supports:
- Fine-grained PATs (recommended)
- Classic PATs
- GitHub App installation tokens

## Input Design

Single tool name: `github`

Uses a `command` field to dispatch (similar to how `git` uses `subcommand`):

```json
{
  "command": "pr_create",
  "owner": "codecuttle",
  "repo": "codecuttlectl",
  "title": "Add web search plugin",
  "head": "feat-web-search-plugin",
  "base": "main",
  "body": "..."
}
```

All fields beyond `command` are command-specific. The schema uses a
permissive object with descriptions per command in the LLM hint.

## API Base

`https://api.github.com` with:
- `Authorization: Bearer <token>`
- `Accept: application/vnd.github+json`
- `X-GitHub-Api-Version: 2022-11-28`

## What requires UI (cannot be done via API)

- Creating the org itself (already done)
- Enabling/disabling GitHub Apps at the org level
- Two-factor authentication settings
- Billing plan changes
- OAuth app authorization
- Transfer repository ownership (needs admin confirmation)

## What CAN be done via API with appropriate token permissions

- Branch protection rules
- Repository settings (description, topics, visibility, features)
- Webhooks
- Actions secrets and variables
- Team management
- Collaborator management
- Default branch changes
- Release management
