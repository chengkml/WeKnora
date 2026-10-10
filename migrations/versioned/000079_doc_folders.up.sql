-- Migration: 000079_doc_folders
-- Description: Multi-level directory tree for document classification in a
-- knowledge base. Adjacency-list tree (parent_id '' = root), path is the
-- materialized "/"-joined name chain for cheap display/sort. knowledges gets a
-- single folder_id (a document belongs to exactly one folder — decided 2026-10-10).
-- Recursive "show subtree" filtering reads the folder tree in memory (doc counts
-- are small), mirroring the wiki_folders design.

DO $$ BEGIN RAISE NOTICE '[Migration 000079] Creating doc_folders'; END $$;

CREATE TABLE IF NOT EXISTS doc_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

-- A folder name is unique among its live siblings under the same parent.
CREATE UNIQUE INDEX IF NOT EXISTS idx_doc_folders_parent_name
    ON doc_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_doc_folders_parent
    ON doc_folders (knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_doc_folders_deleted_at
    ON doc_folders (deleted_at);

-- Single-placement: a document belongs to exactly one folder ('' = KB root).
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder
    ON knowledges (knowledge_base_id, folder_id)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000079] doc_folders ready'; END $$;
