-- FundZim migration: River job queue schema, River "main" migration line versions 5,6,7, in schema `queue`
-- (ADR-025, docs/architecture/background-processing.md §2.1).
-- River version: github.com/riverqueue/river v0.47.0 (released 2026-08-31).
-- Generated VERBATIM (do not hand-edit the River SQL between the markers) with:
--   go run github.com/riverqueue/river/cmd/river@v0.47.0 migrate-get --version 5,6,7 --up --schema queue
--   go run github.com/riverqueue/river/cmd/river@v0.47.0 migrate-get --version 7,6,5 --down --schema queue
-- River's own migration table (version 001) is excluded, as River recommends when an external migration
-- framework (goose) owns the schema; River's runtime does not read it. River versions 002-004 and 005-007 are
-- split into two goose migrations (two transactions) because 004 adds the 'pending' enum value that the
-- immutable function in 006 references (a new enum value cannot be used in the transaction that adds it).
-- Upgrading River: generate the new versions the same way into a NEW migration; never edit this file.
-- Runs as fundzim_migrator; the queue default privileges (20261008120300) give fundzim_app table access.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- ===== BEGIN River-generated SQL (up) =====
-- River main migration 005 [up]
--
-- Rebuild the migration table so it's based on `(line, version)`.
--

DO
$body$
BEGIN
    -- Tolerate users who may be using their own migration system rather than
    -- River's. If they are, they will have skipped version 001 containing
    -- `CREATE TABLE river_migration`, so this table won't exist.
    IF (SELECT to_regclass('queue.river_migration') IS NOT NULL) THEN
        ALTER TABLE queue.river_migration
            RENAME TO river_migration_old;

        CREATE TABLE queue.river_migration(
            line TEXT NOT NULL,
            version bigint NOT NULL,
            created_at timestamptz NOT NULL DEFAULT NOW(),
            CONSTRAINT line_length CHECK (char_length(line) > 0 AND char_length(line) < 128),
            CONSTRAINT version_gte_1 CHECK (version >= 1),
            PRIMARY KEY (line, version)
        );

        INSERT INTO queue.river_migration
            (created_at, line, version)
        SELECT created_at, 'main', version
        FROM queue.river_migration_old;

        DROP TABLE queue.river_migration_old;
    END IF;
END;
$body$
LANGUAGE 'plpgsql'; 

--
-- Add `river_job.unique_key` and bring up an index on it.
--

-- These statements use `IF NOT EXISTS` to allow users with a `river_job` table
-- of non-trivial size to build the index `CONCURRENTLY` out of band of this
-- migration, then follow by completing the migration.
ALTER TABLE queue.river_job
    ADD COLUMN IF NOT EXISTS unique_key bytea;

CREATE UNIQUE INDEX IF NOT EXISTS river_job_kind_unique_key_idx ON queue.river_job (kind, unique_key) WHERE unique_key IS NOT NULL;

--
-- Create `river_client` and derivative.
--
-- This feature hasn't quite yet been implemented, but we're taking advantage of
-- the migration to add the schema early so that we can add it later without an
-- additional migration.
--

CREATE UNLOGGED TABLE queue.river_client (
    id text PRIMARY KEY NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}',
    paused_at timestamptz,
    updated_at timestamptz NOT NULL,
    CONSTRAINT name_length CHECK (char_length(id) > 0 AND char_length(id) < 128)
);

-- Differs from `river_queue` in that it tracks the queue state for a particular
-- active client.
CREATE UNLOGGED TABLE queue.river_client_queue (
    river_client_id text NOT NULL REFERENCES queue.river_client (id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    max_workers bigint NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}',
    num_jobs_completed bigint NOT NULL DEFAULT 0,
    num_jobs_running bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (river_client_id, name),
    CONSTRAINT name_length CHECK (char_length(name) > 0 AND char_length(name) < 128),
    CONSTRAINT num_jobs_completed_zero_or_positive CHECK (num_jobs_completed >= 0),
    CONSTRAINT num_jobs_running_zero_or_positive CHECK (num_jobs_running >= 0)
);

