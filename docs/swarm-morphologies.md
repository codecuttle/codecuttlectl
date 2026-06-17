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

### 1. Nodes (Agents)
A swarm is composed of multiple discrete agents, referred to as **Nodes**. Each node is configured with:
*   **Provider:** `bedrock`, `google`, or `ollama`.
*   **Model ID:** The specific model identifier for that provider.
*   **System Prompt:** A tailored prompt giving the node its persona and instructions.
*   **Skills (Tools):** A restricted set of tools the node is allowed to use (e.g., a "reviewer" node might only have read-only tools like `read_file` and `git diff`).

### 2. Presentation Modes
A morphology can present itself to the user in two ways:
*   **Single Agent (Hidden Committee):** The user interacts with the system as if it were a single entity. Behind the scenes, the primary node coordinates with planners and reviewers, but the TUI only shows a unified output. This is ideal for most users who just want a smart assistant.
*   **Transparent Swarm:** The TUI actively displays the swarm dynamics. Users can see nodes conversing with each other, delegating tasks, and debating implementations. 

### 3. Topologies & Workflows
How do nodes interact? The morphology defines the routing rules. 
*   **Hierarchical / MoE:** A router node examines the user's intent and delegates to the appropriate specialist.
*   **Sequential Pipeline:** `Planner -> Executor -> Reviewer`.
*   **Autonomous Swarm:** Nodes are free to call upon one another using specific "delegate" tools.

## Morphology Configuration (YAML)

Morphologies will be defined in a human-readable format (YAML or JSON) so they can be easily versioned and shared by the community.

```yaml
name: "senior-dev-committee"
version: "1.0.0"
description: "A fast, cheap planner combined with a powerful executor."
presentation: "single_agent" # Options: single_agent, transparent_swarm

# Define the agents in the swarm
nodes:
  orchestrator:
    provider: "bedrock"
    model: "us.anthropic.claude-opus-4-6-v1"
    system_prompt: "You are the lead developer. You synthesize plans from the planner and execute them."
    skills: ["*"] # Has access to all skills
    is_primary: true
    
  planner:
    provider: "google"
    model: "gemini-3.1-pro"
    system_prompt: "You are a software architect. Draft a step-by-step plan for the user's request."
    skills: ["read_file", "list_directory", "glob", "grep"] # Read-only access
    
  reviewer:
    provider: "ollama"
    model: "qwen2.5-coder:32b"
    system_prompt: "Review code diffs and suggest optimizations."
    skills: ["git"]

# Define how tasks flow through the swarm
workflows:
  default_pipeline:
    trigger: "on_user_message"
    steps:
      - node: "planner"
        instruction: "Draft an execution plan based on the user's input."
      - node: "orchestrator"
        instruction: "Execute the plan provided by the planner. Delegate to reviewer if needed."
```

## Migration from PR #25 (`ModelPool`)

PR #25 introduced the concept of multi-model routing via a Bedrock-specific `ModelPool` (`Primary`, `Auxiliary`, `Planning`).

### The Transition Plan:

1. **Generalize the Pool Interface (`internal/provider/pool.go`):**
   * Deprecate the Bedrock-specific pool.
   * Create a new `swarm.Pool` that stores a mapping of `NodeID -> provider.Provider`.
   * This allows any node to be backed by Ollama, Google, or Bedrock seamlessly.

2. **Abstract the Provider Initialization:**
   * Move the provider instantiation logic out of `main.go` and into a factory function that can be called repeatedly for each node defined in a `Morphology` config file.

3. **CLI Updates:**
   * Introduce a `--morph <path.yaml>` flag. 
   * When `--morph` is used, the system overrides standard `--model` and `--provider` flags, initializing the full swarm defined in the file.

4. **Agent Orchestration (`internal/conversation/agent.go`):**
   * The `Agent` struct will be updated to hold a reference to the active `Morphology`.
   * Instead of hardcoded calls to `a.client` or `a.provider`, the agent will execute the defined `workflows` (e.g., querying the `planner` node before streaming the response from the `orchestrator` node).
   * For the "Hidden Committee" presentation mode, the agent will silently aggregate context from the background nodes before initiating the visible TUI stream.

## Future Enhancements
* **Dynamic Node Spawning:** Allowing the orchestrator to spin up transient nodes on-the-fly (e.g., "Spawn 5 test-runner agents to debug this issue").
* **Morphology Registry:** A central repository where users can download community-created morphologies via a command like `codecuttlectl morph install react-specialist`.
