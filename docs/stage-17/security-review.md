# Stage 17 — Security review

Scope: `internal/reconciliation`, the payments/payouts/fees/ledger/storage/psp additions, migrations `20261022090000` and
`20261022090100`, the reconciliation API and admin pages, the mock and real E2E specs.

## 1. Threats and controls

| Threat | Control | Evidence |
|---|---|---|
| A forged or edited statement moves money | statements never move money by themselves: UNKNOWN outcomes need a maker-checker resolution applied by the owning module; settlement postings disabled; adjustments through the ledger's own maker-checker | `TestReconciliationPaymentsEndToEnd`, `TestReconciliationPayoutsUnknownOutcomeNeverResubmitted` |
| Self-approval / linked-account approval | Go identities check, CHECK `decided_by <> proposed_by`, trigger `recon.checker_guard` (`app.actor_identities`) | `TestReconciliationRuleSetsAreMakerChecker` (direct UPDATE refused), resolution self-approval refused in IT and E2E |
| Approval replay / stale approval | proposal hash bound (`approved_sha256 = proposal_sha256`), 24 h expiry CHECK, one live resolution, PENDING_REVIEW lock, keyed execution | `PROPOSAL_CHANGED` IT; racing checkers IT (exactly one execution) |
| Matching by amount (mis-allocation) | references find candidates, amounts only verify; bank lines never allocated by amount | `TestMatchNeverByAmount`, P4 line in IT |
| Malicious file (malware, polyglot, HTML) | private bucket quarantine, ClamAV before parsing, storage text sniffing (UTF-8, no control characters, no markup in the sniff window), declared = sniffed type, CSV/JSON only for `RECONCILIATION_REPORT` (Go + CHECK) | `TestReconciliationImportsAre…` (markup refused), storage unit tests |
| Parser attacks (huge files, quoting tricks, JSON floats, unknown fields) | 10 MiB cap before reading (MaxBytesReader), 256-character cells, 100 000 lines, strict `encoding/csv`, `DisallowUnknownFields`, strings only, digit-only amounts ≤ 10¹⁵, all-or-nothing | `TestParseRejectsBadEvidence` |
| CSV injection in exports | every exported cell starting with `=`, `+`, `-`, `@`, tab, CR neutralised; references cannot start with them at import | `TestCSVSafe`, export IT parses the CSV |
| Evidence tampering | SHA-256 at receipt = evidence record hash; storage hash-verifying reader; re-check before parsing (`EVIDENCE_HASH_MISMATCH`); lines immutable (column guard), no DELETE/TRUNCATE (`forbid_mutation`), `fundzim_app` has no UPDATE on history tables | IT: UPDATE/DELETE on `recon.items`, `recon.imports` hash, batch amounts refused |
| Duplicate import / replay | content hash unique, statement ref unique per source, line identity + hash, supersede once | IT + concurrency IT (6 concurrent uploads → 1 import) |
| Cross-tenant / unauthorised access | staff-only routes (non-staff 404), FINANCE / COMPLIANCE permissions only; support and reviewer roles have none (403); no owner or donor surface; reconciliation data carries no tenant-owned user data | IT and E2E permission checks |
| CSRF | staff mutations go through the platform CSRF middleware; the multipart upload sends the CSRF header | E2E upload |
| Rate limiting | staff routes behind the global per-IP limiter; uploads capped by size and the storage upload slots | — |
| Step-up | `reconciliation.import`, `.resolve`, `.rules.approve`, `.report.export` require step-up (permission flag; middleware) | IT/E2E step-up prompts |
| Financial data leakage | bank account numbers never stored (last four + HMAC); no payer names or phone numbers are part of the canonical format; audit metadata ids/codes/hashes only; validation reports echo field names and codes, never content; statement files C2 private | IT checks the account number is absent |
| Production misuse | `ProductionImportsEnabled`, `SettlementPostingEnabled`, `ProductionAdjustmentsEnabled` false; sources and imports CHECK-limited to TEST; sandbox generator refused in production | IT: production upload refused, PRODUCTION source insert refused |
| Worker crash / duplicate processing | SKIP LOCKED run claim, guarded transitions, heartbeat reaper, one transaction per decision set, idempotent parse | concurrency IT |

## 2. Residual risks

- A FINANCE officer can import a misleading TEST file; it cannot move money without a second officer, but it can create
  noise. Imports are audited with the uploader.
- Statement evidence is staff-supplied until a provider API exists (KI-S17-02).
- Heartbeat and staleness thresholds are engineering defaults (KI-S17-05).
- The development `.env` HMAC key protects the bank account hash; key rotation is Stage 18.

## 3. Privacy

Statement files may contain personal data in real use (payer names, MSISDNs); the canonical format has no field for them
and the parser refuses unknown fields. Retention of statements and lines is LEGAL_REVIEW_REQUIRED (LR-012; corrected in Stage 18: LR-076 is pool funding, not retention).
