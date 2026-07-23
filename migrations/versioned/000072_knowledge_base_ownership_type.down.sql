DO $$ BEGIN RAISE NOTICE '[Migration 000072] Reverting ownership_type from knowledge_bases...'; END $$;

DROP INDEX IF EXISTS idx_knowledge_bases_ownership_type;

ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS ownership_type;

DO $$ BEGIN RAISE NOTICE '[Migration 000072] ownership_type column removed'; END $$;
