-- Migration: 000079_doc_folders (down)
DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS doc_folders;
