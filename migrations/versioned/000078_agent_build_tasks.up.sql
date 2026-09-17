-- Migration: 000078_agent_build_tasks
-- Durable ledger + queue for documents handed to the OpenAI-Agents gateway
-- (WIKI_AGENT_CALLBACK_URL) for external wiki generation.
--
-- Background: when a knowledge base has IndexingStrategy.WikiEnabled = true AND
-- CustomWikiGeneration = true, WeKnora deliberately does NOT auto-build wiki
-- pages -- an external agent is expected to. Until now that hand-off was a
-- fire-and-forget HTTP POST fired from a detached goroutine: the gateway's
-- task_id was discarded, a 429 / 5xx response lost the task silently, and
-- nothing was recorded on the WeKnora side. Operators therefore could not see
-- which documents were building, how many were waiting, or retry / cancel a
-- build.
--
-- This table turns that hand-off into a durable, observable queue:
--   queued    -> row written, waiting for a dispatch slot (the WeKnora queue)
--   running   -> claimed by the dispatcher; POSTed to the gateway once
--                submitted_at / gateway_task_id are set
--   succeeded -> the gateway reported success
--   failed    -> terminal failure (attempts exhausted, gateway task gone, or
--                the gateway task exceeded the max wait)
--   cancelled -> operator cancelled it, queued or in flight
--
-- `next_attempt_at` carries the retry backoff for a *retryable* hand-off
-- failure (gateway down / 429 / 5xx), and `max_attempts` bounds those retries
-- so a permanently broken gateway cannot spin forever.
--
-- NOTE: `payload` holds the fully rendered POST body for the gateway, which
-- includes the knowledge base's bound-model API key. It must never be exposed
-- through the HTTP API (the response DTO omits it).
DO $$ BEGIN RAISE NOTICE '[Migration 000078] Creating table: agent_build_tasks'; END $$;

CREATE TABLE IF NOT EXISTS agent_build_tasks (
    id                  VARCHAR(64)  PRIMARY KEY,
    tenant_id           BIGINT       NOT NULL DEFAULT 0,
    knowledge_base_id   VARCHAR(64)  NOT NULL DEFAULT '',
    knowledge_id        VARCHAR(64)  NOT NULL DEFAULT '',
    doc_name            VARCHAR(512) NOT NULL DEFAULT '',
    skill               VARCHAR(128) NOT NULL DEFAULT '',
    status              VARCHAR(16)  NOT NULL DEFAULT 'queued',
    attempts            INT          NOT NULL DEFAULT 0,
    max_attempts        INT          NOT NULL DEFAULT 3,
    gateway_url         VARCHAR(512) NOT NULL DEFAULT '',
    gateway_task_id     VARCHAR(128),
    payload             TEXT,
    output_text         TEXT         NOT NULL DEFAULT '',
    last_error          TEXT         NOT NULL DEFAULT '',
    queued_at           TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    next_attempt_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    submitted_at        TIMESTAMP WITH TIME ZONE,
    started_at          TIMESTAMP WITH TIME ZONE,
    finished_at         TIMESTAMP WITH TIME ZONE,
    duration_ms         BIGINT,
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Dispatch scan: the oldest queued row whose backoff has elapsed.
CREATE INDEX IF NOT EXISTS idx_abtask_dispatch
    ON agent_build_tasks (status, next_attempt_at);

-- In-flight bookkeeping: concurrency accounting + stuck-run sweeps.
CREATE INDEX IF NOT EXISTS idx_abtask_status_updated
    ON agent_build_tasks (status, updated_at);

-- "Which documents are building in this knowledge base?" (UI filter).
CREATE INDEX IF NOT EXISTS idx_abtask_kb
    ON agent_build_tasks (knowledge_base_id, status);

-- Reverse lookup by gateway task id (status polling / reconcile).
CREATE INDEX IF NOT EXISTS idx_abtask_gateway_task
    ON agent_build_tasks (gateway_task_id)
    WHERE gateway_task_id IS NOT NULL;

-- Per-document history ("why did this document build three times?").
CREATE INDEX IF NOT EXISTS idx_abtask_knowledge
    ON agent_build_tasks (knowledge_id, created_at DESC);

DO $$ BEGIN RAISE NOTICE '[Migration 000078] agent_build_tasks table ready'; END $$;
