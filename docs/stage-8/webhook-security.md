# Stage 8 — Webhook security

How provider callbacks are authenticated, stored, deduplicated and processed. Code: `internal/psp/inbox.go`,
`internal/psp/sandbox/webhook.go`, `internal/payments/webhooks.go`. Design: payment-state-machine §7 (Stage 2),
[ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md) §9, contracts §3.

Only the sandbox simulator sends webhooks in Stage 8. The verification scheme in §2 is the **simulator's**; a real
provider is verified with its own documented procedure (PCR-017, §8).

## 1. Ingestion steps (`POST /api/v1/webhooks/payments/{provider}`)

| # | Check | Failure | Rejection row reason |
|---|---|---|---|
| 1 | Per-client-IP rate limit `payments.webhook.ip` (600 / min, protective) | 429 + `Retry-After` | — (nothing written) |
| 2 | `{provider}` matches `^[a-z][a-z0-9_]{1,31}$` | 404 `WEBHOOK_PROVIDER_UNKNOWN` | — (nothing written) |
| 3 | Provider exists | 404 `WEBHOOK_PROVIDER_UNKNOWN` | `UNKNOWN_PROVIDER` |
| 4 | Provider `ENABLED` and usable now (sandbox: only when the sandbox runs; real: only if live execution were on) and an adapter is registered | 404 `WEBHOOK_PROVIDER_UNKNOWN` | `PROVIDER_DISABLED` |
| 5 | `Content-Type: application/json` | 415 `UNSUPPORTED_MEDIA_TYPE` | `UNSUPPORTED_MEDIA_TYPE` |
| 6 | Body ≤ 64 KiB (read through a limit reader) | 413 `PAYLOAD_TOO_LARGE` | `PAYLOAD_TOO_LARGE` |
| 7 | An `ENABLED` provider account for the environment | 404 `WEBHOOK_PROVIDER_UNKNOWN` | `NO_ACCOUNT` |
| 8 | `Adapter.Verify` (signature, key slot, replay window) | 401 `WEBHOOK_SIGNATURE_INVALID` / `WEBHOOK_TIMESTAMP_INVALID` | `SIGNATURE_MISSING`, `SIGNATURE_INVALID`, `UNKNOWN_KEY`, `TIMESTAMP_OUT_OF_WINDOW` |
| 9 | `Adapter.Parse`; every event's simulation flag equals the provider's `is_sandbox` | 400 `WEBHOOK_MALFORMED` | `MALFORMED_PAYLOAD`, `SIMULATION_MISMATCH` |
| 10 | One transaction: inbox insert `ON CONFLICT (provider_id, provider_event_id) DO NOTHING` + outbox `psp.webhook_received` for each new row | 5xx on a database error (the provider retries) | — |
| 11 | `200 {data: {received: true, duplicate: bool}}`, `no-store` | — | — |

The route is public (no session) and **CSRF-exempt by path** (`/api/v1/webhooks/` in `auth.CSRFMiddleware`); its
authentication is the provider signature. A 2xx means "durably stored", not "applied".

## 2. Verification (sandbox scheme)

`FundZim-Sandbox-Signature: t=<unix>,kid=<current|previous>,v1=<hex HMAC-SHA256(secret, t + "." + raw body)>`. The
exact received bytes are verified (no re-serialisation). The header must match a strict pattern; `kid` selects the
secret slot; the recomputed header is compared in constant time (`hmac.Equal`); then `|received_at − t|` must be
within the account's replay window (300 s). A missing header, a malformed header, a wrong key, a tampered body, an
unknown or empty key slot, an unresolvable secret, and a stale or future timestamp are all refused. Full contract:
[provider-interface.md](provider-interface.md) §6.6.

## 3. Replay protection (three layers)

1. **Replay window** — a captured webhook older than 300 s (or dated more than 300 s ahead) is refused.
2. **Inbox dedupe** — `UNIQUE (provider_id, provider_event_id)`: a replay inside the window is acknowledged
   (`duplicate: true`) and not stored or processed again, including concurrent copies
   (`TestPaymentsWebhookSecurity`: concurrent deliveries of one event ⇒ exactly one row).
3. **Precedence and event dedupe** — even if an event reached `apply` twice, the partial unique index on
   `payment_events (payment_intent_id, provider_event_id) WHERE applied` and the rank rules make it a no-op
   (`DUPLICATE` / `LOWER_RANK`).

## 4. Key rotation

Each provider account has two slots: `webhook_secret_ref` (current) and `webhook_secret_ref_previous`. During a
rotation the provider may sign with either; the slot used is recorded on the inbox row (`signature_key_slot =
CURRENT | PREVIOUS`) so operations can see when the old key stops being used, then clear the previous slot.
Rotation procedure (operator): put the new secret in the secret store, move the current reference to the previous
slot, set the new reference as current, confirm `secrets_resolvable` in the providers view, remove the previous slot
once no `PREVIOUS` deliveries arrive. In Stage 8 the slots are changed by migration or SQL (no staff endpoint).
Tested: sandbox scenario `previous_key` and `TestPaymentsWebhookSecurity` (previous key accepted and recorded as
`PREVIOUS`).

