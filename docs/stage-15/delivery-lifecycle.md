# Stage 15 — Delivery lifecycle

One `app.notification_deliveries` row per notification and channel, created in the notification's transaction with the
routing decision (`decision`, `suppression_reason`). The state machine `notification_delivery` is guarded in SQL
(`app.guard_transition` + `app.status_transitions`) and mirrored in Go (`DeliveryEdges`, `webhook.go`); a test
(`TestDeliveryEdgesMatchMigration`) compares both.

```
(new) ─► QUEUED ─► PROCESSING ─► SUBMITTED ─► ACCEPTED ─► DELIVERED ─► READ
  │        │           │  └────► FAILED_RETRYABLE ─► PROCESSING (retry) / FAILED_PERMANENT / SUPPRESSED / CANCELLED
  │        │           ├────► FAILED_PERMANENT ─► QUEUED (operator replay) / CANCELLED
  │        │           └────► UNKNOWN ─► ACCEPTED / DELIVERED / READ / FAILED_* (evidence) / QUEUED (replay)
  │        └─► SUPPRESSED / CANCELLED
  ├─► SUPPRESSED (routing decision at creation)
  └─► DELIVERED (in-app: the inbox copy is the delivery)
```

| State | Meaning |
|---|---|
| QUEUED | routed; waiting for the sweep (`next_attempt_at`) |
| PROCESSING | claimed under a lease; the attempt row is committed **before** the provider call |
| SUBMITTED / ACCEPTED | the provider took the message — **acceptance is not delivery** |
| DELIVERED / READ | only from provider evidence (callback or status lookup); READ only where a channel reports it (simulated WhatsApp) |
| FAILED_RETRYABLE | a temporary or rate-limited failure; retried with backoff |
| FAILED_PERMANENT | a permanent failure, or retries exhausted; dead-lettered |
| UNKNOWN | the provider may have the message (timeout, lost worker); resolved by a status lookup, never assumed failed |
| SUPPRESSED | not sent, with a reason (consent, preference, contact, suppression, synthetic, live messaging disabled, …) |
| CANCELLED | an operator discarded it |

## Evidence

* `notification_attempts` (append-only): one row per provider call or status check (attempt number, provider, start and
  finish, outcome class, error code, provider message id, content hash, latency) — no message body and no destination.
  The idempotency key is derived (`<delivery id>:<attempt>`), so it is not stored.
* `notification_provider_events`: every verified callback, stored once (`provider_code`, `provider_event_id`
  UNIQUE), with whether it was applied and why not (`IN_FLIGHT`, `LOWER_RANK`, `NO_CHANGE`,
  `CONFLICTS_WITH_DELIVERED`, `CONFLICTS_WITH_FAILURE`). Only the IN_FLIGHT → applied update is allowed (trigger).
* Destinations are stored as a SHA-256 hash in suppressions; deliveries reference the recipient by user id.

## Callback precedence (out-of-order safety)

Positive states rank SUBMITTED < ACCEPTED < DELIVERED < READ; a callback below the current rank is stored and not
applied. A failure after DELIVERED is kept as conflicting evidence (`conflicting_evidence = true`, alert
`NOTIFICATION_CONFLICTING_EVIDENCE`) and never regresses the state. A callback that arrives while the send is still in
flight is stored `IN_FLIGHT` and applied once the send result is recorded (`applyPending`). Result: any arrival order
ends in the same state (tested with the simulator's duplicate and out-of-order callback scenarios and unit tests of
`ApplyCallback`).

## Crash safety

The provider call runs outside any transaction. A worker that dies between the call and the result leaves a
PROCESSING row whose lease expires: the next sweep turns it UNKNOWN (`WORKER_LEASE_EXPIRED`) and looks it up by its
idempotency key before anything could be resent (tested by leaving a claim with an expired lease, as a dead worker
would).
