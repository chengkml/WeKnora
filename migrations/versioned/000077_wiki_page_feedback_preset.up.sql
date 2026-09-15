-- Migration: 000077_wiki_page_feedback_preset
-- Description: Add preset column to wiki_page_feedback. Preset stores the key
-- of a one-click quick-feedback option (e.g. content_error / outdated / broken_link
-- / format_issue / incomplete) chosen from the wiki page detail dropdown, so the
-- maintenance view can distinguish one-click issue marks from typed comments and
-- later aggregate problem types per page.

DO $$ BEGIN RAISE NOTICE '[Migration 000077] Adding preset column to wiki_page_feedback'; END $$;

ALTER TABLE wiki_page_feedback ADD COLUMN IF NOT EXISTS preset VARCHAR(50) NOT NULL DEFAULT '';

DO $$ BEGIN RAISE NOTICE '[Migration 000077] preset column added'; END $$;
