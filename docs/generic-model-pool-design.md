> Status: **Superseded.** This design has been replaced by [Swarm Morphologies](swarm-morphologies.md).

# Generic Model Pool Design

## Overview

The Generic Model Pool extends the original multi-model design to be provider-agnostic. It allows `codecuttlectl` to route different tasks to the most appropriate model, regardless of whether that model is hosted on AWS Bedrock or running locally via Ollama. 

This enables hybrid configurations—for example, using **Opus 4.6 (Bedrock)** for high-reasoning agent loops while offloading summarization and titling to **Gemma 4 (Ollama)** for zero cost and low latency.

## Motivation: The Hybrid Advantage

| Task | Bedrock Option | Ollama Option | Strategic Goal |
| :--- | :--- | :--- | :--- |
| **Agent Loop** | Opus 4.6 / 4.8 | Gemma 4 / Llama 3 (70B) | Maximum intelligence $\rightarrow$ Max reliability |
| **Planning** | Sonnet 4.6 | Gemma 4 / Mistral | Mid-tier reasoning for structured plans |
| **Auxiliary** | Haiku 4.5 | Gemma 4 / Phi-3 | Zero cost, fast turnaround (summaries, titles) |

By allowing a mix of providers, we optimize for:
1. **Cost**: Offloading trivial tasks to local models (`$0/MTok`).
2. **Latency**: Local models avoid network roundtrips for simple classification/titling.
3. **Capability**: Using frontier cloud models when complex reasoning is required.

## Architecture

### 1. Model Roles (Global)

A `ModelRole` defines the *purpose* of a model within a session, independent of its provider.

```go
type ModelRole string

const (
    RolePrimary   ModelRole = "primary"   // Main agent conversation/reasoning
    RoleAuxiliary ModelRole = "auxiliary" // Cheap background tasks (summaries, titles)
    RolePlanning  ModelRole = "planning"  // Mid-tier reasoning for planning/decomposition
)
```

### 2. The Provider Pool (`ProviderPool`)

Instead of a Bedrock-specific pool, the `ProviderPool` manages a set of configurations mapping roles to specific provider implementations.

```go
type ModelAssignment struct {
    Role       ModelRole
    ProviderID string // "bedrock" or "ollama"
    ModelID    string // e.g., "us.anthropic.claude-opus-4-6-v1" or "gemma4:31b"
}

type ProviderPool struct {
    mu       sync.RWMutex
    configs  map[ModelRole]ModelAssignment
    providers map[string]Provider // Map of provider ID to initialized provider client
}
```

### 3. Generic Model Registry

The registry now tracks both cloud and local models, providing metadata for cost estimation and capability matching.

```go
type ModelInfo struct {
    ID             string
    Provider       string  // "bedrock" | "ollama"
    ContextWindow  int32
    InputCost      float64 // $/MTok (0 for local)
    OutputCost     float64 // $/MTok (0 for local)
    SupportsTools  bool
}

var GlobalRegistry = map[string]ModelInfo{
    "opus-4-6": {
        ID: "us.anthropic.claude-opus-4-6-v1",
        Provider: "bedrock",
        ContextWindow: 1_000_000,
        InputCost: 5.00, OutputCost: 25.00, SupportsTools: true,
    },
    "gemma-4": {
        ID: "gemma4:31b",
        Provider: "ollama",
        ContextWindow: 128_000,
        InputCost: 0.00, OutputCost: 0.00, SupportsTools: true,
    },
    "haiku-4-5": {
        ID: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
        Provider: "bedrock",
        ContextWindow: 200_000,
        InputCost: 1.00, OutputCost: 5.00, SupportsTools: true,
    },
}
```

## Hybrid Routing Logic

The `ProviderPool` provides a method to execute a call for a specific role, resolving the provider and model at runtime.

### Example Flow: `GetClient(role ModelRole)`
1. Look up the `ModelAssignment` for the requested `role`.
2. Retrieve the corresponding `Provider` from the internal map (e.g., the Ollama client).
3. Return a wrapper that handles the specific model ID for that provider.

### Default Configuration Strategies

The system can offer several "Profiles" via CLI flags:

| Profile | Primary | Planning | Auxiliary | Strategy |
| :--- | :--- | :--- | :--- | :--- |
| **Cloud-Optimized** | Bedrock (Opus) | Bedrock (Sonnet) | Bedrock (Haiku) | Low latency, stable cloud env |
| **Local-First** | Ollama (Gemma4) | Ollama (Gemma4) | Ollama (Gemma4) | Zero cost, private |
| **Hybrid (Recommended)** | Bedrock (Opus) | Bedrock (Sonnet) | Ollama (Gemma4) | High IQ main loop, $0 auxiliaries |

## Implementation Plan

### Phase 1: Core Abstraction
- Define `ModelRole`, `ModelAssignment`, and the `ProviderPool` struct in `internal/conversation`.
- Create a registry that includes both Bedrock and Ollama model metadata.
- Implement `GetClient(role)` logic to route requests between providers.

### Phase 2: CLI & Config Integration
- Add `--pool-profile` flag (e.g., `cloud`, `local`, `hybrid`).
- Update `main.go` to initialize the pool and inject it into the Agent and TUI.
- Allow explicit overrides via `--primary-model`, `--auxiliary-model`.

### Phase 3: Tooling & Integration
- **Title Generation**: Update agent to use `RoleAuxiliary`.
- **LLM Compaction**: Implement `Summarizer` using `RoleAuxiliary`.
- **TUI Status Bar**: Display current pool composition (e.g., `Primary: Opus | Aux: Gemma4`).

### Phase 4: Observability & Cost
- Update session stats to aggregate costs across different providers.
- Local models count as $0 but can track "Compute Units" or simply tokens for usage analytics.

## Error Handling & Fallback

If a specific role's provider fails (e.g., Ollama process is down), the system falls back in this order:
1. **Role-Specific Fallback**: If Auxiliary fails, try Planning $\rightarrow$ Primary.
2. **Provider Fallback**: If Local fails, attempt to use Cloud equivalent for that role if configured.

Example: `Auxiliary (Ollama) -> Fail -> Auxiliary (Bedrock/Haiku) -> Pass`.
