# Remediation phase 1 — final report

Repository integration, governance and financial reconciliation completeness. Non-production. Branch
`remediation/reconciliation-completeness`. Date 2026-10-25.

**Engineering result: PASS WITH CONDITIONS.** The conditions are the owner decisions D-06, D-05 and D-01, green CI on the integrated commit (one Web unit test failed on the branch run, KI-R1-10), and an owner decision on the development-database incident (F-R1-07). Launch recommendation unchanged: **BLOCKED** (Stage 20). Nothing merged, deployed
or accepted.

## 1. Executive summary

KI-S19-09 is fixed in engineering, and the fix is verified. Before this phase, the payout, fee and ledger reconciliations each
read a fixed newest-N window, and nothing outside it was ever examined again. One ledger window was worse: it read the
**oldest** 5 000 succeeded payments, so at volume no new payment was ever checked (F-R1-01, a new finding of the F-S19-13 class).

All four detectors (PAYOUTS, FEE_ASSESSMENTS, PAYMENT_JOURNALS, PAYMENT_FACTS) now also run a durable, resumable keyset sweep
over the whole table:
- it examines every in-scope record once per cycle;
- the checkpoint advances only by compare-and-swap after a batch is examined and its findings are committed;
- the database guards the checkpoint invariants;
- records inside a grace period are deferred, never skipped;
- the operations portal shows coverage age per detector.

The recency windows remain for latency and now read the newest records.

The conditions:
- the owner sets the coverage freshness target (D-06) and the volumes (D-05);
- the branch is reviewed, and integrated through the proposed protected PR route (D-01);
- CI is green on the integrated commit.

Launch remains blocked by the other Stage 20 blockers (providers, legal, accounting, security, infrastructure, acceptance).

## 2. Provenance

| Item | Value |
|---|---|
| Starting point | `6a65d70` on `stage-20/production-certification` (clean tree); CI run 40179378365, 8/8 success |
| `main` | `a552de2` (Stage 0); unprotected (`protected: false`, API 2026-10-25) |
| Branch | `remediation/reconciliation-completeness` created from `6a65d70` (did not exist before) |
| Commits | `97b8a70` (code, migration, OpenAPI, tests), `8baee4e` (test fix), `a0a687d` (F-R1-06 fix, volume test deadlines), docs commit — see §15 |
| Environments | disposable compose project `fundzim-r1` (fresh volumes, verified empty: goose head `20261025090000`, 0 payouts); scratch DBs `fundzim_perf_r1`, `fundzim_perf_r1b`. Development project stopped without `-v` for the whole phase. At teardown its restart applied the additive migration to it (F-R1-07); stopped again |

Details: [preflight.md](preflight.md).

## 3. Findings

| ID | Severity | Summary | Status |
|---|---|---|---|
| KI-S19-09 | HIGH | windowed reconciliations; records outside the windows never examined again | engineering done; closure pending D-06 + integration |
| F-R1-01 | HIGH | ledger succeeded-payment window read the oldest 5 000 | fixed (newest first + PAYMENT_FACTS sweep) |
| F-R1-02 | HIGH (test defect, caught) | a test fixture planted a duplicate journal; L6 failed permanently on the disposable DB, EC-19 refused all payouts | fixed before commit; rule in CLAUDE.md |
| F-R1-03 | MEDIUM | an open payout exception suppressed every new mismatch type | fixed (recorded as evidence) |
| F-R1-04 | MEDIUM | fee MISSING_FEE judged on partial groups; ledger duplicates counted within the window | fixed |
| F-R1-05 | MEDIUM | test services would share the worker's `default` checkpoints | fixed (own scopes; source-suffix scopes) |
| F-R1-07 | HIGH (process; stop condition) | teardown restarted the development stack with the image tag that the disposable project had rebuilt at `a0a687d`, so `20261025090000` was applied to the **shared development database** (additive only; 0 checkpoint rows and 0 new references written). Development stack stopped; owner decides keep or restore | OPEN — owner decision |
| F-R1-06 | MEDIUM | this phase's own keyset reads ordered by the text output column: a full sort per batch, and order safety depending on the collation (verified: nothing skipped) | fixed in `a0a687d` |
| KI-R1-01 … 10 | LOW–INFO | operational / test limitations (cycle duration, row growth, hot deferrals, in-memory facts, out-of-scope items, local secrets printed, payout test race, discovery flake, sub-batch deadline liveness, CI web unit test failure) | see [known-issues.md](known-issues.md) |

