-- FundZim migration: River job queue schema, River "main" migration line versions 2,3,4, in schema `queue`
-- (ADR-025, docs/architecture/background-processing.md §2.1).
-- River version: github.com/riverqueue/river v0.47.0 (released 2026-08-31).
-- Generated VERBATIM (do not hand-edit the River SQL between the markers) with:
--   go run github.com/riverqueue/river/cmd/river@v0.47.0 migrate-get --version 2,3,4 --up --schema queue
--   go run github.com/riverqueue/river/cmd/river@v0.47.0 migrate-get --version 4,3,2 --down --schema queue
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
-- River main migration 002 [up]
CREATE TYPE queue.river_job_state AS ENUM(
  'available',
  'cancelled',
  'completed',
  'discarded',
  'retryable',
  'running',
  'scheduled'
);

CREATE TABLE queue.river_job(
  -- 8 bytes
  id bigserial PRIMARY KEY,

  -- 8 bytes (4 bytes + 2 bytes + 2 bytes)
  --
  -- `state` is kept near the top of the table for operator convenience -- when
  -- looking at jobs with `SELECT *` it'll appear first after ID. The other two
  -- fields aren't as important but are kept adjacent to `state` for alignment
  -- to get an 8-byte block.
  state queue.river_job_state NOT NULL DEFAULT 'available',
  attempt smallint NOT NULL DEFAULT 0,
  max_attempts smallint NOT NULL,

  -- 8 bytes each (no alignment needed)
  attempted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT NOW(),
  finalized_at timestamptz,
  scheduled_at timestamptz NOT NULL DEFAULT NOW(),

  -- 2 bytes (some wasted padding probably)
  priority smallint NOT NULL DEFAULT 1,

  -- types stored out-of-band
  args jsonb,
  attempted_by text[],
  errors jsonb[],
  kind text NOT NULL,
  metadata jsonb NOT NULL DEFAULT '{}',
  queue text NOT NULL DEFAULT 'default',
  tags varchar(255)[],

  CONSTRAINT finalized_or_finalized_at_null CHECK ((state IN ('cancelled', 'completed', 'discarded') AND finalized_at IS NOT NULL) OR finalized_at IS NULL),
  CONSTRAINT max_attempts_is_positive CHECK (max_attempts > 0),
  CONSTRAINT priority_in_range CHECK (priority >= 1 AND priority <= 4),
  CONSTRAINT queue_length CHECK (char_length(queue) > 0 AND char_length(queue) < 128),
  CONSTRAINT kind_length CHECK (char_length(kind) > 0 AND char_length(kind) < 128)
);

-- We may want to consider adding another property here after `kind` if it seems
-- like it'd be useful for something.
CREATE INDEX river_job_kind ON queue.river_job USING btree(kind);

CREATE INDEX river_job_state_and_finalized_at_index ON queue.river_job USING btree(state, finalized_at) WHERE finalized_at IS NOT NULL;

CREATE INDEX river_job_prioritized_fetching_index ON queue.river_job USING btree(state, queue, priority, scheduled_at, id);

CREATE INDEX river_job_args_index ON queue.river_job USING GIN(args);

CREATE INDEX river_job_metadata_index ON queue.river_job USING GIN(metadata);

CREATE OR REPLACE FUNCTION queue.river_job_notify()
  RETURNS TRIGGER
  AS $$
DECLARE
  payload json;
BEGIN
  IF NEW.state = 'available' THEN
    -- Notify will coalesce duplicate notifications within a transaction, so
    -- keep these payloads generalized:
    payload = json_build_object('queue', NEW.queue);
    PERFORM
      pg_notify('river_insert', payload::text);
  END IF;
  RETURN NULL;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER river_notify
  AFTER INSERT ON queue.river_job
  FOR EACH ROW
  EXECUTE PROCEDURE queue.river_job_notify();

CREATE UNLOGGED TABLE queue.river_leader(
    -- 8 bytes each (no alignment needed)
    elected_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,

    -- types stored out-of-band
    leader_id text NOT NULL,
    name text PRIMARY KEY,

    CONSTRAINT name_length CHECK (char_length(name) > 0 AND char_length(name) < 128),
    CONSTRAINT leader_id_length CHECK (char_length(leader_id) > 0 AND char_length(leader_id) < 128)
);

