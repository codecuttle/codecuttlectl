# Codecuttle Memory Implementation Plan v1: The Optic Lobe

This document outlines the architectural blueprint for implementing the **Optic Lobe**, Codecuttle's persistent, cross-session memory system. Based on state-of-the-art research in multi-agent systems, hybrid vector-graph databases, and JIT AST parsing, the Optic Lobe transitions Codecuttle from stateless context windows to an evolving, git-bound organizational memory.

## 1. Core Database Substrate: Unified PostgreSQL 19

We will eliminate the "multi-database tax" (deploying separate vector, graph, and relational DBs) by consolidating entirely on **PostgreSQL 19**.

*   **Relational Backbone**: Core metadata, JSONB task specs, and basic foreign key constraints stored in standard PostgreSQL tables.
*   **Vector Engine**: `pgvector` for semantic search (using `HNSW` or `DiskANN` for high scale) to handle natural language queries against code chunks and commit intents.
*   **Graph Engine**: **`SQL/PGQ`** (Property Graph Queries), the new native graph standard in PG19. This replaces the need for Apache AGE or external graph databases. It allows us to define `VERTEX` and `EDGE` tables directly over our relational data, converting SQL into highly optimized graph traversals (e.g., finding the blast radius of a function).

## 2. Multi-Level Hierarchical Memory

Memory will not be a flat dump of past chats. It will be structured into a Three-Tier Graph, ensuring Codecuttle retrieves only the necessary context level:

1.  **Insight Graph (Global/Workspace Tier)**:
    *   *Nodes*: Global architectural rules, User preferences, Reusable tool configurations.
    *   *Edges*: `APPLIES_TO`, `SUPERSEDES`.
    *   *Usage*: Queried upward. When the agent touches a DB file, it retrieves global insights like "Always use connection pooling."
2.  **Query Graph (Repository/Task Tier)**:
    *   *Nodes*: Issues, PRs, specific Git branches.
    *   *Edges*: `BLOCKS`, `RESOLVES`, `ASSIGNED_TO`.
    *   *Usage*: Maintains state across the lifecycle of a specific feature or bug.
3.  **Interaction Graph (File/Local Tier)**:
    *   *Nodes*: Functions, Classes, specific Error outputs, condensed execution traces.
    *   *Edges*: `EVOLVED_FROM` (Temporal Code Provenance), `CALLS`, `CO_CHANGED_WITH`.
    *   *Usage*: Queried downward. Captures exact, localized details of how a specific function was modified and why.

## 3. Ingestion & Temporal Code Provenance

To track the "evolutionary narrative" of the codebase without flooding the LLM context with thousands of raw git diffs, we implement the **Temporal Code Property Graph (CPG)**.

*   **JIT AST Parsing**: Using `Tree-sitter`, we parse git commits on-the-fly to extract structural changes rather than line-based text diffs.
*   **Interval-Stamped Lifetimes**: Code nodes (e.g., `Function: initDB()`) will have `valid_from_commit` and `valid_to_commit` properties. This structurally eradicates "staleness." The agent can query the exact state of the graph at any point in history.
*   **The Historian Pattern (Map-Reduce Summaries)**: When the agent asks *why* a function changed, the system runs a PG19 SQL/PGQ query to trace the `EVOLVED_FROM` path. A deterministic middleware (or cheap fast model) synthesizes this into a dense narrative: *"Function `initDB` was introduced in commit A. It was modified in commit B by user X to fix a connection leak."* Only this summary enters the LLM context.

## 4. Multi-Granularity Isolation

In a multi-tenant or multi-project environment, preventing "semantic drift" (hallucinating python rules in a TS project) is critical:

*   **Workspace (Macro)**: **Declarative List Partitioning**. The `pgvector` HNSW indexes are physically partitioned by `workspace_id`. This prevents the graph from spilling to disk and ensures fast, isolated vector searches.
*   **Repository (Meso)**: **JSONB Metadata Filtering + Iterative Index Scans**. Uses composite keys inside the workspace partition to filter by `repo_id`, dynamically diving into the HNSW graph without sacrificing recall.
*   **User (Micro)**: **Row-Level Security (RLS)**. Private agent scratchpads and user preferences are isolated at the database engine level via RLS policies and `SECURITY DEFINER` functions, entirely preventing application-layer memory leaks.

## 5. Next Steps for Implementation

1.  **Infrastructure**: Upgrade/target PostgreSQL 19 + pgvector.
2.  **Cuttlebone Plugins**:
    *   Implement an `optic_ingest` plugin utilizing Tree-sitter to handle Git hook callbacks, generating the CPG.
    *   Implement an `optic_recall` tool that leverages HybridRAG (merging pgvector cosine distance `<=>` with SQL/PGQ graph paths via Reciprocal Rank Fusion).
3.  **Session Compaction**: Route current Phase 2 memory compaction summaries directly into the Interaction Graph.

## 6. Critical Constraints: Optionality & Governance

To ensure `codecuttlectl` serves both individual hobbyists and enterprise clients securely, the implementation MUST adhere to the following constraints:

### Constraint 1: Graceful Optionality
The PostgreSQL / Optic Lobe graph database must be **strictly optional**. 
* **The `OpticStore` Interface**: The Go codebase will implement an `OpticStore` interface with two implementations: `PostgresOpticStore` and `LocalOpticStore`. 
* **Fallback Behavior**: If the `--optic-lobe-uri` flag or `CODECUTTLECTL_OPTIC_LOBE` environment variable is not provided, the system gracefully degrades. It disables cross-session global graph queries and falls back to local JSON-based session tracking and basic Phase 2 in-memory context compaction. 
* **Zero-Config Setup**: A new user downloading the binary can run it immediately without setting up Docker or PostgreSQL. 

### Constraint 2: Auditing & Enterprise Governance
For corporate deployments, safety and observability are paramount. 
* **Database Level (`pgaudit`)**: The provided `docker-compose.yml` initializes the `pgaudit` extension. All deep multi-agent memory insertions, schema mutations, and `SECURITY DEFINER` (RLS-bypassing) queries will be systematically logged by the database engine for compliance reviews.
* **Application Level (Audit Hooks)**: A dedicated logging hook in the Orchestrator will write structured JSON audit events tracking every time the agent retrieves or modifies the knowledge graph. This supports both simple local files for hobbyists and log-forwarding (e.g., Datadog/Splunk) for enterprise governance.