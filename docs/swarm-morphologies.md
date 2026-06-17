# Swarm Morphologies (Morphs)

## Overview

As `codecuttlectl` evolves beyond a single-model coding assistant, we are introducing **Swarm Morphologies** (or **Morphs**). A Morphology is a declarative configuration that defines a multi-agent system, detailing the models, providers, system prompts, skills, and the topologies through which they interact.

This replaces the hardcoded `Primary / Planning / Auxiliary` Bedrock-only Model Pool with a generic, extensible, provider-agnostic framework supporting **Bedrock, Google, and Ollama** simultaneously.

## Motivation

Modern agentic workflows require specialization. A single frontier model (like Claude Opus or Gemini Pro) is often too expensive or slow for every sub-task, while a smaller model (like Haiku or local Llama 3) might lack the reasoning required for complex architectural decisions.

By defining a **Morphology**, users can orchestrate a swarm where:
1. A local `Ollama` model handles quick syntax checks or code formatting.
2. A fast `Google` model (Gemini Flash) drafts execution plans or performs deep web searches.
3. A frontier `Bedrock` model (Claude Opus) synthesizes the final output and performs delicate file edits.

## Core Concepts

### 1. Nodes (Agents) & Strict Tool Sandboxing
A swarm is composed of multiple discrete agents, referred to as **Nodes**. Each node is configured with:
*   **Provider:** `bedrock`, `google`, or `ollama`.
*   **Model ID:** The specific model identifier for that provider.
*   **System Prompt:** A tailored prompt giving the node its persona and instructions.
*   **Workbench (Skills):** A strictly scoped execution sandbox containing only the tools the node is explicitly allowed to use. For example, a "reviewer" node might only have read-only tools like `read_file` and `git diff`. This prevents unauthorized cross-contamination or hallucinated destructive actions by generalist models.

### 2. Presentation Modes & Epistemic Transparency
A morphology can present itself to the user in different ways to solve the "trust calibration problem":
*   **Single Agent (Hidden Committee):** The user interacts with the system as if it were a single entity. The primary node coordinates with planners and reviewers silently. *Crucially*, to avoid the "Aggregation Paradox" (where majority voting destroys correct intermediate logic), the orchestrator is fed the **full reasoning traces** of the subordinate nodes, performing **Trace-Level Synthesis** rather than a simple vote.
*   **Transparent Swarm:** The TUI actively displays the swarm dynamics in real-time.
*   **Progressive Disclosure (Default):** A hybrid approach. The TUI streams the primary agent's consensus by default but provides interactive toggles (similar to our current `<thinking>` blocks) to expand and inspect specific agent critiques, chronological thought paths, or dissenting opinions on demand.

### 3. Topologies & Dynamic Handoffs
Instead of brittle, static sequential pipelines, `codecuttlectl` utilizes **Explicit Handoffs** (inspired by the OpenAI Agents SDK and LangGraph's Command object).
*   A node has conversational authority until it yields control.
*   A node can invoke a native `handoff` tool to dynamically transfer execution (and the full conversation state) to another specialized node based on real-time needs.

### 4. Resilience & Fallbacks
Multi-agent swarms face compounding probabilities of failure. Morphologies encode graceful degradation:
*   **Hierarchical Fallbacks:** If the primary provider (e.g., Anthropic) hits rate limits or 5xx errors, the node dynamically falls back to an alternative provider (e.g., Google).
*   **Intelligent Circuit Breakers:** If an external tool (e.g., the GitHub API) repeatedly times out, a circuit breaker trips to the `OPEN` state, preventing retry storms and immediately forcing the agent to attempt an alternative reasoning path or escalate to a Human-in-the-Loop (HITL) pause.
*   **Timeout & Heartbeat Policies:** Agents that stall without emitting streamed tokens or "heartbeat" progress signals are aggressively halted and retried or escalated.

## Morphology Configuration (YAML)

Morphologies are defined in a standardized declarative YAML format. This eliminates configuration drift across repositories and allows users to share topologies easily.

```yaml
name: "senior-dev-committee"
version: "1.0.0"
description: "A fast, cheap planner combined with a powerful executor."
presentation: "progressive_disclosure"

# Define the agents in the swarm
nodes:
  orchestrator:
    provider: "bedrock"
    model: "us.anthropic.claude-opus-4-6-v1"
    system_prompt: "You are the lead developer. You synthesize plans from the planner and execute them."
    workbench: ["*"] # Has access to all tools
    is_primary: true
    fallbacks:
      - provider: "google"
        model: "gemini-3.1-pro"
    
  planner:
    provider: "google"
    model: "gemini-3.1-pro"
    system_prompt: "You are a software architect. Draft a step-by-step plan for the user's request."
    workbench: ["read_file", "list_directory", "glob", "grep"] # Strictly read-only sandbox
    
  reviewer:
    provider: "ollama"
    model: "qwen2.5-coder:32b"
    system_prompt: "Review code diffs and suggest optimizations."
    workbench: ["git"]

# Define routing rules and allowed handoffs
topology:
  type: "handoff"
  rules:
    orchestrator: ["planner", "reviewer"]
    planner: ["orchestrator"]
    reviewer: ["orchestrator"]
```

## Migration from PR #25 (`ModelPool`)

PR #25 introduced the concept of multi-model routing via a Bedrock-specific `ModelPool` (`Primary`, `Auxiliary`, `Planning`).

### The Transition Plan:

1. **Generalize the Pool Interface (`internal/provider/pool.go`):**
   * Deprecate the Bedrock-specific pool.
   * Create a new `swarm.Morphology` that parses the YAML schema and stores a mapping of `NodeID -> provider.Provider`.
   * Implement the `handoff` context passing mechanism.

2. **Strict Workbench Loading (`internal/pluginhost`):**
   * Update the plugin manager to instantiate isolated tool registries per node, rather than a global tool array injected into every system prompt.

3. **CLI Updates:**
   * Introduce a `--morph <path.yaml>` flag. 
   * When `--morph` is used, the system overrides standard `--model` and `--provider` flags, initializing the full swarm defined in the file.
   * *Note: The existing `--aux-model` and `--plan-model` flags are considered transitional convenience flags and will be superseded by morphology files.*

4. **Agent Orchestration (`internal/conversation/agent.go`):**
   * The `Agent` struct will be updated to handle the active `Morphology`.
   * It will support executing a `handoff` tool, suspending the current active node, and resuming the loop with the target node's provider and system prompt.

## Future Enhancements
* **The SAGA Pattern:** Allowing agents to revert orphaned states (e.g., executing idempotent compensating tools) if a multi-step workflow fails catastrophically midway through.
* **Morphology Registry:** A central repository where users can download community-created morphologies via a command like `codecuttlectl morph install react-specialist`.
