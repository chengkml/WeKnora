-- Migration: 000076_wiki_page_feedback
-- Description: Manually-added human feedback (comments / questions) against
-- individual wiki pages. Unlike wiki_page_issues (agent/linter-generated
-- findings), this table holds feedback added by users in the UI so the
-- knowledge base "维护" (maintenance) view can surface and later act on them.
--
-- feedback_type: comment | question
-- status:        pending | resolved | ignored

DO $$ BEGIN RAISE NOTICE '[Migration 000076] Applying wiki_page_feedback table'; END $$;

CREATE TABLE IF NOT EXISTS wiki_page_feedback (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL DEFAULT '',
    page_title        VARCHAR(255) NOT NULL DEFAULT '',
    feedback_type     VARCHAR(20) NOT NULL DEFAULT 'comment',
    content           TEXT NOT NULL DEFAULT '',
    status            VARCHAR(20) NOT NULL DEFAULT 'pending',
    reported_by_id    VARCHAR(100) NOT NULL DEFAULT '',
    reported_by_name  VARCHAR(100) NOT NULL DEFAULT '',
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_feedback_kb
    ON wiki_page_feedback (knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_wiki_page_feedback_kb_slug
    ON wiki_page_feedback (knowledge_base_id, slug);

CREATE INDEX IF NOT EXISTS idx_wiki_page_feedback_kb_type_status
    ON wiki_page_feedback (knowledge_base_id, feedback_type, status);

CREATE INDEX IF NOT EXISTS idx_wiki_page_feedback_tenant
    ON wiki_page_feedback (tenant_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000076] wiki_page_feedback applied successfully'; END $$;