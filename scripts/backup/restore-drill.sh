#!/usr/bin/env bash
# Stage 18 restore drill (docs/stage-18/backup-recovery.md §5). Restores a backup made by backup.sh into DISPOSABLE
# containers (their own network, volumes and 127.0.0.1 ports), verifies it, starts the API against it and logs in.
# It never writes to the running stack; it removes only the disposable resources it created (--keep leaves them).
#
#   BACKUP_KEY_FILE=~/.fundzim-backup.key scripts/backup/restore-drill.sh <backup-dir> [--drill-user STATE] [--keep]
#
# Prints a JSON report (also written to DRILL_REPORT, default <scratch>/restore-drill-<backup-id>.json): every step with
# its start, end and duration, and every check with its outcome. Exit 0 only when every check passed.
set -euo pipefail
umask 077
cd "$(dirname "$0")/../.."
BACKUP="${1:?usage: restore-drill.sh <backup-dir> [--drill-user STATE] [--keep]}"; shift
DRILL_USER=""; KEEP=0
while [ $# -gt 0 ]; do case "$1" in --drill-user) DRILL_USER="$2"; shift 2;; --keep) KEEP=1; shift;; *) echo "unknown $1" >&2; exit 2;; esac; done
: "${BACKUP_KEY_FILE:?set BACKUP_KEY_FILE}"
set -a; . ./.env; set +a
export PATH="$HOME/.local/go/bin:$PATH"

ID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]+'/manifest.json'))['backup_id'])" "$BACKUP")
SFX=$(head -c3 /dev/urandom | od -An -tx1 | tr -d ' \n')
WORK=$(mktemp -d "${TMPDIR:-/tmp}/fundzim-drill-$SFX.XXXX")
REPORT="${DRILL_REPORT:-$WORK/../restore-drill-$ID.json}"
NET=fz-drill-$SFX; PG=fz-drill-pg-$SFX; S3=fz-drill-s3-$SFX
VPG=fz-drill-pg-$SFX; VMETA=fz-drill-meta-$SFX; VDATA=fz-drill-data-$SFX
PGPORT=${DRILL_PG_PORT:-15432}; S3PORT=${DRILL_S3_PORT:-13900}; APIPORT=${DRILL_API_PORT:-18080}; INTPORT=$((APIPORT + 1))
PGIMG=$(docker compose config --images 2>/dev/null | grep '^postgres' | head -1)
S3IMG=$(docker compose config --images 2>/dev/null | grep '^dxflrs/garage' | head -1)
STEPS="$WORK/steps.jsonl"; CHECKS="$WORK/checks.jsonl"; : > "$STEPS"; : > "$CHECKS"
API_PID=""

