-- Optic Lobe PostgreSQL Initialization

-- Enable pgvector for semantic similarity search
CREATE EXTENSION IF NOT EXISTS vector;

-- Enable pgaudit for strict enterprise governance and auditing 
-- (allows tracking of RLS bypasses and critical schema mutations)
CREATE EXTENSION IF NOT EXISTS pgaudit;

-- Note: SQL/PGQ (Property Graph Queries) is a native feature in PostgreSQL 19
-- and does not require an extension to be explicitly created.
