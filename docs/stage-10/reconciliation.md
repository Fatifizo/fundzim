# Stage 10 — Reconciliation foundation

**Settlement reconciliation is NOT_PERFORMED.** No provider is selected (ADR-040) and no settlement statement or
settlement evidence exists (PCR-007, PCR-016). Every run says so in its report (`settlement_reconciliation`), and
nothing in Stage 10 claims funds were settled. Statement ingestion and matching are Stage 17.

## 1. What a run does (`ledger.Reconcile`; job every 15 min; staff `POST /admin/ledger/reconciliation/run`)

| Check | Exception type |
|---|---|
| A capture journal from payments whose payment does not exist | ORPHAN_POSTING |
| Two capture journals for one payment | DUPLICATE_POSTING |
| Journal currency ≠ payment currency | CURRENCY_MISMATCH |
| Journal gross ≠ payment amount | AMOUNT_MISMATCH (expected and actual amounts recorded) |
| Journal provider reference ≠ payment provider reference | UNMATCHED_PROVIDER_REFERENCE |
| A SUCCEEDED payment (older than 5 min) never evaluated by the ledger consumer | MISSING_POSTING (`NOT_EVALUATED_BY_LEDGER`) |
| A SUCCEEDED payment eligible for this ledger (PRODUCTION payment, PRODUCTION ledger) without a POSTED decision | MISSING_POSTING (`ELIGIBLE_NOT_POSTED`) — impossible in Stage 10 |
| A payment UNKNOWN for more than 1 h | UNKNOWN_OUTCOME (informational; resolved by payments, never by the ledger) |
| A suspense account with a non-zero balance | SUSPENSE_BALANCE |

A TEST ledger can hold no payments-sourced journal (Go validation and CHECK
`ck_ledger_transactions_no_payment_postings_in_test`), so the journal-side checks are exercised in tests against
synthetic capture journals through `Service.ReconcileJournalsFrom = SourceSynthetic` (a test seam; the runtime always
reconciles `payments`).

Payment facts come from the payments module through the read-only `ledger.PaymentFacts` interface (adapter in
`internal/app/ledger.go`); the ledger never reads payments tables. A run scans at most 10 000 payment journals and
5 000 succeeded payments (KI-S10-08).

## 2. Exceptions (`ledger_reconciliation_references`)

OPEN → INVESTIGATING (owner assigned) → RESOLVED (resolution code and note), or OPEN → RESOLVED. Detection fields
(type, source, amounts, detected time and run) are immutable; one unresolved row per type and source, so repeated
runs do not duplicate. Each new exception publishes `ledger.reconciliation_exception`. Working an exception needs
`ledger.reconciliation.operate` (step-up). **Resolving an exception never posts a journal**: a correction is a
separate maker-checker adjustment.

## 3. Suspense controls

`suspense:settlement_discrepancy` is reachable only through `SETTLEMENT_DISCREPANCY` (and cleared by the
approval-requiring `SETTLEMENT_DISCREPANCY_RESOLVED` or an adjustment). It cannot hide an unbalanced journal: every
journal must balance on its own (L1) before it can commit, whatever accounts it uses. Any non-zero suspense balance
is an exception with an owner and an age.

## 4. Source references (`ledger_source_references`)

Every source event the ledger considers is recorded once with its decision (POSTED, NOT_ELIGIBLE, BLOCKED,
REJECTED), reason, evidence class and amount. In Stage 10 the payments consumer records every
`payments.payment_succeeded` as NOT_ELIGIBLE / TEST_PAYMENT (or LEDGER_ENVIRONMENT_MISMATCH), so reconciliation can
prove that each succeeded test payment was seen and deliberately not posted.

Tested: `TestLedgerReconciliationFindsExceptionsAndWorkflow`, `TestLedgerPaymentsConsumerNeverPostsTestPayments`.
