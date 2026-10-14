# Stage 11 — Payout reservations

A reservation is the ledger journal `payout:{id}:reserved` (PAYOUT_RESERVED: Dr `campaign_payable:{c}`, Cr
`campaign_payout_pending:{c}`) plus its `app.payout_reservations` row (one per payout; `account_scope` =
`liability:campaign_payable:{campaign}`; FKs to the ledger journals it links). It reuses the Stage 10 locking: the ledger
locks the campaign's non-negative balance rows in ascending account order, refuses `LEDGER_INSUFFICIENT_FUNDS` and has the
CHECK `balance_minor >= 0` as backstop. There is no separate balance table.

## 1. States (machine `payout_reservation`)

| State | Meaning | Allowed next |
|---|---|---|
| ACTIVE | funds reserved (still reserved while the payout is submitted: money in `payout_in_transit`) | BLOCKED, CONSUMED, RELEASED, EXPIRED |
| BLOCKED | the provider outcome is UNKNOWN: neither releasable nor consumable until an authoritative answer | ACTIVE, CONSUMED, RELEASED |
| CONSUMED | authoritative completion (`payout:{id}:completed`) | — |
| RELEASED | rejected/cancelled before submission (`:released`) or authoritative failure after it (`:failed`) | — |
| EXPIRED | released because `payout.request_validity` (if configured) passed before submission | — |

Database rules (`app.payout_reservations_guard`): facts and journal links are set once; RELEASED/EXPIRED only when the
payout is REJECTED, CANCELLED or FAILED (never while UNKNOWN or in flight); CONSUMED only when it is COMPLETED or REVERSED;
EXPIRED only before submission. Rows are never deleted.

## 2. Ordering and atomicity (ADR-043 §3)

1. App transaction: the payout row (PAYOUT_REQUESTED) claims the single in-flight slot.
2. Ledger (own pool): the reservation journal, idempotent by key.
3. App transaction: reservation row ACTIVE + PENDING_REVIEW.

A crash or ledger error between 1 and 3 leaves PAYOUT_REQUESTED; the recovery job (every 30 s, requests older than 30 s)
re-posts with the same key and finishes, or REJECTs if the ledger refuses. Releases and consumptions follow the payout's
state through `syncLedger`; a ledger failure there opens LEDGER_POSTING_FAILED and is re-driven.

## 3. Concurrency results (integration tests)

- 100 concurrent requests for one campaign: exactly 1 accepted, 99 `PAYOUT_IN_PROGRESS`; one reservation; payable and
  pending exact (`TestPayoutHundredConcurrentRequestsReserveOnce`).
- Over-allocation (request > payable): refused (`PAYOUT_NOT_ELIGIBLE`, INSUFFICIENT_FUNDS).
- UNKNOWN keeps the reservation BLOCKED; direct SQL release refused (`TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted`).
- Withdrawal vs reservation expiry: expiry only acts on PENDING_REVIEW/APPROVED payouts and cancels through the same
  transition (row lock), so a concurrently submitted payout is never expired (the guard refuses EXPIRED after submission).
