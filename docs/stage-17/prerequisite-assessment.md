# Stage 17 — Prerequisite assessment

Date: 2026-10-22. Branch `stage-17/reconciliation`, created from `stage-16/discovery` at `c80c39c`. Inputs read: CLAUDE.md,
README, docs/PRODUCT.md, ARCHITECTURE.md, SECURITY.md, ROADMAP.md, `docs/stage-handover/STAGE-16-TO-STAGE-17.md`,
ADR-030 and `docs/database/reconciliation-schema.md` (with the Stage 2 draft `design/sql/0017_reconciliation.sql`), the
Stage 8–16 implementation documents and the code of `payments`, `psp`, `ledger`, `payouts`, `fees`, `operations`,
`notifications`, `storage` and `audit`.

Classification: SATISFIED · SATISFIED_WITH_CONDITIONS · BLOCKED · NOT_VERIFIED.

## 1. Governance

| Item | Status | Evidence |
|---|---|---|
| Stage 16 remote CI | SATISFIED (observed) | GitHub Actions run 39383009700 on `c80c39c` (stage-16/discovery): completed, success, all 8 jobs (observed in the session that pushed it, 2026-10-20T07:13:22Z). Two earlier runs on that branch failed (39378221574: an integration test sharing the global per-IP limit; 39380352273: a real-stack test racing asynchronous re-indexing); both were test defects fixed by `55313c8` and `c80c39c` |
| Stage 16 documentation updated after CI | NOT_VERIFIED → corrected here | `docs/stage-16/testing.md` still said remote CI NOT_VERIFIED after the green run; Stage 17 records the run here and in the acceptance register row |
| Stage 16 pull request | NOT DONE | the GitHub API lists no pull request for the repository (2026-10-22); every stage branch is stacked on the previous one |
| Stage 16 merged | NOT DONE | `main` is at `a552de2` (Stage 0); nothing after Stage 0 is merged |
| Stage 16 acceptance | NOT RECORDED | `docs/governance/stage-acceptance-register.md` row 16: NOT_RECORDED (also rows 0–15). Never recorded by an engineer |
| Stage 16 real-stack discovery spec | DEFECT FOUND (fixed) | `apps/web/e2e-real/discovery.spec.ts` was not in any project of `playwright.real.config.ts`, so it never ran (locally or in CI) despite Stage 16's report. Wired in Stage 17; it passes (3 tests) — see testing.md |

## 2. Financial production restrictions (re-verified, unchanged)

| Control | Status |
|---|---|
| `psp.LiveExecutionEnabled = false`, kill switch, PRODUCTION execution refused at startup, `ck_*_no_production_stage9` | SATISFIED |
| `payouts.LiveExecutionEnabled = false`, `PAYOUTS_EXECUTION_ENVIRONMENT` DISABLED / LOCAL_SIMULATOR only, `ck_payout_requests_test_only_stage11` | SATISFIED |
| Ledger designated TEST, `ledger.ProductionPostingEnabled = false`, PRODUCTION refused by CHECK | SATISFIED |
| `fees.ProductionFeesEnabled = false`, provider fee evidence SYNTHETIC / UNVERIFIED only | SATISFIED |
| No provider selected (ADR-040 DECISION_DEFERRED); no settlement evidence; production available funds BLOCKED | BLOCKED (external) |
| Accounting review of the chart and posting rules (KI-S10-02) | BLOCKED (external) |
| Production secrets management operated (Stage 9/18) | BLOCKED (Stage 18) |

Stage 17 adds `ProductionImportsEnabled = false`, `SettlementPostingEnabled = false`, `ProductionAdjustmentsEnabled =
false` and TEST-only CHECKs on sources and imports (ADR-049).

## 3. Stage 11 payout retry review (KI-S15-19)

