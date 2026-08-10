-- Reverse migration for 000073_users_email_nullable.
--
-- Restore the original NOT NULL constraint on users.email. Only valid once
-- every user row has a non-null email; NULL rows must be backfilled before
-- applying this down migration (e.g. UPDATE users SET email = '' WHERE email IS NULL).

DO $$ BEGIN RAISE NOTICE '[Migration 000073] Restoring NOT NULL on users.email...'; END $$;

UPDATE users SET email = '' WHERE email IS NULL;

ALTER TABLE users
    ALTER COLUMN email SET NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000073] users.email NOT NULL restored'; END $$;
