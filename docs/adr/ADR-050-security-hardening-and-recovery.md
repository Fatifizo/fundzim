# ADR-050: Security hardening and recovery — keys from the secret manager with rotation overlap, MAC-checkpointed parallel audit verification, persisted security alerts, encrypted verified backups, hardened and segmented containers, supply-chain gates

- **Status:** Proposed
- **Date:** 2026-10-23
- **Deciders:** Technical lead (Stage 18)
- **Stage:** 18
- **Amends:** ADR-019 (audit evidence: verification strategy), ADR-035 (key handling: key source and rotation), ADR-041
  (secret management: application keys join provider credentials in the secret manager), ADR-046 (operations portal:
  persisted security alerts and backup evidence), Stage 9 payments audit (repeated status polls).

## Context

Stage 17 left production unable to start (`FIELD_ENCRYPTION_PROVIDER=kms` was "not implemented yet") and recorded
that full audit-chain verification took 36.6–40 s on a 1.3 M-row development table (KI-S17-12). A Stage 18 audit
(`docs/stage-18/prerequisite-assessment.md`) found: one key per purpose with no rotation path; a staff step-up bypass
through password change; unauthenticated internal readiness returning raw errors; permissive staging settings; a key
deny-list for logs that missed error texts; a flat container network with every capability; no backups, restore drills,
SBOMs, image scans or SAST. Production financial operations stay blocked by Stages 8–17 regardless of anything here.

## Decisions

1. **Application keys come from the secret manager (`FIELD_ENCRYPTION_PROVIDER=secretstore`).** Every key variable
   holds a `secretref://` reference resolved once at startup through the existing KV v2 client (ADR-041), before any
   service is built; a key that cannot be resolved stops the process (no fallback). `local` stays development/test
   only; `kms` is refused as not implemented (no KMS is selected — envelope encryption with an HSM-backed KMS remains
   the preferred end state and needs its own ADR). With this, a hardened production configuration validates (tested);
   the financial gates still refuse every live operation.
2. **Rotation with overlap.** AES-GCM field keys and SSE-C master keys take up to three previous keys
   (`*_PREVIOUS_KEYS`): new data always uses the active key; data under a previous key stays readable (GCM
   authenticates, so a wrong key fails cleanly); a retired key's data becomes unavailable — re-encrypt before retiring.
   Storage selects the SSE-C master by the key id recorded on each object. Blind-index (HMAC) keys and the CSRF secret
   are not rotatable without a re-index or session reset (KI-S18-04).
3. **Audit verification is split, never weakened.** `audit.verify_chain_range` verifies a contiguous range including
   the link to the row before it, so ranges on several connections cover the chain exactly like one pass (40.1 s →
   13.2 s on 8 connections for 1.3 M rows). The worker records every run in `audit.chain_verifications` and, when
   intact, a checkpoint `(seq, hash)` MAC'd under `AUDIT_CHECKPOINT_KEY`, a worker-only key the database does not hold;
   incremental runs re-verify the checkpoint row and everything after it (8 ms). Only checkpoints under the current key
   are used. Incremental every 15 min, full daily. A set-based rewrite was measured and rejected (no faster: the cost
   is the hashing). This is tamper **evidence**: a database superuser can still disable triggers; external anchoring of
   checkpoint heads is pending.
4. **Repeated identical status polls are not re-audited.** A GET_STATUS answer identical to the previous one is kept in
   `app.payment_attempts` and not written to the audit trail again (it was 1.07 M of 1.19 M audit rows). The first
   poll, every change and every other operation are still audited.
5. **Security alerts are persisted records owned by operations.** The worker's security scan evaluates fixed rules over
   module snapshots (each module reads only its own tables) — gate metrics not reported BLOCKED, audit verification
   failures or staleness, authentication anomalies, break-glass grants, critical reconciliation exceptions, failed
   sources, backup evidence — and keeps one alert per condition (`app.ops_security_alerts`, deduplicated, occurrences,
   cleared/recurred) with a database-guarded lifecycle OPEN → ACKNOWLEDGED → RESOLVED by people
   (`security.alert.manage`, step-up). Escalation declares an incident. Thresholds are engineering defaults. No
   external notification.
6. **Backups are encrypted, verified and recorded.** `scripts/backup/backup.sh` dumps PostgreSQL as a read-only
   `BYPASSRLS` role (a partial dump is refused, never produced), snapshots Garage after the database (so every
   referenced object is present) and encrypts every file with OpenPGP AES-256 under a key file kept outside the backup;
   a MAC-authenticated manifest lists hashes and bracketing counts. `restore-drill.sh` restores into disposable
   containers and verifies schema, counts, both audit chains, ledger integrity, every object's SHA-256, reconciliation
   evidence, API startup and a real login. Evidence goes to `app.ops_backup_runs` via `fundzimctl backup record`
   (MAC verified first). Secret-manager contents are not in the backup (their own recovery procedure).
7. **Containers are hardened and segmented.** Every container drops all capabilities (third-party images add back only
   what their entrypoint needs), has `no-new-privileges`, memory and PID limits; FundZim's containers run read-only;
   images are pinned by digest; the web container sits on an `edge` network with the API only. `internal/deploytest`
   checks compose, Dockerfiles and the CI workflow mechanically.
8. **Supply chain gates in CI.** gosec (reviewed `#nosec` exceptions only), CycloneDX SBOMs (syft), Trivy image scans
   that fail on fixable CRITICAL/HIGH and report everything else, binary checksums and an unsigned provenance record,
   all uploaded as artifacts. Signing waits for signing infrastructure.

## Consequences

Production configuration can now be validated end to end; production remains BLOCKED by the financial gates, provider
selection, accounting review, legal sign-off, real infrastructure and acceptance. New operator duties: key rotation
procedure, backup key custody, restore drills. New configuration: `AUDIT_CHECKPOINT_KEY`, `AUDIT_VERIFY_INTERVAL`,
`AUDIT_FULL_VERIFY_INTERVAL`, `*_PREVIOUS_KEYS`, `POSTGRES_BACKUP_PASSWORD`. Migration `20261023090000`.

## Alternatives considered

- **KMS envelope encryption now:** no KMS/HSM is selected or provisioned; inventing one would be fabricated
  infrastructure.
- **Raising the test timeouts for the chain check:** hides the growth; rejected (the brief forbids it).
- **Deleting old poll audit rows:** audit history is never deleted; only future duplicates are avoided.
- **Alerts derived on read only (Stage 14):** no acknowledgement, deduplication or history; kept for the dashboard,
  persisted alerts added on top.
