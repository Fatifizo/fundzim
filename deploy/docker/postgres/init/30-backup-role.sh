#!/bin/sh
# Stage 18 (docs/stage-18/backup-recovery.md): the backup role fundzim_backup — read-only, used only by pg_dump in
# scripts/backup/backup.sh. pg_read_all_data reads every table; BYPASSRLS is required because pg_dump refuses to dump a
# table whose row-level security would hide rows (the compliance schema), and a silently partial backup is worse than
# none. It cannot write (default_transaction_read_only, no DML grant) and owns nothing. Idempotent: runs on first
# initialisation of an empty data volume, and against an existing local volume with
#   docker compose exec -T -e POSTGRES_BACKUP_PASSWORD="$POSTGRES_BACKUP_PASSWORD" fundzim-postgres \
#     sh /docker-entrypoint-initdb.d/30-backup-role.sh
set -eu
: "${POSTGRES_BACKUP_PASSWORD:?}"
psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER:-postgres}" --dbname postgres -v pw="$POSTGRES_BACKUP_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE fundzim_backup LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS', :'pw')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_backup') \gexec
SELECT format('ALTER ROLE fundzim_backup LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE BYPASSRLS', :'pw') \gexec
GRANT pg_read_all_data TO fundzim_backup;
ALTER ROLE fundzim_backup SET default_transaction_read_only = on;
ALTER ROLE fundzim_backup SET idle_in_transaction_session_timeout = '0';
SELECT 'GRANT CONNECT ON DATABASE fundzim TO fundzim_backup' WHERE EXISTS (SELECT 1 FROM pg_database WHERE datname = 'fundzim') \gexec
SQL