## 4. Root causes

1. fixed `LIMIT` windows with no persisted position;
2. windows ordered by creation or posting time, so changes to old records produced no signal;
3. one oldest-first window;
4. grouping and duplicate counting inside the window;
5. suppression of new mismatches under an open exception.

Details: [reconciliation-coverage.md](reconciliation-coverage.md) §2.

## 5. Changes

- `internal/platform/reconsweep` — engine (`Run`, `Store`, `SQLStore`, `Coverage.Metric`) and unit tests.
- `internal/payouts`, `internal/fees`, `internal/ledger` — sweep per detector alongside the recency pass. Also: payout
  open-exception evidence, fee whole-group MISSING_FEE, ledger cross-ledger duplicate count and source-suffixed scope, coverage
  metrics in each `OpsSnapshot`.
- `internal/payments/facts.go` — newest-first window and `PaymentFactIDsAfter`; `internal/app/ledger.go` — the matching adapter.
- `internal/archtest` — the three new tables registered to their owners.
- `api/openapi/fundzim-v1.yaml` — `ReconSweepReport` and `ReconCoverage`; payout run gains `open_exception_evidence`, `sweep`
  and `coverage`; fee run gains optional `sweep` and `coverage`. Redocly lint is valid, with the baseline's 2 warnings.
- Tests:
  - `tests/integration/reconciliation_coverage_test.go` and the opt-in `…_volume_test.go`;
  - `tests/perf/ledger/recon_sweep_perf_test.go`;
  - existing payout, fee and ledger ITs moved to their own sweep scopes;
  - payout SoD test cleanup (KI-R1-07);
  - discovery typo test (KI-R1-08).
- ADR-052 (Proposed); CLAUDE.md remediation block; phase documents.

## 6. Migration

`20261025090000_reconciliation_coverage.sql`:
- creates `app.payout_reconciliation_sweeps`, `app.fee_reconciliation_sweeps` and `ledger.ledger_reconciliation_sweeps`;
- adds the guard function `app.reconciliation_sweep_guard()`;
- adds three status machines in `app.status_transitions`;
- adds no-delete/no-truncate triggers and runtime grants.

It touches no existing table or row and needs no backfill: the first run opens cycle 1. Production is forward-only. Down drops
only the new objects (development/CI round trip). It was applied to fresh databases only; no shared database was migrated.

## 7. Guarantees

These guarantees hold for every record class in [reconciliation-coverage.md](reconciliation-coverage.md) §3:
- every in-scope record that exists when a cycle starts is examined in that cycle, unless it is still deferred, which is
  visible;
- a change committed at time *t* is examined by the end of the first cycle that starts after *t*;
- an interrupted run repeats at most its unfinished batch and never skips one;
- concurrent runs never skip and never record a finding twice;
- a completed cycle's `examined` equals the in-scope count, which the tests check;
- invariants C1–C5 and C7 are enforced by the database; C6 and C8 by the engine, and both are tested.

**Not guaranteed:** a freshness bound in wall-clock time. That depends on table size, the run budget and the 15-minute
schedule, and the acceptable bound is owner decision D-06.

## 8. Regression results

All results come from runs executed in this session and preserved in `evidence/` ([test-evidence.md](test-evidence.md) §2).

**Final candidate `a0a687d`:**
- unit tests with `-race`: 40 packages ok; gofmt and vet clean;
- full integration suite on a freshly recreated disposable stack with rebuilt images: **243 pass, 0 fail, 7 skip** (the 7 are
  opt-in or environment tests: load, crash, volume sample, coverage volume, replicas, frontend, Valkey stop);
