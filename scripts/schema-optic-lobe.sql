-- Optic Lobe PostgreSQL Schema Definition
-- This schema establishes the relational backbone for the Three-Tier Hierarchical Graph,
-- utilizing pgvector for semantic search and preparing the data for PG19 SQL/PGQ.

-- 1. MACRO TIER (Workspaces & Users)
CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID REFERENCES workspaces(id),
    username VARCHAR(255) NOT NULL,
    preferences JSONB DEFAULT '{}'::jsonb
);

-- Insight Graph Nodes (Global Rules & Heuristics)
CREATE TABLE insights (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID REFERENCES workspaces(id),
    author_id UUID REFERENCES users(id),
    content TEXT NOT NULL,
    embedding vector(1536), -- Standard embedding dimension
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- 2. MESO TIER (Repositories & Tasks)
CREATE TABLE repositories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID REFERENCES workspaces(id),
    name VARCHAR(255) NOT NULL,
    remote_url TEXT
);

-- Query Graph Nodes (Tasks, Issues, PRs)
CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id UUID REFERENCES repositories(id),
    external_id VARCHAR(255), -- e.g., GitHub Issue #
    title TEXT NOT NULL,
    status VARCHAR(50),
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- 3. MICRO TIER (Commits & Code Nodes)
CREATE TABLE commits (
    hash VARCHAR(40) PRIMARY KEY,
    repository_id UUID REFERENCES repositories(id),
    author_id UUID REFERENCES users(id),
    message TEXT,
    message_embedding vector(1536),
    timestamp TIMESTAMPTZ NOT NULL
);

-- Interaction Graph Nodes (AST Nodes: Functions, Classes)
CREATE TABLE code_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id UUID REFERENCES repositories(id),
    file_path TEXT NOT NULL,
    symbol_name VARCHAR(255) NOT NULL,
    node_type VARCHAR(50) NOT NULL, -- 'function', 'class', 'struct'
    content TEXT NOT NULL,
    content_embedding vector(1536),
    
    -- Temporal Interval Stamping
    valid_from_commit VARCHAR(40) REFERENCES commits(hash),
    valid_to_commit VARCHAR(40) REFERENCES commits(hash), -- NULL if active
    
    -- Fast Meso-Filtering Metadata
    metadata JSONB DEFAULT '{}'::jsonb
);

-- 4. EDGES (Relational mapping for SQL/PGQ)
-- Temporal Provenance Edge: EVOLVED_FROM
CREATE TABLE code_evolutions (
    from_node_id UUID REFERENCES code_nodes(id),
    to_node_id UUID REFERENCES code_nodes(id),
    commit_hash VARCHAR(40) REFERENCES commits(hash),
    PRIMARY KEY (from_node_id, to_node_id)
);

-- Structural Edge: CALLS / DEPENDS_ON
CREATE TABLE code_dependencies (
    caller_id UUID REFERENCES code_nodes(id),
    callee_id UUID REFERENCES code_nodes(id),
    PRIMARY KEY (caller_id, callee_id)
);

-- Hierarchical Edge: APPLIES_TO (Connects Insights to Code/Repos)
CREATE TABLE insight_links (
    insight_id UUID REFERENCES insights(id),
    repository_id UUID REFERENCES repositories(id), -- If null, applies to whole workspace
    code_node_id UUID REFERENCES code_nodes(id),    -- If null, applies to whole repo
    PRIMARY KEY (insight_id, repository_id, code_node_id)
);

-- 5. INDEXING
-- Semantic Vector Indexes (HNSW)
CREATE INDEX ON insights USING hnsw (embedding vector_cosine_ops);
CREATE INDEX ON commits USING hnsw (message_embedding vector_cosine_ops);
CREATE INDEX ON code_nodes USING hnsw (content_embedding vector_cosine_ops);

-- RLS & Filtering Indexes
CREATE INDEX idx_code_nodes_repo ON code_nodes(repository_id);
CREATE INDEX idx_insights_workspace ON insights(workspace_id);

-- Define SQL/PGQ Property Graph (PG 19 syntax)
-- Note: This is an architectural projection for PG 19's CREATE PROPERTY GRAPH
/*
CREATE PROPERTY GRAPH optic_lobe_graph
    VERTEX TABLES (
        insights,
        tasks,
        commits,
        code_nodes
    )
    EDGE TABLES (
        code_evolutions SOURCE KEY (from_node_id) REFERENCES code_nodes(id) DESTINATION KEY (to_node_id) REFERENCES code_nodes(id) LABEL EVOLVED_FROM,
        code_dependencies SOURCE KEY (caller_id) REFERENCES code_nodes(id) DESTINATION KEY (callee_id) REFERENCES code_nodes(id) LABEL CALLS,
        insight_links SOURCE KEY (insight_id) REFERENCES insights(id) DESTINATION KEY (code_node_id) REFERENCES code_nodes(id) LABEL APPLIES_TO
    );
*/