-- River main migration 006 [up]
CREATE OR REPLACE FUNCTION queue.river_job_state_in_bitmask(bitmask BIT(8), state queue.river_job_state)
RETURNS boolean
LANGUAGE SQL
IMMUTABLE
AS $$
    SELECT CASE state
        WHEN 'available' THEN get_bit(bitmask, 7)
        WHEN 'cancelled' THEN get_bit(bitmask, 6)
        WHEN 'completed' THEN get_bit(bitmask, 5)
        WHEN 'discarded' THEN get_bit(bitmask, 4)
        WHEN 'pending'   THEN get_bit(bitmask, 3)
        WHEN 'retryable' THEN get_bit(bitmask, 2)
        WHEN 'running'   THEN get_bit(bitmask, 1)
        WHEN 'scheduled' THEN get_bit(bitmask, 0)
        ELSE 0
    END = 1;
$$;

--
-- Add `river_job.unique_states` and bring up an index on it.
--
-- This column may exist already if users manually created the column and index
-- as instructed in the changelog so the index could be created `CONCURRENTLY`.
--
ALTER TABLE queue.river_job ADD COLUMN IF NOT EXISTS unique_states BIT(8);

-- This statement uses `IF NOT EXISTS` to allow users with a `river_job` table
-- of non-trivial size to build the index `CONCURRENTLY` out of band of this
-- migration, then follow by completing the migration.
CREATE UNIQUE INDEX IF NOT EXISTS river_job_unique_idx ON queue.river_job (unique_key)
    WHERE unique_key IS NOT NULL
      AND unique_states IS NOT NULL
      AND queue.river_job_state_in_bitmask(unique_states, state);

-- Remove the old unique index. Users who are actively using the unique jobs
-- feature and who wish to avoid deploy downtime may want od drop this in a
-- subsequent migration once all jobs using the old unique system have been
-- completed (i.e. no more rows with non-null unique_key and null
-- unique_states).
DROP INDEX queue.river_job_kind_unique_key_idx;

-- River main migration 007 [up]
--
-- Notification outbox.
--

CREATE TABLE queue.river_notification (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    payload text NOT NULL,
    topic text NOT NULL,
    CONSTRAINT topic_length CHECK (length(topic) > 0 AND length(topic) < 128)
);

CREATE INDEX river_notification_created_at_idx ON queue.river_notification (created_at);
CREATE INDEX river_notification_topic_id_idx ON queue.river_notification (topic, id);

--
-- SQLite JSONB conversion.
--
-- No-op. PostgreSQL already stores River JSON columns as jsonb.

--
-- SQL cleanup.
--

--
-- Drop unused tables `river_client` and `river_client_queue`.
--

DROP TABLE queue.river_client_queue;
DROP TABLE queue.river_client;

--
-- Adds `DEFAULT 25` to `river_job.max_attempts`.
--

ALTER TABLE queue.river_job
    ALTER COLUMN max_attempts SET DEFAULT 25;

--
-- Changes `river_queue.updated_at` to have a default of `CURRENT_TIMESTAMP`.
--

ALTER TABLE queue.river_queue
    ALTER COLUMN updated_at SET DEFAULT CURRENT_TIMESTAMP;

-- ===== END River-generated SQL (up) =====
CALL app.apply_runtime_grants();
-- River calls queue.river_job_state_in_bitmask explicitly in its unique-insert and job-delete queries, so the
-- runtime role needs EXECUTE (fundzim_worker inherits it through fundzim_app). This is a plain grant placed
-- after the CALL above because apply_runtime_grants() revokes routine privileges in managed schemas; the
-- lead's runtime_grants_v2 (20261008140500) grants EXECUTE on queue routines on every later re-application.
GRANT EXECUTE ON FUNCTION queue.river_job_state_in_bitmask(bit, queue.river_job_state) TO fundzim_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ===== BEGIN River-generated SQL (down) =====
-- River main migration 007 [down]
--
-- SQL cleanup rollback.
--

