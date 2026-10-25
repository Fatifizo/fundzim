# Stage 20 — Financial correctness certification

Candidate code: `16429d2` (identical under `internal/` and `migrations/` to the candidate head `2015978`;
`git diff 16429d2 2015978 -- internal migrations` is empty). Result categories as in the Stage 20 brief.

**Area result: PASS WITH CONDITIONS (engineering) — production BLOCKED.** The invariants hold under every executed
test, on clean data, at the candidate commit. Conditions: reconciliation coverage (§5, launch-blocking), accounting
review (KI-S10-02), provider semantics (§6 of provider-readiness.md).

## 1. Executed evidence at the candidate commit

| Evidence | Where | Data isolation | Result |
|---|---|---|---|
| Full integration suite, **disposable compose project** started from empty volumes (Stage 20, 2026-10-25), `FUNDZIM_IT_PERF=1` as in CI | `go test -tags integration ./tests/integration/...` against `docker compose -p fundzim-s20` | isolated (fresh database, fresh object store, fresh secrets) | **238 passed, 0 failed, 6 skipped** (3 opt-in Stage 19 harnesses, 3 environment-dependent tests that also skip in CI); 1 461 s |
| Remote CI integration job, same commit | run 40034115480 (`2015978`), run 40031324294 (`16429d2`) | throwaway stack per job | success (8/8 jobs each) |
| Race-enabled unit tests | Stage 19, `16429d2` | n/a | 39 packages ok, no race |

All 20 non-opt-in `TestFinancial*` tests passed in the isolated run, including the regression tests for the Stage 19
fixes. `TestReconciliationPaymentsEndToEnd`, which fails on the contaminated development database (KI-S19-17), passed
in 6.4 s on clean data.

## 2. Stage 19 fixes — verified

