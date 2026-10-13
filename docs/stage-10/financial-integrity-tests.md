# Stage 10 — Financial integrity tests

All results below were produced in this session on 2026-10-13 (local machine; remote CI results are recorded in the
completion report once the run has been inspected).

## 1. Unit and property tests (`internal/ledger`, 7 test functions)

| Test | What it proves |
|---|---|
| `TestValidateAcceptsBalancedJournal` | a well-formed journal validates |
| `TestValidateRejectsEveryInvalidShape` | unbalanced, single line, zero/negative amounts, bad currency, unknown class, owner errors, rule violations, two campaigns, unknown rule, source type, key format, future time, REJECTED evidence, production disabled, synthetic outside TEST, payments-sourced journal in a TEST ledger, reversal/approval mismatches, int64 overflow — each with its stable error code |
| `TestChartMatchesSQL` | the Go chart equals `ledger.account_class_signature()` in the migration |
| `TestParseAccountCode` | account codes round-trip; an owned class without an owner is refused |
| `TestPropertyValidJournalsBalanceAndInvalidOnesAreRejected` | seeds 1, 7, 42, 20261013, 987654321 × 2 000 random journals: every valid one balances; every perturbed one is refused |
| `TestPropertyReversalCancelsAndAggregationMatches` | same seeds × 500: journal + mirror nets to zero per account; aggregate balances equal the per-line sum |
| `TestMapDBErrorPassesThroughKnownErrors` | database refusals map to stable ledger errors |

Run: `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` in `golang:1.27.2` (Docker) — **34 packages
ok**, no race reported.

## 2. Integration tests (`tests/integration`, 13 `TestLedger*` + the payments suite)

| Test | What it proves |
|---|---|
| `TestLedgerSyntheticFixturesBalancesAndTrialBalance` | all four fixtures (USD 10.00/25.00/0.50 fee, 100.00 with 30.00 partial refund, 100.00 settlement and release, 25.00 with 5.00 refund) post balanced, labelled TEST_ONLY; balances and trial balance are exact; replay posts nothing |
| `TestLedgerDatabaseInvariantsDirectSQL` | as `fundzim_ledger` with raw SQL: unbalanced, single-line, negative, rule-violating, PRODUCTION, synthetic-with-real-evidence, payments-sourced-in-TEST, overdraw, unapproved reversal, UPDATE/DELETE of history, sealed journal (L11), duplicate key, ZWG account, direct balance update (even with the maintenance flag), environment change, DELETE/TRUNCATE — all refused |
| `TestLedgerApplicationRoleHasNoLedgerAccess` | `fundzim_app` can neither read nor write any ledger table |
| `TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal` | 100 concurrent identical postings ⇒ 1 journal, balance counted once; different body ⇒ conflict |
| `TestLedgerReversalsMakerCheckerAndNoDoubleReversal` | self-approval refused; two concurrent approvals ⇒ exactly one reversal; original untouched; no double reversal; no reversal of a reversal; audited |
| `TestLedgerAdjustmentExpiry` | an overdue request expires, is audited `ledger.adjustment.expired`, and cannot be approved or posted |
| `TestLedgerAccountFreezeVersusConcurrentPostings` | freeze racing 40 concurrent postings: every posting either committed before the freeze or was refused; none after; closing an account with a balance is refused (`LEDGER_ACCOUNT_BALANCE_NOT_ZERO`) |
| `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings` | close needs an ended period and a second person; closed period refuses postings; allowed prior-period rules land in the open period |
| `TestLedgerReconciliationFindsExceptionsAndWorkflow` | AMOUNT_MISMATCH, ORPHAN_POSTING, MISSING_POSTING, UNKNOWN_OUTCOME, SUSPENSE_BALANCE found; reruns do not duplicate; investigate → resolve; detection fields immutable; settlement reported NOT_PERFORMED |
| `TestLedgerPaymentsConsumerNeverPostsTestPayments` | duplicate deliveries of a test `payment_succeeded` ⇒ one NOT_ELIGIBLE source reference, zero journals |
| `TestLedgerFailureInjection` | rollback, killed connection, lock timeout, outbox gateway refusal: nothing partial; deliberate projection drift detected by L10 and repaired only by the owner function |
| `TestLedgerTrialBalanceStaysBalancedDuringPosting` | trial balance read repeatedly under concurrent posting is always balanced |
| `TestLedgerStaffAPI` | permissions, step-up, Idempotency-Key, maker-checker over HTTP, fixtures only for ADMIN in a TEST ledger |

