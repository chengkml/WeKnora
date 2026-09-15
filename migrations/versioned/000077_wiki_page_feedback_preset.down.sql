-- Reverse migration for 000077_wiki_page_feedback_preset.

DO $$ BEGIN RAISE NOTICE '[Migration 000077] Dropping preset column from wiki_page_feedback'; END $$;

ALTER TABLE wiki_page_feedback DROP COLUMN IF EXISTS preset;

DO $$ BEGIN RAISE NOTICE '[Migration 000077] preset column dropped'; END $$;
