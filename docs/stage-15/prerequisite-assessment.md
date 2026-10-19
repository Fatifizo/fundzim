# Stage 15 prerequisite assessment

Date: 2026-10-16. Branch `stage-15/notifications`, created from `stage-14/operations` at `6af7229` (CI run 38903528991: all
8 jobs succeeded). Inputs read: `CLAUDE.md`, `README.md`, `docs/ARCHITECTURE.md`, `docs/SECURITY.md`, `docs/ROADMAP.md`,
[STAGE-14-TO-STAGE-15.md](../stage-handover/STAGE-14-TO-STAGE-15.md), `internal/platform/outbox`, `internal/platform/jobs`,
the worker composition (`internal/app/worker.go`), `internal/notifications`, every outbox consumer (`internal/app/*.go`,
`internal/auth/consumers.go`), the outbox event types present in the development database, `internal/users`, the
legal register, the acceptance register, OpenAPI, the web app and CI.

Classification: **SATISFIED**, **SATISFIED_WITH_CONDITIONS**, **BLOCKED**, **NOT_VERIFIED**.

## 1. Governance

| Item | Status | Evidence |
|---|---|---|
| Formal acceptance of Stages 0–14 | **NOT_VERIFIED** | every row of `docs/governance/stage-acceptance-register.md` is `NOT_RECORDED`; nothing is marked accepted in Stage 15 |
| Stage 9 | PARTIAL (report recommendation) | provider selection deferred (ADR-040) |
| Stages 10–14 | PASS WITH CONDITIONS (report recommendations) | TEST data only |
| Production financial operations | BLOCKED | code constants and CHECKs, unchanged |
| Live messaging | BLOCKED (new) | no email, SMS or WhatsApp provider is selected; Stage 15 adds `notifications.LiveMessagingEnabled = false` |

## 2. Legal and privacy dependencies (exact register entries, `docs/compliance/open-legal-questions.md`)

| ID | Question (verbatim, abridged only where marked …) | Effect on Stage 15 |
|---|---|---|
| LR-010 | "Registration/licensing requirements as data controller with POTRAZ (Data Protection Authority) under the Cyber and Data Protection Act; DPO appointment requirements; breach notification obligations and timelines." Stage 1 note: S.I. 155/2024 licence tiers, DPO, breach notification 24 h / 72 h. OPEN, P0. | Messaging vendors are processors; production messaging waits for the controller registration (pre-launch gate). **LEGAL_REVIEW_REQUIRED** |
| LR-011 | "May personal data (incl. KYC) be hosted outside Zimbabwe? Conditions for transfers to cloud providers and vendors (KYC, SMS, email, error tracking)? …" OPEN, P0. | Any email/SMS/WhatsApp provider outside Zimbabwe is a cross-border transfer; provider selection is blocked on it. **LEGAL_REVIEW_REQUIRED**, **PROVIDER_CONFIRMATION_REQUIRED** |
| LR-012 | "Minimum and maximum retention for KYC records, financial/transaction records, ledger, audit logs, compliance cases, and marketing data …" "No retention numbers in code until resolved." OPEN, P1. | Notification, delivery, consent and audit records carry a retention class only; nothing is purged and no period is coded. **LEGAL_REVIEW_REQUIRED** |
| LR-023 | "Consent requirements for marketing messages, transactional SMS/WhatsApp, use of WhatsApp Business messaging policies; referral attribution and tracking consent." Stage 1 note: CPA s54 unsolicited communications. OPEN, P1. | Marketing delivery is disabled; SMS and WhatsApp service messages require an explicit opt-in record until counsel says otherwise; email service messages keep the Stage 4–6 behaviour (sent to verified addresses) pending review. **LEGAL_REVIEW_REQUIRED**, **PRIVACY_REVIEW_REQUIRED** |
| LR-008 | tipping-off rules (FIU) | risk, screening and compliance outcomes never notify their subject; only the generic campaign/verification status notices that already exist |

No Zimbabwean communication or privacy rule is invented; the conservative defaults above are engineering choices to
revisit with counsel (see [consent.md](consent.md)).

## 3. Technical prerequisites

