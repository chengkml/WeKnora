DO $$ BEGIN RAISE NOTICE '[Migration 000072] Adding ownership_type to knowledge_bases...'; END $$;

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS ownership_type VARCHAR(16) NOT NULL DEFAULT 'personal';

CREATE INDEX IF NOT EXISTS idx_knowledge_bases_ownership_type
    ON knowledge_bases(ownership_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000072] ownership_type column added, defaulting to personal'; END $$;