| Fix | Code at candidate | Regression test (isolated run) |
|---|---|---|
| F-S19-01 ledger lock order | `internal/ledger/post.go` `lockAndCheck` locks every SYNC row in ascending id | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` PASS (200/200, 4.1 s) |
| F-S19-12 payout attempt lock order | `internal/payouts/orchestration.go` `startAttempt` / `recordObservation` (payout row first), `errStatusInFlight` | `TestFinancialPayoutConcurrentStatusQueries` PASS |
| KI-S18-25 reconciliation window | `internal/payouts/reconcile.go` `reconcileFresh`, `ledgerSyncPending` | `TestFinancialPayoutCompletionWindowVersusReconciliation`, `…ConnectionLostBetweenJournalAndConsumption`, `…LedgerRefusalIsStillReported` PASS |
| F-S19-02 history written once | `link`: event only when `RowsAffected() == 1` | window test PASS |
| KI-S18-22 bounded polling | `internal/payments/payments.go` `PollHorizon`, `MaxStatusPolls`, `PollJitter`; `poll.go` | `TestFinancialPaymentPoll*` (4) PASS |
| F-S19-13 newest-first ledger reconciliation | `internal/ledger/reconcile.go` `ORDER BY t.posted_at DESC LIMIT 10000` | `TestLedgerReconciliationFindsExceptionsAndWorkflow` PASS |
| F-S19-14 run failure recorded | `internal/reconciliation/runs.go` non-cancelled context | observed in Stage 19 (FAILED/RUN_ERROR); not separately re-tested in Stage 20 |

## 3. Control-by-control assessment

| Control | Result | Basis |
|---|---|---|
| Double-entry accounting (L1, L2, L9) | PASS | Go builder + deferred DB trigger; integrity checker in every payout test and during concurrent posting |
| Ledger immutability (L5) | PASS | grants + triggers; `TestSecurityRuntimeRolesCannotRewriteFinancialHistory` |
| Idempotency (L6, keys, inbox, outbox) | PASS | unique constraints; concurrent duplicate and conflicting-payload tests |
| Payment state transitions | PASS | `apply()` edge/rank/source rules mirrored in SQL; `TestPaymentEdgesMatchMigration` |
| Payout state transitions and reservations | PASS | DB-guarded machines, `ck_payout_reservations_*`, set-once links; reservation invariants test |
| Maker-checker separation | PASS | Go + DB + `actor_identities`; concurrent approvals test |
| Reversals and adjustments | PASS | maker-checker; no double reversal (L7) |
| Audit integrity | PASS | hash chain, MAC checkpoints; tamper and interruption tests |
| Financial gate controls | PASS | 11 Go constants false, gate CHECKs; `TestFinancialGateConstantsStayOff`, `TestSecurityFinancialGatesInDatabase` |
| Unknown outcomes | PASS | UNKNOWN never FAILED/resubmitted; poll budget hand-off |
| Duplicate/reordered provider events | PASS | inbox dedupe, precedence; Stage 8/11 scenarios |
| Worker crash recovery | PASS WITH CONDITIONS | Recover job, River rescue, IN_FLIGHT sweeper, worker-kill exercise (Stage 19, development stack, opt-in); KI-S19-07 (kill landed between calls) |
| Transaction boundaries and concurrency | PASS | database-contention.md (Stage 19); deadlock fixes verified |
| **Reconciliation coverage** | **FAIL for production (launch-blocking)** | §5 |
| Accounting semantics | BLOCKED | KI-S10-02 — no accounting review of the chart, rules or donation model |

## 4. Meaning of the Stage 19 load figures (11 763 payouts, 11 763 donations)

Verified against the preserved reports (`evidence/stage-19/load-run-1.md`: 5 622 + `load-run-2.md`: 6 141). Each run
counts its own payouts once; the SQL checks (accepted CREATE > 1, consumed > 1, COMPLETED not CONSUMED, SUCCEEDED > 1,
duplicate success notices, reconciliation references, final integrity) were computed over exactly the IDs the run
created. They are **TEST** payouts and donations through the simulator on the shared development database, with the
compose worker acting alongside. They demonstrate **absence of duplicate effects under 1–16-way concurrency on one
host**. They are not isolated from other development data, not a capacity measurement and not a production
transaction-rate claim.

## 5. Reconciliation coverage (brief §9)

| Reconciliation | Selection | Schedule | Coverage guarantee |
|---|---|---|---|
| Payout (internal) | newest 5 000 payouts changed before now − 5 min | every 15 min | **none**: > 5 000 payouts changing within one interval leaves the excess unexamined; a payout outside the window is never re-checked (e.g. a later provider-side change) |
| Fee (internal) | newest 5 000 assessments | every 15 min | **none** (same pattern) |
| Ledger (internal, journals vs payment facts) | newest 10 000 payment-sourced journals (Stage 19: was the oldest 10 000) | every 15 min | **none**: older journals are not re-compared; a burst > 10 000 within an interval is partially skipped |
| Stage 17 statement reconciliation | parsed statement periods within 93 days of the newest import; scopes with open lines re-run | scheduled per scope | covers statement periods; nothing outside the 93-day horizon is re-run |

What *is* guaranteed: a finding, once recorded, stays recorded (references and exceptions are immutable and remain
OPEN until resolved; dedupe prevents multiplication); concurrent runs record once; interrupted runs record nothing
partial; findings are audited. Changing the ledger window to newest-first did not delete or hide any recorded
discrepancy (references persist); it does mean the internal detector no longer re-reads old journals.

**Conclusion.** The current design does not guarantee eventual coverage of every record. At pilot volumes (far below
5 000 payouts per 15 minutes) the windows are unlikely to be exceeded, but "unlikely at expected volume" is a capacity
assumption with no approved target behind it, and re-detection of later changes is absent regardless of volume.
Incremental reconciliation (a persisted watermark per detector, covering every record exactly once, plus periodic full
re-verification) requires an approved volume target and a design; it was **not implemented** in Stage 20 (the brief
forbids implementing it without approved requirements and deterministic coverage tests in the same change).
**KI-S19-09 is raised to HIGH and blocks launch.**

## 6. Remaining limitations

No production data, no real provider, no settlement evidence (settlement reconciliation is NOT_PERFORMED by design);
ZWG paths are not exercised by the simulator; F-S19-14's fix was observed once, not regression-tested in isolation.
