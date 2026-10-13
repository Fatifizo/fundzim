# Stage 9 → Stage 10 Handover: Double-Entry Financial Ledger

> Stage 9 is implemented on branch `stage-9/provider-integrations` and **awaits acceptance** (Stages 1–8 too). Do not
> start Stage 10 until the owner accepts and asks for it. No payment provider is selected (ADR-040); every payment
> that exists is a **test payment**.

Stage 9 docs: [implementation](../stage-9/implementation.md) · [payment security](../stage-9/payment-security.md) ·
[reconciliation evidence](../stage-9/reconciliation-evidence.md) · [secret management](../stage-9/secret-management.md)
· [known issues](../stage-9/known-issues.md). ADR-040, ADR-041.

## 1. What the ledger receives

| Signal | Where | Ledger meaning |
|---|---|---|
| `payments.LedgerPoster.Post` (posting key `payment:<id>:capture`, rule `DONATION_CAPTURED`) | `payments.onSucceeded`, inside the SUCCEEDED transaction | **Called only for `execution_environment = PRODUCTION`** — which cannot exist yet (DB CHECK). Stage 10 must keep this guard |
| Outbox `payments.payment_succeeded` | same transaction | Carries `test_payment` and `execution_environment`; consumers must ignore `test_payment = true` |
| `payment_provider_evidence` | append-only | Evidence for reconciliation; **not** a balance; never post from it |
| Reconciliation references (`LATE_SUCCESS`, `DUPLICATE_SUCCESS`, `AMOUNT_MISMATCH`, `CONTRADICTORY_EVIDENCE`, …) | `payment_reconciliation_references` | Inputs to Stage 17; a contradiction after posting will need a reversal journal under dual control |

## 2. Rules Stage 10 must keep

- Never post a test payment; never infer production from `is_simulated = false` (PROVIDER_SANDBOX payments are not
  simulated but are test payments) — use `execution_environment`.
- Currency isolation: one ledger per currency; USD only until LR-043/PCR-018; no FX.
- No settlement values (PCR-007/016 open): settled vs available states stay unmodelled until a statement source.
- Custody (LR-001/LR-003/LR-076) is open: ledger account naming must not imply FundZim holds funds.
- Lifting `ck_*_no_production_stage9` or `psp.LiveExecutionEnabled` is **not** a Stage 10 task: it needs a provider
  selection ADR, legal sign-off and owner acceptance.

## 3. Open blockers carried

ADR-040 DECISION_DEFERRED; all PCR items unconfirmed; legal items in [legal-review.md](../stage-9/legal-review.md);
secret manager operation (Stage 18/20); screening provider; stage acceptance not recorded.
