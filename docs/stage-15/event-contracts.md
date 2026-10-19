# Stage 15 — Event contracts

The engine consumes outbox events through one consumer, `notifications.dispatch`. It maps only the events below
(`internal/notifications/events.go`, `EventTypes()`); any other event is acknowledged and ignored. Payloads carry ids,
codes and amounts — never message texts, which come from templates.

## 1. Changes to existing events

| Event | Change | Why |
|---|---|---|
| `payments.payment_status_changed` | for `to` = `FAILED` and `UNKNOWN` the payload now also carries `donor_user_id`, `amount_minor` (string) and `currency` | so the donor can be told "not confirmed yet" / "did not go through" without the engine reading payment tables |
| `payments.payment_succeeded` | now carries `donor_user_id`, `amount_minor` and `currency` | the donation confirmation needs the amount; still emitted only by `payments.apply()` on a verified success |

Both are additive (existing consumers ignore unknown fields). `donor_user_id` is absent for a guest donor, who
receives no notification (there is no recipient account).

## 2. New events (Stage 15)

Written by `internal/operations` in the same transaction as the change (`emit()` helper):

| Event | Payload | Notifies |
|---|---|---|
| `operations.support_case_opened` | `case_id`, `case_number`, `requester_user_id?` | the requester (SUPPORT) |
| `operations.support_case_assigned` | `case_id`, `case_number`, `assigned_to` | the assignee (staff, in-app) |
| `operations.support_case_status_changed` | `case_id`, `case_number`, `requester_user_id?`, `from`, `to` | the requester, for `WAITING_FOR_CUSTOMER`, `RESOLVED`, `REOPENED` only |
| `operations.incident_declared` | `incident_id`, `incident_number`, `severity`, `affected_module` | staff with `ops.incident.manage` (in-app) |

Support notices never contain internal notes, the assignee's name or the case text: only the case reference.

## 3. Mapping

| Source event(s) | Template(s) | Category | Recipients |
|---|---|---|---|
| `campaigns.created|submitted|changes_requested|approved|rejected|published|paused|suspended|reactivated|completed` | `CAMPAIGN_*` (10) | CAMPAIGN | campaign owner (user or organisation admins) |
| `kyc.*` (7), `beneficiaries.verification_*` (5), `payouts.destination_verified|rejected|information_requested|suspended|expired` | 17 verification templates | VERIFICATION | the subject (user or organisation admins) |
| `identity.password_changed`, `identity.mfa_changed`, `identity.email_change_requested`, `identity.registration_existing_account`, `identity.account_suspended` | `PASSWORD_CHANGED`, `MFA_CHANGED`, `EMAIL_CHANGE_REQUESTED`, `REGISTRATION_EXISTING_ACCOUNT`, `ACCOUNT_SUSPENDED` | SECURITY | the account |
| `auth.staff_link_confirmed` | `STAFF_LINK_CONFIRMED_STAFF`, `STAFF_LINK_CONFIRMED_PERSONAL` | SECURITY | the staff account and the linked personal account |
| `payouts.destination_created|changed` | `PAYOUT_DESTINATION_CHANGED` | SECURITY | the owner |
| `payments.payment_status_changed` (`FAILED`, `UNKNOWN`) | `PAYMENT_FAILED`, `PAYMENT_PENDING_CONFIRMATION` | PAYMENT | the donor |
| `payments.payment_succeeded` | `DONATION_CONFIRMED` | PAYMENT | the donor |
| `payouts.requested` | `WITHDRAWAL_REQUESTED` | WITHDRAWAL | the requester |
| `payouts.status_changed` | `WITHDRAWAL_UNDER_REVIEW|APPROVED|REJECTED|CANCELLED`, `PAYOUT_SUBMITTED|OUTCOME_UNKNOWN|COMPLETED|FAILED|REVERSED` | WITHDRAWAL / PAYOUT | the requester |
| `payouts.exception_opened` | `PAYOUT_EXCEPTION_OPENED` | ADMINISTRATIVE | staff with `payout.view` |
| `payments.refund_requested` | `REFUND_AWAITING_APPROVAL` | ADMINISTRATIVE | staff with `refund.approve` |
| `fees.adjustment_requested` | `FEE_ADJUSTMENT_AWAITING_APPROVAL` | ADMINISTRATIVE | staff with `fee.adjust.approve`, excluding the requester |
| `risk.rule_version_changed` (`VERSION_SUBMITTED`) | `RISK_RULE_AWAITING_APPROVAL` | ADMINISTRATIVE | staff with `risk.rule.approve` |
| `risk.case_opened` (P1, P2) | `RISK_CASE_OPENED_HIGH` | RISK | staff with `risk.case.work` |
| `risk.case_escalated` | `RISK_CASE_ESCALATED` | RISK | staff with `compliance.case.view` |
| `compliance.case_opened` (not `RESTRICTED_STR`) | `COMPLIANCE_CASE_OPENED` | COMPLIANCE | staff with `compliance.case.view` |
| the four operations events | `SUPPORT_CASE_*`, `INCIDENT_DECLARED` | SUPPORT / ADMINISTRATIVE | see §2 |

61 templates in all: 27 moved from the Stage 4–6 notices (origin `STAGE_6_NOTICE`) and 34 new (origin
`STAGE_15_BASELINE`).

## 4. Rules the mapping keeps

* **Payment success comes only from `payments.payment_succeeded`**, which `payments.apply()` emits on a verified
  success. A donation intent, a browser return, an unverified webhook, a timeout, an `UNKNOWN` outcome or a
  status change to anything else never produces `DONATION_CONFIRMED` (ADR-047 §8; tested in
  `TestNotificationsFinancialSafety`).
* **Payout completion comes only from `payouts.status_changed` to `COMPLETED`**, the state reached on provider
  confirmation (Stage 11 machine). `SUBMITTED` says "sent to the provider", `UNKNOWN` says "being confirmed".
* **TEST money is labelled and kept in-app.** `test_payment`, `test_payout`, `simulated` or an
  `execution_environment` other than `PRODUCTION` make the notification synthetic: in-app only with a `[TEST]`
  title and the `TEST data` badge; external channels are suppressed `SYNTHETIC_SOURCE`. Today every payment and payout
  is TEST, so no external payment or payout message is ever sent.
* **Risk and compliance never reach their subject** (LR-008): those categories are staff-only by policy;
  `RESTRICTED_STR` compliance cases produce nothing at all; no notification names a rule, score, signal or
  screening match. Subjects see only the generic campaign / verification status notices that existed before.
* **Amounts** are formatted from integer minor units with the currency (`money.String`); an unparseable amount
  becomes "your donation", never a guessed number.
* **Exactly-once creation:** the outbox inbox plus `notification_events.source_event_id UNIQUE`; a redelivered event
  creates nothing (tested with 100 concurrent duplicates).
