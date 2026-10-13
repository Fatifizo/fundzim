# Stage 8 — Payment state machine (as implemented)

The Stage 2 machine ([payment-state-machine.md](../architecture/payment-state-machine.md), ADR-020, ADR-024)
implemented in `internal/payments/machine.go` and `apply.go`, and guarded in the database by
`app.guard_transition('payment_intent')` + `app.status_transitions`. Go and SQL edge lists are identical
(`TestPaymentEdgesMatchMigration`), and so are the ranks (`TestRanksMatchMigration`).

## 1. Edges (42)

| Code | Edges |
|---|---|
| P1 | `'' → CREATED` |
| P2 | `CREATED → PENDING` |
| P3 | `CREATED → UNKNOWN` |
| P4 | `CREATED → FAILED` |
| P5 | `CREATED → CANCELLED` |
| P6 | `PENDING → REQUIRES_ACTION` |
| P7 | `REQUIRES_ACTION → PENDING` |
| P8 | `PENDING / REQUIRES_ACTION / UNKNOWN → AUTHORISED` (3) |
| P9 | `PENDING / REQUIRES_ACTION / AUTHORISED / UNKNOWN → SUCCEEDED` (4) |
| P10 | `PENDING / REQUIRES_ACTION / AUTHORISED / UNKNOWN → FAILED` (4) |
| P11 | `PENDING / REQUIRES_ACTION / AUTHORISED / UNKNOWN → EXPIRED` (4) |
| P12 | `PENDING / REQUIRES_ACTION / AUTHORISED / UNKNOWN → CANCELLED` (4) |
| P13 | `PENDING → UNKNOWN` |
| P14 | `UNKNOWN → PENDING`, `UNKNOWN → REQUIRES_ACTION` (2) |
| P15 | `FAILED / EXPIRED / CANCELLED → SUCCEEDED` (3) — late authoritative success |
| P16 | `SUCCEEDED → PARTIALLY_REFUNDED`, `SUCCEEDED → REFUNDED`, `PARTIALLY_REFUNDED → REFUNDED` (3) |
| P17 | `SUCCEEDED / PARTIALLY_REFUNDED → DISPUTED` (2) |
| P18 | `DISPUTED → SUCCEEDED` |
| P18b | `DISPUTED → PARTIALLY_REFUNDED` |
| P19 | `DISPUTED → CHARGED_BACK` |
| P20 | `SUCCEEDED / PARTIALLY_REFUNDED → CHARGED_BACK` (2) |

Every applied transition writes a `payment_events` row with its code (`ck_payment_events_transition_code`), in the
same transaction (deferred trigger `trg_payment_intents_require_event`). Nothing in Stage 8 drives P17–P20 (disputes
and chargebacks are deferred); P16 is reachable only from a provider-reported status (KI-S8-10). `AUTHORISED` is
applied only for `CARD` (PCR-025); the sandbox never reports it.

## 2. Ranks (`app.payment_status_rank`, `payments.Rank`)

| Status | Rank | Status | Rank |
|---|---|---|---|
| `CREATED` | 0 | `FAILED`, `CANCELLED`, `EXPIRED` | 50 |
| `UNKNOWN` | 5 | `SUCCEEDED` | 60 |
| `PENDING` | 10 | `PARTIALLY_REFUNDED` | 70 |
| `REQUIRES_ACTION` | 20 | `DISPUTED` | 80 |
| `AUTHORISED` | 30 | `REFUNDED` | 90 |
| | | `CHARGED_BACK` | 95 |

