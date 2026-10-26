# Remediation register — current status after Phase 3 (2026-10-26)

This register supersedes `docs/remediation/phase-2/remediation-register.md` for the rows below. Every other row there is
unchanged. The Stage 20 registers stay the historical record.
- Nothing is closed by documentation.
- No owner, legal, accounting, provider, security or operational approval exists in the repository.

## 1. Findings

| ID | Severity | Root cause | Change (files) | Tests | Schema | Status |
|---|---|---|---|---|---|---|
| F-R2-01 | MEDIUM | Dedupe key without a check discriminator: MISSING_FEE per group and two AMOUNT_MISMATCH comparison pairs shared keys | `internal/fees/reconcile.go` | unit (3 tests, mutation-checked); integration `…FeeEveryMissingType` | none (keys are text; no data migration) | **FIXED (pending review and integration)** |
| F-R2-02 | MEDIUM | No representation of "required provider comparison could not be made"; engine deferral retries and batches were not bounded by the time budget | `internal/payouts/reconcile.go`, `internal/platform/reconsweep/sweep.go` | unit (judgeProvider ×18, engine ×3 new, mutation-checked); integration `…PayoutLookupFailure`, `…UnknownPayoutLookupFailure` | none | **FIXED (pending review and integration)** |
| F-R2-03 | LOW | Failed or stalled runs left no trace on the checkpoint; no alert rule; PAYMENT_FACTS skipped after a journal-sweep error | `migrations/20261026120000…`, `reconsweep/{health,sqlstore}.go`, `{payouts,fees,ledger}/ops.go`, `ledger/reconcile.go`, `operations/security.go`, `opsview`, OpenAPI | `TestHealthClassification`, `TestSweepRecordsEveryAttempt`, `TestSecurityFindingsReconciliationSweepHealth`, integration attempt assertions | **additive migration** (4 columns × 3 tables; guard accepts attempt-only updates) | **FIXED for visibility (pending review and integration)**. Thresholds are engineering defaults (D-06, D-10); no external alert delivery (B-12); a record whose examination always errors still stops its sweep (fails closed, now visible as FAILING) |
| F-R3-01 (new) | LOW (test reliability; latent product race) | Stage 18 `SecurityScan` observed before locking, so concurrent scans could set `last_seen_at < first_seen_at` and fail | `internal/operations/security.go` | `TestSecurityAlertsOutOfOrderScanTimes` (fails on the pre-fix code) | none | **FIXED (pending review and integration)** |
| KI-R1-07 | LOW (pre-existing test race) | The worker submits an approved payout before `TestPayoutApprovalSeparationOfDutiesAndInvalidation` suspends the destination | — | — | — | **OPEN.** Recurred in CI run 40305574851; makes CI red at random |
| Checkpoint forward moves (from the Phase 2 review) | — (design limit) | The database cannot know which records were examined | — | — | — | **OPEN, documented** (coverage-guarantees.md); detective control proposed, not implemented. Tech-lead and owner decision |

## 2. Launch blockers affected

| # | Blocker | Status after Phase 3 | Still required |
|---|---|---|---|
| B-05 | Reconciliation coverage | **IN_PROGRESS**. Engineering findings F-R2-01…03 fixed on the branch, with tests in isolated environments | Independent review (D-01a), integration through a protected PR, green CI on the integrated commit, D-06 coverage target and stall/alert thresholds, D-05 volumes |
| B-12 | Alerting, on-call | OPEN. Sweep-health in-app alerts exist; no delivery channel | D-10 |
| B-13 | Release governance | OPEN, IN_PROGRESS (unchanged since Phase 2). `main` still unprotected, no baseline | D-01, D-01a, D-12 |
| Others (B-01 … B-04, B-06 … B-11, B-14 … B-17) | — | unchanged | see Phase 2 register |

## 3. Owner decisions still required

| # | Decision | New input from Phase 3 |
|---|---|---|
| D-01, D-01a | Protection and independent reviewer | needed before this branch can be integrated through a protected PR |
| D-06 | Coverage freshness target | also sets the production stall, slow and deferral-age thresholds (engineering defaults: 1 h / 24 h / 24 h) |
| D-10 | Alert delivery and on-call | sweep-health alerts are in-app only; a destination must be named |
| D-05 | Volumes | the deferral limit (10 000) and the run budgets are engineering defaults |
| (tech lead and owner) | Accept the checkpoint forward-move design limit, or add the detective control | coverage-guarantees.md |
| D-12, D-13, D-14 and D-02 … D-11 | unchanged | — |
