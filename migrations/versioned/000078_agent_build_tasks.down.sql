-- Rollback for 000078_agent_build_tasks.
-- Dropping the ledger loses agent build history; the gateway tasks themselves
-- are unaffected.
DO $$ BEGIN RAISE NOTICE '[Migration 000078] Dropping table: agent_build_tasks'; END $$;

DROP INDEX IF EXISTS idx_abtask_knowledge;
DROP INDEX IF EXISTS idx_abtask_gateway_task;
DROP INDEX IF EXISTS idx_abtask_kb;
DROP INDEX IF EXISTS idx_abtask_status_updated;
DROP INDEX IF EXISTS idx_abtask_dispatch;
DROP TABLE IF EXISTS agent_build_tasks;

DO $$ BEGIN RAISE NOTICE '[Migration 000078] agent_build_tasks table dropped'; END $$;
