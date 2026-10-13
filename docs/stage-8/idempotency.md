# Stage 8 — Idempotency and concurrency

Every payment mutation is idempotent at three layers: the HTTP middleware, database unique constraints, and the
provider reference (CLAUDE.md financial rule 7).

## 1. HTTP: `Idempotency-Key` (REQUIRED mode)

`internal/platform/idempotency` runs in **Required** mode on:

| Route | Scope | DB backstop |
|---|---|---|
| `POST /donation-intents` | `payments.donation_intents.create:user:<id>` | `uq_donation_intents_idempotency (donor_user_id, idempotency_key)` |
| `POST /donation-intents/{id}/attempts` | `payments.attempts.create:user:<id>` | `uq_payment_intents_idempotency (donation_intent_id, idempotency_key)` |
| `POST /admin/payments/attempts/{id}/refund-requests` | `payments.refund_requests.create:user:<staff id>` | `uq_refund_requests_idempotency (requested_by, idempotency_key)` |

Semantics (unchanged platform behaviour): missing key → 400 `IDEMPOTENCY_KEY_REQUIRED`; malformed → 400
`IDEMPOTENCY_KEY_INVALID`; same key + same request (method, route, path, query, canonical JSON body) → the stored
response is replayed with `Idempotent-Replayed: true`; same key + different request → 409 `IDEMPOTENCY_KEY_REUSED`;
same key while the first is running → 409 `IDEMPOTENCY_REQUEST_IN_PROGRESS` with `Retry-After`; 5xx, 408 and 429
release the key. Records expire after the platform TTL; the database backstop then still returns the original row
(and refuses a different body with `IDEMPOTENCY_KEY_REUSED`).

Attempt creation answers 201 with the payment's real status for every provider outcome (including `UNKNOWN`), so the
outcome is stored and a replay never triggers a second provider call.

## 2. Provider reference

`payment_id` (= `payment_intents.id`, UUIDv7) is the merchant reference and the provider idempotency key. It is
never reused: `ck_payment_attempts_reference` forces every call's `our_reference` to equal it. After an unknown
outcome the next action is a status query by that reference; a re-create happens only when the capability says
`idempotent_create` (PCR-009) and always with the same reference. A donor retry after a definite failure is a **new**
payment with a new reference. The sandbox's create is idempotent per reference.

## 3. Webhooks

`uq_provider_webhook_inbox_event (provider_id, provider_event_id)` with `ON CONFLICT DO NOTHING`: a duplicate is
acknowledged (200, `duplicate: true`) and creates no dispatch. A re-dispatched or replayed event that reaches
`apply` again is recorded `DUPLICATE` (`uq_payment_events_applied_provider_event (payment_intent_id,
provider_event_id) WHERE applied`). Dispatch itself is idempotent: processed, parked, unrecognised and dead-lettered
rows are left alone.

## 4. Concurrency control

| Operation | Mechanism |
|---|---|
| Attempts on one donation intent | `SELECT … FOR UPDATE` on the donation intent: exactly one live payment (test: 20 concurrent attempts → one 201) |
| Any change to one payment (sync result, webhook, poll, sweeper, refund request) | `SELECT … FOR UPDATE` on the payment row; polls and the sweeper use `SKIP LOCKED` and retry later |
| Over-refund | DB trigger locks the payment row and sums approved requests (concurrent requests serialise) |
| Inbox claim | row lock; status machine guarded by `app.guard_transition` |
| Simulator delivery | `FOR UPDATE SKIP LOCKED` + 30 s lease (HTTP outside the transaction) |
| Same request, same key, 100 at once | middleware lease + DB unique (test: one intent, one id returned) |

`READ COMMITTED` throughout: every decision reads state covered by a single row lock (payment-schema §6). Provider
calls never run inside a database transaction.

## 5. Exactly-once effects

- One applied `SUCCEEDED` event per payment (state machine + duplicate check), one `payments.payment_succeeded`
  outbox event, at most one posting request per key `payment:{id}:capture` (test recorder; no ledger in Stage 8).
- Status changes and their `payment_events` row are committed together (deferred trigger
  `app.require_payment_event`): a rolled-back transaction leaves neither.
