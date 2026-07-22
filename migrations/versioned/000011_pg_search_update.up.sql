-- Migration 000011: Update pg_search extension to latest version (no-op without pg_search)
-- Equivalent to: psql -c 'ALTER EXTENSION pg_search UPDATE;'

DO $$
BEGIN
    IF current_setting('app.skip_embedding', true) = 'true' THEN
        RAISE NOTICE 'Skipping pg_search update (app.skip_embedding=true)';
        RETURN;
    END IF;

    BEGIN
        ALTER EXTENSION pg_search UPDATE;
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE '[Migration 000011] pg_search extension not available, skipping update';
    END;
END $$;
