#!/usr/bin/env bash
# Stage 18 encrypted backup of the LOCAL compose stack (docs/stage-18/backup-recovery.md). Development and drills only:
# it never touches production and never deletes anything.
#
#   BACKUP_KEY_FILE=~/.fundzim-backup.key BACKUP_DIR=~/fundzim-backups scripts/backup/backup.sh
#
# Writes BACKUP_DIR/<backup-id>/ with:
#   postgres.dump.gpg   pg_dump -Fc of the fundzim database as fundzim_backup (read-only role; one snapshot)
#   storage.tar.gpg     Garage data blocks + a consistent metadata snapshot (taken AFTER the database dump, so every
#                       object the database references is in it)
#   config.tar.gpg      deployment configuration (compose.yaml, deploy/, .env.example) — never .env or any secret
#   manifest.json       backup id, schema version, git commit, sizes and SHA-256 of every file, row counts and the
#                       audit chain heads used by the restore drill; authenticated by manifest.hmac
# Encryption: OpenPGP symmetric AES-256 (gpg, integrity-protected) under BACKUP_KEY_FILE, which must be mode 600 and live
# outside BACKUP_DIR (a backup never sits next to its key). The manifest MAC uses a key derived from the same file under a
# separate label. Secret-manager contents are NOT in this backup (docs/stage-18/backup-recovery.md §6).
set -euo pipefail
umask 077
cd "$(dirname "$0")/../.."

: "${BACKUP_KEY_FILE:?set BACKUP_KEY_FILE (64 hex characters, mode 600, outside BACKUP_DIR)}"
BACKUP_DIR="${BACKUP_DIR:-$HOME/fundzim-backups}"
ENV_NAME="${BACKUP_ENV_NAME:-development}"
[ -f .env ] || { echo "backup: .env missing (local stack only)" >&2; exit 1; }
set -a; . ./.env; set +a
: "${POSTGRES_BACKUP_PASSWORD:?the backup role password is missing from .env}"

