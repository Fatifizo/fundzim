# Stage 8 → Stage 9 Handover: Zimbabwe Payment Integrations

> Stage 8 (payment abstraction layer, sandbox simulator, webhook inbox, idempotency, state machine, refund requests,
> reconciliation references, staff payment operations UI) is implemented on branch `stage-8/payments` and **awaits
> acceptance**. Acceptance of Stages 1–7 is not recorded either. Do not start Stage 9 until the owner accepts and asks
> for it. Stage 9 must not enable real payments unless every gate below is lifted with evidence.

Stage 8 docs: [implementation](../stage-8/implementation.md) · [architecture](../stage-8/payment-architecture.md) ·
[provider interface](../stage-8/provider-interface.md) · [state machine](../stage-8/payment-state-machine.md) ·
[webhooks](../stage-8/webhook-security.md) · [idempotency](../stage-8/idempotency.md) ·
[refunds](../stage-8/refund-architecture.md) · [reconciliation](../stage-8/reconciliation-foundation.md) ·
[security](../stage-8/security-review.md) · [testing](../stage-8/testing.md) · [performance](../stage-8/performance.md) ·
[known issues](../stage-8/known-issues.md) · [capability register](../stage-8/provider-capability-register.md) ·
[legal dependencies](../stage-8/legal-and-provider-dependencies.md). ADR-039.

## 1. Provider adapter interface

Implement `psp.Adapter` (`internal/psp/psp.go`) per provider in `internal/psp/<name>`: `CreatePayment`, `GetPayment`,
`CancelPayment`, `Verify`, `Parse`, `MapPaymentStatus` (versioned table), `Limits`, `RedirectHosts`. Classify every
outcome ([provider-interface.md §2](../stage-8/provider-interface.md)): a timeout after the request was written is
`INDETERMINATE`, never a failure; "not found" is final only when the provider documents it. Register it in
`internal/app/payments.go`. Refund, payout and statement interfaces are not defined yet (Stages 10/11/17).

## 2. Capabilities

| Item | State |
|---|---|
| Confirmed capabilities | **None.** No provider is selected; all rails (EcoCash, OneMoney, InnBucks, O'Mari, ZIPIT, ZimSwitch, bank transfer, international cards) are UNKNOWN |
| Unconfirmed | Everything in [provider-capability-matrix.md](../payments/provider-capability-matrix.md) (Stage 1 public documentation, dated 2026-10-08) |
| How to record | Migration adding the provider (`selection_status = SELECTED`, `selection_adr`), its accounts (secret **references**) and one capability row per (method, currency) with `evidence_status` VERIFIED + source, or REQUIRES_PROVIDER_CONFIRMATION + PCR; flags false unless verified |
| ZIPIT | Needs a method code (migration + Go list) when a provider documents it |

## 3. Credentials and secrets

Required credentials are unknown until a provider is selected. They must be stored in a secret manager and referenced
by `secretref://…`; replace `psp.EnvResolver` with a KMS/secret-manager resolver (KI-S8-04) before any real
credential exists. Never in `.env` files or the database.

## 4. Sandbox requirements

Each provider's own sandbox (if any) gets a `SANDBOX` account; the FundZim simulator stays for automated tests. A real
adapter must pass the same integration scenarios: success, decline, timeout (created / not created), duplicate and
out-of-order webhooks, late success, invalid signature, stale timestamp, unmapped status, amount mismatch, provider
unavailable, crash recovery, dead letter.

## 5. Webhook verification

Use only the provider's documented procedure (PCR-017): algorithm, signed content, timestamp or nonce, key rotation,
source IPs. Map it onto `Verify` (key slots current/previous) and record the replay window on the account. If a
provider cannot sign, use `confirmation_mode = WEBHOOK_UNSIGNED_VERIFY_BY_API` (store with `STATUS_API_CONFIRM` and
confirm by an authenticated status query before applying) — not implemented in Stage 8.

## 6. Currencies

USD only. ZWG stays refused until LR-043 / PCR-018 are resolved and `minor_units_verified` is set by migration. A rail
is recorded only if it settles in the donation currency (DB CHECK); no FX.

## 7. Settlement, refunds, reconciliation

Settlement is not modelled (Model A: the PSP holds funds; LR-001, PCR-003). Refund execution needs `refund_transactions`,
ledger reservation journals (Stage 10/11), PCR-006 and LR-018/019/081. Reconciliation needs statement sources (PCR-007,
PCR-016) — Stage 17.

## 8. Regulatory dependencies

LR-001, LR-003, LR-004, LR-005/LR-044, LR-012, LR-018, LR-019, LR-020, LR-022, LR-026, LR-035, LR-043, LR-075, LR-081;
screening (`SCREENING_PROVIDER_NOT_SELECTED`). Production remains BLOCKED until they are resolved and stages accepted.

## 9. Security controls to keep

Required idempotency keys; payment row locks; `apply()` as the only state path; source checks in Go and SQL;
append-only history; inbox dedupe and outbox dispatch; rejection log without payloads; redirect allow-list; masked /
HMAC'd payer data; step-up for `payment.operate` and `refund.approve`; no manual success override.

## 10. Known blockers

No selected provider (ADR needed); no secret manager; no ledger (Stage 10); no screening provider; legal items above;
stage acceptance not recorded.