-- River main migration 003 [up]
ALTER TABLE queue.river_job ALTER COLUMN tags SET DEFAULT '{}';
UPDATE queue.river_job SET tags = '{}' WHERE tags IS NULL;
ALTER TABLE queue.river_job ALTER COLUMN tags SET NOT NULL;

-- River main migration 004 [up]
-- The args column never had a NOT NULL constraint or default value at the
-- database level, though we tried to ensure one at the application level.
ALTER TABLE queue.river_job ALTER COLUMN args SET DEFAULT '{}';
UPDATE queue.river_job SET args = '{}' WHERE args IS NULL;
ALTER TABLE queue.river_job ALTER COLUMN args SET NOT NULL;
ALTER TABLE queue.river_job ALTER COLUMN args DROP DEFAULT;

-- The metadata column never had a NOT NULL constraint or default value at the
-- database level, though we tried to ensure one at the application level.
ALTER TABLE queue.river_job ALTER COLUMN metadata SET DEFAULT '{}';
UPDATE queue.river_job SET metadata = '{}' WHERE metadata IS NULL;
ALTER TABLE queue.river_job ALTER COLUMN metadata SET NOT NULL;

-- The 'pending' job state will be used for upcoming functionality:
ALTER TYPE queue.river_job_state ADD VALUE IF NOT EXISTS 'pending' AFTER 'discarded';

ALTER TABLE queue.river_job DROP CONSTRAINT finalized_or_finalized_at_null;
ALTER TABLE queue.river_job ADD CONSTRAINT finalized_or_finalized_at_null CHECK (
    (finalized_at IS NULL AND state NOT IN ('cancelled', 'completed', 'discarded')) OR
    (finalized_at IS NOT NULL AND state IN ('cancelled', 'completed', 'discarded'))
);

DROP TRIGGER river_notify ON queue.river_job;
DROP FUNCTION queue.river_job_notify;

--
-- Create table `river_queue`.
--

CREATE TABLE queue.river_queue (
    name text PRIMARY KEY NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}' ::jsonb,
    paused_at timestamptz,
    updated_at timestamptz NOT NULL
);

--
-- Alter `river_leader` to add a default value of 'default` to `name`.
--

ALTER TABLE queue.river_leader
    ALTER COLUMN name SET DEFAULT 'default',
    DROP CONSTRAINT name_length,
    ADD CONSTRAINT name_length CHECK (name = 'default');

-- ===== END River-generated SQL (up) =====
CALL app.apply_runtime_grants();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ===== BEGIN River-generated SQL (down) =====
-- River main migration 004 [down]
ALTER TABLE queue.river_job ALTER COLUMN args DROP NOT NULL;

ALTER TABLE queue.river_job ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE queue.river_job ALTER COLUMN metadata DROP DEFAULT;

-- It is not possible to safely remove 'pending' from the river_job_state enum,
-- so leave it in place.

ALTER TABLE queue.river_job DROP CONSTRAINT finalized_or_finalized_at_null;
ALTER TABLE queue.river_job ADD CONSTRAINT finalized_or_finalized_at_null CHECK (
  (state IN ('cancelled', 'completed', 'discarded') AND finalized_at IS NOT NULL) OR finalized_at IS NULL
);

CREATE OR REPLACE FUNCTION queue.river_job_notify()
  RETURNS TRIGGER
  AS $$
DECLARE
  payload json;
BEGIN
  IF NEW.state = 'available' THEN
    -- Notify will coalesce duplicate notifications within a transaction, so
    -- keep these payloads generalized:
    payload = json_build_object('queue', NEW.queue);
    PERFORM
      pg_notify('river_insert', payload::text);
  END IF;
  RETURN NULL;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER river_notify
  AFTER INSERT ON queue.river_job
  FOR EACH ROW
  EXECUTE PROCEDURE queue.river_job_notify();

DROP TABLE queue.river_queue;

ALTER TABLE queue.river_leader
    ALTER COLUMN name DROP DEFAULT,
    DROP CONSTRAINT name_length,
    ADD CONSTRAINT name_length CHECK (char_length(name) > 0 AND char_length(name) < 128);

-- River main migration 003 [down]
ALTER TABLE queue.river_job
    ALTER COLUMN tags DROP NOT NULL,
    ALTER COLUMN tags DROP DEFAULT;

-- River main migration 002 [down]
DROP TABLE queue.river_job;
DROP FUNCTION queue.river_job_notify;
DROP TYPE queue.river_job_state;

DROP TABLE queue.river_leader;

-- ===== END River-generated SQL (down) =====
-- +goose StatementEnd