`TestPaymentsSandboxSuccessAndIdempotency` now also asserts that a succeeded test payment produces no journal and
that no payments-sourced journal appears.

Run: `flock /tmp/claude-1000/fundzim-stack.lock go test -tags integration -count=1 ./tests/integration/...` —
**package ok (540 s)**: 139 test functions; the opt-in ones (`FUNDZIM_IT_PERF`, scale-test profile, Valkey stop/start, external API/web URLs) skip by design as in earlier stages, all others passed. The first full run surfaced two defects, both fixed before this
result: (1) a Stage 8 assertion "no ledger tables exist" (replaced by the stricter assertion above); (2) the
reconciliation test posting payments-sourced journals into the TEST ledger (KI-S10-17; new CHECK and Go refusal).

## 3. Integrity checker

`ledger.RunIntegrity` (L1, L2, L4, L6, L7, L9, L10, L13, L15) runs hourly and on demand; results are recorded per
invariant; a failure writes a SYSTEM_SNAPSHOT evidence record and logs an alert. At 1 016 300 journals it passed 3/3
runs in about 32 s ([performance.md](performance.md) §3).

## 4. UI tests

- Web unit tests (vitest): 49 files, **481 tests passed** (includes `src/lib/ledger/normalise.test.ts`).
- Mock-API Playwright: **161 passed, 1 skipped** (the existing mobile-only skip), including `e2e/staff-ledger.spec.ts`
  (TEST banner, journals, maker-checker reversal with step-up, freeze, trial balance, reconciliation workflow, axe,
  CSP; staff without `ledger.view` see nothing).
- Real-stack Playwright: **48 passed**, including the new `e2e-real/staff-ledger.spec.ts` (an ADMIN posts a
  synthetic fixture through the API; a COMPLIANCE officer sees the balanced TEST_ONLY journal and a zero trial
  balance difference; ADMIN gets 403 on reads; a REVIEWER gets 404 on the finance pages).

## 5. Backup and recovery test (logical dump/restore of the development database)

Procedure (2026-10-13, under the stack lock): `pg_dump -Fc fundzim` (67.8 MB) → `CREATE DATABASE fundzim_restore` →
plain `pg_restore` (no errors; pg_dump creates triggers, constraint triggers and foreign keys after loading data, so
the ledger's insert triggers do not refire on restore) → `tests/recovery` (`TestLedgerRestoreVerification`, tag
`recovery`) → scratch database dropped.

| Check | Result |
|---|---|
| Journals up to the dump point (content hash over every column that matters) | 606 restored = 606 source, md5 identical |
| Entries up to the dump point | 1 212 restored = 1 212 source, md5 identical |
| Integrity checker on the restore | all nine checks PASSED, 0 violations |
| Trial balance on the restore | USD debits 771 222 = credits 771 222 (minor units) |
| History still immutable after restore | UPDATE of a journal and DELETE of entries refused |
| Idempotency constraint present | yes |
| Ledger triggers / constraints | 46 / 137 in both databases |
| Audit hash chains (`audit.verify_chain` on audit and security audit events) | no problems |

This is a **local logical backup test only**. It is not a disaster-recovery test: no production backups, PITR,
off-site copies, encryption at rest of backups or restore-time objectives exist (KI-S10-06).