## 5. Rejection log (no payloads)

A rejected webhook stores **only** a row in `app.provider_webhook_rejections`: provider id (if known), provider code
as requested (validated format), reason code (closed list), SHA-256 of the body (when read), body size, time. No body,
no headers, no signature. The table is append-only. Rejections are counted in
`payment_webhooks_rejected_total{provider,reason}` and logged with the reason and the first 8 bytes of the body hash.
Staff see them at `GET /admin/payments/webhook-rejections`. The rejection insert is best effort (a failure is logged,
the 4xx is still returned). The per-IP rate limit runs before anything is written, which bounds rejection-row volume
per client.

## 6. Durable processing (outbox dispatch)

The inbox row and its work item (outbox event `psp.webhook_received {inbox_id}`) commit together, so an
acknowledged webhook is never lost. The worker's outbox consumer `psp.webhook_dispatch` (`psp.Dispatch`):

| Situation | Inbox status |
|---|---|
| Claimed (`RECEIVED`, `FAILED_RETRYING`, or `DISPATCHED` after a crash) | `DISPATCHED` (`attempts + 1`) |
| Class `OTHER` or no handler for the class | `UNRECOGNISED` (alert; re-queueable) |
| Handler applied it | `PROCESSED` (same transaction as `apply`) |
| Prerequisite not reached (refund/dispute before success) | `PARKED` until `SUCCEEDED`; deadline 72 h ⇒ reconciliation reference; never discarded |
| Unmapped provider status / unknown payment reference / adapter not registered | `UNRECOGNISED` (alert) |
| Handler error (attempt < 8) | `FAILED_RETRYING` with `last_error_code`; the outbox delivery is retried with backoff 5 s → 1 h (jittered) |
| Handler error on attempt 8 (`MaxDispatchAttempts`) | `DEAD_LETTERED` (error log); the delivery is completed |
| Already `PROCESSED`, `PARKED`, `UNRECOGNISED`, `DEAD_LETTERED` | Left alone (idempotent consumer) |

Inbox status machine (`provider_webhook_inbox`): `'' → RECEIVED`; `RECEIVED → DISPATCHED / UNRECOGNISED /
FAILED_RETRYING`; `DISPATCHED → PROCESSED / PARKED / FAILED_RETRYING / UNRECOGNISED`; `PARKED → DISPATCHED`;
`FAILED_RETRYING → DISPATCHED / DEAD_LETTERED`; `UNRECOGNISED → DISPATCHED`; `DEAD_LETTERED → DISPATCHED`. The raw
hash and redacted payload cannot change; rows cannot be deleted.

**Re-queue** (`POST /admin/payments/webhooks/{inbox_id}/requeue`, `payment.operate`, step-up, reason 3–2000
characters): only `DEAD_LETTERED` or `UNRECOGNISED` rows; moves the row to `DISPATCHED`, writes a new outbox event and
an audit event `payments.webhook.requeued` in one transaction; anything else is 409 `INBOX_EVENT_NOT_REQUEUEABLE`.
`TestPaymentsDeadLetterAndRequeue` injects a failing handler (database outage during apply), proves 7 retries, the
dead letter at attempt 8, no payment change, then a successful re-queue.

ADR-039 §9 and contracts §3/§6 describe a River job inserted in the ingestion transaction; the outbox is used instead
because the API has no River client. Guarantees are the same.

## 7. What is stored for an accepted webhook

`provider_webhook_inbox`: provider, account, event id, class, raw type, provider and our reference, occurred/received
times, `signature_verified = true`, `verification_method = SIGNATURE`, key slot, `payload_redacted` (adapter
allow-list plus the normalised raw status and reported amount), `raw_body_sha256`, `headers_subset` (only
`Content-Type` and `User-Agent`, ≤ 200 characters — never signatures or credentials), `is_simulated`, processing
state. Staff detail (`GET /admin/payments/webhooks/{id}`) shows the redacted payload, the hash and the verification
facts.

## 8. What a real provider integration must do (Stage 9)

- Implement `WebhookVerifier` with the provider's **documented** procedure (signature algorithm, header, canonical
  string, key identification, timestamp rules) — confirmed in writing (PCR-017). Do not reuse the sandbox scheme.
- If the provider cannot sign webhooks, use `confirmation_mode = WEBHOOK_UNSIGNED_VERIFY_BY_API`: an authenticated
  status query must confirm the event **before** it is applied. This path is designed (Stage 2 §7 step 6) but **not
  implemented** in Stage 8 (KI-S8-27); a provider needing it cannot be enabled until it is.
- Map its event types to classes and its statuses to a versioned table; unmapped ⇒ `UNRECOGNISED`.
- Provide a stable event id for dedupe; if the provider has none, derive one deterministically from signed content.
- Document its retry behaviour and replay window; set the account's `webhook_replay_window_seconds` accordingly
  (0–3600).
- Consider IP allow-listing where the provider documents source ranges (ROADMAP Stage 9).
- Run the same webhook security tests as `TestPaymentsWebhookSecurity` against the adapter.
