-- Migration: 000074_wiki_page_folders
-- Description: Many-to-many wiki page <-> folder membership (a page may
-- belong to multiple directories). WikiPage.FolderID stays as the primary
-- placement driving the derived category_path/wiki_path/depth caches; the full
-- membership set (including the primary) lives in wiki_page_folders so
-- directory browsing, page counts, and the folder tree can all be multi-
-- directory consistent without double counting.

DO $$ BEGIN RAISE NOTICE '[Migration 000074] Applying wiki_page_folders membership table'; END $$;

CREATE TABLE IF NOT EXISTS wiki_page_folders (
    page_id           VARCHAR(36) NOT NULL,
    folder_id         VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, folder_id)
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_folders_folder
    ON wiki_page_folders (folder_id);

CREATE INDEX IF NOT EXISTS idx_wiki_page_folders_kb
    ON wiki_page_folders (knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_wiki_page_folders_tenant
    ON wiki_page_folders (tenant_id);

-- Backfill: every page with a non-root folder_id gets a membership row, so
-- existing single-directory data behaves exactly as before.
INSERT INTO wiki_page_folders (page_id, folder_id, knowledge_base_id, tenant_id, created_at, updated_at)
SELECT id, folder_id, knowledge_base_id, tenant_id, created_at, updated_at
FROM wiki_pages
WHERE folder_id IS NOT NULL AND folder_id <> '' AND deleted_at IS NULL
ON CONFLICT (page_id, folder_id) DO NOTHING;

DO $$ BEGIN RAISE NOTICE '[Migration 000074] wiki_page_folders applied successfully'; END $$;
