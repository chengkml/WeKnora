-- Migration: 000073_users_email_nullable
--
-- Registration now takes a caller-chosen user_id mapped to users.id; email
-- becomes optional. Existing rows keep their emails; new rows may store NULL.
-- The unique index on email is preserved — Postgres unique indexes allow
-- multiple NULL rows, so any number of users may omit an email without
-- colliding.

DO $$ BEGIN RAISE NOTICE '[Migration 000073] Making users.email nullable...'; END $$;

ALTER TABLE users
    ALTER COLUMN email DROP NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000073] users.email is now nullable'; END $$;
