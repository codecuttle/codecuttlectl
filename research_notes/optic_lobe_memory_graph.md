# Research Notes: Optic Lobe Memory & Knowledge Graphs

## Current State of codecuttlectl Memory
The `codecuttlectl` system architecture currently includes plans for the **Optic Lobe**, intended to be the cross-session semantic memory and multi-dimensional visual processing center for the agent swarm.

**Key Architecture Details:**
- **Stack:** PostgreSQL + pgvector + Apache AGE (a graph database extension).
- **Current implementation:** Not yet implemented (planned future work). Currently, memory is limited to the session store (`~/.local/share/codecuttlectl/sessions/`), in-memory Inkwell, and Context Compaction (Phase 2).
- **Phase 3 Compaction Plan:** When context is compacted, the full content will be stored in the Optic Lobe with a reference ID. The model can use a `recall` tool to retrieve semantic context from previous sessions.

## Immediate User Goal
The user wants to accelerate the "memory" implementation by focusing on **knowledge graphs** that capture the full git history and narrative evolution of a codebase. The objective is to give the agent the ability to "peer into a section of code base and understand the full timeline and narrative of where it came from and how it evolved over time."

They envision tiered granularities:
- Per Workspace
- Per Repo
- Per User

## Gemini Deep Research Prompts
To explore options for implementing this temporal knowledge graph on the `PostgreSQL + pgvector + AGE` stack, I have drafted the following 3 Deep Research prompts for the user to run:

1. **Graph Structuring for Git Histories:**
   > "Research state-of-the-art approaches for ingesting and structuring full Git histories into a graph database (specifically Apache AGE or similar openCypher systems). How are commits, file diffs, semantic code blocks (functions/classes), and developer intents typically modeled as nodes and edges to allow an autonomous agent to query the 'evolutionary narrative' of a specific section of code? Please include examples of effective schema designs."

2. **Temporal Code Provenance & Vector Search:**
   > "Investigate hybrid architectures that combine semantic vector search (like pgvector) with graph traversal (like Apache AGE) to query temporal code provenance. When an AI agent needs to understand *why* a specific function evolved a certain way over time, what are the most effective retrieval patterns to avoid overwhelming the LLM context window with raw git logs? Provide case studies or design patterns for 'tiered context retrieval' in autonomous systems."

3. **Multi-level Hierarchical Memory for Agents:**
   > "Explore design patterns for multi-level hierarchical memory in autonomous coding agents. Specifically, how can a persistent knowledge graph be designed to isolate and query memory at different granularities: per-workspace, per-repository, and per-user? Focus on strategies for cross-referencing global agent learnings with repo-specific historical context."

## Instructions for `fable` Node
Please review these notes on the current `codecuttlectl` architecture and the proposed deep research prompts. Consider the technical feasibility of using Apache AGE + pgvector to achieve this temporal knowledge graph feature, and formalize any additional design insights. Once you're done, handoff back to `orchestrator`.
