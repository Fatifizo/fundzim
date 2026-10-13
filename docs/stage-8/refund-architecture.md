# Stage 8 — Refund architecture

Stage 8 records and decides refund **requests**. It never executes a refund: no provider refund call, no
`refund_transactions`, no ledger reservation (ADR-039 §4). Design: [refund-and-dispute-architecture.md](../payments/refund-and-dispute-architecture.md).

## 1. Machine `refund_request` (Stage 8 edges only)

```
'' → REQUESTED → PENDING_APPROVAL | WITHDRAWN
PENDING_APPROVAL → APPROVED | REJECTED | ON_HOLD | WITHDRAWN
ON_HOLD → PENDING_APPROVAL | REJECTED
```

`APPROVED` is final in Stage 8: the edges `APPROVED → EXECUTING → COMPLETED | FAILED` are **not seeded**, so execution
is impossible even in SQL (integration test `TestPaymentsRefundRequestsNeverExecute` tries it). Stage 11 adds them
together with `refund_transactions` and the reservation journal. Every status change has a `payment_events` row
(subject `REFUND_REQUEST`) in the same transaction.

## 2. Controls

| Control | Where |
|---|---|
| Request (maker): `refund.request` (SUPPORT, COMPLIANCE) | route policy |
| Approve / reject / hold / resume (checker): `refund.approve` (FINANCE) with a fresh step-up | route policy (permission `requires_step_up`); `decided_step_up_at` stored and required for APPROVED (`ck_refund_requests_approval_step_up`) |
| Maker ≠ checker | Go (`REFUND_SELF_APPROVAL_FORBIDDEN`) and `ck_refund_requests_maker_checker` |
| Withdraw | requester only |
| Payment must be `SUCCEEDED` / `PARTIALLY_REFUNDED` | Go and trigger `app.refund_requests_check_refundable` |
| No over-refund | trigger locks the payment and checks Σ approved + this ≤ captured (at request and at approval); currency forced equal by composite FK |
| Idempotent request | `Idempotency-Key` (required) + `uq_refund_requests_idempotency` |
| Reason | `reason_code` allow-list + `reason_text` 3–2000 characters; decision reason required |
| Audit | `payments.refund.requested` / `.approve` / `.reject` / `.hold` / `.resume` / `.withdraw`; outbox `payments.refund_requested`, `payments.refund_decided` (`execution: DISABLED`) |
| Expiry | `expires_at` = request + 72 h; approval refused after expiry |

Every API response carries `execution: "DISABLED"`; the staff UI shows that approved refunds are not sent anywhere.

## 3. Open dependencies

LR-018 (consumer refund rights), LR-019 (cancelled/frozen campaigns), LR-081 (refund to another instrument;
same-instrument only), LR-083 (undeliverable refunds), PCR-006 (refund API, partial and idempotent refunds). Disputes
and chargebacks (LR-020, PCR-011) are deferred with their tables; the payment machine keeps their edges.
