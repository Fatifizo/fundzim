# Stage 18 — Incident response

Extends `docs/SECURITY.md` §22 and the Stage 14 incident records (`app.ops_incidents`, `/admin/operations/incidents`).
Named roles, not people: **Incident commander (IC)** — SECURITY_ADMIN on duty; **Finance lead**; **Compliance lead**;
**Technical lead**; **Communications** (owner-designated). Contact details are kept outside the repository by the owner.
Regulatory or data-protection notifications are decided by the Compliance lead with legal counsel — this document never
decides that a notification is or is not required (LR-010, LR-012 and successors).

## 1. Framework

| Phase | Actions | Records |
|---|---|---|
| Detection | security alert (`/admin/security`), staff report, provider notice, monitoring | alert id or incident declared (SEV1–SEV4) |
| Triage | IC acknowledges, sets severity and owner, opens the timeline | `ops_incident_events` |
| Containment | the runbook's containment steps; prefer reversible actions (suspend, revoke, hold, kill switch) over destructive ones | timeline entries |
| Investigation | audit search (`/admin/audit`), `fundzimctl verify audit|ledger|objects`, logs (scrubbed), database read-only role | evidence records (hashes) |
| Evidence preservation | never delete or edit logs, audit rows, objects or backups; snapshot volumes before any repair; hash and record exports | evidence records, legal hold (`audit.evidence_holds`, maker-checker) |
| Recovery | restore from verified backups (backup-recovery.md), rotate credentials (secrets-management.md), re-verify | drill-style report |
| Post-incident review | within 5 working days: timeline, root cause, control gaps, actions with owners | incident `post_incident_notes` |

Categories: account compromise, financial fraud, data exposure, infrastructure compromise, secrets leakage, ransomware,
provider compromise, service outage. Severity: SEV1 (money, KYC data, audit integrity or platform-wide outage), SEV2
(single-tenant data, privileged account), SEV3 (degraded control), SEV4 (low impact).

**Never:** delete forensic evidence automatically, disable an audit trigger, move money to "fix" a balance, release a
hold or reservation outside its maker-checker path, resubmit an UNKNOWN payout, or announce externally without the
Compliance lead.

## 2. Containment tools that exist

Staff session revocation (`session.revoke`), staff suspension (`staff.suspend`), role revocation (dual-controlled),
kill switches `payments.live_execution.enabled` / `payouts.live_execution.enabled` (already off; Go constants false),
risk holds (WITHDRAWAL_HOLD, PAYOUT_DESTINATION_HOLD), compliance restrictions, provider routing disable, rate limits,
container stop (`docker compose stop <service>`), secret-manager token/AppRole revocation, credential rotation.

## 3. Runbooks

### 3.1 Compromised administrator
1. Revoke the account's sessions and suspend it (two SECURITY_ADMINs if the suspect is a SECURITY_ADMIN). 2. Revoke
pending role requests and break-glass grants it requested or approved. 3. Search audit for its actions since the
suspected time (`actor_id`); list approvals it gave as checker; freeze affected payouts with holds. 4. Rotate any secret it
could read. 5. Re-run `fundzimctl verify ledger` and `verify audit`. 6. Re-issue access through a new named identity.

### 3.2 Exposed API secret (provider credential, webhook secret, application key)
1. Identify the secret by name (never paste the value into tickets). 2. Rotate: provider/webhook secrets → new
CURRENT, old as PREVIOUS only as long as needed; application keys → secrets-management.md §4. 3. Revoke the old value
at the source. 4. Search logs and audit for use of the old value window. 5. If the secret was in Git: rotate first, then
rewrite history only with owner approval (history rewrite never replaces rotation).

### 3.3 Suspected database breach
1. Snapshot the volume (preserve). 2. Rotate every database role password and the application keys. 3. Check
`pg_stat_activity` / logs for unknown roles or addresses; restrict network access. 4. Verify audit chains and ledger
integrity. 5. Compliance lead assesses data exposure (C3 KYC data is field-encrypted; keys are in the secret manager).

