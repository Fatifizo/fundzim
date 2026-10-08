# FundZim Audit Logging Architecture

Status: Stage 0 design. Table and hash chain implemented in Stage 3; coverage grows with each stage;
tamper-evidence verification hardened in Stage 18.
Related: [SECURITY.md](SECURITY.md), [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md),
[PRIVACY.md](PRIVACY.md), [LEDGER.md](LEDGER.md), [OBSERVABILITY.md](OBSERVABILITY.md), [DATABASE.md](DATABASE.md).

---

## 1. Purpose

The audit log answers, for every important action: **who** did it, **what** happened, **when**, **why**,
**what records changed**, and — for financial actions — **which provider and provider reference** were
involved and **which ledger transaction** resulted.

It exists for investigations, dispute handling, staff accountability, financial audit and regulatory
evidence. It is not a debugging tool and not an analytics feed.

## 2. Audit log vs other records

| Record | Purpose | Store | Mutability |
|---|---|---|---|
| **Audit events** | Accountability for actions | PostgreSQL `audit_events` | Append-only |
| **Ledger** | Financial truth (amounts, accounts) | PostgreSQL `ledger_*` | Append-only |
| **Payment/payout state history** | State machine transitions | PostgreSQL module tables | Append-only history rows |
| **Webhook inbox** | Raw (redacted) provider evidence | PostgreSQL `webhook_inbox` | Append-only payload; processing status updatable |
| **Application logs** | Operations and debugging | Log pipeline | Rotated; not evidence |
| **Traces/metrics** | Performance and health | Telemetry backend | Sampled; not evidence |

The audit log references ledger transactions and payments by ID; it does not duplicate amounts as a second
source of truth. Application logs may include an `audit_event_id` for correlation but are never the
authoritative record of an action.

## 3. Schema (conceptual)

```sql
CREATE TABLE audit_events (
    id               uuid        PRIMARY KEY,          -- UUIDv7
    seq              bigint      NOT NULL UNIQUE,  -- total order for hash chain; Stage 2: assigned under the chain lock (identity values can commit out of order)
    occurred_at      timestamptz NOT NULL,             -- when the action happened (UTC)
    recorded_at      timestamptz NOT NULL DEFAULT now(),
    actor_type       text        NOT NULL CHECK (actor_type IN ('user','staff','system','provider')),
    actor_id         uuid,                              -- null only for unauthenticated/provider events
    actor_role       text,                              -- role/permission used, e.g. 'FINANCE'
    on_behalf_of     uuid,                              -- e.g. support acting for a user, org context
    action           text        NOT NULL,              -- e.g. 'campaign.state.changed'
    target_type      text        NOT NULL,              -- e.g. 'campaign'
    target_id        uuid,
    outcome          text        NOT NULL CHECK (outcome IN ('success','denied','failed')),
    request_id       text,
    correlation_id   text,
    ip               inet,                              -- where lawful; see PRIVACY
    user_agent       text,                              -- truncated; where lawful
    reason           text,                              -- machine reason code, e.g. 'FRAUD_REPORT'
    justification    text,                              -- human text; REQUIRED for staff sensitive actions
    metadata         jsonb       NOT NULL DEFAULT '{}', -- redacted before/after, refs (payment_id, ledger_transaction_id, provider, provider_reference)
    break_glass      boolean     NOT NULL DEFAULT false,
    prev_hash        bytea,
    hash             bytea       NOT NULL
);
```

Notes:

- `occurred_at` is set from the injected clock in the same DB transaction as the action. Audit events for
  state changes are written **in the same transaction** as the change, so a committed change always has its
  audit event and a rolled-back change never does.
- Denied attempts at sensitive actions (`outcome='denied'`) are also recorded — e.g. a staff member trying to
  approve their own payout.
- Events for actions that call external systems (provider calls) are recorded via the outbox/job that
  performs them, with the provider reference once known.
