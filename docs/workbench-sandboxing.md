# Workbench Sandboxing & Distributed Execution

As `codecuttlectl` evolves into a multi-agent framework via Swarm Morphologies, it becomes critical to restrict what individual agents (Nodes) can do. A "planner" should not be able to execute `bash_exec`, and a "reviewer" should only be able to read files and diffs.

This document outlines our strategy for **Workbench Sandboxing**, aligning with our "Modular Monolith" architecture. We want the system to be blazing fast on a single machine (developer laptop), but robustly scalable to microVMs and dedicated "Arm Nodes" in the cloud.

## The "Modular Monolith" Philosophy
A single `codecuttlectl` binary must be able to orchestrate a complex swarm entirely in-memory on one machine, coordinating dozens of node handoffs seamlessly. However, the system must be designed so that any node can easily be swapped out for a remote, highly-isolated service (e.g., swapping local memory for Postgres, or local execution for a Firecracker microVM) as scale and security demands increase.

---

## Phase 1: Logical Workbenches (Available Now)

For single-machine, local orchestration, copying binaries to ephemeral directories per node introduces unnecessary disk I/O and process-spawning overhead (especially during rapid agent handoffs). Instead, we use **Logical Sandboxing** within the single monolith process.

### How it Works:
1. **Global Tool Registry:** At startup, `pluginhost.Manager` discovers and loads all `cuttlebone-*` plugins in the main plugin directory. It creates a global registry of available tools.
2. **Node Initialization:** When a Swarm Morphology is parsed, each `Node` parses its `Workbench` array (e.g., `["read_file", "list_directory"]`).
3. **Prompt Filtering:** When the Agent transitions to a Node, the Prompt Manager filters the global tool registry. The LLM's system prompt is *only* injected with the schemas of the allowed tools.
4. **Execution Interception:** If an LLM hallucinates a tool call outside its Workbench (e.g., a planner trying to run `write_file`), the Agent intercepts the request before it reaches the `pluginhost`. It returns a hard error to the LLM: `Error: Tool 'write_file' is not authorized in this node's workbench. Allowed tools: [read_file, list_directory].`

**Pros:** Blazing fast handoffs, zero extra disk I/O, prevents accidental destructive actions by specialized models, keeps context windows small.

---

## Phase 2: Ephemeral Binary Sandboxing (Near Future)

For environments that require stricter local security (e.g., executing partially untrusted code on a local machine), we implement the ephemeral directory approach.

### How it Works:
1. When a node is initialized with `sandbox: strict`, `codecuttlectl` creates an ephemeral `/tmp/codecuttle-node-<id>` directory.
2. It aggressively copies or symlinks *only* the allowed `cuttlebone-*` binaries into this directory.
3. It initializes a dedicated `pluginhost.Manager` pointing strictly to that ephemeral directory.
4. (Optional) It wraps the plugin execution in OS-level constraints (e.g., `chroot`, `cgroups`, or restricted user namespaces) so the tool itself cannot break out of the filesystem boundaries.

**Pros:** Hard OS-level boundaries preventing a hijacked plugin from executing sibling binaries.

---

## Phase 3: Arm Nodes & Distributed Swarms (Long-Term Scale)

When `codecuttlectl` is deployed as a massive backend swarm (e.g., a CI/CD cloud brain), we transition to true distributed execution via **Arm Nodes**.

An "Arm Node" is a remote, dumb execution environment (a Docker container or microVM) running a headless `codecuttlectl` agent whose sole purpose is to safely execute bash commands and file writes, reporting back to the Orchestrator.

### How it Works:
1. The Morphology YAML specifies `execution_context: "remote://worker-pool-alpha"` for the Orchestrator node.
2. The primary `codecuttlectl` binary acts as the brain. It does not load plugins locally.
3. Instead of local RPC, tool calls (like `bash_exec`) are serialized over gRPC to the Arm Node.
4. The Arm Node container is physically built with only the specific binaries it needs. If the container is compromised by a malicious `bash_exec`, the attacker is trapped in a stateless microVM that is destroyed immediately after the session.

**Pros:** True zero-trust architecture, horizontally scalable execution, protects the primary orchestrator's state and memory from malicious user prompts.
