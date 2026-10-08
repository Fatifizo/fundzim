#!/bin/sh
# Local development only: creates FundZim's least-privilege roles and the database (DATABASE §8,
# ADR-022). Runs once, on first initialisation of an empty data volume, as the postgres superuser.
# Passwords come from the environment (generated into .env by scripts/dev-env-init.sh). Nothing in the
# application connects as a superuser.
set -eu

: "${POSTGRES_MIGRATOR_PASSWORD:?}" "${POSTGRES_APP_PASSWORD:?}" "${POSTGRES_WORKER_PASSWORD:?}"
: "${POSTGRES_KYC_PASSWORD:?}" "${POSTGRES_COMPLIANCE_PASSWORD:?}" "${POSTGRES_READONLY_PASSWORD:?}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v pw_mig="$POSTGRES_MIGRATOR_PASSWORD" -v pw_app="$POSTGRES_APP_PASSWORD" \
  -v pw_wrk="$POSTGRES_WORKER_PASSWORD" -v pw_kyc="$POSTGRES_KYC_PASSWORD" \
  -v pw_cmp="$POSTGRES_COMPLIANCE_PASSWORD" -v pw_ro="$POSTGRES_READONLY_PASSWORD" <<'SQL'
-- The migrator owns every FundZim object and runs DDL; it is not a superuser.
CREATE ROLE fundzim_migrator LOGIN PASSWORD :'pw_mig' NOSUPERUSER NOCREATEDB NOCREATEROLE;
-- Runtime roles: login only, privileges granted by migrations (app.apply_runtime_grants).
CREATE ROLE fundzim_app        LOGIN PASSWORD :'pw_app'        NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE fundzim_worker     LOGIN PASSWORD :'pw_wrk'     NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE fundzim_kyc        LOGIN PASSWORD :'pw_kyc'        NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE fundzim_compliance LOGIN PASSWORD :'pw_cmp' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE ROLE fundzim_readonly   LOGIN PASSWORD :'pw_ro'   NOSUPERUSER NOCREATEDB NOCREATEROLE;
-- The worker holds every app privilege plus worker-only routines (I-15).
GRANT fundzim_app TO fundzim_worker;

-- Safety timeouts for every runtime role (DATABASE §12).
ALTER ROLE fundzim_app        SET statement_timeout = '15s';
ALTER ROLE fundzim_app        SET lock_timeout = '3s';
ALTER ROLE fundzim_app        SET idle_in_transaction_session_timeout = '30s';
ALTER ROLE fundzim_worker     SET statement_timeout = '60s';
ALTER ROLE fundzim_worker     SET lock_timeout = '3s';
ALTER ROLE fundzim_worker     SET idle_in_transaction_session_timeout = '30s';
ALTER ROLE fundzim_kyc        SET statement_timeout = '15s';
ALTER ROLE fundzim_compliance SET statement_timeout = '15s';
ALTER ROLE fundzim_readonly   SET statement_timeout = '60s';
ALTER ROLE fundzim_readonly   SET default_transaction_read_only = on;

CREATE DATABASE fundzim OWNER fundzim_migrator;
REVOKE ALL ON DATABASE fundzim FROM PUBLIC;
GRANT CONNECT ON DATABASE fundzim TO fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly;
SQL

# public schema: owned by the database owner, nobody else may create objects in it
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname fundzim <<'SQL'
ALTER SCHEMA public OWNER TO fundzim_migrator;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
ALTER DATABASE fundzim SET timezone TO 'UTC';
SQL
