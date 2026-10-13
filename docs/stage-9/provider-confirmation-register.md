# Stage 9 — Provider confirmation register

Date of assessment: **2026-10-13**. Source questions: [provider-questions.md](../payments/provider-questions.md)
(exact text there; summarised here by topic). **No provider has been contacted** and no written answer, contract or
signed letter exists. Statuses:

| Status | Meaning |
|---|---|
| **CONFIRMED** | Written provider answer (contract, signed letter) **and**, for technical items, observed in the provider's sandbox |
| **PARTIALLY_CONFIRMED** | Official provider developer documentation, read on the date shown, answers part of the question. Not observed in a sandbox and not confirmed in writing for FundZim |
| **UNCONFIRMED** | No official evidence, or only marketing / Stage 1 secondary evidence |
| **NOT_APPLICABLE** | The question does not apply to that provider |

Marketing pages are not evidence. Stage 1 bracketed sources ([P*] Paynow, [S*] Pesepay, [Y*] Payonify, [L*] Linkwa)
are in [provider-capability-matrix.md](../payments/provider-capability-matrix.md). **Owner** for every item: the
project owner (founders) to request a written answer from the provider; engineering records it by migration
([provider-capabilities.md](provider-capabilities.md) §4).

## Summary

| PCR | Topic | Overall | Integration impact | Production impact |
|---|---|---|---|---|
| PCR-001 | RBZ licence / authorisation, settlement bank | **UNCONFIRMED** (all providers) | None for sandbox | **Blocks production** (with LR-004) |
| PCR-002 | Terms permit donation crowdfunding for third-party beneficiaries; KYC/KYB required | **UNCONFIRMED** | None for sandbox | **Blocks production** |
| PCR-003 | Who holds funds before disbursement, under which authorisation | **UNCONFIRMED** | None | **Blocks production** (Model A, LR-001) |
| PCR-004 | Production currency × method matrix with limits | **UNCONFIRMED** (sandbox lists partially documented) | Capability rows stay `REQUIRES_PROVIDER_CONFIRMATION`, not routable | **Blocks production** |
| PCR-005 | Foreign cards, 3-D Secure, chargebacks | **UNCONFIRMED** | INTERNATIONAL_CARD unsupported | Blocks international rails |
| PCR-006 | Refund API, partials, windows, idempotency | **UNCONFIRMED** | No refund adapter call (refund sandbox not implemented) | Blocks refunds |
| PCR-007 | Settlement schedule and reports | **UNCONFIRMED** | Evidence capture only, no settlement values | Blocks settled/available states (Stages 10/17) |
| PCR-009 | Idempotent payment creation | **UNCONFIRMED** (Payonify partially documented in Stage 1) | Retry after UNKNOWN stays forbidden; status query only | Blocks retry policy |
| PCR-010 | Status-query semantics, "not found", expiry | **UNCONFIRMED** | UNKNOWN horizon stays 72 h default (KI-S8-10) | Blocks UNKNOWN → FAILED rules |
| PCR-017 | Webhook security | **PARTIALLY_CONFIRMED** (Paynow, Pesepay, Payonify docs read 2026-10-13) | Verification modes designed per provider; none implemented without sandbox access | Blocks production webhooks until sandbox-observed |
| PCR-018 | ZiG code, minor units, selection | **UNCONFIRMED** (Pesepay sandbox: USD only) | ZWG refused | Blocks ZWG |

## Detail per requirement and provider

