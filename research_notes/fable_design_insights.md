# Fable Node: Architectural Insights on Optic Lobe Memory & Knowledge Graphs

Reviewing the notes on the evolutionary knowledge graph, this aligns perfectly with the biological metaphor of the **Optic Lobe**: moving from reactive, static processing to multi-dimensional, temporal understanding.

Here are the formalized design approaches and potential pitfalls for building a temporal codebase graph using **PostgreSQL + pgvector + Apache AGE**.

## 1. Hybrid Schema Design (Graph + Vector)
To avoid overwhelming the system, we must treat PostgreSQL as the unified substrate, bridging relational, vector, and graph paradigms.

*   **Nodes (Vertices):**
    *   `Developer` (User level)
    *   `Commit`, `Branch`, `Tag` (Repo level)
    *   `File`, `Symbol` (Code level - e.g., structs, interfaces, exported functions)
    *   `Session`, `Task` (Agent level - links past orchestrator context to code)
*   **Edges (Relationships):**
    *   `AUTHORED` (Developer -> Commit)
    *   `INTRODUCED` / `MODIFIED` / `DELETED` (Commit -> Symbol/File)
    *   `EVOLVED_FROM` (Symbol_V2 -> Symbol_V1) — **The critical temporal edge**
    *   `REFERENCES` (Symbol -> Symbol) — Captures structural dependencies
*   **Vector Embeddings (pgvector):**
    *   Attach dense vector embeddings to `Commit` nodes (for commit messages/intent) and `Symbol` nodes (for code semantics).
    *   This allows a hybrid query: *1) Semantic search to find the relevant function in the graph, 2) Graph traversal backward via `EVOLVED_FROM` to gather the history.*

## 2. Granularity and Tiered Context
Implementing per-workspace, per-repo, and per-user memory requires strict boundary management.
*   **Apache AGE Graph Labels:** Create isolated graphs within the same Postgres database using AGE's `CREATE GRAPH` functionality. (e.g., `graph_repo_codecuttlectl`, `graph_user_alice`).
*   **Cross-Graph Traversal:** The agent's workspace graph can contain edge pointers to specific repo graphs, allowing global rules ("Always use this linter") to propagate down to specific repos.

## 3. "Peering" into the Codebase (The Historian Pattern)
Raw Git logs and diffs are poison to LLM context windows due to their density and token cost.
*   **The Historian Sub-agent:** Instead of the orchestrator querying the Optic Lobe directly, it delegates to a specialized background agent/tool (e.g., `trace_provenance`).
*   **Map-Reduce Narrative:** The tool runs Cypher queries to extract the chain of commits that modified a function. It then does a localized, fast map-reduce (using a cheaper model like Haiku or Gemini Flash) to summarize the diffs into a human-readable narrative.
*   **Output:** Instead of returning 5000 tokens of git diffs, it returns: *"Function `loadConfig` was introduced in Jan 2025 by Bob to support YAML. In Mar 2025, Alice refactored it to add concurrent loading (Commit a1b2c), which caused a race condition that was patched last week (Commit d4e5f)."*

## 4. Potential Pitfalls & Mitigations
*   **The AST Explosion:** Trying to map every variable and local scope into the graph will result in an unmanageable database size and massive ingestion latency.
    *   *Mitigation:* **Coarse-grained ingestion.** Only index files, exported functions, classes, and structs. Leave local implementation details as text attributes on the `Symbol` node, not as separate graph entities.
*   **Cold Start Ingestion Cost:** Scanning 10 years of Linux kernel history into a graph database will take hours.
    *   *Mitigation:* **Lazy or Shallow Ingestion.** By default, ingest only the last 30 days or the last 100 commits. Expand the graph dynamically when the agent encounters an unknown symbol. "JIT (Just-In-Time) History Loading."
*   **Graph/Vector DB Sync:** Apache AGE and pgvector operate in the same Postgres instance, but querying them simultaneously can be complex (AGE uses Cypher, pgvector uses SQL operators).
    *   *Mitigation:* Use CTEs (Common Table Expressions) in Postgres. Use SQL to perform the pgvector nearest-neighbor search, grab the resulting Postgres IDs, and feed them into a Cypher `agtype` query in the next pipeline step.

## Next Steps for the Orchestrator
When integrating the Deep Research results:
1.  Look specifically for tools that perform **JIT AST parsing for Git diffs** (like `tree-sitter` combined with git logs).
2.  Assess the maturity of Apache AGE vs. simpler relational approaches (recursive SQL CTEs) for modeling Git trees. Sometimes a graph DB is overkill if simple recursive SQL can traverse the commit parent chain.