--
-- Add back unused tables `river_client` and `river_client_queue`.
--

CREATE UNLOGGED TABLE queue.river_client (
    id text PRIMARY KEY NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}',
    paused_at timestamptz,
    updated_at timestamptz NOT NULL,
    CONSTRAINT name_length CHECK (char_length(id) > 0 AND char_length(id) < 128)
);

CREATE UNLOGGED TABLE queue.river_client_queue (
    river_client_id text NOT NULL REFERENCES queue.river_client (id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    max_workers bigint NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}',
    num_jobs_completed bigint NOT NULL DEFAULT 0,
    num_jobs_running bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (river_client_id, name),
    CONSTRAINT name_length CHECK (char_length(name) > 0 AND char_length(name) < 128),
    CONSTRAINT num_jobs_completed_zero_or_positive CHECK (num_jobs_completed >= 0),
    CONSTRAINT num_jobs_running_zero_or_positive CHECK (num_jobs_running >= 0)
);

--
-- Revert addition of `DEFAULT 25` to `river_job.max_attempts`.
--

ALTER TABLE queue.river_job
    ALTER COLUMN max_attempts DROP DEFAULT;

--
-- Changes `river_queue.updated_at` to revert the default of `CURRENT_TIMESTAMP`.
--

ALTER TABLE queue.river_queue
    ALTER COLUMN updated_at DROP DEFAULT;

--
-- SQLite JSONB conversion rollback.
--
-- No-op. PostgreSQL already stores River JSON columns as jsonb.

--
-- Notification outbox rollback.
--

DROP TABLE queue.river_notification;

-- River main migration 006 [down]
--
-- Drop `river_job.unique_states` and its index.
--

DROP INDEX queue.river_job_unique_idx;

ALTER TABLE queue.river_job
    DROP COLUMN unique_states;

CREATE UNIQUE INDEX IF NOT EXISTS river_job_kind_unique_key_idx ON queue.river_job (kind, unique_key) WHERE unique_key IS NOT NULL;

--
-- Drop `river_job_state_in_bitmask` function.
--
DROP FUNCTION queue.river_job_state_in_bitmask;

-- River main migration 005 [down]
--
-- Revert to migration table based only on `(version)`.
--
-- If any non-main migrations are present, 005 is considered irreversible.
--

DO
$body$
BEGIN
    -- Tolerate users who may be using their own migration system rather than
    -- River's. If they are, they will have skipped version 001 containing
    -- `CREATE TABLE river_migration`, so this table won't exist.
    IF (SELECT to_regclass('queue.river_migration') IS NOT NULL) THEN
        IF EXISTS (
            SELECT *
            FROM queue.river_migration
            WHERE line <> 'main'
        ) THEN
            RAISE EXCEPTION 'Found non-main migration lines in the database; version 005 migration is irreversible because it would result in loss of migration information.';
        END IF;

        ALTER TABLE queue.river_migration
            RENAME TO river_migration_old;

        CREATE TABLE queue.river_migration(
            id bigserial PRIMARY KEY,
            created_at timestamptz NOT NULL DEFAULT NOW(),
            version bigint NOT NULL,
            CONSTRAINT version CHECK (version >= 1)
        );

        CREATE UNIQUE INDEX ON queue.river_migration USING btree(version);

        INSERT INTO queue.river_migration
            (created_at, version)
        SELECT created_at, version
        FROM queue.river_migration_old;

        DROP TABLE queue.river_migration_old;
    END IF;
END;
$body$
LANGUAGE 'plpgsql'; 

--
-- Drop `river_job.unique_key`.
--

ALTER TABLE queue.river_job
    DROP COLUMN unique_key;

--
-- Drop `river_client` and derivative.
--

DROP TABLE queue.river_client_queue;
DROP TABLE queue.river_client;

-- ===== END River-generated SQL (down) =====
-- +goose StatementEnd
