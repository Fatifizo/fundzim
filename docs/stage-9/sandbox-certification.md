# Stage 9 — Sandbox certification

Statuses: **PASS**, **FAIL**, **BLOCKED** (cannot run: no sandbox access), **NOT_TESTED** (the sandbox cannot produce
the scenario). A provider is certified only when every applicable scenario is PASS in its own sandbox **and** the
end-to-end integration scenarios pass with its adapter. No real provider is certified in Stage 9.

## 1. Adapter conformance suite

`internal/psp/psptest` (`psptest.Run`), 19 scenarios: create success / decline / timeout after send / not sent /
provider unavailable; idempotent create; status query of a known and an unknown reference; webhook with the current
key, the previous key, a tampered body, no signature, a wrong key, a stale timestamp; stable dedupe key; unmapped
status never guessed; limits fail closed for unknown methods and currencies; redirect hosts well formed; no secret
value in any observation. A new adapter supplies a `psptest.Harness` against its sandbox.

## 2. Results (2026-10-13)

| Provider | Contract basis | Conformance suite | End-to-end scenarios | Certification |
|---|---|---|---|---|
| FundZim simulator | SIMULATED | **19/19 PASS** (`TestPaymentsConformanceSuiteSimulator`) | PASS (Stage 8 `TestPayments*` + Stage 9 tests) | Simulator only — **not a provider certification** |
| Paynow | DOCUMENTED_BUT_UNVERIFIED | **BLOCKED** (no adapter, no sandbox credentials) | BLOCKED | **BLOCKED** |
| Pesepay | DOCUMENTED_BUT_UNVERIFIED | **BLOCKED** | BLOCKED | **BLOCKED** |
| Payonify | DOCUMENTED_BUT_UNVERIFIED | **BLOCKED** | BLOCKED | **BLOCKED** |
| Linkwa, Smile&Pay, ContiPay, EcoCash Open API | — | **BLOCKED** | BLOCKED | **BLOCKED** |

The first run of the suite found a real simulator defect: `Limits` returned limits for an unknown method (it checked
the currency only). Fixed: unknown methods now fail closed.

## 3. Per-provider checklist (to complete when access exists)

| # | Item | Paynow | Pesepay | Payonify |
|---|---|---|---|---|
| 1 | Written answers PCR-001/002/003 reviewed by counsel | BLOCKED | BLOCKED | BLOCKED |
| 2 | Sandbox credentials in the secret manager (`fundzim/sandbox/<provider>/…`) | BLOCKED | BLOCKED | BLOCKED |
| 3 | Adapter implemented from official docs; contract basis recorded | NOT_TESTED | NOT_TESTED | NOT_TESTED |
| 4 | Conformance suite against the sandbox (19 scenarios) | BLOCKED | BLOCKED | BLOCKED |
| 5 | Webhook verification per [webhook-security.md](webhook-security.md) §2 | BLOCKED | BLOCKED | BLOCKED |
| 6 | Status-mapping table versioned; every documented status mapped, the rest UNKNOWN | BLOCKED | BLOCKED | BLOCKED |
| 7 | End-to-end: success, decline, timeout created/not created, duplicate and out-of-order webhooks, late success, invalid signature, stale timestamp, unmapped status, amount mismatch, provider unavailable, crash recovery, dead letter | BLOCKED | BLOCKED | BLOCKED |
| 8 | Error normalisation of every documented error code | BLOCKED | BLOCKED | BLOCKED |
| 9 | Capability rows recorded VERIFIED by migration | BLOCKED | BLOCKED | BLOCKED |
| 10 | Refund sandbox (only if documented and approved: PCR-006, LR-018/019/081) | BLOCKED | BLOCKED | BLOCKED |

## 4. Refund sandbox

Not implemented. No provider's refund behaviour is confirmed (PCR-006), refund legal questions are open, and Stage 8
refund requests have no execution edge. `REFUND_REQUESTED_SANDBOX` is the audit label of a refund **request** on a
test payment; nothing is executed.
