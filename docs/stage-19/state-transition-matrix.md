# Stage 19 — Payment, payout and reservation transition matrices

Source of truth: `internal/payments/machine.go` (`PaymentEdges`, `Rank`, `sourceAllowed`) mirrored by
`app.status_transitions` machine `payment`; `migrations/20261014090000_payouts.sql` machines `payout` and
`payout_reservation`. `TestPaymentEdgesMatchMigration` keeps Go and SQL identical. Sources: SYNC (the provider call's
own answer), WEBHOOK, POLL (status query), RECON (Stage 17 approved resolution), INTERNAL (system), DONOR, STAFF,
OWNER, SYSTEM.

## 1. Payments

| From → to | Code | Allowed sources | Evidence required | Ledger | Notifications / events | Reversible |
|---|---|---|---|---|---|---|
| — → CREATED | P1 | INTERNAL | donor's attempt (idempotency key) | none | — | — |
| CREATED → PENDING | P2 | SYNC, WEBHOOK, POLL | provider acknowledgement | none | `payment_status_changed` | yes (→ UNKNOWN by INTERNAL only from PENDING, P13) |
| CREATED → UNKNOWN | P3 | INTERNAL | timeout / crash during create (`CRASH_DURING_CREATE`, `TIMEOUT`) | none | `payment_status_changed` → donor "being confirmed" notice, risk signal | resolved by P8/P9/P10/P11/P12/P14 |
| CREATED → FAILED | P4 | SYNC, WEBHOOK, POLL, RECON; INTERNAL only from CREATED | definite provider refusal or documented not-found finality | none | donor failure notice | P15 late success only by provider evidence |
| PENDING ↔ REQUIRES_ACTION | P6/P7 | provider sources | provider status | none | status changed | — |
| … → AUTHORISED | P8 | provider sources | provider status | none | status changed | — |
| … → SUCCEEDED | P9 | **SYNC, WEBHOOK, POLL, RECON only** | authoritative provider evidence | PRODUCTION only: `DONATION_CAPTURED` posting request (never for test payments; production refused) | `payments.payment_succeeded` once → donor success notice; donation intent PAID | P16/P17 |
| … → FAILED / EXPIRED / CANCELLED | P10–P12 | SYNC, WEBHOOK, POLL, RECON (never STAFF, never DONOR) | provider status | none | failure notice | P15 |
| PENDING → UNKNOWN | P13 | INTERNAL | lost answer | none | status changed | P14 |
| UNKNOWN → PENDING / REQUIRES_ACTION | P14 | provider sources | provider status | none | — | — |
| FAILED / EXPIRED / CANCELLED → SUCCEEDED | P15 | provider sources | late authoritative success | as P9 | `late_success` + `payment_succeeded`; LATE_SUCCESS reference | — |
| SUCCEEDED → (PARTIALLY_)REFUNDED, DISPUTED, CHARGED_BACK | P16–P20 | provider sources | provider evidence (refund execution is disabled; disputes deferred) | none in Stage 19 | — | P18 |

Precedence: a lower-rank status never overwrites a higher one (`Rank`), except the listed P7/P13/P18 exceptions. A
stale or duplicate event is recorded `applied = false` (`LOWER_RANK`, `NOT_AN_EDGE`, `DUPLICATE`, `TERMINAL`) and never
overwrites; event timestamps alone are never authoritative. Contradictory evidence (FAILED after SUCCEEDED, other
amount) is a CONTRADICTORY_EVIDENCE reference and is never applied.

**Polling (Stage 19).** Awaiting statuses (CREATED, PENDING, REQUIRES_ACTION, AUTHORISED, UNKNOWN) are polled with
backoff 5 s doubling to 10 min, ±20 % jitter, until the budget ends: `PollHorizon` from creation (non-UNKNOWN),
`UnknownHorizon` from `unknown_since` (UNKNOWN), or `MaxStatusPolls` queries. Exhaustion is *not* a transition: the
status stays, `needs_reconciliation` is set, a POLL_BUDGET_EXHAUSTED (or UNKNOWN_HORIZON_EXCEEDED /
NOT_FOUND_AFTER_HORIZON) reference opens, `next_poll_at` is cleared. Resolution: a later WEBHOOK (apply resolves the
reference when the status becomes terminal) or Stage 17 RECON evidence through an approved resolution. Nothing is ever
resubmitted to resolve it.

