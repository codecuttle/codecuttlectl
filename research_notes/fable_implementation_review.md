# Fable Node: Optic Lobe Implementation Review

Reviewing the V1 implementation of the Codecuttle Optic Lobe memory system.

## 1. The PostgreSQL 16 "Graceful Degradation"
Falling back to PG16 and utilizing **Recursive CTEs** (Common Table Expressions) rather than forcing a dependency on PG19's `SQL/PGQ` was an excellent architectural pivot. 

*   **Why it works**: A CTE with `WITH RECURSIVE` allows us to compute the exact same multi-hop transitive closure as a graph query. 
*   **The HybridRAG Query**: The implementation in `postgres_store.go` correctly isolates the semantic nearest-neighbor search into the base query (`semantic_matches`), ordering by `content_embedding <=> $1`, and then executes a strict recursive union over the `EVOLVED_FROM` edge. This is highly performant and eliminates the need for any external Graph DB layer like Apache AGE.
*   **Recommendation for V2**: When PG19 becomes standard, replacing the CTE with a native `MATCH` clause will be a purely backend refactor; the `OpticStore` Go interface will not need to change.

## 2. Tree-sitter AST Ingestion
The `cuttlebone-optic-ingest` plugin successfully maps Git events into semantic Code Property Graph (CPG) nodes.

*   **Strengths**: Using `git diff-tree` and `git show` to grab exact file states at `commit_hash` combined with `github.com/smacker/go-tree-sitter` ensures we are logging *semantic blocks* (functions/classes) rather than raw text diffs. This solves the token-poisoning problem for the LLM.
*   **Gap to Address**: Currently, the implementation mocks the vector embeddings (`make([]float32, 1536)`). Before this goes to production, we must wire the Cuttlebone plugin to either hit the local `ollama` embedding endpoint or proxy back to the Orchestrator's `Bedrock` connection.

## 3. The Optionality Constraint
The creation of `LocalOpticStore` vs `PostgresOpticStore` behind the `OpticStore` interface satisfies the core requirement perfectly. Codecuttle remains a lightweight CLI for hobbyists, while offering a deep, persistent organizational graph for enterprise users simply by passing the `--optic-lobe-uri` flag.

## Next Steps for the Orchestrator
1.  **Orchestrator Injection**: Wire the initialized `OpticStore` (either Local or Postgres) into the core `conversation.ExecuteTurn` loop.
2.  **Context Compaction**: Ensure that when `compact.Compact()` summarizes a session, the resulting `Result` is routed to `store.AddInsight()` or the Interaction Graph.
3.  **Real Embeddings**: Update the `createMockEmbedding` functions in the plugins to hit a real Text Embedding model.