Exceptions (moves allowed without a higher rank): `PENDING → UNKNOWN` (source `INTERNAL` only), `REQUIRES_ACTION →
PENDING`, `DISPUTED → SUCCEEDED`, `DISPUTED → PARTIALLY_REFUNDED`. Stage 2's `PARTIALLY_REFUNDED →
PARTIALLY_REFUNDED` exception is not implemented (no refund execution): a repeated status is recorded `LOWER_RANK`.

## 3. `apply` (the only path that changes a payment's status)

`apply(tx, lockedPayment, observation)` is called by `applyCreate` (source `SYNC` / `INTERNAL`), `PollPayment`
(`POLL`), `recreate` (`SYNC`), `ApplyWebhook` (`WEBHOOK`) and the in-flight sweeper (`INTERNAL`). The payment row is
always locked `FOR UPDATE` by the caller, so a webhook, a poll and the sweeper for one payment serialise. Steps:

1. **Record the provider observation** (provider sources with a raw status): upsert `payment_transactions` (raw
   verbatim, mapped status, mapping version, reported amount, payer fingerprint/mask), set
   `provider_payment_ref` if empty, insert `payment_provider_references` (`ON CONFLICT DO NOTHING`).
2. **Unmapped status** (`mapped = false`): event `applied = false, ANOMALY` targeting `UNKNOWN`, reconciliation
   reference `UNMAPPED_STATUS`, alert; the inbox row becomes `UNRECOGNISED`. Never guessed.
3. **Amount or currency mismatch** on a `SUCCEEDED` or `AUTHORISED` target: `applied = false, ANOMALY`, reference
   `AMOUNT_MISMATCH` or `CURRENCY_MISMATCH`, alert. Never applied.
4. **Duplicate provider event** (same `provider_event_id` already applied to this payment): `applied = false,
   DUPLICATE`. Backed by the partial unique index on `payment_events`.
5. **Implied intermediate step**: from `CREATED`, a target that is not an edge from `CREATED` but is an edge from
   `PENDING` is applied as `CREATED → PENDING` (P2) first, each step with its own event.
6. **Decide**:
   - same status ⇒ `applied = false, LOWER_RANK`;
   - an edge, and (higher rank **or** an exception), and the source allowed (§4), and (`AUTHORISED` ⇒ `CARD`) ⇒
     **transition** (§3.1);
   - a refund/dispute/chargeback target before `SUCCEEDED` that arrived through the inbox ⇒ `applied = false,
     PARKED`, inbox row `PARKED` until `SUCCEEDED` (§5);
   - lower or equal rank ⇒ `applied = false, LOWER_RANK` (out-of-order event, e.g. `pending` after `SUCCEEDED`);
   - otherwise (a forbidden jump, e.g. `CREATED → REFUNDED` from a poll) ⇒ `applied = false, ANOMALY` and, for provider
     sources, a reference `ANOMALY`.

### 3.1 Transition side effects (same transaction)

- Event (`applied = true`, code), `UPDATE payment_intents` (status, rank, `last_authoritative_source` for provider
  sources, unknown fields, polling fields, failure code), outbox `payments.payment_status_changed`.
- To `UNKNOWN`: `unknown_since`, `unknown_reason`, horizon = now + 72 h, first poll after 3 s, poll counter reset,
  metric `payment_unknown_outcomes_total`.
- To a terminal status: polling stops; `FAILED` keeps the provider's failure code.
- To `PENDING`, `REQUIRES_ACTION`, `AUTHORISED`: safety-net poll after 5 s (webhooks are primary).
- To `SUCCEEDED` (not from `DISPUTED`): the `LedgerPoster` seam (nil at runtime; test recorder only) with key
  `payment:{id}:capture`; outbox `payments.payment_succeeded`; for P15 outbox `payments.late_success` and reference
  `LATE_SUCCESS`; if another payment of the same donation intent already succeeded, reference `DUPLICATE_SUCCESS`;
  donation intent → `PAID` (event source `INTERNAL`).
- Any terminal status resolves open `UNKNOWN_HORIZON_EXCEEDED` / `NOT_FOUND_AFTER_HORIZON` references as
  `RESOLVED_BY_PROVIDER_STATUS` (never "reconciled").
- Parked inbox events waiting for the reached status are re-dispatched (`psp.ReleaseParked`).

## 4. Source rules

| Target | Allowed sources (Go `sourceAllowed`) | Database |
|---|---|---|
| `SUCCEEDED` | `SYNC`, `WEBHOOK`, `POLL`, `RECON` | `ck_payment_events_success_source` |
| `UNKNOWN` | `INTERNAL` only (our own indeterminate call or the crash sweeper) | `ck_payment_events_unknown_internal` |
| `FAILED` | `SYNC`, `WEBHOOK`, `POLL`, `RECON`; `INTERNAL` only from `CREATED` (nothing reached the provider) | `ck_payment_events_failed_source` also allows `STAFF` with an evidence record (`ck_payment_events_staff_failed_evidence`); no Stage 8 endpoint uses it |
| any other | any source except `DONOR` | `ck_payment_events_donor` (donor events need a donor actor) |
| donation intent `PAID` | `INTERNAL` (because a payment succeeded) | `ck_payment_events_paid_internal` |

A browser never confirms a payment: the donor return endpoint only schedules a status query, and no endpoint sets a
payment to `SUCCEEDED` or `FAILED` (`TestPaymentsStaffOperations` probes for such routes).

## 5. Parking

A `REFUNDED`, `PARTIALLY_REFUNDED`, `DISPUTED` or `CHARGED_BACK` webhook that arrives before the payment reached
`SUCCEEDED` is recorded `applied = false, PARKED` and the inbox row moves to `PARKED` with
`parked_until_status = SUCCEEDED` and `park_deadline_at = parked_at + 72 h` (`psp.ParkDeadline`). When the payment
reaches `SUCCEEDED`, the parked rows are re-dispatched in the same transaction, ordered by
`provider_occurred_at`, then `provider_event_id`, then receipt. Past the deadline the sweep opens a
`PARKED_DEADLINE_EXCEEDED` reference; the event is never discarded. Observations from a poll are not parked (no
inbox row): a poll that reports such a status early is an `ANOMALY`.

## 6. UNKNOWN

| Aspect | Implementation |
|---|---|
| Entry | `INDETERMINATE`, `AUTH_CONFIG_ERROR` or `NOT_FOUND` on create (P3); a create call left `IN_FLIGHT` past deadline + 30 s (`CRASH_DURING_CREATE`, P3). `PENDING → UNKNOWN` (P13) exists but no Stage 8 path uses it |
| Donor effect | New attempts and cancellation refused with 409 `PAYMENT_OUTCOME_UNKNOWN` ("Please don't pay again") |
| Resolution | Webhook, status poll (`POLL`), or staff status query; P14 back to `PENDING`/`REQUIRES_ACTION`, P9/P10/P11/P12 to a final state |
| Polling | First after 3 s, then 5 s × 2ⁿ capped at 10 min |
| `NOT_FOUND` | `FAILED` only with documented finality (sandbox: > 60 s since our reference was sent); otherwise stays `UNKNOWN`; after ≥ 2 polls a re-create with the same reference only when the capability has `idempotent_create` |
| Horizon | 72 h (`payments.UnknownHorizon`) because no provider documents one (PCR-010, flagged KI-S8-11). Past it: reference `UNKNOWN_HORIZON_EXCEEDED` (or `NOT_FOUND_AFTER_HORIZON` if the last query was not-found), `needs_reconciliation = true`, automatic polling stops. The payment stays `UNKNOWN` |
| Never | `UNKNOWN → FAILED` by assumption (Go and DB) |

## 7. Brief states ↔ Stage 8 (ADR-039 §3)

| Brief state | Stage 8 |
|---|---|
| `CREATED` | `CREATED` |
| `VALIDATED` | Not a state: validation runs before anything is stored; failure is a 4xx with nothing written |
| `PROVIDER_PENDING`, `PROCESSING` | `PENDING` |
| `REQUIRES_ACTION` | `REQUIRES_ACTION` (+ a payment session) |
| `SUCCEEDED`, `FAILED`, `CANCELLED`, `EXPIRED`, `UNKNOWN` | Same names |
| `RECONCILIATION_REQUIRED` | Not a state: `payment_intents.needs_reconciliation = true` + an open `payment_reconciliation_references` row ([reconciliation-foundation.md](reconciliation-foundation.md)) |
| (Stage 2 only) `AUTHORISED`, `PARTIALLY_REFUNDED`, `REFUNDED`, `DISPUTED`, `CHARGED_BACK` | Kept in the machine; see §1 |

## 8. Other machines (all DB-guarded)

| Machine | Edges |
|---|---|
| `donation_intent` | `'' → OPEN`; `OPEN → PAID / EXPIRED / CANCELLED`; `EXPIRED → PAID`; `CANCELLED → PAID` (late success wins) — [donation-intents.md](donation-intents.md) |
| `payment_session` | `'' → OPEN`; `OPEN → RETURNED / EXPIRED / CLOSED`; `RETURNED → CLOSED` (only `OPEN → RETURNED` is driven in Stage 8, KI-S8-21) |
| `refund_request` | [refund-architecture.md](refund-architecture.md) §1 (no execution edges) |
| `payment_reconciliation_reference` | [reconciliation-foundation.md](reconciliation-foundation.md) |
| `provider_webhook_inbox` | [webhook-security.md](webhook-security.md) §6 |
