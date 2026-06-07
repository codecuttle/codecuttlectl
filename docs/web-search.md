# Web Search & Fetch Plugins

Two plugins provide web access capabilities:

## `websearch` — Web Search via Exa MCP

Searches the web using [Exa's](https://exa.ai) free MCP (Model Context Protocol) endpoint. Returns structured results with titles, URLs, and content highlights optimized for LLM consumption.

### How it works

The plugin sends a JSON-RPC request to `https://mcp.exa.ai/mcp`, which is Exa's public MCP endpoint. No API key is required for basic usage — the endpoint is free and returns high-quality search results with extracted page content.

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | ✅ | — | The search query |
| `num_results` | integer | — | 8 | Number of results (max 20) |
| `type` | string | — | `"auto"` | `"auto"`, `"fast"`, or `"deep"` |

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `EXA_API_KEY` | No | Optional. Provides higher rate limits and priority access. |

### Example usage (from the agent)

```
websearch({"query": "go 1.25 generics constraints", "num_results": 5})
```

### Response format

Returns a text block with structured results:
```
Title: Example Page Title
URL: https://example.com/page
Published: 2026-01-15T...
Highlights:
Relevant content extracted from the page...

---

Title: Another Result
URL: https://another.com
...
```

---

## `webfetch` — URL Content Retrieval

Fetches a URL and converts the content to readable text or markdown. Handles HTML pages by stripping non-content elements (scripts, styles, navigation) and preserving meaningful structure.

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `url` | string | ✅ | — | Full URL (must start with `http://` or `https://`) |
| `format` | string | — | `"text"` | `"text"`, `"markdown"`, or `"html"` |
| `max_size` | integer | — | 50000 | Max characters to return (max 200000) |

### Format options

- **`text`** — Strips all HTML, extracts readable text. Best for general reading.
- **`markdown`** — Converts HTML to Markdown, preserving headings, code blocks, lists, bold/italic.
- **`html`** — Returns raw HTML. Use only when you need the actual markup.

### Limits

- Maximum raw response: 5MB
- Maximum output: 200,000 characters
- Request timeout: 30 seconds
- Maximum redirects: 5

### Example usage

```
webfetch({"url": "https://pkg.go.dev/golang.org/x/net/html", "format": "markdown"})
```

---

## Architecture

```
┌─────────────┐     JSON-RPC/POST      ┌──────────────┐
│  websearch  │ ───────────────────────▶│ mcp.exa.ai   │
│  (plugin)   │ ◀──────────────────────│ (free MCP)   │
└─────────────┘     Search results      └──────────────┘

┌─────────────┐     HTTP GET            ┌──────────────┐
│  webfetch   │ ───────────────────────▶│  Any URL     │
│  (plugin)   │ ◀──────────────────────│              │
└─────────────┘     HTML → text/md      └──────────────┘
```

Both plugins run as isolated gRPC subprocesses. They use Go's standard `net/http` client — no external dependencies beyond the standard library and the pluginkit framework.

## When to use which

| Scenario | Tool |
|----------|------|
| "What's the latest version of X?" | `websearch` |
| "Find docs for Y" | `websearch` → then `webfetch` on the best URL |
| "Read this GitHub README" | `webfetch` |
| "What are the arguments to Z function?" | `websearch` (often enough) or `webfetch` if you have the doc URL |
| User pastes a URL | `webfetch` |