- Indexes: `(target_type, target_id, occurred_at)`, `(actor_id, occurred_at)`, `(action, occurred_at)`.
  Partitioning by month when volume requires (Stage 17/18).

## 4. Immutability

Layered so no single failure permits silent edits:

1. **Grants:** `fundzim_app` has `INSERT` and `SELECT` only on `audit_events`. No `UPDATE`, `DELETE`,
   `TRUNCATE`.
2. **Triggers:** a `BEFORE UPDATE OR DELETE` row trigger and a `BEFORE TRUNCATE` statement trigger raise an
   exception, catching mis-grants and owner-role mistakes.
3. **Hash chain** (tamper evidence): each row stores
   `hash = SHA-256(prev_hash || canonical_json(row fields except hash))`, with `prev_hash` taken from the row
   at `seq - 1`. Insertion is serialised for the chain (advisory lock or single writer per partition; design
   detail in Stage 3 to avoid becoming a throughput bottleneck — option: chain per day/partition with daily
   anchor).
4. **External anchoring** (Stage 18): periodically export the latest hash (daily anchor) to a separate
   write-once store (object lock) owned by a different account, so a database-level attacker cannot rewrite
   the chain undetected.
5. **Verification job:** recompute the chain on a schedule and on demand; a break is a SEV1 alert.

Corrections are new events (e.g. `audit.annotation.added`) that reference the original; the original is
never changed.

## 5. Action catalogue (initial)

Naming: `<domain>.<object>.<verb>` in lowercase. The catalogue is a Go constant set; unknown action names
fail a test. Entries marked **J** require staff justification text.

| Domain | Actions |
|---|---|
| auth | `auth.login.succeeded`, `auth.login.failed`, `auth.otp.sent`, `auth.otp.failed_max_attempts`, `auth.session.revoked`, `auth.mfa.enrolled`, `auth.mfa.removed`, `auth.step_up.succeeded`, `auth.recovery.started`, `auth.recovery.completed` |
| users | `user.created`, `user.phone.changed`, `user.email.changed`, `user.suspended` (J), `user.data_export.requested`, `user.deletion.requested`, `user.anonymised` |
| roles | `role.grant.requested` (J), `role.grant.approved` (J), `role.revoked` (J), `breakglass.started` (J), `breakglass.ended` |
| organisations | `organisation.created`, `organisation.member.added`, `organisation.member.removed`, `organisation.verification.decided` (J) |
| campaigns | `campaign.created`, `campaign.submitted`, `campaign.state.changed` (J when staff), `campaign.material_edit.flagged`, `campaign.beneficiary.changed`, `campaign.reported`, `campaign.review.claimed`, `campaign.review.check_recorded` |
| kyc | `kyc.submission.created`, `kyc.document.uploaded`, `kyc.document.viewed` (J), `kyc.identity_number.revealed` (J), `kyc.decision.recorded` (J), `kyc.level.changed`, `kyc.status.changed` (J when staff), `kyc.vendor.result.received`, `beneficiary.verification.decided` (J) |
| payments | `payment.created`, `payment.state.changed`, `payment.refund.requested`, `payment.refund.approved` (J), `payment.dispute.opened`, `payment.provider.called` |
| webhooks | `webhook.received`, `webhook.signature.failed`, `webhook.deadlettered` |
| ledger | `ledger.transaction.posted`, `ledger.adjustment.requested` (J), `ledger.adjustment.approved` (J), `ledger.reversal.posted` (J), `ledger.invariant.violation` |
| payouts | `payout.destination.added`, `payout.destination.changed`, `payout.destination.verified`, `payout.requested`, `payout.approved` (J), `payout.rejected` (J), `payout.state.changed`, `payout.hold.applied` (J), `payout.hold.released` (J), `payout.approval.denied_self`, `payout.eligibility.rechecked` |
| fees | `fee.config.change.requested` (J), `fee.config.change.approved` (J) |
| risk/compliance | `risk.score.recorded`, `risk.case.opened`, `risk.case.closed` (J), `compliance.report.filed` (J), `sanctions.screening.hit`, `compliance.override.*` (J), `compliance.exemption.expired` |
| reconciliation | `reconciliation.run.completed`, `reconciliation.mismatch.detected`, `reconciliation.mismatch.resolved` (J) |
| admin/ops | `killswitch.activated` (J), `killswitch.deactivated` (J), `config.changed` (J), `audit.exported` (J), `policy.change.*` (J), `evidence.exported` (J), `evidence.purged` (J) |

