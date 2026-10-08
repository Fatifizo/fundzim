# ADR-024: Payment intent vs provider transaction; database-guarded state machines

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2

## Context

ADR-020 fixed the canonical payment and payout states. Providers expose their own state vocabularies, which
differ from each other and from ours. The Stage 2 brief requires us to distinguish the payment intent's state
from the provider's transaction state, and to keep the raw provider event. DATABASE §6 requires illegal
transitions to be impossible even through direct SQL.

## Decision

1. **Separate records:**
   - `payment_intents` holds FundZim's canonical state, which is authoritative for business logic.
   - `payment_transactions` holds the provider-side record: the provider's `raw_status` verbatim, the mapped
     canonical status, and the source of that mapping.
   - `payment_attempts` logs every outbound call (create, status query, cancel) with its classification.
   - `payment_events` is the append-only history of intent transitions.

   Raw webhook bodies stay in `psp.provider_webhook_inbox`. Refunds and payouts follow the same pattern
   (`refund_transactions`, `payout_attempts`, `payout_provider_references`).
2. **Mapping is code in the provider adapter**, versioned with the adapter. An unmapped raw status maps to
   `UNKNOWN` and raises an alert. It never maps to `FAILED`.
3. **The brief's `PROCESSING` payment state is not added.** It is represented by `PENDING` /
   `REQUIRES_ACTION` (ADR-020). `PROCESSING` remains a payout state.
4. **Database-guarded transitions:** every critical machine has `status` + `CHECK`, an
   `app.guard_transition('<machine>')` trigger, and its edges in `app.status_transitions`. The critical
   machines are payment intent, refund request, refund transaction, dispute, payout request, campaign,
   compliance case, KYC case and ledger adjustment. The Go state machine must agree with the table; this is
   tested in both directions.
5. Ranking and precedence for out-of-order events stay in application code (transaction-lifecycle §5). The
   trigger is the backstop for impossible edges, for example `SUCCEEDED → FAILED` or `UNKNOWN → SUBMITTED`
   for payouts.

6. **ADR-020 amendment:** a won dispute returns the payment to the status it had before the dispute. A new
   edge, `DISPUTED → PARTIALLY_REFUNDED` (P18b), covers a dispute won on a partially refunded payment. Without
   it, the partial-refund state would be lost.

7. **Synchronous completion:** an authoritative synchronous provider response may complete a payout (`SYNC`
   source). This refines Stage 1 Y10. Database CHECKs restrict `→ FAILED` to authoritative sources, so a
   timeout handler can never write `FAILED` (design-baseline §12 I-21, I-22).

## Consequences

### Positive
- Raw provider truth is never lost.
- A single canonical vocabulary is used for business logic.
- Illegal transitions are impossible even under direct SQL.

### Negative / costs
- Edges are duplicated in Go and SQL; a test keeps them in sync.

### Follow-up work
- Stage 8 implements payment intents. Stage 11 implements payouts.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Store only canonical status | Loses provider evidence needed for disputes and reconciliation |
| Store provider status as the state | Business logic would depend on each provider's vocabulary |
| Postgres `ENUM` + transition function per table | ENUMs are hard to evolve (DATABASE §6); a generic registry is simpler |

## Security implications

Raw payloads may contain personal data. They stay in the inbox (C2/C3 per field), with retention under LR-012.

## Financial implications

A transition and its ledger posting commit together (PAYMENTS §6). Only authoritative sources may set
`SUCCEEDED`.

## Related

ADR-007, ADR-020, ADR-029; [payment-state-machine.md](../architecture/payment-state-machine.md),
[payout-state-machine.md](../architecture/payout-state-machine.md), [payment-schema.md](../database/payment-schema.md).
