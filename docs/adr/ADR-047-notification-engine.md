# ADR-047: Notification engine — outbox-fed, recorded, consent- and preference-aware, simulator-only delivery

- **Status:** Proposed (Stage 15, 2026-10-16)
- **Deciders:** Technical lead (Stage 15); owner acceptance pending
- **Implements:** ROADMAP Stage 15; [STAGE-14-TO-STAGE-15.md](../stage-handover/STAGE-14-TO-STAGE-15.md)
- **Related:** ADR-025 (outbox), ADR-046 (operations portal); LR-008, LR-010, LR-011, LR-012, LR-023

## Context

Stages 4–6 send notices by emailing straight from outbox handlers (`campaigns.notify`, `verification.notify`, identity
notices): no record of what was sent, no preferences, no consent, no retry state (a failure re-sends the batch), no
in-app channel, texts hard-coded in Go. No email, SMS or WhatsApp provider is selected and the legal basis for SMS,
WhatsApp and marketing messages is open (LR-023).

## Decision

1. **One engine in `internal/notifications`** (imports `platform`, `audit`, `users`; organisation admins, staff by
   permission and phone verification come through interfaces wired in `internal/app`). It owns `app.notification_*`
   and `app.communication_consent*` tables. The ad-hoc campaign and verification consumers are removed; their texts become
   version 1 of the corresponding templates. Identity security notices (password, MFA, e-mail change) move to the
   engine.
2. **Creation is exactly-once per event, delivery at-least-once.** A single consumer (`notifications.dispatch`)
   subscribes to the mapped event types. In one transaction it records the event (`notification_events`, unique source
   event), the notifications (unique per event, recipient and type), one delivery per channel with its routing decision
   (QUEUED or SUPPRESSED with a reason) and the consumer inbox row. Nothing is created before the source transaction has
   committed (the outbox guarantees it).
3. **Delivery** is a periodic worker sweep: due deliveries are claimed with `FOR UPDATE SKIP LOCKED` and a lease, the
   provider is called outside any transaction, every attempt is recorded, and the delivery follows a database-guarded
   state machine. Transient errors back off exponentially with jitter up to a limit, then dead-letter; permanent errors
   fail (and suppress bad destinations); a timeout is UNKNOWN and is resolved by a status query before any retry.
   Provider acceptance is never recorded as delivery.
4. **Providers** are per-channel interfaces (send, status, verify webhook, capabilities, validate configuration).
   Implementations: deterministic TEST_ONLY simulators for email, SMS and WhatsApp; the existing development SMTP sender
   (Mailpit) and development SMS sender for local stacks. `LiveMessagingEnabled = false`: in production every external
   delivery is SUPPRESSED (`LIVE_MESSAGING_DISABLED`); provider readiness can never be VERIFIED or PRODUCTION_ENABLED
   without a selection ADR (database CHECK).
5. **Policy** (code-reviewed table per category): SECURITY is mandatory on email and in-app; marketing is disabled
   (`MarketingEnabled = false`) and would need GRANTED marketing consent; SMS and WhatsApp service messages require a
   GRANTED opt-in consent record (LR-023 open); external channels need a verified contact and no active suppression;
   channel fallback is disabled for every category until a routing policy is approved; risk, screening and compliance
   outcomes never notify their subject (LR-008). Staff accounts receive in-app notifications only, except the SECURITY
   e-mail about their own account, which stays mandatory as for every account.
6. **Credential messages stay in `auth`.** E-mail verification links, password reset links, staff invitations and phone
   OTPs are generated at send time and never stored; they keep the Stage 4 path (rate limits, TTLs) and are not
   persisted by the engine.
7. **Templates** are database records with immutable versions (DRAFT → PENDING_APPROVAL → APPROVED → ACTIVE → RETIRED),
   maker-checker with step-up, hashed content, `{{variable}}` placeholders from a per-template allow-list (no logic, no
   code execution), HTML built only by escaping the text. Each notification records the version it used. WhatsApp
   provider approval status is NOT_SUBMITTED everywhere; a simulator acceptance (`SIMULATOR_ACCEPTED`) counts only while
   the active WhatsApp provider is the simulator, and `APPROVED` requires provider evidence (trigger).
8. **Financial accuracy.** A donation success notification comes only from `payments.payment_succeeded` (verified
   evidence through `payments.apply`); intents, UNKNOWN outcomes and failures never produce one. A payout success comes
   only from the payout reaching its paid state. Every Stage 15 payment and payout is TEST: such notifications are in-app
   only and labelled TEST; external delivery is SUPPRESSED (`SYNTHETIC_SOURCE`).
9. **Webhooks** (`POST /api/v1/webhooks/notifications/{provider}`): HMAC signature, timestamp window, provider event id
   dedupe, payload validation, rate limiting and audit; status updates are applied by a deterministic precedence rule
   (out-of-order and duplicate callbacks never regress a delivery) and every callback is kept as evidence.
10. **Operations integration:** a notifications `OpsSnapshot` (queues, failures, dead letters, provider readiness) and
    health component (worker heartbeat, backlog, measured delivery error rate), alerts, and in-app notifications for
    staff (assignments, incidents, escalations).

## Consequences

- One auditable record of every notification and delivery attempt, without storing e-mail bodies or secrets.
- Nothing reaches a real person outside the local stack until a provider ADR, legal review and stage acceptance exist.
- Some messages that were e-mailed in Stages 5–6 now depend on templates being ACTIVE (baseline seeded by migration).