Initiators: webhook dispatcher (`ApplyWebhook`), sweep (`Sweep` → routine `poll`), staff status query
(`RequestStatusQuery`, audited, never sets a status itself), Stage 17 resolutions (`ApplyReconciliationOutcome`,
source RECON). Every applied transition writes a `payment_events` row and an outbox event in the same transaction;
transitions out of UNKNOWN from a provider source are audited (`PAYMENT_STATUS_RECONCILED`).

## 2. Payouts

| From → to | Code | Initiator / source | Required evidence or check | Ledger (`syncLedger`, after the state commits) | Reservation |
|---|---|---|---|---|---|
| — → PAYOUT_REQUESTED | Y1 | OWNER | request-time eligibility PASS (22 checks), step-up | — | — |
| PAYOUT_REQUESTED → PENDING_REVIEW | Y2 | SYSTEM (`reserve`) | reservation journal posted in the same flow | `PAYOUT_RESERVED` (payable → pending) | ACTIVE |
| → REJECTED | Y4 | SYSTEM / STAFF | ledger refusal, staff rejection, pre-submit FAIL | `PAYOUT_RELEASED` (pending → payable/held) | RELEASED (EXPIRED for REQUEST_EXPIRED) |
| → CANCELLED | Y5 | OWNER / STAFF / SYSTEM (expiry) | before submission only | `PAYOUT_RELEASED` | RELEASED / EXPIRED |
| PENDING_REVIEW → APPROVED | Y6 | STAFF (FINANCE) | enough distinct approvals for the tier; separation of duties (Go + DB, linked identities); step-up | — | ACTIVE |
| APPROVED → PENDING_REVIEW | Y7 | SYSTEM | approvals invalidated (destination change, round + 1) | — | ACTIVE |
| APPROVED → SUBMITTED | Y8 | SYSTEM | pre-submission re-check PASS | `PAYOUT_SUBMITTED` (pending → in-transit); the CREATE_PAYOUT attempt is refused by the DB until it exists | ACTIVE |
| SUBMITTED → PROCESSING | Y9 | SYNC, WEBHOOK, POLL, RECON | provider status | — | ACTIVE |
| SUBMITTED/PROCESSING/UNKNOWN → COMPLETED | Y10 | SYNC, WEBHOOK, POLL, RECON | provider status (amount and currency must match, else ANOMALY) | `PAYOUT_COMPLETED` (in-transit → settled) | CONSUMED (BLOCKED → CONSUMED from UNKNOWN) |
| → FAILED | Y11 | SYNC, WEBHOOK, POLL, RECON, STAFF (authoritative final failure only) | provider evidence | `PAYOUT_FAILED` (in-transit → payable/held) | RELEASED |
| SUBMITTED/PROCESSING → UNKNOWN | Y12 | SYSTEM | timeout, crash during the call (IN_FLIGHT recovery) | — | BLOCKED; **never resubmitted** |
| UNKNOWN → PROCESSING | Y13 | provider sources | provider status | — | ACTIVE |
| COMPLETED → REVERSED | Y14 | provider sources | provider "returned" | `PAYOUT_REVERSED` (settled → payable/held) | stays CONSUMED |

Initiators: owner and staff HTTP routes (policy-checked, step-up), the submit/dispatch job, the poll job, webhooks,
the Recover job (reservations, crashed attempts, missing journals, expiry), payout reconciliation (Stage 19: settles a
pending ledger sync before evaluating), Stage 17 resolutions (`ApplyReconciliationOutcome`). Notifications:
`payouts.requested`, `payouts.status_changed`, `payouts.exception_opened` through the outbox (owner sees coarse reason
categories only, LR-008). Every transition writes `payout_events` and is audited.

## 3. Reservation (`payout_reservation`)

| From → to | When | Journal link set |
|---|---|---|
| — → ACTIVE | reservation journal posted (`reserve`) | `ledger_reserve_txn_id` |
| ACTIVE → BLOCKED | payout UNKNOWN | — |
| BLOCKED → ACTIVE | payout leaves UNKNOWN to a non-final status | — |
| ACTIVE/BLOCKED → CONSUMED | completion journal linked (`syncLedger.link`) | `ledger_consume_txn_id` (requires `ledger_submit_txn_id`) |
| ACTIVE/BLOCKED → RELEASED | failure or release journal linked | `ledger_release_txn_id` |
| ACTIVE → EXPIRED | release journal of an expired request | `ledger_release_txn_id` |

Every other change is refused by the database (guard, CHECKs, set-once trigger). The submit journal link
(`ledger_submit_txn_id`) changes no status. Stage 19: a link writes its RESERVATION_* / LEDGER_POSTED event only when
its own UPDATE linked the row; a concurrent sync that found the link already set records nothing.
