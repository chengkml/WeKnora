-- Reverse migration for 000076_wiki_page_feedback.

DO $$ BEGIN RAISE NOTICE '[Migration 000076] Dropping wiki_page_feedback'; END $$;

DROP TABLE IF EXISTS wiki_page_feedback;

DO $$ BEGIN RAISE NOTICE '[Migration 000076] wiki_page_feedback dropped'; END $$;