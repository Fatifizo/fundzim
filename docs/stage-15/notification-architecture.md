# Stage 15 — Notification architecture

Decision record: [ADR-047](../adr/ADR-047-notification-engine.md). Code: `internal/notifications` (engine),
`internal/app/notifications.go` (composition), `apps/web/src/{components,lib}/notifications`,
`apps/web/src/app/{dashboard/notifications,admin/notifications,admin/communications}`.

## 1. One engine, existing infrastructure

Stage 15 reuses what earlier stages built; it adds no queue, broker or second notification path:

| Need | Reused infrastructure |
|---|---|
| Event intake | the transactional outbox (`internal/platform/outbox`, Stage 3) — every mapped event is written by its owning module in the same transaction as the change, so a notification never exists for a change that rolled back |
| Exactly-once creation | the outbox consumer inbox (`outbox.Consume`) **and** `notification_events.source_event_id UNIQUE` |
| Background work | the River `notifications` queue of `cmd/worker` (half the worker concurrency): the periodic jobs `notifications.deliver` (every 5 s) and `notifications.simulator_callbacks` (every 2 s), and the outbox deliveries of `notifications.dispatch` (`outbox.Relay.ConsumerQueues`), so the notification fan-out never delays media scans, payments or other consumers on the default queue |
| Recipients | `users` (contact, status, account kind), organisation admins and staff-by-permission through the `notifications.Directory` interface implemented in `internal/app` (no cross-module SQL) |
| Permissions and step-up | `platform/authz` + the Stage 4 step-up; six new `communications.*` permissions (migration) |
| Rate limits | `platform/ratelimit` policies `notifications.webhook.ip` (600/min) and `notifications.consent.user` (30/h) |
| Audit | `audit.Service` (security audit stream), metadata without bodies, destinations or secrets |
| Operations portal | a notifications `OpsSnapshot` registered as source `notifications` in the Stage 14 portal (ADR-046), plus seven `NOTIFICATION_*` alerts |
| Dev mail | the Stage 4 senders already in `internal/notifications` (`smtp.go` SMTP sender, `notifications.go` dev SMS sender; transaction guard, no body logging) wrapped as the `email_dev_smtp` and `sms_dev_mailpit` providers, delivering to the compose Mailpit |

The Stage 4–6 notice consumers in `auth`, `campaigns` (orchestration in `internal/app/campaigns.go`) and
`verification` were **moved** into the engine; their e-mails are now notification templates (origin
`STAGE_6_NOTICE`) and are delivered, recorded and governed like every other notification. Credential messages —
e-mail verification and password reset links, staff invitations, phone OTPs — stay in `auth` (ADR-047 §6): they carry
secrets, must not be stored in notification records or be replayable, and must not be subject to preferences.

## 2. Flow

```
module tx ──► app.outbox_events ──► relay ──► consumer notifications.dispatch
                                               │  (outbox.Consume: inbox dedupe)
                                               ▼
                                     Handle(event) → []Intent (events.go)
                                               │
                                               ▼  one transaction
             notification_events (source_event_id UNIQUE) → notifications (per recipient, rendered in-app)
                    → notification_deliveries (per channel: routing decision, QUEUED / SUPPRESSED / DELIVERED[in-app])
                                               │
             periodic notifications.deliver ◄──┘
               claim (FOR UPDATE SKIP LOCKED, lease) → re-route (consent, preference, suppression, contact)
               → render → commit PROCESSING + attempt → provider.Send (outside any transaction)
               → record result (SUBMITTED / ACCEPTED / DELIVERED / FAILED_* / UNKNOWN), backoff, dead letter
                                               │
             provider callback ──► POST /api/v1/webhooks/notifications/{provider}
               verify HMAC + timestamp → store provider event once → apply by precedence (or keep as evidence)
```

## 3. Components (`internal/notifications`)

| File | Responsibility |
|---|---|
| `policy.go` | categories, channels, the routing policy table and the pure `Route` function |
| `events.go` | event → intent mapping (only documented events; [event-contracts.md](event-contracts.md)) |
| `engine.go` | `Service`, `Handle`, `Create`, `Notify`, recipient and contact resolution, delivery creation |
| `render.go` | placeholder-only rendering, validation, content hash, SMS segment estimate |
| `phone.go` | E.164 normalisation with `github.com/nyaruka/phonenumbers` (default region ZW) |
| `providers.go` | the `Provider` interface, error classes, dev SMTP and dev SMS adapters |
| `simulator.go` | the three TEST_ONLY simulators (send, status, signed callbacks, scenarios) |
| `deliver.go` | the delivery sweep: leases, claims, sends, backoff, dead letters, unknown-outcome resolution |
| `webhook.go` | the delivery state machine (`DeliveryEdges`), callback verification and precedence |
| `user.go` | inbox, unread count, mark read, preferences, consent |
| `templates.go` | template versions, maker-checker lifecycle, WhatsApp simulator submission |
| `admin.go` | dashboard, deliveries, dead letters, replay, suppressions, consent overview, reports |
| `ops.go` | `OpsSnapshot` for the Stage 14 portal |
| `http.go` | 32 routes (user, webhook, staff) and error codes |

`archtest` registers the module (imports `platform`, `audit`, `users`) and its tables (`app.notification*`,
`app.communication_consents*`).

## 4. Safety gates (code constants and database CHECKs)

| Gate | Where |
|---|---|
| `LiveMessagingEnabled = false` | Go constant; in production every external channel is suppressed `LIVE_MESSAGING_DISABLED` (in-app still works) |
| `MarketingEnabled = false` | Go constant; marketing is suppressed `MARKETING_DISABLED` on every channel, and a marketing consent grant is refused (`MARKETING_NOT_AVAILABLE`) |
| provider configs TEST only | `ck_notification_provider_configs_no_production_stage15`, `..._test_only_stage15`: readiness never VERIFIED / PRODUCTION_ENABLED |
| simulator refused in production | config validation: `NOTIFICATIONS_SIMULATOR_ENABLED` and the `simulator` / `dev_*` providers are refused for `APP_ENV=production` (`dev_*` only in development and test) |
| synthetic sources in-app only | `Route` → `SYNTHETIC_SOURCE` for every external channel when the event is a TEST payment / payout / simulated source; in-app titles are prefixed `[TEST]` |
| WhatsApp approval needs evidence | template version trigger: `whatsapp_provider_status = 'APPROVED'` requires provider evidence (none exists; the simulator yields `SIMULATOR_ACCEPTED`) |
| consent grant source | `communication_consents` events accept grant source `USER_SETTINGS` only — staff cannot record consent on someone's behalf |
| deep links | CHECK: relative `/dashboard|/admin|/campaigns|/settings` paths only |

## 5. What is deliberately not here

No production provider, no real-time push (the unread count is polled every 60 s), no quiet hours, no digests, no
marketing campaigns, no translations (English baseline; `locale` is a column and Shona/Ndebele versions need
reviewed translations — none was fabricated), no tax receipts.
