# Stage 20 — Payment-provider readiness

**Area result: BLOCKED.** No payment, payout, screening or messaging provider is selected (ADR-040 DECISION_DEFERRED).
The only adapter in the code base is FundZim's own sandbox simulator.

## 1. Adapters (verified in code at the candidate)

| Provider | Adapter | Environment | Credentials | Webhook verification | Idempotency | Methods | Status query | Payouts | Refunds/reversals | Ambiguous outcomes | Evidence class |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `sandbox` (FundZim simulator, `internal/psp/sandbox`) | yes — registered only when `PAYMENTS_SANDBOX_ENABLED` (`internal/app/payments.go:81`), refused in production | LOCAL_SIMULATOR | test secrets from the local OpenBao via `secretref://fundzim/sandbox/…` | HMAC with current + previous key, timestamp window, dedupe (Stage 8/9 tests) | our reference reused; deterministic scenarios by amount suffix | simulated EcoCash-style rails | `GetPayment` / `GetPayout` | simulated (`PAYOUTS_EXECUTION_ENVIRONMENT=LOCAL_SIMULATOR` only) | refund requests are maker-checker and **cannot be executed**; payout "returned" simulated | UNKNOWN, never FAILED; never resubmitted (payouts); poll budget (payments) | SIMULATED — not provider evidence |
| any real Zimbabwean or international provider | **none** | — | none exist anywhere (DB CHECK requires `secretref://`) | — | — | — | — | — | — | — | NOT_PROVIDED |
| in-test doubles (`fakeProvider`, `scriptedProvider`) | test code only | PROVIDER_SANDBOX inside tests | throwaway | HMAC fake | — | — | scripted | — | — | exercised | TEST — not an integration |

The provider-adapter conformance suite (`internal/psp/psptest`) exists; a real provider without sandbox access reports
BLOCKED, never PASS (`TestPaymentsConformanceSuiteSimulator`, Stage 9; passed in the Stage 20 isolated run).

## 2. Provider semantics still unconfirmed

66 `PCR-xxx` questions in docs/payments/provider-questions.md, **none confirmed** (status/finality semantics PCR-009
and PCR-010, statement formats PCR-007/016/045/051, settlement custody model, fees, limits, webhook guarantees).

## 3. Payment polling policy — PROVISIONAL

72 h horizon from creation (awaiting statuses), 72 h from `unknown_since` (UNKNOWN), 500 status queries per payment,
backoff 5 s → 10 min, ±20 % jitter, POLL_BUDGET_EXHAUSTED hand-off (ADR-051). These are internal safety defaults, not
provider-approved guarantees (KI-S19-04). They must be replaced or confirmed against the selected provider's documented
status semantics and approved by the owner.

## 4. Required before any live payment

1. Provider selection ADR superseding ADR-040, with due diligence and the custody model confirmed as Model A (ADR-013).
2. Contract and sandbox credentials in the secret manager; the conformance suite passing against the provider sandbox.
3. PCR answers recorded; poll budget, finality and statement format configured from them.
4. Legal items for the chosen rails (LR-005/LR-044 international rails, LR-043 ZiG).
5. Gates remain false until the ADR, legal sign-off, accounting review and acceptance exist.

No real payment, payout, sign-up or credential request was made during Stage 20.
