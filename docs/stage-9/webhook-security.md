# Stage 9 — Webhook verification and processing

The Stage 8 pipeline is unchanged ([../stage-8/webhook-security.md](../stage-8/webhook-security.md)): raw bytes →
provider's documented `Verify` → `Parse` → inbox (dedupe on provider event id) + outbox → dispatch → `apply()`.
Stage 9 adds audit events, evidence capture and per-provider verification designs.

## 1. Additions

| Addition | Detail |
|---|---|
| WEBHOOK_VERIFIED | `psp.webhook.verified` (business stream) when a stored event is first claimed for dispatch (before anything is applied), with its receipt time: provider, class, verification method, key slot. Not in the ingest transaction: the hash-chained audit log serialises writers and cut ingestion from ~2 500 to ~460 req/s at c=32 ([testing.md](testing.md) §4) |
| WEBHOOK_REJECTED | `psp.webhook.rejected` (security stream) for authentication failures against a **known** provider (`SIGNATURE_MISSING`, `SIGNATURE_INVALID`, `UNKNOWN_KEY`, `TIMESTAMP_OUT_OF_WINDOW`, `SIMULATION_MISMATCH`, `MALFORMED_PAYLOAD`). Unknown endpoints and transport refusals stay in the rejection log only, so scanners cannot flood the hash-chained audit log |
| Allowlist | A provider not on `PAYMENTS_PROVIDER_ALLOWLIST` is not usable, so its deliveries are rejected (`PROVIDER_DISABLED`) |
| Evidence | Every applied webhook observation is appended to `app.payment_provider_evidence` once per provider event (re-dispatch does not duplicate it) |

## 2. Verification design per documented scheme (not implemented: no sandbox access)

| Provider (docs read 2026-10-13) | Scheme | FundZim mode | Replay protection | Notes |
|---|---|---|---|---|
| Payonify | HMAC-SHA256 `Payonify-Signature: t=…,v1=…` over `{t}.{raw body}`, 5 min tolerance | `WEBHOOK_SIGNED` (current/previous key slots) | Timestamp window + inbox dedupe on event id | Rotation overlap undocumented → keep previous slot until confirmed (PCR-017) |
| Paynow | SHA-512 uppercase hex of field values in message order + integration key; no timestamp | `WEBHOOK_SIGNED` **plus** status poll before any financially significant transition | None from the provider: dedupe on (reference, status) and **confirm by polling `pollurl`** (Paynow itself recommends polling) | Form-encoded body: the verifier must preserve field order exactly as received |
| Pesepay | **Unsigned**; `Authorization` header carries the integration key; Check Payment Status is authenticated | `WEBHOOK_UNSIGNED_VERIFY_BY_API`: a callback is only a hint; the state changes only after an authenticated status query | From the status query | The header is a shared secret sent on every callback: treat it as a filter, never as authentication |

`WEBHOOK_UNSIGNED_VERIFY_BY_API` (store as `STATUS_API_CONFIRM`, then query) is designed in Stage 2/8 and remains
unimplemented until a provider that needs it is selected.

## 3. Rate limits

KI-S8-22 (one provider IP limited by `global.ip` before `payments.webhook.ip`) stays open: the source IP ranges of
no provider are known (PCR-017). Set per-provider limits when they are.
