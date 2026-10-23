#!/usr/bin/env bash
# Stage 18 backup failure injection (docs/stage-18/backup-recovery.md §7). Works on COPIES of a backup in a scratch
# directory; the original backup is only read. Each scenario must be refused:
#   corrupt     one flipped byte in an encrypted file          → manifest verification fails
#   wrong_key   verification and decryption with another key   → MAC and decryption fail
#   tampered    a manifest edited to match a corrupted file    → MAC fails (the manifest cannot be "fixed")
#   truncated   a truncated database dump restored directly    → pg_restore fails and leaves no FundZim table
#   missing     a file removed from the set                    → manifest verification fails
#
#   BACKUP_KEY_FILE=~/.fundzim-backup.key scripts/backup/failure-drills.sh <backup-dir>
set -euo pipefail
umask 077
cd "$(dirname "$0")/../.."
SRC="${1:?usage: failure-drills.sh <backup-dir>}"
: "${BACKUP_KEY_FILE:?}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/fundzim-faildrill.XXXX")
PG=fz-faildrill-$(head -c3 /dev/urandom | od -An -tx1 | tr -d ' \n')
trap 'docker rm -f "$PG" >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT
PGIMG=$(docker compose config --images 2>/dev/null | grep '^postgres' | head -1)
results=()
expect_fail() { # expect_fail <scenario> <command...>
  local name=$1; shift
  if "$@" >"$WORK/$name.out" 2>&1; then results+=("{\"scenario\":\"$name\",\"outcome\":\"NOT_DETECTED\"}"); else
    results+=("{\"scenario\":\"$name\",\"outcome\":\"refused\",\"evidence\":$(tail -c 300 "$WORK/$name.out" | python3 -c 'import json,sys;print(json.dumps(sys.stdin.read().strip()))')}"); fi
}
copyset() { rm -rf "$WORK/set"; cp -r "$SRC" "$WORK/set"; }

copyset; printf '\x00' | dd of="$WORK/set/storage.tar.gpg" bs=1 seek=4096 count=1 conv=notrunc 2>/dev/null
expect_fail corrupt python3 scripts/backup/manifest.py verify "$WORK/set" "$BACKUP_KEY_FILE"

copyset; head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$WORK/other.key"; chmod 600 "$WORK/other.key"
expect_fail wrong_key_manifest python3 scripts/backup/manifest.py verify "$WORK/set" "$WORK/other.key"
expect_fail wrong_key_decrypt sh -c "gpg --batch --quiet --pinentry-mode loopback --passphrase-file '$WORK/other.key' --decrypt '$WORK/set/config.tar.gpg' >/dev/null"

copyset; printf '\x00' | dd of="$WORK/set/config.tar.gpg" bs=1 seek=100 count=1 conv=notrunc 2>/dev/null
python3 - "$WORK/set" <<'PY'
import hashlib, json, os, sys
d = sys.argv[1]; m = json.load(open(d + "/manifest.json"))
p = d + "/config.tar.gpg"; m["files"]["config.tar.gpg"]["sha256"] = hashlib.sha256(open(p, "rb").read()).hexdigest()
open(d + "/manifest.json", "w").write(json.dumps(m, indent=2, sort_keys=True))
PY
expect_fail tampered_manifest python3 scripts/backup/manifest.py verify "$WORK/set" "$BACKUP_KEY_FILE"

copyset; rm "$WORK/set/config.tar.gpg"
expect_fail missing_file python3 scripts/backup/manifest.py verify "$WORK/set" "$BACKUP_KEY_FILE"

# truncated dump: decrypt, cut to half, restore into a fresh disposable PostgreSQL in one transaction
docker run -d --name "$PG" -e POSTGRES_PASSWORD=drill -e POSTGRES_DB=fundzim "$PGIMG" >/dev/null
for _ in $(seq 1 60); do docker exec "$PG" pg_isready -U postgres -d fundzim >/dev/null 2>&1 && break; sleep 1; done
sleep 2
gpg --batch --quiet --pinentry-mode loopback --passphrase-file "$BACKUP_KEY_FILE" --decrypt "$SRC/postgres.dump.gpg" > "$WORK/dump"
truncate -s $(( $(stat -c %s "$WORK/dump") / 2 )) "$WORK/dump"
expect_fail truncated_restore sh -c "docker exec -i '$PG' pg_restore -U postgres -d fundzim --exit-on-error --single-transaction --no-owner < '$WORK/dump'"
left=$(docker exec "$PG" psql -U postgres -d fundzim -Atc "SELECT count(*) FROM pg_tables WHERE schemaname IN ('app','ledger','audit','recon','kyc','risk','compliance')")
results+=("{\"scenario\":\"truncated_restore_leaves_nothing\",\"outcome\":\"$([ "$left" = 0 ] && echo refused || echo PARTIAL_RESTORE)\",\"evidence\":\"$left FundZim tables after the failed restore\"}")
rm -f "$WORK/dump"

printf '%s\n' "${results[@]}" | python3 -c '
import json, sys
r = [json.loads(l) for l in sys.stdin]
ok = all(x["outcome"] == "refused" for x in r)
print(json.dumps({"result": "PASSED" if ok else "FAILED", "scenarios": r}, indent=2))
sys.exit(0 if ok else 1)'
