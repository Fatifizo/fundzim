# Stage 10 — Known issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S10-01 | Formal acceptance of Stages 1–9 (and 10) is not recorded; Stage 9 remains PARTIAL (carries KI-S9-01) | Stage 10 is non-production engineering only | OPEN — owner |
| KI-S10-02 | The chart of accounts, posting rules and donation accounting model have **no accounting review** (accountant/auditor) | Must be reviewed before any production ledger | OPEN — owner |
| KI-S10-03 | **Production posting BLOCKED** by design: TEST ledger only; `ProductionPostingEnabled = false`; no provider (KI-S9-02), no production payments (KI-S9-03), legal items LR-001/002/003/076 open | No real money is recorded | BLOCKED (by design) |
| KI-S10-04 | Settlement reconciliation NOT_PERFORMED: no settlement statements or evidence (PCR-007/016); statement ingestion is Stage 17 | `psp_settled` / RELEASE exercised only by synthetic fixtures | BLOCKED |
| KI-S10-05 | Immutability holds against application roles only. A superuser, the table owner or someone with storage/backup access can alter data; no journal hash chain or external anchoring | Tamper evidence relies on the audit hash chain and backups | OPEN |
| KI-S10-06 | Production ledger database designation, hosting, backup schedule, PITR and DR are not set up (Stage 18/20); the restore test in [financial-integrity-tests.md](financial-integrity-tests.md) is a local logical dump/restore, not a DR certification | — | OPEN |
| KI-S10-07 | Retention of ledger records is LEGAL_REVIEW_REQUIRED (LR-012); nothing is purged and no purge exists | Data kept indefinitely | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S10-08 | A reconciliation run scans at most 10 000 payment journals and 5 000 succeeded payments (no batching cursor) | Sufficient for TEST volumes; must page before production | OPEN |
| KI-S10-09 | No "balance as of timestamp" API; trial balance is whole-ledger or per period | Historical balances need a recompute | OPEN |
| KI-S10-10 | Self-approval prevention compares staff user ids (maker ≠ checker); it does not resolve other linked identities | Same as other staff maker-checker flows | ACCEPTED |
| KI-S10-11 | Payment postings are asynchronous (outbox consumer), so a payment can briefly exist without its journal; reconciliation detects gaps after 5 min (ADR-042 §3) | Eventual consistency | ACCEPTED |
| KI-S10-12 | Platform fees are designed in `DONATION_CAPTURED` but no fee schedule exists (fees, LR items); fixtures carry no platform fee | — | DEFERRED |
| KI-S10-13 | ZWG cannot be posted (minor units unverified, LR-043/PCR-018) | USD only | BLOCKED — legal/provider |
| KI-S10-14 | Restricted-fund and agency treatment of campaign money is not modelled (LR-002) | Accounting representation only | OPEN — LEGAL_REVIEW_REQUIRED |
| KI-S10-15 | Campaign freeze and payouts do not post (`CAMPAIGN_FROZEN`, `PAYOUT_*` rules exist, unused until Stage 11+) | — | DEFERRED (Stage 11) |
| KI-S10-16 | Concurrent PostOnce throughput is bounded by the serial audit hash chain (each posting appends one audit event); see [performance.md](performance.md) | Throughput ceiling, not a correctness issue | OPEN |

## Stage 9 known issues

KI-S9-01..16 remain as defined in [../stage-9/known-issues.md](../stage-9/known-issues.md); their effect on the ledger
is in [prerequisite-assessment.md](prerequisite-assessment.md) §2. None is resolved by Stage 10.