| PCR | Provider | Evidence (official source, date read) | Status | Date | Integration impact | Production impact |
|---|---|---|---|---|---|---|
| PCR-001 | All candidates | None. RBZ lists could not be retrieved in Stage 1 | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-002 | All candidates | None (Stage 1: Pesepay and Linkwa mention donations [S19][L4] — marketing-level) | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-003 | All candidates | None | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-004 | Pesepay | Sandbox environment page (last updated 2026-09-10): sandbox supports USD only; EcoCash (`PZW211`), Visa (`PZW204`), Mastercard (`PZW205`); InnBucks, O'Mari, ZimSwitch and PayGo not in sandbox. **Production** matrix and limits not documented | UNCONFIRMED (production) | 2026-10-13 | Sandbox rows could be recorded once access exists | Blocks |
| PCR-004 | Payonify | Get-started page (2026-10-13): EcoCash and OneMoney listed as available; Visa/Mastercard via hosted Checkout only; Zimswitch "Coming Soon"; currencies not listed (examples use `usd`); no limits | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-004 | Paynow, others | Not re-read in Stage 9 | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-005 | All candidates | None in official developer docs read | UNCONFIRMED | 2026-10-13 | INTERNATIONAL_CARD not routable | Blocks international |
| PCR-006 | All candidates | Stage 1: Payonify documents a full-refund API [Y8] (not re-verified) | UNCONFIRMED | 2026-10-13 | No refund sandbox call | Blocks refunds |
| PCR-007 | All candidates | None | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-009 | Payonify | Stage 1: idempotency key documented [Y6] (not re-verified, not observed) | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-009 | Paynow | Stage 1: `merchanttrace` for token card payments only [P7] | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-009 | Pesepay, others | None | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-010 | Paynow | Stage 1: "A trace error does not necessarily mean that the transaction was not found" [P7] — confirms not-found is **not** final; expiry undocumented | UNCONFIRMED | 2026-10-13 | Treat not-found as non-final | Blocks |
| PCR-010 | Others | None | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-017 | Paynow | Status-update documentation (2026-10-13): POST to merchant `resulturl` with `reference`, `amount`, `paynowreference`, `pollurl`, `status`, `hash`; resent up to 10 times on HTTP error; polling `pollurl` recommended. Hash: SHA-512 (uppercase hex) of field values in message order excluding `hash`, with the integration key appended. **No timestamp, nonce, rotation procedure or source IPs documented** | PARTIALLY_CONFIRMED | 2026-10-13 | Verify hash **and** confirm by poll before applying (no replay protection) | Blocks until sandbox-observed |
| PCR-017 | Pesepay | Verifying-callbacks page (last updated 2026-09-01): callbacks are **not signed or encrypted**; an `Authorization` header carries the integration key; verify by the authenticated Check Payment Status API | PARTIALLY_CONFIRMED | 2026-10-13 | `WEBHOOK_UNSIGNED_VERIFY_BY_API` mode required | Blocks until sandbox-observed |
| PCR-017 | Payonify | Webhooks page (last modified 2026-09-10): HMAC-SHA256, header `Payonify-Signature` with `t=` and `v1=` over `{timestamp}.{raw_body}`, 5-minute tolerance, constant-time comparison; secret rotation from the dashboard (overlap period not described); retries with exponential backoff, 10 attempts. Source IPs not documented | PARTIALLY_CONFIRMED | 2026-10-13 | Maps onto the Stage 8 HMAC verifier shape (current/previous slots) | Blocks until sandbox-observed |
| PCR-017 | Linkwa, Smile&Pay, ContiPay, EcoCash Open API | Not read in Stage 9 | UNCONFIRMED | 2026-10-13 | — | Blocks |
| PCR-018 | Pesepay | Sandbox USD only (2026-10-13). Stage 1: both `ZiG` and `ZWG` codes used by method [S2] | UNCONFIRMED | 2026-10-13 | ZWG refused | Blocks ZWG |
| PCR-018 | Paynow | Stage 1: no currency field in API [P1] | UNCONFIRMED | 2026-10-13 | Currency cannot be selected per transaction | Blocks ZWG |
| PCR-018 | Payonify, others | Currencies not listed | UNCONFIRMED | 2026-10-13 | — | Blocks ZWG |

## Contradictions and escalations

None recorded: no written answers exist to contradict. When answers arrive, a contradiction between an answer and
documentation, or between providers' documents and observed sandbox behaviour, is recorded as `ESCALATED` in
[provider-questions.md](../payments/provider-questions.md) and never silently resolved.