Stage 1 additions (`campaign.review.*`, `kyc.status.changed`, `beneficiary.verification.decided`,
`payment.provider.called`, `payout.approval.denied_self`, `payout.eligibility.rechecked`, `compliance.override.*`,
`compliance.exemption.expired`, `policy.change.*`, `evidence.*`) are specified, with their required fields and
evidence links, in [compliance/audit-evidence-model.md](compliance/audit-evidence-model.md).

Not every event is equally voluminous; high-frequency, low-value events (e.g. every public page view) are
**not** audit events — they are metrics.

## 6. What must never be in audit metadata

Audit metadata is C2 and widely readable by investigators, so it must never contain:

- passwords, OTPs, session/CSRF tokens, API keys, webhook secrets, signing or encryption keys;
- card PAN, CVV, PINs (including mobile-money PINs);
- full national ID / passport numbers, document images, extracted document text, liveness media;
- full payout account numbers (use masked form plus destination ID);
- full medical detail from campaign stories (reference the campaign version ID instead);
- raw webhook payloads (reference the `webhook_inbox` row ID instead).

`before`/`after` values are produced by a per-entity redaction function that allow-lists safe fields.
A unit test feeds every audited entity with sentinel sensitive values and asserts none appear in the
serialised metadata.

## 7. Access to the audit log

- Read access is itself permissioned (`audit.read`, scoped: COMPLIANCE broadly; FINANCE for financial
  actions; SUPER_ADMIN for role/admin actions). Users can see a limited, user-facing activity history
  for their own account (logins, security changes, payouts).
- Reading or exporting the audit log by staff is itself audited (`audit.exported`).
- No staff member can delete or modify audit events through any product feature.

## 8. Retention

Retention periods for audit events (and their interaction with financial and AML record-keeping) are
**LEGAL_REVIEW_REQUIRED** (LR-012) (see [COMPLIANCE.md](COMPLIANCE.md), [PRIVACY.md](PRIVACY.md)). Design
assumptions until then:

- audit events are retained at least as long as the related financial records;
- personal data in audit events (IP, user agent) may be pseudonymised or dropped after a shorter period
  if legal review permits, without breaking the hash chain (store personal fields in a separate linked
  table, or hash over a commitment rather than the raw value — design decision in Stage 3);
- deletion/anonymisation requests do not erase audit events required for legal retention; they may
  pseudonymise the actor reference where permitted.

## 9. Operational alerts from audit events

Audit events feed security alerting (Stage 3 onward, expanded Stage 13/18):

- any `role.grant.*`, `breakglass.started`, `killswitch.*`;
- `kyc.document.viewed` volume per staff member above baseline;
- `*.denied` spikes (e.g. repeated self-approval attempts);
- `webhook.signature.failed` spikes;
- `ledger.invariant.violation` or audit hash chain break → SEV1.

## 10. Testing expectations

- Every state-changing service method has a test asserting the expected audit event (action, actor,
  target, outcome) is written in the same transaction.
- Rollback test: a failed operation leaves no audit event for the success path.
- Immutability tests: `UPDATE`/`DELETE`/`TRUNCATE` on `audit_events` fail for the app role and via the triggers.
- Hash chain tests: verification detects a modified, deleted or reordered row.
- Redaction tests as in §6.
