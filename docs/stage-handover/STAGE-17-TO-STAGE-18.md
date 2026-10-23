# Stage 17 → Stage 18 handover (Security Hardening, Compliance Controls and Production Infrastructure Readiness)

Status of Stage 17: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 17
NOT_RECORDED). Stage 18 must not start until the owner asks for it. This document prepares it; it implements nothing.

## 1. What Stage 18 inherits from Stage 17

| Area | State | Where |
|---|---|---|
| Reconciliation module and `recon` schema | built; TEST evidence only; production imports, settlement postings and production adjustments disabled in code and by CHECK | ADR-049, docs/stage-17 |
| Statement evidence | private evidence bucket (C2, SSE-C, ClamAV), evidence records with SHA-256 | settlement-management.md, audit-evidence.md |
| Bank account references | last four + HMAC under the application blind-index key | bank-reconciliation.md |
| New permissions | `reconciliation.view/import/investigate/rules.request/rules.approve/report.export` (+ `run`, `resolve` from Stage 4); step-up on import, resolve, rules approve, export | migration 20261022090000 |
| Payout retry follow-up | RATE_LIMITED budgeted (KI-S17-01) | payout-reconciliation.md §5 |

## 2. Stage 18 topics

| Topic | Starting point / open question |
|---|---|
| Secrets management | OpenBao dev server locally; production refuses `SECRETS_PROVIDER=env`; operate a real secret manager (HA, audit, unseal), provider credentials only as `secretref://` (Stage 9) |
| Encryption and key rotation | SSE-C keys for private buckets, app AEAD and blind-index (HMAC) keys, TOTP secrets, webhook secrets: rotation is not implemented (storage `Open` refuses objects under another key id; the HMAC for bank account references and phone numbers would need a re-key plan) |
| Authentication hardening | staff MFA (TOTP), step-up windows, session lifetimes, WebAuthn for staff, break-glass review |
| Authorization review | the permission matrix across 17 stages (FINANCE now holds reconciliation, ledger, payouts, fees duties: review separation of duties and the need for a distinct RECONCILIATION_CHECKER role) |
| Penetration testing | external test of the API, admin portal (including the statement upload and CSV export), webhooks, storage |
| Dependency security | `govulncheck`, `npm audit` in CI already; SBOM, pinning, update cadence |
| Container security | non-root images, read-only filesystems, image scanning, base image updates |
| Infrastructure hardening | network policy, TLS everywhere, database roles per environment, backups of the ledger and recon schemas, WAF/rate limits at the edge |
| Backup and disaster recovery | append-only financial tables (ledger, recon, audit) need point-in-time recovery and restore drills; RPO/RTO are owner decisions |
| Audit retention | no retention period is set for ledger, recon, audit or notification records (LR-012; Stage 18 correction: LR-076 concerns pool funding): never purge until decided |
| Security monitoring | alerting on worker heartbeats, CRITICAL reconciliation exceptions, ledger integrity failures, step-up failures, upload rejections |
| Compliance evidence | map controls to evidence (operational-controls.md, audit-evidence-model.md); no compliance claim without evidence |
| Provider dependencies | ADR-040 deferred: no provider, no statement format, no settlement schedule, no fee evidence (PCR-007, PCR-016, PCR-045, PCR-051) |

## 3. Production readiness blockers (carried)

Provider selection (ADR-040); legal sign-off (LR-001, LR-002, LR-003, LR-012, LR-023, LR-076, LR-090 …); accounting
review of the chart and posting rules (KI-S10-02) — prerequisite for settlement postings (ADR-049 §8) and EC-20; owner
acceptance of Stages 0–17; production infrastructure (Stage 18–20). Production financial operations stay BLOCKED.

## 4. Constraints carried forward

- Only `ledger` writes ledger tables; only `reconciliation` writes `recon` tables; no settlement posting, no production
  import, no production adjustment without an ADR, accounting review, legal sign-off and acceptance.
- Unknown ≠ failed; an UNKNOWN payout is never resubmitted; statement evidence never moves money without a second person.
- Never match by amount alone; never combine or convert currencies.
- Never fabricate provider confirmations, settlement evidence, acceptance or CI results.
