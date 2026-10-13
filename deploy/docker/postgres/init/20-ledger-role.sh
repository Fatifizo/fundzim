#!/bin/sh
# Local development only (Stage 10, ADR-042): creates the ledger posting role fundzim_ledger — the only role with
# write privileges on the ledger schema (grants come from migrations: app.apply_runtime_grants). Idempotent: runs
# on first initialisation of an empty data volume, and can be run against an existing local volume:
#   docker compose exec -T -e POSTGRES_LEDGER_PASSWORD="$POSTGRES_LEDGER_PASSWORD" fundzim-postgres \
#     sh /docker-entrypoint-initdb.d/20-ledger-role.sh
set -eu
: "${POSTGRES_LEDGER_PASSWORD:?}"
psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-postgres}" --dbname postgres -v pw="$POSTGRES_LEDGER_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE fundzim_ledger LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE', :'pw')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_ledger') \gexec
SELECT format('ALTER ROLE fundzim_ledger LOGIN PASSWORD %L', :'pw') \gexec
ALTER ROLE fundzim_ledger SET statement_timeout = '30s';
ALTER ROLE fundzim_ledger SET lock_timeout = '3s';
ALTER ROLE fundzim_ledger SET idle_in_transaction_session_timeout = '30s';
SELECT 'GRANT CONNECT ON DATABASE fundzim TO fundzim_ledger' WHERE EXISTS (SELECT 1 FROM pg_database WHERE datname = 'fundzim') \gexec
SQL