key_mode=$(stat -c '%a' "$BACKUP_KEY_FILE")
[ "$key_mode" = "600" ] || [ "$key_mode" = "400" ] || { echo "backup: $BACKUP_KEY_FILE must be mode 600 or 400" >&2; exit 1; }
grep -Eqx '[0-9a-f]{64}' "$BACKUP_KEY_FILE" || { echo "backup: the key file must hold 64 lower-case hex characters" >&2; exit 1; }
mkdir -p "$BACKUP_DIR"; chmod 700 "$BACKUP_DIR"
key_real=$(realpath "$BACKUP_KEY_FILE"); dir_real=$(realpath "$BACKUP_DIR")
case "$key_real" in "$dir_real"/*) echo "backup: the key file must not live inside BACKUP_DIR" >&2; exit 1;; esac

BACKUP_ID="fundzim-${ENV_NAME}-$(date -u +%Y%m%dT%H%M%SZ)-$(head -c3 /dev/urandom | od -An -tx1 | tr -d ' \n')"
OUT="$BACKUP_DIR/$BACKUP_ID"
mkdir -m 700 "$OUT"
# a backup that did not finish is renamed so it can never be mistaken for a usable one (and is never deleted)
trap 'rc=$?; if [ $rc -ne 0 ] && [ -d "$OUT" ]; then mv "$OUT" "$OUT.INCOMPLETE"; echo "backup: FAILED (exit $rc): $OUT.INCOMPLETE" >&2; fi' EXIT
STARTED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
encrypt() { gpg --batch --quiet --yes --pinentry-mode loopback --passphrase-file "$BACKUP_KEY_FILE" --symmetric \
  --cipher-algo AES256 --s2k-digest-algo SHA512 --s2k-count 65011712 --compress-algo none -o "$1"; }
PGIMG=$(docker compose config --images 2>/dev/null | grep '^postgres' | head -1)
dur() { python3 -c "print(round($2 - $1, 3))"; }
log() { printf '%s backup: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# Facts the restore drill checks. The stack keeps running, so they are read just before and just after the dump: every
# counted table is append-only, so the restored value must lie between the two (inclusive).
facts() {
  docker compose exec -T -e PGPASSWORD="$POSTGRES_BACKUP_PASSWORD" fundzim-postgres psql -h 127.0.0.1 -U fundzim_backup -d fundzim -At -F '|' -c "
    SELECT 'schema_version', max(version_id)::text FROM public.goose_db_version WHERE is_applied
    UNION ALL SELECT 'ledger_transactions', count(*)::text FROM ledger.ledger_transactions
    UNION ALL SELECT 'ledger_entries', count(*)::text FROM ledger.ledger_entries
    UNION ALL SELECT 'audit_events_head', coalesce(max(seq), 0)::text FROM audit.audit_events
    UNION ALL SELECT 'security_audit_events_head', coalesce(max(seq), 0)::text FROM audit.security_audit_events
    UNION ALL SELECT 'recon_imports', count(*)::text FROM recon.imports
    UNION ALL SELECT 'payment_events', count(*)::text FROM app.payment_events" |
  awk -F'|' 'NF==2{printf "%s\"%s\": %s", (n++?", ":""), $1, $2}'
}
before=$(facts)

# 1. PostgreSQL: one consistent snapshot (pg_dump runs in a single REPEATABLE READ transaction)
log "postgres dump"
t0=$(date +%s.%N)
docker compose exec -T -e PGPASSWORD="$POSTGRES_BACKUP_PASSWORD" fundzim-postgres \
  pg_dump -h 127.0.0.1 -U fundzim_backup -d fundzim -Fc --no-password | encrypt "$OUT/postgres.dump.gpg"
t1=$(date +%s.%N)
after=$(facts)

# 2. Object storage: metadata snapshot (consistent sqlite copy), then blocks + metadata files
log "storage snapshot"
docker compose exec -T fundzim-storage /garage meta snapshot >/dev/null 2>&1
snap=$(docker run --rm -v fundzim_storage-meta:/m:ro --entrypoint sh "$PGIMG" -c 'ls -1t /m/snapshots | head -1')
[ -n "$snap" ] || { echo "backup: no Garage metadata snapshot was written" >&2; exit 1; }
docker run --rm -v fundzim_storage-meta:/m:ro -v fundzim_storage-data:/d:ro --entrypoint sh "$PGIMG" -ec "
  mkdir /meta
  for f in cluster_layout data_layout node_key node_key.pub peer_list; do [ ! -e /m/\$f ] || cp /m/\$f /meta/; done
  cp /m/snapshots/$snap/db.sqlite /meta/db.sqlite
  tar -C / -cf - meta d" | encrypt "$OUT/storage.tar.gpg"
t2=$(date +%s.%N)

# 3. Deployment configuration (no secrets: .env is never included)
log "configuration"
tar -cf - compose.yaml deploy .env.example | encrypt "$OUT/config.tar.gpg"
t3=$(date +%s.%N)

FINISHED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
python3 scripts/backup/manifest.py write "$OUT" "$BACKUP_KEY_FILE" <<EOF
{"backup_id": "$BACKUP_ID", "environment": "$ENV_NAME", "started_at": "$STARTED", "finished_at": "$FINISHED",
 "git_commit": "$(git rev-parse HEAD)", "garage_snapshot": "$snap",
 "durations_s": {"postgres": $(dur "$t0" "$t1"), "storage": $(dur "$t1" "$t2"), "config": $(dur "$t2" "$t3")},
 "facts_before_dump": {$before}, "facts_after_dump": {$after},
 "order": ["postgres", "storage", "config"], "encryption": "OpenPGP symmetric AES-256 (gpg), key file outside the backup",
 "excluded": [".env and every secret", "secret-manager contents (separate recovery procedure)", "Valkey (cache only)", "ClamAV signatures"]}
EOF
log "done: $OUT"
echo "$OUT"
