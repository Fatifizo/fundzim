# Stage 18 — Backup and recovery

Scope: the local compose stack (development host). These procedures are what production will run on its own
infrastructure; no production backup exists.

## 1. Architecture

| Component | Method | Consistency | File in the set |
|---|---|---|---|
| PostgreSQL (all schemas) | `pg_dump -Fc` as `fundzim_backup` (read-only, `pg_read_all_data`, BYPASSRLS so a dump is never silently partial) | one REPEATABLE READ snapshot | `postgres.dump.gpg` |
| Object storage (Garage) | `garage meta snapshot` (consistent sqlite copy) + data blocks + layout/node files, **after** the database dump (every object the dump references exists) | snapshot + immutable content-addressed blocks | `storage.tar.gpg` |
| Deployment configuration | `compose.yaml`, `deploy/`, `.env.example` (never `.env`) | — | `config.tar.gpg` |
| Audit evidence | inside PostgreSQL (chains, evidence records, checkpoints) and private buckets | as above | — |
| Secrets manager | **not in this backup**: its own encrypted storage backup and unseal-key custody (§6) | — | — |
| Valkey, ClamAV signatures | not backed up (cache; re-downloaded) | — | — |

Identifiers: `fundzim-<environment>-<UTC timestamp>-<6 hex>`. Manifest `manifest.json`: id, environment, start/finish,
git commit, Garage snapshot name, per-step durations, every file's size and SHA-256, and facts read just before and just
after the dump (schema version, ledger transactions and entries, audit chain heads, reconciliation imports, payment
events). `manifest.hmac`: HMAC-SHA-256 under a key derived from the backup key (label `fundzim/backup-manifest/v1`).

## 2. Security

Encryption: OpenPGP symmetric AES-256 (GnuPG, integrity-protected, S2K SHA-512), every file. The key file (64 hex, mode
600 or 400) must be outside the backup directory — the script refuses otherwise. Files are written mode 600 into a
mode-700 directory. A set that did not finish is renamed `.INCOMPLETE` (never deleted, never mistaken for a good one).
Credential separation: the backup role's password is its own; the backup key is not any application key. Off-host
copies, object-lock/immutable storage, retention and split key custody are production infrastructure
(EXTERNALLY_PENDING; retention LEGAL_REVIEW_REQUIRED, LR-012). Monitoring: `fundzimctl backup record` stores each
successful backup and each restore drill in `app.ops_backup_runs` after verifying the manifest MAC; the security scan
alerts when no successful backup exists, the last is older than 26 hours or the latest failed, and when no verified
restore drill is younger than 35 days.

## 3. Procedures

```bash
BACKUP_KEY_FILE=~/.fundzim-backup.key BACKUP_DIR=~/fundzim-backups scripts/backup/backup.sh        # prints the set path
python3 scripts/backup/manifest.py verify <set> ~/.fundzim-backup.key                              # integrity only
python3 scripts/backup/drill_user.py prepare <state.json>                                          # optional: a TEST login to prove after restore
BACKUP_KEY_FILE=… scripts/backup/restore-drill.sh <set> --drill-user <state.json>                   # restore + verify (disposable)
BACKUP_KEY_FILE=… scripts/backup/failure-drills.sh <set>                                           # corruption / wrong key / tamper / truncation
go run ./apps/api/cmd/fundzimctl backup record -key KEY -dir <set> [-report drill.json]           # evidence
```

Restore order: secrets manager (or its keys) → PostgreSQL (roles from `deploy/docker/postgres/init`, then `pg_restore
--single-transaction --exit-on-error`) → object storage → configuration → API/worker → verification. Point-in-time
recovery needs WAL archiving (production PostgreSQL; not available on the development stack).

## 4. Restore verification (what the drill checks)

manifest MAC and hashes; schema version equals the manifest; restored counts lie within the before/after window;
`fundzimctl verify audit` (full recomputation of both chains); `fundzimctl verify ledger` (L1–L15);
`fundzimctl verify objects -sample 0` (every CLEAN object opened with its SSE-C key, SHA-256 compared); every
reconciliation import's evidence object present and CLEAN; the API starts against the restored data and is ready; a
TEST user created before the backup logs in, `/me` matches and a wrong password is refused.

## 5. Results (2026-10-23, development host: 16 vCPU, 30 GiB, local SSD)

Backup `fundzim-development-20261023T090624Z-cfb044` (database 599 MB encrypted, storage 52 MB, config 72 kB):
PostgreSQL 84.1 s, storage 4.7 s, configuration 0.3 s.

Restore drill on that backup — **PASSED**, recovery steps 173.0 s:

| Step | Seconds | | Check | Outcome |
|---|---|---|---|---|
| verify manifest | 2.2 | | manifest MAC and 3 file hashes | pass |
| start disposable PostgreSQL | 4.1 | | schema version 20261023090000 | pass |
| restore PostgreSQL (single transaction) | 131.8 | | counts within the dump window (ledger 9 374 tx / 19 316 entries, audit heads 1 317 110 and 37 015, 62 imports, 1 239 482 payment events) | pass |
| restore object storage | 2.7 | | audit chains (full): 1 317 110 rows INTACT in 5.6 s on 8 connections; 37 015 security rows INTACT | pass |
| build verification binaries | 3.0 | | ledger integrity L1–L15 | pass |
| verify audit / ledger / objects | 6.9 / 0.3 / 19.9 | | objects: 6 602 verified, 192 under throwaway test keys (not configured — expected), 0 integrity failures, 0 missing | pass |
| reconciliation evidence | 0.1 | | 62 imports, 62 evidence objects CLEAN | pass |
| API start / authentication | 1.1 / 0.5 | | API ready; drill user authenticated, `/me` matches, wrong password refused | pass |

Failure drills on copies of the same set — **PASSED** (each refused): one flipped byte (manifest hash mismatch), wrong
key (MAC fails; gpg "Bad session key"), manifest edited to match a corrupted file (MAC fails), a missing file, a
truncated dump restored directly (pg_restore end of file; 0 FundZim tables left).

Recorded with `fundzimctl backup record` (BACKUP and RESTORE_DRILL); a second record of the same set was skipped and a
wrong key was refused. Re-run on the final Stage 18 build: security-testing.md §5.

## 6. Secrets-manager recovery (procedure; no production store exists)

Back up the secret manager's storage backend with its own tooling (OpenBao raft snapshots), encrypted, off-host; keep
unseal/recovery keys split among named custodians (never in Git or next to backups); after a restore, re-authenticate
services (new AppRole secret ids), rotate any key whose exposure is suspected, and verify by starting the API with
`FIELD_ENCRYPTION_PROVIDER=secretstore`. The development OpenBao is in-memory: after a restart its contents are gone and
`fundzim-secrets-init` re-seeds the sandbox secrets (DR exercise: 3.3 s).
