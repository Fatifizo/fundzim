# Stage 15 — Notifications & Communications: implementation

Date: 2026-10-19. Branch `stage-15/notifications`. Decision record: [ADR-047](../adr/ADR-047-notification-engine.md).
TEST environment only: no e-mail, SMS or WhatsApp provider is selected; live messaging, live donations, real payouts,
production ledger posting and production screening stay BLOCKED.

## Documents

| Topic | Document |
|---|---|
| Prerequisites, governance, legal dependencies, Stage 14 remediation | [prerequisite-assessment.md](prerequisite-assessment.md) |
| Architecture, reuse of existing infrastructure, safety gates | [notification-architecture.md](notification-architecture.md) |
| Event contracts and mapping | [event-contracts.md](event-contracts.md) |
| Channels | [email.md](email.md), [sms.md](sms.md), [whatsapp.md](whatsapp.md), [in-app.md](in-app.md) |
| Templates | [templates.md](templates.md) |
| Preferences and consent | [preferences.md](preferences.md), [consent.md](consent.md) |
| Delivery, retries, dead letters | [delivery-lifecycle.md](delivery-lifecycle.md), [retries.md](retries.md), [dead-letters.md](dead-letters.md) |
| Providers | [provider-readiness.md](provider-readiness.md) |
| Security review | [security-review.md](security-review.md) |
| Tests and measurements | [testing.md](testing.md) |
| Known issues | [known-issues.md](known-issues.md) |
| Stage 16 preparation | [../stage-handover/STAGE-15-TO-STAGE-16.md](../stage-handover/STAGE-15-TO-STAGE-16.md) |

## What was built

**Backend.** The notification engine in `internal/notifications` (policy, event mapping, creation, rendering, phone
normalisation, providers and simulators, delivery sweep, webhooks, inbox, preferences, consent, templates, staff
administration, operations snapshot; 32 HTTP routes), composed in `internal/app/notifications.go` (directory, routes,
consumer `notifications.dispatch`, periodic jobs `notifications.deliver` and `notifications.simulator_callbacks`). The
Stage 4–6 notice consumers in `auth`, campaigns and verification orchestration were moved into the engine; credential
messages stay in `auth`. `internal/operations` emits four new events (support cases, incidents) and raises seven
`NOTIFICATION_*` alerts; `payments.apply()` adds donor and amount fields to two existing events. Configuration:
`NOTIFICATIONS_SIMULATOR_ENABLED`, `NOTIFICATIONS_{EMAIL,SMS,WHATSAPP}_PROVIDER` (simulator and dev senders refused in
production). Rate-limit policies `notifications.webhook.ip` and `notifications.consent.user`.

**Database.** Migration `20261018090000_notifications`: 17 `app.notification*` / `app.communication_consent*` tables,
three guarded state machines (`notification_delivery` 33 edges, `notification_template` 9, `communication_consent`
10), immutability and guard triggers (template content after DRAFT, baseline versions migrator-only, attempts
append-only, provider events apply-once), the Stage 15 TEST-only CHECKs on provider configs, 61 baseline templates, 8
provider rows and six `communications.*` permissions with role mappings.

**API.** OpenAPI: 29 paths and 64 schemas (tag "Notifications (Stage 15)"); `/me/notification-preferences` (Stage 6)
replaced by `/notifications/preferences`.

**Frontend.** `/dashboard/notifications` (+ `preferences`), `/admin/notifications`, `/admin/communications` (+
`deliveries`, `deliveries/[id]`, `dead-letters`, `templates`, `templates/[id]`, `providers`, `suppressions`, `reports`,
`consent`); unread counts on the dashboard home, account menu and admin shell; a "Communications" navigation group.

## Acceptance criteria (brief §87) — engineering self-assessment

| # | Criterion | Result | Evidence |
|---|---|---|---|
| 1–4 | Prerequisites, governance, legal/privacy documented, blockers tracked | MET | prerequisite-assessment.md; register row 15 NOT_RECORDED; known-issues.md |
| 5–6 | Architecture, domain models | MET | ADR-047; migration |
| 7–9 | Event processing, outbox, idempotency | MET | `TestNotificationsOutboxCreatesOnceAndDelivers` (100 duplicate and 100 concurrent deliveries → 1) |
| 10–13 | Recipient resolution and validation, routing, no fallback | MET | `TestRouteChannelRules`, `TestNoFallbackAndPreferences`, consent test |
| 14–20 | E-mail, SMS, WhatsApp abstractions and simulators; ZW phone normalisation | MET (simulators and dev senders; no provider) | `TestSimulatorScenariosAndWebhookVerification`, `TestSMSSegmentsAndPhones`, `TestNotificationsDeliveryScenarios` |
| 21–22 | In-app notifications, unread counts | MET | isolation test; E2E `notifications.spec.ts`; vitest |
| 23–25 | Templates, versioning, approvals | MET | `TestNotificationsDeadLetterReplayAndTemplates`; E2E maker-checker |
| 26–29 | Preferences, consent records, withdrawal, marketing suppression | MET | `TestNotificationsConsentAndPreferences` |
| 30–31 | Delivery state machine, evidence | MET | `TestDeliveryEdgesMatchMigration`, `TestCallbackPrecedenceIsDeterministic` |
| 32–34 | Retry, dead letters, replay safety | MET | `TestBackoff…`, delivery scenarios, dead-letter test (concurrent replays: one winner) |
| 35–36 | Webhook security, out-of-order | MET | `TestNotificationsWebhookSecurityAndCrashRecovery` |
| 37–46 | Campaign, donation, payment-success safety, verification, withdrawal, payout safety, risk, security, administrative, support notifications | MET | `TestFinancialEventMapping`, `TestNotificationsFinancialSafety`, staff RBAC test; event-contracts.md |
| 47–49 | Dashboard, provider readiness, reporting | MET | E2E staff spec; `/admin/communications/*` |
| 50–52 | Health, alerts, audit | MET | `OpsSnapshot` in the operations overview; seven alerts; audit actions `notifications.*` |
| 53–55 | Migrations, API, RBAC | MET | migration round-trip; Redocly; RBAC tests |
| 56–61 | Tenant isolation, security, consent, financial, concurrency, failure-injection tests | MET, with KI-S15-12 | [testing.md](testing.md) |
| 62–64 | Backend, frontend, accessibility | MET (automated) | Go, vitest, Playwright with axe (desktop and Pixel 7); no manual screen-reader audit |
| 65 | OpenAPI | MET | Redocly 2.54.3 |
| 66 | Remote CI | see the completion report | |
| 67–69 | No unapproved live messaging, no fabricated provider results, no real payment/payout success messages | MET | `LiveMessagingEnabled = false`; provider CHECKs; synthetic sources in-app only |
| 70–71 | Documentation, Stage 16 handover | MET | this folder; STAGE-15-TO-STAGE-16.md |

## Deviations from the brief and the roadmap

- **No provider selected** (the roadmap expected e-mail and SMS providers in this stage): blocked by LR-010, LR-011,
  LR-023 and provider confirmations; simulators and development senders stand in (KI-S15-02).
- Marketing messages are disabled entirely rather than consent-gated (LR-023).
- Real-time updates are polling every 60 s, not SSE/WebSocket.
- English templates only; no Shona or Ndebele versions.
- Staff receive in-app notifications only (plus security e-mail about their own accounts); no incident paging.
- Credential messages (verification and reset links, invitations, OTPs) remain in `auth` by design (ADR-047 §6).
