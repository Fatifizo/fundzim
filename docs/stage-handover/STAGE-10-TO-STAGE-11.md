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

## 3. Stage 11 preparation topics (brief §60)

| Topic | What exists after Stage 10 | What Stage 11 must decide or obtain |
|---|---|---|
| Ledger account model | [chart-of-accounts.md](../stage-10/chart-of-accounts.md): `campaign_unsettled` → `campaign_payable` (available) → `campaign_payout_pending` → `payout_in_transit`; `campaign_held` and `campaign_reserve` for holds and reserves | Use these accounts only; adding a class is a migration + ADR-014 amendment |
| Campaign liability accounting | Campaign obligations are SYNC, non-negative, locked in ascending order when decreased | Whether these are FundZim liabilities, agency balances or off-balance-sheet is LR-002 (open); the ledger is an accounting record, never stored value (LR-003) |
| Settlement evidence requirements | `SETTLEMENT_MATCHED` moves a claim to `psp_settled`; evidence class SETTLEMENT_VERIFIED exists; reconciliation reports settlement NOT_PERFORMED | Provider settlement statements and their matching (PCR-007, PCR-016; Stage 17). No RELEASE to `campaign_payable` may happen in production without them |
| Available versus restricted funds | "Available" = `campaign_payable` only; held/reserve/pending are separate accounts | Restricted-fund treatment is not modelled (KI-S10-14, LR-002); what happens to cancelled or frozen campaign funds is LR-019 |
| Refund exposure | `REFUND_RESERVED` takes from where the money sits; `REFUND_FUND_SHORTFALL` and `refund_recoverable` exist; partial refunds are separate journals | Donor refund rights LR-018; provider refund API PCR-006; refunds still stop at APPROVED (KI-S9-07). A payout must leave enough for open refund requests (policy decision) |
| Chargeback exposure | `DISPUTE_*` rules and `chargeback_recoverable` exist, unused | Who bears chargeback loss after payout is LR-020; provider dispute behaviour PCR-011, PCR-012 |
| Reserve requirements | `RELEASE_WITH_RESERVE` / `RESERVE_RELEASE` rules exist (PD-33) | Reserve percentage and period are configuration with an owner and source (never hard-coded); provider-imposed reserves PCR-007 |
| Payout reservation | `PAYOUT_RESERVED` debits `campaign_payable` under a row lock (SC-1); insufficient balance is refused | Request-time and pre-submission eligibility re-checks ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)) |
| Payout idempotency | Keys `payout:{payout_id}:<step>` per rule; UNIQUE key; replay returns the same journal | The payout id is reused on every retry; UNKNOWN is never resubmitted (CLAUDE.md rule 9); provider recall/return behaviour PCR-014, PCR-015 |
| Payout approval dependencies | Maker-checker infrastructure, step-up, self-approval refusal | Thresholds for manual approval are LR-030 and must be configured limits; unconfigured ⇒ manual review |
| Beneficiary verification dependencies | Stage 5 beneficiaries and payout destinations (PAYOUT_VERIFIED) | Payouts to third-party or nominee accounts LR-024; age to receive payouts LR-021; KYC tiers LR-007; screening provider not selected (KI-S5-04) |
| Provider payout capabilities | None: no provider selected (ADR-040) | Payout API, destinations, limits PCR-013; recall PCR-014; returns PCR-015; fees PCR-016 |
| Maker-checker requirements | Ledger adjustments, period close and account status already maker-checker/step-up | Payout approval above thresholds, staff changes to destinations, unfreezing (ADR-017, operational-controls.md) |
| Reconciliation dependencies | Exception table, workflow, source references | Payout exception types (completed without journal, journal without payout, returned payout) and provider payout reports |
| Outstanding legal requirements | — | LR-001, LR-002, LR-003, LR-004, LR-007, LR-018, LR-019, LR-020, LR-021, LR-024, LR-030, LR-036 – LR-038, LR-076 ([open-legal-questions.md](../compliance/open-legal-questions.md)) |

## 4. Open blockers carried

ADR-040 DECISION_DEFERRED; PCR items unconfirmed (payout rails, fees, settlement statements); LR-001/002/003/076;
accounting review of the chart (KI-S10-02); production ledger database and DR (KI-S10-06); stage acceptance not
recorded.