- all Stage 19 financial regressions and all 5 coverage ITs pass (`…ConcurrentAndInterrupted`: 6 interrupted runs, cycle
  complete).

**Earlier runs:**
- `97b8a70`, first full run: 233 pass and 10 fail. One payout test race (KI-R1-07) left a shared destination SUSPENDED, and 9
  later tests failed because of it. The cascade was fixed; the race itself remains.
- `97b8a70`, second full run: 242 pass and 1 fail. The failure was a discovery random-token flake (KI-R1-08), fixed in
  `8baee4e`; the test then passed 5/5.

## 9. Failure injection

Unit tests (forced):
- examine error mid-batch;
- key-query error;
- checkpoint-write failure before and after advancement;
- interleaved concurrent runs;
- deferral overflow (fail closed).

Integration tests (real):
- context deadlines from 1 ms to 2 s, expiring inside batches on real PostgreSQL;
- two services with separate pools on one scope;
- database guard refusals as runtime roles.

Scratch database: runs under deadlines from 1/8 to 8/8 of the slowest run, at 20 000 and 100 000 journals.

No record was skipped or duplicated in any of these. The first scratch attempt surfaced KI-R1-09: a run whose deadline is
shorter than one batch makes no progress. That is safe but stalls the cycle; production job timeouts are 10 min against a 3 min
budget.

## 10. Performance

All measurements were taken on the dev host only; no capacity target is invented. See [performance.md](performance.md).

- **Ledger, scratch database:**
  - 20 000 journals: a full cycle of both sweeps took 3 runs and 19 s, at about 2 070 records/s.
  - 100 000 journals: 11 runs and 1 m 46 s, at about 1 880 records/s; the slowest run was 10.9 s.
  - The cost is about 1 ms per record examined.
  - Under deadlines inside batches, every cycle completed with every journal examined.
- **F-R1-06 fix:** the keyset read per batch went from 49 ms to 3.3 ms at 100 000 journals (index range scan instead of a full
  sort).
- **Payouts and fees on the disposable stack:** only small volumes were measured (≤ 853 payouts, ≤ 475 assessments). One cycle
  took one run of about 4 s, and no lock waits were seen. `TestFinancialLoad` was not run.
- **Arithmetic, not a target:** with the default budgets and the 15-minute schedule, a cycle takes ⌈N / budget⌉ runs. The
  budgets are 2 000 records per run for payouts, 5 000 for fees and 10 000 for each ledger sweep.

## 11. Financial integrity

Throughout this phase:
- no posted ledger entry was modified or deleted;
- no invariant, trigger, CHECK or grant was weakened;
- no financial gate was enabled;
- no real donation, payment or payout happened (simulator only);
- no production credential was loaded.

**Environment isolation was breached once (F-R1-07).** During teardown, this phase's migration was applied to the shared
development database. The migration is additive: no existing row changed and no reconciliation effect was written. The
development API and worker ran the branch code for about a minute before the stack was stopped. This is a stop condition
("a migration affecting shared data"). Work stopped there, apart from documenting it. No Down migration was run.

The only invariant failure observed was L6 on the disposable database. It was caused by this phase's own test fixture
(F-R1-02), investigated under the stop condition, and removed; the disposable database was recreated from empty.

The Stage 19 financial regressions all pass on `97b8a70`: KI-S18-25, KI-S18-22, F-S19-01 and F-S19-12.

## 12. Blockers

All 17 Stage 20 launch blockers remain open. B-05 (reconciliation coverage) is ENGINEERING_DONE_PENDING_DECISION, and B-13
(unprotected `main`) is still open. The current status of every workstream is in
[remediation-register.md](remediation-register.md).

## 13. Owner decisions

| Decision | Item |
|---|---|
| D-01 | apply branch protection and approve the PR route ([integration-plan.md](integration-plan.md) §4) |
| D-06 | maximum acceptable reconciliation cycle duration per detector (measured costs: performance.md) |
| D-05 | pilot volumes |
| D-13 | stage acceptance in order, including this phase |
| D-12 | signing, release and rollback procedure |
| F-R1-07 | development database: keep the additive migration (recommended; forward-only) or restore from the newest backup (2026-10-23, which discards later writes); the development stack stays stopped until decided |

