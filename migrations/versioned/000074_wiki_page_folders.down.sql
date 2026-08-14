-- Reverse migration for 000074_wiki_page_folders.

DO $$ BEGIN RAISE NOTICE '[Migration 000074] Dropping wiki_page_folders'; END $$;

DROP TABLE IF EXISTS wiki_page_folders;

DO $$ BEGIN RAISE NOTICE '[Migration 000074] wiki_page_folders dropped'; END $$;
