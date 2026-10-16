# Stage 14 → Stage 15 handover (Notifications & Communications)

Status of Stage 14: complete, **awaiting acceptance** (`docs/governance/stage-acceptance-register.md`, row 14
NOT_RECORDED). Stage 15 must not start until the owner asks for it. This document prepares it; it implements nothing.

## 1. What Stage 15 inherits

- `internal/notifications`: transactional email (SMTP; Mailpit locally) and SMS through the `dev_mailpit` development
  sender only. Senders refuse to run inside a database transaction, never log bodies/codes/full numbers and return
  `ErrDisabled` when disabled. **No real email, SMS or WhatsApp provider is selected.**
- The transactional outbox (`app.outbox_events`, relay and delivery in the worker, dead letters) and River jobs.
- Stage 14 in-app **task indicators** (`GET /api/v1/admin/operations/tasks`) and operational **alerts**
  (`/api/v1/admin/operations/alerts`) — computed on request, never delivered outside the app.

## 2. Notification event sources (outbox event types observed in the development database)

| Area | Events (examples) |
|---|---|
| Identity | `identity.email_verification_requested`, `identity.email_verified`, `identity.phone_verified`, `identity.staff_invited`, `identity.signed_in`, `identity.mfa_changed`, `identity.registration_existing_account` |
| Verification | `kyc.case_submitted`, `kyc.case_approved`, `kyc.level_changed`, `compliance.case_opened`, `compliance.case_resolved`, `compliance.restriction_applied` |
| Campaigns | `campaigns.submitted`, `campaigns.approved`, `campaigns.published`, `campaigns.suspended`, `campaigns.re_review_requested`, `campaigns.update_published`, `campaigns.update_submitted_for_moderation` |
| Payments | `payments.donation_intent_created`, `payments.payment_status_changed`, `payments.payment_succeeded` (TEST) |
| Payouts | `payouts.requested`, `payouts.status_changed`, `payouts.exception_opened` (TEST) |
| Risk | `risk.evaluation_recorded`, `risk.rule_version_changed`, `risk.screening_updated` (staff-only; never shown to the subject) |
| Ledger / fees | `ledger.journal_posted`, `ledger.reconciliation_exception`, `fees.policy_version_changed` (TEST; staff-only) |

Stage 15 should consume the outbox (never domain tables) and decide per event: audience, channel, template, whether the
message may reveal anything (risk, compliance and screening events must never notify their subject — LR-008).

## 3. Requirements to settle in Stage 15

| Topic | Starting point / open question |
|---|---|
| In-app notifications | Stage 14 indicators are computed; a persisted, per-user notification inbox (read/unread) is not built |
| Email delivery | provider selection, sender domain authentication (SPF/DKIM/DMARC), bounce and complaint handling, suppression list |
| SMS | provider selection for Zimbabwean networks; cost-abuse limits; OTP vs informational traffic; no provider chosen |
| WhatsApp | Business messaging policy and template approval; consent (LR-023); no integration exists |
| Preferences | per-user channel preferences and opt-outs; security and transactional messages that cannot be disabled need a documented basis |
| Transactional | receipts (no receipt may present a TEST donation as real), verification outcomes, campaign status, withdrawal status |
| Administrative alerts | the Stage 14 alert keys (`WORKER_STOPPED`, `OUTBOX_BACKLOG`, `LEDGER_INTEGRITY_FAILURE`, …) as staff notification sources; on-call routing |
| Delivery tracking | per-message state (queued, sent, delivered, failed), provider message ids, idempotent sends (one receipt per payment) |
| Retry and dead letters | reuse the outbox/River retry policy; never resend an OTP or a receipt blindly after an ambiguous provider response |
| Templates | versioned templates with review; masking rules (no full numbers, no document numbers, no amounts for TEST data shown as real) |
| Privacy and consent | LR-010 (registration), LR-011 (cross-border transfer to messaging vendors), LR-012 (retention of communication records), LR-023 (marketing and messaging consent) |
| Provider dependencies | secrets only through the secret manager (`secretref://`); provider credentials scoped; a provider ADR before any adapter |
| Communication audit history | which message, template version, channel and recipient reference was sent, by which event, without storing message bodies containing codes |

## 4. Constraints that carry over

All money is TEST money; live donations, real payouts, production ledger posting and production screening remain BLOCKED.
Do not invent provider capabilities, regulatory deadlines or consent rules; record them as LR/PCR entries. Stage 14
operations rules (CLAUDE.md) stay binding: the portal is not an authority, metrics keep documented sources, no
superuser.

## 5. Open Stage 14 items relevant to Stage 15

KI-S14-10 (task indicators in-app only), KI-S14-11 (no incident paging or status page), KI-S14-02 (no error-rate store),
KI-S14-04 (no SLA target configured). Full list: [../stage-14/known-issues.md](../stage-14/known-issues.md).