Stage 20's D-02 … D-14 are otherwise unchanged. No decision was recorded by Claude Code.

## 14. CI results

- **Baseline:** CI run 40179378365 on `6a65d70`: 8/8 success (verified through the GitHub API in preflight).
- **This branch:** pushed to `origin/remediation/reconciliation-completeness` (not `main`) to obtain remote CI. The observed
  result is recorded below only after the run was seen. Local results are in §8 and are not a substitute for CI.

- **CI trigger:** the workflow ran only for `main`, `stage-**` and pull requests, so commit `08411f6` adds `remediation/**` to the
  push trigger. Permissions, pins and jobs are unchanged, and `internal/deploytest` passes.
- **Run [40211164419](https://github.com/Fatifizo/fundzim/actions/runs/40211164419) on `08411f6`** (code identical to
  `a0a687d`; observed through the API): **7 of 8 jobs succeeded.**
  - Succeeded: Go, Integration, Real-stack E2E, Web end-to-end, Container images, OpenAPI/SQL drafts, Secret scanning.
  - **Failed: Web (lint, types, unit tests, build, audit)**, at step `npm test`.
    `apps/web/src/__tests__/discovery-components.test.tsx:83` (*placement requests … send an idempotency key once*) reported
    `expected "vi.fn()" to be called 1 times, but got 0 times`. Build and audit were skipped after it.
  - This phase changed no file under `apps/web`, and the test is unchanged since Stage 16. It passed in Stage 20's run
    40179378365.
  - Locally on Node 24.21.0 it passes: full suite 578/578, the file 15/15 in isolation, and the full suite 3/3 pinned to one CPU.
    The full job log needs authentication and was not read, so the cause is **not established**. The assertion is synchronous
    right after an asynchronous submit, so a timing dependence is suspected but not proven.
  - Nothing was changed to make it pass (KI-R1-10).
- **The CI result therefore does not meet "green".** CI must be green on the integrated commit before B-05 closes (§12).
- The run on the commit that records this result is reported in the completion message, not here.

## 15. Commit and branch details

| Item | Value |
|---|---|
| Branch | `remediation/reconciliation-completeness` (new; created from `6a65d70`; no existing branch overwritten) |
| Commits | `97b8a70` code, migration, OpenAPI, tests · `8baee4e` discovery test fix · `a0a687d` F-R1-06 fix and volume test deadlines · `871248d`, `3134d21` documents and evidence · `08411f6` CI trigger · final docs commit (CI result, F-R1-07) |
| Code candidate | `a0a687d` (the docs commit changes no code) |
| `main` | unchanged at `a552de2`; nothing merged; no PR opened; no repository setting changed |

## 16. Risks

- **Cycle duration grows linearly with table size.** Payouts need one provider status query per submitted payout, so they are
  the slowest detector at volume. Without a D-06 target, a stale cycle is visible (coverage age) but nothing alerts on it
  (no monitoring destination, B-12).
- **Checkpoint rows accumulate** (KI-R1-02). No retention period is invented (LR-012).
- **The payout test race (KI-R1-07)** can still fail one test intermittently.
- **Volume figures come from one dev host**, with payment facts held in memory for the ledger harness (KI-R1-04).
- **CI is not green** (Web unit test, KI-R1-10). It must be green on the integrated commit.
- **The development database is at the branch migration (F-R1-07)** while every other branch lacks it. Starting the
  development stack from an older commit fails the migration check or runs code that does not know the new tables, until the
  owner decides.
- **The branch is not integrated.** Until D-01, `main` still lacks every stage since Stage 0.

## 17. Next workstream

Release governance and integration, after the owner applies branch protection: merge the stack and this branch, tag a baseline
with green CI, and add signing, a release procedure and a rollback procedure. See
[REMEDIATION-PHASE-1-TO-PHASE-2.md](../../stage-handover/REMEDIATION-PHASE-1-TO-PHASE-2.md). This phase stops here.
