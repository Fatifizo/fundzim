# Stage 10 → Stage 11 Handover: Withdrawals & Payouts

> Stage 10 is implemented on branch `stage-10/ledger` and **awaits acceptance** (Stages 1–9 too; Stage 9 is PARTIAL).
> Do not start Stage 11 until the owner accepts and asks for it. This document prepares Stage 11; it implements
> nothing. No payout may execute, no withdrawable balance may be exposed, and no real money may move until a
> provider is selected (ADR-040), legal sign-off exists and production posting is enabled under an ADR.

Stage 10 docs: [implementation](../stage-10/implementation.md) · [ledger architecture](../stage-10/ledger-architecture.md)
· [posting rules](../stage-10/posting-rules.md) · [balances](../stage-10/balance-calculation.md) ·
[reconciliation](../stage-10/reconciliation.md) · [known issues](../stage-10/known-issues.md). ADR-042.

## 1. What the ledger offers Stage 11

| Capability | Where | Notes |
|---|---|---|
| Payout posting rules | `PAYOUT_RESERVED`, `PAYOUT_SUBMITTED`, `PAYOUT_COMPLETED`, `PAYOUT_FAILED`, `PAYOUT_RELEASED`, `PAYOUT_REVERSED`, `PAYOUT_FEE` (design: payout-lifecycle Y1–Y14) | Present, version 1, **unused**. Key formats `payout:{payout_id}:<step>` |
| Sufficiency under lock | `Post` locks non-negative SYNC accounts in ascending id order and refuses `LEDGER_INSUFFICIENT_FUNDS`; CHECK backstop | `PAYOUT_RESERVED` debits `campaign_payable` (SC-1): concurrent payout requests cannot overdraw |
| Account status | FROZEN accounts accept only approved adjustments | A frozen campaign's payable cannot be reserved |
| Campaign freeze rules | `CAMPAIGN_FROZEN` / `CAMPAIGN_UNFROZEN` (payable ↔ held) | Not wired; Stage 11 decides with campaigns (maker-checker unfreeze, ADR-017) |
| Balances | `ledger.Service.Account`, `v_balances_current` | `campaign_payable` is the only candidate for "available"; it is **not** withdrawable while the ledger is TEST |
| Idempotency | unique key per payout step | Payout id reused on every retry; UNKNOWN never resubmitted (CLAUDE.md rule 9) |
| Reconciliation | exception table and workflow | Add payout exception types (e.g. payout completed without journal) |

## 2. Rules Stage 11 must keep

- A payout may only be requested against `liability:campaign_payable`, which is reached only through `RELEASE`
  after `SETTLEMENT_MATCHED` — i.e. after real settlement evidence. Until settlement reconciliation exists
  (KI-S10-04) there is no available balance in any production sense.
- Payout eligibility re-checks at request and before submission (CLAUDE.md rule 12); unconfigured limits route to
  manual review.
- The ledger posts; the payouts module must call `ledger.Service` (through an interface, archtest), never write
  ledger tables, and must not run provider calls inside the posting transaction. Decide whether payout postings
  are synchronous (payouts given a ledger-service call on the ledger pool, committed before the provider call) or
  outbox-driven like payments (ADR-042 §3) — record it in an ADR.
- Never display a TEST ledger balance as funds; never expose a withdrawable balance before production posting is
  enabled under an ADR with legal sign-off.

## 3. Open blockers carried

ADR-040 DECISION_DEFERRED; PCR items unconfirmed (payout rails, fees, settlement statements); LR-001/002/003/076;
accounting review of the chart (KI-S10-02); production ledger database and DR (KI-S10-06); stage acceptance not
recorded.
