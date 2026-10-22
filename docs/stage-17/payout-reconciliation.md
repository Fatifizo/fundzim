# Stage 17 — Payout reconciliation

## 1. Chain

withdrawal request → payout (approved, maker-checker) → reservation → payout attempt(s) → provider acknowledgement →
provider final outcome → statement line → ledger journal (`payout:{id}:{reserved|submitted|completed|failed|released}`).
Payouts keep their own provider-status reconciliation (Stage 11); Stage 17 adds statement evidence.

## 2. Reads

`payouts.ReconFacts` (status, amount, currency, provider reference, TEST flag, submitted/completed times, reservation
status, CREATE_PAYOUT attempt count), `ReconCompletedInWindow` (for MISSING_SETTLEMENT) and `ReconQueue` (payouts in
SUBMITTED, PROCESSING or UNKNOWN with attempt summary — refused-before-processing attempts counted separately — last
classification, open payout exceptions and the retry-budget halt). No destination data beyond the masked form.

## 3. Decisions

See matching-engine.md §3 (PAYOUT rows). Money left the provider for a payout FundZim never made → MISSING_PAYOUT
(CRITICAL). Paid while FundZim recorded failure or never submitted → PAYOUT_MISMATCH (CRITICAL). Completed payouts must
have their completion journal (TEST payouts post SIMULATED journals) → POSTED or MISSING_LEDGER_ENTRY.

## 4. Safety

- **Unknown outcomes are never resubmitted.** Statement evidence for an UNKNOWN payout opens UNKNOWN_PAYOUT_OUTCOME;
  only an approved PROVIDER_OUTCOME_APPLIED resolution applies it, through `payouts.ApplyReconciliationOutcome` →
  `applyStatus` with source `RECON` (Y10 completed / Y11 failed — the machine's own edges; `ck_payout_events_*_source`
  already allowed RECON). The same evidence key is applied once; a differing amount or currency is an anomaly with a
  SEV1 payout exception, never applied. Nothing is ever submitted.
- **Reservations**: reconciliation never releases or consumes a reservation; the payout's own transitions do, and
  journals follow through `syncLedger`.
- Maker-checker approvals, risk holds (EC-12), compliance restrictions and the database attempts guard are untouched.
- Integration test `TestReconciliationPayoutsUnknownOutcomeNeverResubmitted` checks: attempt count unchanged before and
  after the resolution, one RECON completion event, reservation CONSUMED, completion journal posted, ledger integrity.

## 5. Retry follow-up (KI-S17-01)

Stage 15's budget bounded same-reference retries after DEFINITELY_NOT_SENT. A RATE_LIMITED create (refused before
processing, `psp.CategoryRetry` AFTER_BACKOFF) was left out: never re-dispatched, never counted, status-polled without
end. Now treated exactly like DEFINITELY_NOT_SENT by the attempts guard (migration `20261022090100`), the dispatcher,
the budget counter and the poller; simulator scenario 13 (`RATE_LIMITED`, amount ending 13) exercises it
(`TestReconciliationRateLimitedPayoutIsBudgeted`: exactly the budget of attempts, no status polling, halted with the
reservation and journals unchanged, the database refuses a further create).

## 6. Duplicates

Duplicate payout instructions: the payout's own reference (our id) is the provider idempotency key (Stage 11); two
statement lines for one payout → DUPLICATE_TRANSACTION; duplicate provider references → payouts' own
DUPLICATE_PAYOUT_REFERENCE; conflicting provider outcomes → CONFLICTING_EVIDENCE (statement) or payouts' own
LATE_COMPLETION_AFTER_FAILURE.