| Item | Status | Evidence |
|---|---|---|
| Unbounded same-reference resubmission after DEFINITELY_NOT_SENT | SATISFIED (fixed in the Stage 15 finalisation, `04c024f`, `558825d`) | `PAYOUTS_SUBMIT_RETRY_BUDGET` (default 5) with jittered backoff; SUBMISSION_RETRIES_EXHAUSTED halts submission (dispatcher check and `payout_attempts_guard`); tests `TestPayoutSubmitRetryBudgetHaltsAndResumes`, `TestPayoutHistoricalExcessiveAttemptsHaltedWithoutAnotherCall` |
| The historical loop has stopped | SATISFIED (observed) | development database, 2026-10-22: the five heaviest payouts have 731, 731, 729, 728 and 580 CREATE_PAYOUT attempts, the latest at 2026-10-19 08:54 UTC (before the fix was deployed); 0 attempts of any kind in the preceding 24 h; 26 SUBMISSION_RETRIES_EXHAUSTED exceptions open, 6 resolved. The history is kept (never deleted) |
| Residual gap: RATE_LIMITED | DEFECT FOUND (fixed, KI-S17-01) | a CREATE_PAYOUT classified RATE_LIMITED (psp: refused before processing, AFTER_BACKOFF) was treated by the guard and the dispatcher as "may have reached the provider": never re-dispatched, never counted against the budget, status-polled indefinitely without an exception. No RATE_LIMITED attempt exists in the development data (the simulator had no such scenario). Fixed by migration `20261022090100_payout_rate_limited_retry` and the dispatcher, budget and poller (ADR-049 §11); simulator scenario 13 RATE_LIMITED; test `TestReconciliationRateLimitedPayoutIsBudgeted` |

UNKNOWN payouts are still never resubmitted (unchanged guard); reservations are untouched by the fix.

## 4. Existing reconciliation building blocks

| Area | State before Stage 17 | Stage 17 use |
|---|---|---|
| `recon` schema | exists in the runtime grants (`fundzim_app` SELECT/INSERT/UPDATE), no tables | owned by the new `reconciliation` module |
| ADR-030 + Stage 2 draft 0017 | design only | implemented with the deviations in ADR-049 (names, lifecycle, posting order) |
| Payments | `payment_reconciliation_references` (never marked reconciled), provider evidence log with contradiction detection, source `RECON` allowed by Go and SQL rules but never produced | read facts by id/reference/window; `ApplyReconciliationOutcome` through `apply()` (source RECON) |
| Payouts | provider-status reconciliation and exceptions; source `RECON` allowed in SQL, never produced | read facts and a watch queue; `ApplyReconciliationOutcome` through `applyStatus` |
| Ledger | integrity checks, journal reconciliation, adjustments (maker-checker), `SETTLEMENT_MATCHED` / `SETTLEMENT_DISCREPANCY(_RESOLVED)` rules seeded but unused; `fundzim_app` has no ledger privilege | journal lookup by key, trial balance, integrity, adjustments; no settlement posting (ADR-049 §8) |
| Fees | recompute reconciliation; provider fee evidence SYNTHETIC/UNVERIFIED | assessed provider fee per payment/payout |
| Storage | private evidence bucket JPEG/PNG/PDF only; purpose `RECONCILIATION_REPORT` and owner `reconciliation` pre-registered | CSV/JSON allowed for that purpose only, ClamAV scan before parsing |
| Operations / notifications | module snapshots, exception centre, staff in-app notifications | reconciliation snapshot; five staff templates |
| Permissions | `reconciliation.run`, `reconciliation.resolve` seeded for FINANCE (Stage 4) | kept; six new ones |

## 5. Conditions Stage 17 works under

- No real statement, settlement report or bank statement exists or may be invented (PCR-007, PCR-016, PCR-045,
  PCR-051): the only formats are FundZim's canonical contracts; the only evidence is staff-supplied TEST files and
  statements generated from the sandbox simulator.
- Test payments are never posted (ADR-042): matched sandbox payments report `NOT_POSTED_TEST_PAYMENT`.
- Model A (ADR-013): the operating bank statement can only be matched against fee remittances.
- No settlement schedule is known: matching parameters are a maker-checker rule set, none seeded.

Overall: **SATISFIED_WITH_CONDITIONS** to build Stage 17 as non-production engineering; production BLOCKED.