| Prerequisite | Status | Evidence |
|---|---|---|
| Transactional outbox (written in the source transaction, relay, at-least-once delivery, inbox dedupe, dead letters) | SATISFIED | `internal/platform/outbox` (ADR-025) |
| Background jobs (River, periodic jobs, retries) | SATISFIED | `internal/platform/jobs`, `internal/app/worker.go` |
| Email and SMS senders | SATISFIED_WITH_CONDITIONS | `internal/notifications`: SMTP (Mailpit locally) and `dev_mailpit` SMS only; no production provider |
| Existing notification consumers | SATISFIED_WITH_CONDITIONS | campaign notices (`campaigns.notify`), verification notices (`verification.notify`) and identity notices email directly from the outbox handler: no delivery record, no preferences, no retry state, a partial failure re-sends the whole batch. **Replaced by the Stage 15 engine** |
| Credential emails (verification and reset links, staff invitations, phone OTP) | SATISFIED | Stage 4: the secret is generated at send time and never stored. Kept on the identity path (see ADR-047 §6) |
| Users' contacts and verification flags | SATISFIED | `users.ByID` (email + verified), `users.PrimaryPhone` (verified E.164) |
| Organisation admins, staff by permission | SATISFIED | `organisations.AdminIDs`; `auth.EffectivePermissions` |
| Phone normalisation library | SATISFIED | `github.com/nyaruka/phonenumbers` (already a dependency) |
| User notification preferences | **missing** | built in Stage 15 |
| Consent records | **missing** | built in Stage 15 |
| In-app notifications | **missing** (Stage 14 task counts only) | built in Stage 15 |
| Template management | **missing** (texts hard-coded in Go) | built in Stage 15; existing texts become version 1 |
| Support-case and incident events | **missing** (operations writes no outbox events) | Stage 15 adds `operations.support_case_*` and `operations.incident_declared` (documented in [event-contracts.md](event-contracts.md)) |
| Payment success safety | SATISFIED | `payments.payment_succeeded` is emitted only by `payments.apply()` for verified provider evidence; every payment is `test_payment` |
| Operations portal integration points | SATISFIED | `OpsSnapshot` sources, health components, alerts (ADR-046) |

## 4. Stage 14 remediation review (KI-S14-01 … KI-S14-15, [../stage-14/known-issues.md](../stage-14/known-issues.md))

| ID | Issue (abridged) | Classification | Rationale |
|---|---|---|---|
| KI-S14-01 | Acceptance of Stages 1–13 not recorded | EXTERNAL_BLOCKER | owner |
| KI-S14-02 | No error-rate store; overall health never HEALTHY | **REMEDIATE_IN_STAGE_15** (notification scope) | delivery attempts are stored, so the notification error rate is measured from data; application-wide error rates stay NOT_AVAILABLE |
| KI-S14-03 | Health thresholds are engineering defaults | DEFER_WITH_RATIONALE | no approved thresholds exist; Stage 15 thresholds are documented engineering defaults too |
| KI-S14-04 | No SLA target configured | EXTERNAL_BLOCKER | owner decision |
| KI-S14-05 | Aggregates recomputed per request | DEFER_WITH_RATIONALE | Stage 17 scale; the notifications snapshot uses indexed counts |
| KI-S14-06 | Providers snapshot latency | DEFER_WITH_RATIONALE | not notification-related |
| KI-S14-07 | Exact-id search only | DEFER_WITH_RATIONALE | by design |
| KI-S14-08 | No staff picker | DEFER_WITH_RATIONALE | UX |
| KI-S14-09 | No saved views or bulk actions | DEFER_WITH_RATIONALE | not required |
| KI-S14-10 | Task indicators in-app only | **REMEDIATE_IN_STAGE_15** | staff receive persisted in-app notifications (assignments, incidents, escalations); external staff delivery stays disabled with live messaging |
| KI-S14-11 | No incident paging | **REMEDIATE_IN_STAGE_15** (in-app) | incident declarations notify incident managers in-app; paging and status pages need a provider (deferred) |
| KI-S14-12 | No access-review reminders | DEFER_WITH_RATIONALE | no cadence may be invented |
| KI-S14-13 | Intermittent public-experience test | DEFER_WITH_RATIONALE | monitor; not reproducible |
| KI-S14-14 | Simplified mock API | DEFER_WITH_RATIONALE | real coverage is the Go integration suite |
| KI-S14-15 | Stage 13 external blockers | EXTERNAL_BLOCKER | legal and provider |

The Stage 14 health report also gains a notification worker heartbeat and delivery backlog (see
[notification-architecture.md](notification-architecture.md)).
