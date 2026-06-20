# Providers

codecuttlectl supports multiple LLM providers through a unified `provider.Provider` interface. Any backend that implements streaming chat completions with tool calling can be integrated.

## AWS Bedrock (default)

The default provider. Uses Claude models via the AWS Bedrock ConverseStream API.

```bash
codecuttlectl --model us.anthropic.claude-opus-4-6-v1    # Explicit model ID
codecuttlectl                                             # Default: opus-4-6
codecuttlectl --region us-east-1                          # Override region
codecuttlectl --profile my-aws-profile                    # Named AWS profile
```

**Features exclusive to Bedrock:**
- 3-tier incremental extension caching (evaluated per-request)
- Cache keepalive pings (4-min TTL refresh)
- Extended thinking/reasoning mode (`--thinking`)

## Ollama (local models)

Run any local model via [Ollama](https://ollama.com). Zero cost, full privacy, no network dependency.

```bash
# Explicit provider flag
codecuttlectl --provider ollama --model gemma4:31b

# Auto-detect from model prefix
codecuttlectl --model ollama:gemma4:31b

# Remote Ollama server
codecuttlectl --provider ollama --model gemma4:31b --ollama-url http://192.168.1.50:11434

# Any Ollama model works
codecuttlectl --model ollama:llama3.3:70b
codecuttlectl --model ollama:qwen3:32b
codecuttlectl --model ollama:deepseek-coder-v2:16b
```

**How it works:**
- Uses Ollama's OpenAI-compatible `/v1/chat/completions` endpoint
- Supports streaming (SSE), tool calling, and reasoning/thinking
- Context window auto-discovered from Ollama's `/api/show` endpoint
- Token usage reported via `stream_options.include_usage`

**TUI differences for local models:**
- Status bar shows `Xk in Yk out N% ctx` (no cost, no cache stats)
- Context window % uses the model's actual limit (e.g., 262k for gemma4)
- Provider name shown in status bar instead of AWS region

### Supported models (tested)

| Model | Parameters | Context | Tool Calling | Notes |
|-------|-----------|---------|:------------:|-------|
| `gemma4:31b` | 31B | 262k | ✅ | Good balance of speed + capability |
| `llama3.3:70b` | 70B | 128k | ✅ | Strong reasoning, slower |
| `qwen3:32b` | 32B | 128k | ✅ | Fast tool calling |

Any model that supports Ollama's tool calling format should work. Models without tool support will generate text-only responses.

### Agentic enhancements for local models

Local models have smaller parameter counts and benefit from additional harness scaffolding:

1. **State Dictionary Injection** — After each tool call, the harness injects a compact ground-truth state snapshot into the context, preventing the model from hallucinating about what it has/hasn't done.

2. **Auto-Planning** — The harness extracts plans from the model's natural-language output (numbered lists, bullet points with action verbs) and automatically populates the task panel without requiring the model to call `todo_manage`.

3. **Read-Only Grounding** — System prompt explicitly instructs models not to implement features found in design docs or backlogs when performing exploratory tasks.

## Google AI (Gemini)

Use Google's Gemini models directly via the `google.golang.org/genai` SDK.

```bash
# Explicit provider flag
codecuttlectl --provider=google --model=gemini-2.5-pro

# Use Flash for speed
codecuttlectl --provider=google --model=gemini-2.5-flash

# Adjust context caching threshold
codecuttlectl --provider=google --model=gemini-2.5-pro --google-cache-threshold=16000

# List available models
codecuttlectl --provider=google --list-models
```

Requires the `GEMINI_API_KEY` environment variable. If not set, codecuttlectl will securely prompt for it interactively and optionally save it to the system keyring for future sessions (or you can use Application Default Credentials).

> **Note on Keyring Support (Linux):** If you opt to save the API key to your system keyring on Linux, `codecuttlectl` utilizes the Secret Service API via D-Bus. This requires a functioning D-Bus session and a secret service provider (e.g., `gnome-keyring` or `kwallet`). In headless environments or dev containers, you may see errors like `exec: "dbus-launch": executable file not found in $PATH` if `dbus-x11` or a secret service provider is not installed.
>
> To install the necessary requirements on common Linux distributions:
> 
> **Ubuntu / Debian:**
> ```bash
> sudo apt-get update && sudo apt-get install -y dbus-x11 gnome-keyring libsecret-1-0
> ```
> 
> **Fedora / RHEL:**
> ```bash
> sudo dnf install dbus-x11 gnome-keyring libsecret
> ```
> 
> **Arch Linux:**
> ```bash
> sudo pacman -S dbus gnome-keyring libsecret
> ```
> 
> *(macOS and Windows utilize their native secure credential stores out of the box and require no additional packages).*

**Features:**
- Full streaming with tool calling (`ConverseStream` via `iter.Seq2`)
- Context caching (automatic for system+tools > threshold, 5-min TTL with proactive refresh)
- Per-model pricing tiers (Gemini 2.5 Pro, 2.5 Flash, 2.0 Flash, 1.5 Pro/Flash, 3.x series)
- Cost estimation in status bar (includes cache read tokens)
- Model aliases for convenience (e.g., `gemini-3.1-flash` → `gemini-3.1-flash-lite`)
- Context window reporting (1M–2M tokens depending on model)
- Cache cleanup on graceful shutdown (prevents zombie billing)

**TUI integration:**
- Status bar shows cost, tokens, cache hit %, and ctx% via `CostEstimator` and `ContextWindowProvider` interfaces
- Context caching handled transparently — strips system/tools from requests when cache is active

### Context Caching

Google AI's Context Caching API caches the static prefix (system prompt + tool definitions) server-side:

- Automatically created when estimated token count exceeds `--google-cache-threshold` (default: 32k)
- 5-minute TTL with proactive refresh 1 minute before expiry
- Cache is deleted on graceful shutdown to avoid per-hour storage billing
- Falls back gracefully to uncached calls if cache creation fails

## Provider Interface

All providers implement the `provider.Provider` interface:

```go
type Provider interface {
    ID() string
    Name() string
    Converse(ctx context.Context, req Request) (*Response, error)
    ConverseStream(ctx context.Context, req Request) <-chan StreamEvent
}
```

Optional interfaces:
- `ContextWindowProvider` — reports context window size for accurate ctx% display
- `CostEstimator` — estimates dollar cost from token usage for the status bar

The provider abstraction lives in `internal/provider/`. Adding a new provider requires:
1. Implement `Provider` interface in `internal/provider/<name>/`
2. Wire it into `cmd/codecuttlectl/main.go` flag handling
3. Add provider name to the system prompt template conditional

## Adding future providers

Candidates for future integration:
- **Anthropic API** (direct, non-Bedrock)
- **OpenAI** (GPT-4o, o1)
- **vLLM** (self-hosted inference servers)
- **LM Studio** (OpenAI-compatible local servers)

All of these speak OpenAI-compatible chat completions, so the Ollama provider could be adapted with minimal changes (different base URL, auth headers).