cleanup() {
  [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true
  if [ "$KEEP" = 0 ]; then
    docker rm -f "$PG" "$S3" >/dev/null 2>&1 || true
    docker volume rm "$VPG" "$VMETA" "$VDATA" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
now() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
step() { # step <name> <command...>: timed; a failing step ends the drill (recorded)
  local name=$1; shift; local s e rc=0; s=$(date +%s.%N); local st; st=$(now)
  "$@" >"$WORK/$name.out" 2>"$WORK/$name.err" || rc=$?
  e=$(date +%s.%N)
  python3 -c "import json,sys;print(json.dumps({'step':sys.argv[1],'started_at':sys.argv[2],'finished_at':sys.argv[3],'seconds':round(float(sys.argv[5])-float(sys.argv[4]),3),'exit':int(sys.argv[6])}))" \
    "$name" "$st" "$(now)" "$s" "$e" "$rc" >> "$STEPS"
  if [ $rc -ne 0 ]; then echo "drill: step $name FAILED:" >&2; tail -n 20 "$WORK/$name.out" "$WORK/$name.err" >&2; finish 1; fi
}
check() { # check <name> <pass|fail> <detail-json>
  python3 -c "import json,sys;print(json.dumps({'check':sys.argv[1],'outcome':sys.argv[2],'detail':json.loads(sys.argv[3])}))" "$1" "$2" "$3" >> "$CHECKS"
}
finish() {
  python3 - "$ID" "$STEPS" "$CHECKS" "$REPORT" "$1" <<'PY'
import json, sys
bid, steps, checks, out, rc = sys.argv[1:6]
s = [json.loads(l) for l in open(steps)]
c = [json.loads(l) for l in open(checks)]
ok = rc == "0" and all(x["outcome"] == "pass" for x in c) and all(x["exit"] == 0 for x in s)
rep = {"backup_id": bid, "result": "PASSED" if ok else "FAILED", "steps": s, "checks": c,
       "recovery_seconds": round(sum(x["seconds"] for x in s), 3),
       "environment": "disposable containers on the development host (never production)"}
open(out, "w").write(json.dumps(rep, indent=2))
print(json.dumps(rep, indent=2))
PY
  exit "$1"
}

# ---- 1. integrity of the backup set -------------------------------------------------------------------------------------
step verify_manifest python3 scripts/backup/manifest.py verify "$BACKUP" "$BACKUP_KEY_FILE"
check manifest pass "$(cat "$WORK/verify_manifest.out")"
dec() { gpg --batch --quiet --pinentry-mode loopback --passphrase-file "$BACKUP_KEY_FILE" --decrypt "$1"; }

# ---- 2. disposable infrastructure ---------------------------------------------------------------------------------------
gen() { head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 32; }
for v in SU MIG APP WRK KYC CMP RO LED BAK; do eval "DP_$v=$(gen)"; done
start_pg() {
  docker network create "$NET" >/dev/null
  docker volume create "$VPG" >/dev/null; docker volume create "$VMETA" >/dev/null; docker volume create "$VDATA" >/dev/null
  docker run -d --name "$PG" --network "$NET" --shm-size 256m -p 127.0.0.1:$PGPORT:5432 \
    -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD="$DP_SU" -e POSTGRES_DB=postgres -e POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256 \
    -e POSTGRES_MIGRATOR_PASSWORD="$DP_MIG" -e POSTGRES_APP_PASSWORD="$DP_APP" -e POSTGRES_WORKER_PASSWORD="$DP_WRK" \
    -e POSTGRES_KYC_PASSWORD="$DP_KYC" -e POSTGRES_COMPLIANCE_PASSWORD="$DP_CMP" -e POSTGRES_READONLY_PASSWORD="$DP_RO" \
    -e POSTGRES_LEDGER_PASSWORD="$DP_LED" -e POSTGRES_BACKUP_PASSWORD="$DP_BAK" \
    -v "$VPG":/var/lib/postgresql/data -v "$PWD/deploy/docker/postgres/init":/docker-entrypoint-initdb.d:ro "$PGIMG" >/dev/null
  for _ in $(seq 1 120); do docker exec "$PG" pg_isready -U postgres -d fundzim >/dev/null 2>&1 && docker exec "$PG" psql -U postgres -d fundzim -Atc 'select 1' >/dev/null 2>&1 && return 0; sleep 1; done
  return 1
}
step start_postgres start_pg

# ---- 3. restore the database (all or nothing) ---------------------------------------------------------------------------
restore_pg() { dec "$BACKUP/postgres.dump.gpg" | docker exec -i "$PG" pg_restore -U postgres -d fundzim --exit-on-error --single-transaction; }
step restore_postgres restore_pg

# ---- 4. restore the object store ----------------------------------------------------------------------------------------
restore_s3() {
  dec "$BACKUP/storage.tar.gpg" | docker run -i --rm -v "$VMETA":/meta -v "$VDATA":/d --entrypoint tar "$PGIMG" -C / -xf -
  docker run -d --name "$S3" --network "$NET" -p 127.0.0.1:$S3PORT:3900 -e GARAGE_RPC_SECRET="$GARAGE_RPC_SECRET" -e GARAGE_ADMIN_TOKEN="$GARAGE_ADMIN_TOKEN" \
    -v "$PWD/deploy/docker/garage/garage.toml":/etc/garage.toml:ro -v "$VMETA":/var/lib/garage/meta -v "$VDATA":/var/lib/garage/data "$S3IMG" >/dev/null
  for _ in $(seq 1 60); do docker exec "$S3" /garage status >/dev/null 2>&1 && return 0; sleep 1; done
  return 1
}
step restore_storage restore_s3

# ---- 5. verification ----------------------------------------------------------------------------------------------------
BIN="$WORK/bin"; mkdir -p "$BIN"
step build_binaries go build -o "$BIN/" ./apps/api/cmd/fundzimctl ./apps/api/cmd/api
H=127.0.0.1:$PGPORT
DENV=(APP_ENV=development DATABASE_URL="postgres://fundzim_app:$DP_APP@$H/fundzim?sslmode=disable"
  DATABASE_MIGRATION_URL="postgres://fundzim_migrator:$DP_MIG@$H/fundzim?sslmode=disable"
  DATABASE_WORKER_URL="postgres://fundzim_worker:$DP_WRK@$H/fundzim?sslmode=disable"
  DATABASE_KYC_URL="postgres://fundzim_kyc:$DP_KYC@$H/fundzim?sslmode=disable"
  DATABASE_COMPLIANCE_URL="postgres://fundzim_compliance:$DP_CMP@$H/fundzim?sslmode=disable"
  DATABASE_LEDGER_URL="postgres://fundzim_ledger:$DP_LED@$H/fundzim?sslmode=disable"
  STORAGE_ENDPOINT="http://127.0.0.1:$S3PORT" AUDIT_CHECKPOINT_KEY="$(head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  HTTP_PORT=$APIPORT INTERNAL_HTTP_PORT=$INTPORT LOG_FORMAT=json PAYMENTS_SANDBOX_ENABLED=false PAYMENTS_EXECUTION_ENVIRONMENT=DISABLED
  PAYOUTS_EXECUTION_ENVIRONMENT=DISABLED REDIS_URL= REDIS_REQUIRED=false)

step schema_version env "${DENV[@]}" "$BIN/fundzimctl" migrate version
python3 - "$BACKUP/manifest.json" "$WORK/schema_version.out" "$CHECKS" <<'PY'
import json, re, sys
m = json.load(open(sys.argv[1])); out = open(sys.argv[2]).read()
want = m["facts_before_dump"]["schema_version"]; got = [int(x) for x in re.findall(r"\b(\d{14})\b", out)]
ok = bool(got) and all(g == want for g in got)
open(sys.argv[3], "a").write(json.dumps({"check": "schema_version", "outcome": "pass" if ok else "fail", "detail": {"manifest": want, "restored": got}}) + "\n")
PY

facts_restored() {
  docker exec "$PG" psql -U postgres -d fundzim -At -F '|' -c "
    SELECT 'ledger_transactions', count(*)::text FROM ledger.ledger_transactions
    UNION ALL SELECT 'ledger_entries', count(*)::text FROM ledger.ledger_entries
    UNION ALL SELECT 'audit_events_head', coalesce(max(seq), 0)::text FROM audit.audit_events
    UNION ALL SELECT 'security_audit_events_head', coalesce(max(seq), 0)::text FROM audit.security_audit_events
    UNION ALL SELECT 'recon_imports', count(*)::text FROM recon.imports
    UNION ALL SELECT 'payment_events', count(*)::text FROM app.payment_events"
}
step restored_facts facts_restored
python3 - "$BACKUP/manifest.json" "$WORK/restored_facts.out" "$CHECKS" <<'PY'
import json, sys
m = json.load(open(sys.argv[1])); lo, hi = m["facts_before_dump"], m["facts_after_dump"]
got = {k: int(v) for k, v in (l.split("|") for l in open(sys.argv[2]).read().split() if "|" in l)}
bad = {k: [lo[k], got.get(k), hi[k]] for k in got if not (lo[k] <= got[k] <= hi[k])}
open(sys.argv[3], "a").write(json.dumps({"check": "restored_counts_within_dump_window", "outcome": "fail" if bad else "pass",
    "detail": {"restored": got, "outside_window": bad}}) + "\n")
PY

step verify_audit env "${DENV[@]}" "$BIN/fundzimctl" verify audit
check audit_chains_full_verification pass "$(python3 -c "import json,sys;print(json.dumps([{k:r[k] for k in ('chain','to_seq','outcome','duration_ms','workers')} for r in json.load(open(sys.argv[1]))]))" "$WORK/verify_audit.out")"
step verify_ledger env "${DENV[@]}" "$BIN/fundzimctl" verify ledger
check ledger_integrity pass "$(python3 -c "import json,sys;d=json.load(open(sys.argv[1]));print(json.dumps({'passed':d['passed'],'checks':[(c['code'],c['status'],c['checked']) for c in d['checks']]}))" "$WORK/verify_ledger.out")"
step verify_objects env "${DENV[@]}" "$BIN/fundzimctl" verify objects -sample 0
check objects_sha256_and_sse_keys pass "$(python3 -c "import json,sys;d=json.load(open(sys.argv[1]));d.pop('failed_ids');d.pop('description');print(json.dumps(d))" "$WORK/verify_objects.out")"
recon_evidence() {
  docker exec "$PG" psql -U postgres -d fundzim -At -c "SELECT count(*), count(o.id) FILTER (WHERE o.scan_status = 'CLEAN' AND o.deleted_at IS NULL)
    FROM recon.imports i LEFT JOIN app.stored_objects o ON o.id = i.stored_object_id"
}
step recon_evidence recon_evidence
r=$(tail -n 1 "$WORK/recon_evidence.out"); total=${r%%|*}; present=${r##*|}
[ "$total" = "$present" ] && o=pass || o=fail
check reconciliation_evidence_records "$o" "{\"imports\": $total, \"evidence_objects_clean\": $present}"

# ---- 6. application startup and authentication against the restored data ------------------------------------------------
start_api() {
  env "${DENV[@]}" "$BIN/api" >"$WORK/api.log" 2>&1 &
  API_PID=$!
  for _ in $(seq 1 60); do curl -fsS "http://127.0.0.1:$APIPORT/api/v1/ready" >/dev/null 2>&1 && return 0; sleep 1; done
  tail -n 20 "$WORK/api.log"; return 1
}
step start_api start_api
check api_ready pass "{\"ready\": true, \"port\": $APIPORT}"
if [ -n "$DRILL_USER" ]; then
  step authentication python3 scripts/backup/drill_user.py check "$DRILL_USER" "http://127.0.0.1:$APIPORT"
  check authentication pass "$(tail -n 1 "$WORK/authentication.out")"
fi
finish 0