### 3.4 Unauthorized payout attempt
1. Do not cancel or resubmit at the provider without evidence. 2. Place WITHDRAWAL_HOLD / PAYOUT_DESTINATION_HOLD. 3.
Check approvals (maker ≠ checker, linked identities) and destination change history (step-up, invalidated approvals).
4. If UNKNOWN: wait for authoritative status (never resubmit). 5. Reconcile against provider evidence (Stage 17).

### 3.5 Ledger integrity alarm (LEDGER_INTEGRITY_FAILURE)
1. **Stop and report** (CLAUDE.md financial rule 13): no data edits, no loosened checks. 2. Run `fundzimctl verify ledger`,
record the failing checks and samples. 3. Halt postings by stopping the affected producers (outbox consumers) if the
failure is spreading. 4. Corrections only through maker-checker reversals/adjustments after root cause.

### 3.6 Reconciliation evidence tampering
1. Compare the stored SHA-256 with the evidence record and the file. 2. Do not delete the import; mark it superseded only
through the explicit supersede path. 3. Restrict `reconciliation.import`; review audit for the upload. 4. Re-import the
provider's original file under dual control.

### 3.7 Object-storage exposure
1. Revoke and rotate the affected S3 key pair (one per bucket). 2. Check bucket policies (private buckets have no public
access). 3. `fundzimctl verify objects -sample 0`. 4. SSE-C objects are unreadable without the master key — confirm the key
was not exposed; if it was, rotate the master (overlap) and plan re-encryption.

### 3.8 Ransomware incident
1. Isolate affected hosts (network), keep them powered for forensics. 2. Do not pay; involve the owner and counsel. 3.
Restore into clean infrastructure from the latest verified backup (restore drill procedure) whose key custody is
separate. 4. Rotate every credential. 5. Verify audit and ledger before reopening.

### 3.9 Failed backup (BACKUP_FAILED / BACKUP_STALE)
1. Read the backup output (the set is renamed `.INCOMPLETE`, never deleted). 2. Fix the cause (disk, role, key file
permissions). 3. Run a backup and a restore drill; record both with `fundzimctl backup record`. 4. Resolve the alert with
the run ids.

### 3.10 Compromised CI pipeline
1. Disable the workflow (repository settings) and revoke any tokens. 2. Compare action SHAs with upstream releases;
check workflow file history. 3. Rebuild from a clean runner; compare binary checksums and SBOMs with the last good
provenance record. 4. CI has no deployment rights and no secrets — confirm nothing was published.

## 4. Exercises

Stage 18 executed the technical parts that exist: backup failure drills (corrupt/wrong key/tampered/missing/truncated —
all refused), the restore drill, and DR exercises (disaster-recovery.md). No tabletop exercise with people has been held
(owner action).

## 5. Penetration-test package (prepared, not performed)

| Item | Content |
|---|---|
| Architecture | security-architecture.md, ARCHITECTURE.md, OpenAPI contract |
| Attack surface | public web pages, `/api/v1` public routes (auth flows, public campaigns, discovery, webhooks), staff portal `/admin`, uploads (campaign media, KYC documents, statements), CSV exports |
| Test accounts | created on a disposable stack through `scripts/e2e/bootstrap-admins.sh` and the invitation flow (never production; credentials handed over out of band) |
| Roles | SUPER_ADMIN, ADMIN, SECURITY_ADMIN, FINANCE, COMPLIANCE, REVIEWER, KYC_REVIEWER, SUPPORT, BUSINESS_APPROVER; users and organisation members |
| Financial safety constraints | sandbox simulator only; no real provider; production gates cannot be turned on by testers; do not attempt to bypass by editing the database |
| Known issues | known-issues.md (F-S18-*, KI-S18-*) |
| Environment | disposable compose stack on an isolated host; data seeded by tests |
| Rules of engagement | written authorisation from the owner; scope and dates; no denial-of-service; no social engineering of staff; stop on finding C3 data; report within 24 h for CRITICAL |
| Logging and contacts | IC role on call during the window; logs retained for the test period |

No independent penetration test has taken place (KI-S18-11).
