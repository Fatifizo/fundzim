# Stage 20 — Infrastructure, backup, disaster recovery and environment hygiene

**Area result: BLOCKED.** Nothing is provisioned beyond the local development stack; the deployment architecture is a
Stage 18 proposal; RPO/RTO are unapproved. The backup and restore *mechanism* was re-proven in Stage 20 on an isolated
stack after fixing a defect the drill exposed.

## 1. Questions of brief §14

| Question | Answer and evidence | Status |
|---|---|---|
| Architecture provisioned? | No (KI-S18-01); Stage 18 deployment-security.md is a proposal | BLOCKED |
| Single-node or HA? | single-node; no HA claimed (KI-S18-20) | decision required |
| Backups encrypted? | yes: OpenPGP AES-256 per file, MAC-authenticated manifest | PASS |
| Backup keys protected independently? | key file mode 600 outside the backup directory (enforced by `backup.sh`); no KMS/HSM, no off-host escrow | PASS WITH CONDITIONS |
| Restore tests repeatable? | **yes after a fix** — §3 | PASS WITH CONDITIONS |
| Recovery procedures documented? | Stage 18 backup-recovery.md, disaster-recovery.md, runbooks | PASS |
| Monitoring detects service failure? | health/readiness endpoints exist; nothing watches them outside compose healthchecks | BLOCKED |
| Stopped container restarted by the real supervisor? | Docker `unless-stopped` does not restart a container stopped with `docker kill` (KI-S18-19); no supervisor (systemd/orchestrator) with health-based restarts is defined | BLOCKED |
| Database and object storage recovery coordinated? | yes: the database is dumped first, then a Garage snapshot taken **after** it (every referenced object present); since Stage 20 both come from the same compose project (§3) | PASS |
| Secrets and configuration restorable? | configuration is in the backup; secret-manager contents are **not** (Stage 18 §6) — restoring requires the production secret manager's own backup (not provisioned) | BLOCKED |
| Migration rollback strategy | forward-only in production; Downs tested in CI's destructive round trip (run 40034115480); no concurrent index-build procedure (KI-S19-15) | PASS WITH CONDITIONS |
| Full infrastructure loss tested? | no — only local restore into disposable containers | NOT_VERIFIED |

RPO/RTO: Stage 18 proposals (RPO ≤ 24 h with dumps / ≤ 5 min with WAL archiving; RTO ≤ 4 h full restore) are **not
approved** (KI-S18-12). No target is invented here.

## 2. Measured recovery figures (development host; not production)

| Exercise | Result |
|---|---|
| Stage 20 backup of the disposable stack | 9 s (PostgreSQL dump, Garage snapshot, configuration) |
| Stage 20 restore drill | **PASSED**, 8/8 checks, 24.2 s of recovery steps: manifest, schema version, restored counts within the dump window, both audit chains INTACT (36 725 and 1 393 rows), ledger integrity L1–L15, objects (317 sampled: 311 verified, 6 under test-only throwaway keys, **0 integrity failures, 0 missing**), 15/15 reconciliation evidence objects, API ready — `evidence/stage-20/restore-drill.json` |
| Stage 20 failure drills | **PASSED**: corrupt, wrong key (manifest and decrypt), tampered manifest, missing file, truncated restore, truncated restore leaves nothing — all refused (`evidence/stage-20/failure-drills.json`) |
| Stage 18 (for comparison) | restore 178.7 s on the 1.3 M-row development data set |

## 3. Defects found by repeating the drill

| ID | Defect | Effect | Fix | Verified |
|---|---|---|---|---|
| F-S20-07 (HIGH) | `scripts/backup/backup.sh` took the database dump and the Garage snapshot through `docker compose` (honouring the compose project) but read the Garage volumes by the **hard-coded** names `fundzim_storage-meta` / `fundzim_storage-data`. Backing up any other project produced an **inconsistent set**: that project's database with the default project's object store and an old snapshot; `backup.sh` reported success | first Stage 20 drill: 311 of 317 objects missing — the restore drill's verification refused it (the control worked; the backup step did not) | volumes derived from the active project (`docker compose config`), both must exist, the snapshot must be new in that volume (fail closed), and the manifest records `compose_project` | second drill PASSED; manifest shows `compose_project: fundzim-s20` |
| F-S20-08 (LOW) | the drill and other scripts used the obsolete `tail -N`; on this host (Ubuntu 25.10, uutils coreutils 0.2.2) `tail -20 a b` fails, so a failing drill step printed a usage error instead of its output | diagnosis hidden | `tail -n N` everywhere in `scripts/` | drill output shown correctly |

These matter for production: a backup procedure that silently mixes sources would have passed until the day it was
needed. The deployment must run the restore drill against every backup type it uses.

## 4. Environment hygiene (brief §17)

**Affected data (KI-S19-17).** The development database holds the Stage 19 load data: ~11 800 TEST sandbox payments and
payouts, the ~23 600 Stage 17 MISSING_SETTLEMENT exceptions those created, and ~7 600 OPEN `UNRECONCILED_PROVIDER_FEE`
items. All are TEST records (simulator, `execution_environment` LOCAL_SIMULATOR, synthetic or TEST ledger), owned by no
other person, in append-only domains: payments events, ledger, audit and `recon` are never deleted by project rule.

**Decision: left untouched.** Deleting them would violate the append-only rules and the audit chain; there is no
supported purge path. The only clean remedy is to recreate the local development volumes (`docker compose down -v`),
which is **destructive** to all local development data and therefore an owner/developer choice, not taken here.

**Remediation plan (implemented as a practice in Stage 20).**
1. Load, endurance and fault tests run on a disposable compose project (`docker compose -p <name>`), as Stage 20 did,
   never on the shared development database (CLAUDE.md Stage 19 rule, now demonstrated).
2. Backup tooling honours the project (F-S20-07) so drills can run against disposable stacks.
3. Integration tests keep scoping assertions to their own records (Stage 17–19 rules).

**Production/test separation (verified in code).** PRODUCTION payment/payout/ledger/fee/reconciliation records are
refused by CHECKs; startup gates refuse PRODUCTION execution environments; the ledger is designated TEST; secrets are
`secretref://fundzim/<environment>/…` references (CHECK); production refuses `SECRETS_PROVIDER=env`; queues and buckets
exist only on the local stack. No production database, queue, bucket or credential exists that could be confused.
