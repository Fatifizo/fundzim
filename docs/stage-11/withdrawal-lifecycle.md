# Stage 11 — Withdrawal (payout) lifecycle

States are ADR-020's, guarded by the database (machine `payout` in `app.status_transitions`, `app.guard_transition`,
plus `app.payout_requests_guard` and the event requirement). Go and SQL edge lists are identical.

## 1. Brief names → implemented states

| Brief | Implemented |
|---|---|
| REQUESTED, ELIGIBILITY_REVIEW | `PAYOUT_REQUESTED` (request-time eligibility passed; reservation being posted) |
| RESERVED, PENDING_APPROVAL | `PENDING_REVIEW` (funds reserved; approvals pending) |
| APPROVED, READY_FOR_SUBMISSION | `APPROVED` (re-checked immediately before submission) |
| SUBMITTED, PROCESSING | `SUBMITTED`, `PROCESSING` |
| SUCCEEDED | `COMPLETED` |
| FAILED, CANCELLED, REJECTED, UNKNOWN | same names |
| RECONCILIATION_REQUIRED | `needs_reconciliation` flag + RECONCILIATION_MISMATCH exception |
| — | `REVERSED` (a completed payout returned by the rail) |

## 2. Transitions

| # | From → To | Trigger | Source | Journal |
|---|---|---|---|---|
| Y1 | — → PAYOUT_REQUESTED | request passes request-time eligibility | OWNER | (next step) |
| Y2 | PAYOUT_REQUESTED → PENDING_REVIEW | ledger reservation posted | SYSTEM | `payout:{id}:reserved` |
| Y4 | PAYOUT_REQUESTED / PENDING_REVIEW / APPROVED → REJECTED | ledger refused / FINANCE rejects / hard failure at re-check | SYSTEM / STAFF | `:released` if reserved |
| Y5 | PENDING_REVIEW / APPROVED → CANCELLED | owner (step-up) or staff; system on request expiry (when configured) | OWNER / STAFF / SYSTEM | `:released` |
| Y6 | PENDING_REVIEW → APPROVED | enough FINANCE approvals of the current round | STAFF | — |
| Y7 | APPROVED → PENDING_REVIEW | destination changed/suspended/expired, or a new REVIEW finding, hold, campaign state or missing approvals at re-check (new round) | SYSTEM | — |
| Y8 | APPROVED → SUBMITTED | ELIGIBLE PRE_SUBMIT decision in the same transaction | SYSTEM | `:submitted` |
| Y9 | SUBMITTED → PROCESSING | provider acknowledged | SYNC / WEBHOOK / POLL | — |
| Y10 | SUBMITTED / PROCESSING / UNKNOWN → COMPLETED | authoritative completion | SYNC / WEBHOOK / POLL / RECON | `:completed` |
| Y11 | SUBMITTED / PROCESSING / UNKNOWN → FAILED | authoritative final failure (or "not found" after the documented finality window) | SYNC / WEBHOOK / POLL / RECON / STAFF+evidence | `:failed` |
| Y12 | SUBMITTED / PROCESSING → UNKNOWN | indeterminate call, query or crash | SYSTEM | — |
| Y13 | UNKNOWN → PROCESSING | provider shows it in progress | POLL / WEBHOOK | — |
| Y14 | COMPLETED → REVERSED | provider reports the disbursement returned | WEBHOOK / POLL / RECON | `:reversed` |

Not allowed (absent edges): PAYOUT_REQUESTED → APPROVED (no AUTO tier), UNKNOWN → SUBMITTED (never resubmitted), anything
out of FAILED/REJECTED/CANCELLED/REVERSED, COMPLETED → anything but REVERSED. Event CHECKs: COMPLETED/PROCESSING/REVERSED
only from provider sources; UNKNOWN only from SYSTEM; FAILED never from SYSTEM; a staff FAILED needs an evidence record;
after submission the owner drives nothing.

## 3. Owner-visible labels

Requested · Under review · Approved · Processing · "Processing — we're confirming with the provider" (UNKNOWN) ·
"Paid (test)" · "Failed — funds returned to your balance" · Rejected · Cancelled · "Returned — please check your payout
details". Refusals carry only a reason category (LR-008: no compliance or fraud detail).

## 4. Approval invalidation

Approvals bind to `approval_round`. A new round starts when: the destination's details change, it is retired, suspended
or its verification expires (in the destination transaction); the pre-submission re-check finds a new REVIEW item, a
hold, a campaign that is no longer ACTIVE/COMPLETED, a destination mismatch, a reservation problem, or missing/expired
approvals. Amount, currency, parties, destination snapshot and environment are immutable (database guard).
Time-based approval expiry applies only when `payout.approval_validity` is configured.
