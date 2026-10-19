# Stage 15 — Provider readiness

`app.notification_provider_configs` (8 rows) and `/admin/communications/providers`. Readiness values:
`NOT_SELECTED`, `NOT_CONFIGURED`, `CONFIGURED_TEST`, `CERTIFICATION_PENDING`, `VERIFIED`, `PRODUCTION_ENABLED`.
Two CHECKs hold for Stage 15: no row may be `VERIFIED` or `PRODUCTION_ENABLED`
(`ck_notification_provider_configs_no_production_stage15`) and every row is environment `TEST`
(`ck_notification_provider_configs_test_only_stage15`). Credentials, when one exists, are `secretref://fundzim/…`
references only (CHECK; ADR-041).

| Code | Channel | Readiness | Simulated | Notes |
|---|---|---|---|---|
| `email_simulator` | EMAIL | CONFIGURED_TEST | yes | sends nothing |
| `email_dev_smtp` | EMAIL | CONFIGURED_TEST | no | local Mailpit; development and test only |
| `email_production` | EMAIL | NOT_SELECTED | no | LR-010, LR-011, PROVIDER_CONFIRMATION_REQUIRED |
| `sms_simulator` | SMS | CONFIGURED_TEST | yes | sends nothing |
| `sms_dev_mailpit` | SMS | CONFIGURED_TEST | no | SMS rendered as a Mailpit e-mail |
| `sms_production` | SMS | NOT_SELECTED | no | coverage, sender IDs, opt-out, receipts, pricing unconfirmed |
| `whatsapp_simulator` | WHATSAPP | CONFIGURED_TEST | yes | template messages only |
| `whatsapp_production` | WHATSAPP | NOT_SELECTED | no | no BSP, no business account, LR-023 |

Capabilities shown are those of the adapters FundZim wrote (simulators, dev senders). Production rows have **no
capabilities** listed: none was confirmed, so none is claimed. Health is shown only from observations (attempts and
callbacks in the last hour); with none it is "Not measured", never healthy.

## What selecting a provider will need (not done in Stage 15)

1. A selection decision (ADR superseding the provider part of ADR-047) after LR-010 (controller registration) and
   LR-011 (cross-border transfer) are answered, and LR-023 for SMS/WhatsApp consent.
2. Written provider confirmation of: idempotency keys or status lookup by our reference, callback signing and replay
   protection, delivery-status semantics (accepted vs delivered), bounce/complaint/opt-out feeds, rate limits,
   character sets and segment counting, data residency and retention.
3. Sandbox credentials in the secret manager; an adapter implementing `notifications.Provider` and passing the same
   scenario suite the simulators pass.
4. Changing the Stage 15 CHECKs and `LiveMessagingEnabled` by a reviewed migration and ADR, with stage acceptance.
