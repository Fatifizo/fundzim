# Schema Overview

**Stage 2 — design only.** This is the map of every table in the FundZim database, grouped by owning module.
The table catalogue and ownership are fixed by [design-baseline.md §5](../stage-2/design-baseline.md); this
document adds a one-line purpose per table, the counts, a high-level ERD and the cross-schema rules.
Per-domain detail: [identity-schema.md](identity-schema.md), [organisation-beneficiary-schema.md](organisation-beneficiary-schema.md),
[campaign-schema.md](campaign-schema.md), [audit-notification-schema.md](audit-notification-schema.md),
[ledger-schema.md](ledger-schema.md), [payment-schema.md](payment-schema.md) and the other `docs/database/*`
files. Principles: [design-principles.md](design-principles.md).

SQL drafts (non-executable): [`design/sql/`](../../design/sql/), loaded in file order 0001 → 0018.

---

## 1. Schemas

| Schema | Purpose | Runtime role | Draft file(s) |
|---|---|---|---|
| `app` | Core domain + platform tables | `fundzim_app` | 0002, 0004, 0005, 0007, 0009, 0011, 0012, 0014, 0015, 0016 |
| `queue` | River job tables (library migrations, pinned) | `fundzim_app` | — (created by River) |
| `ledger` | Double-entry ledger | `fundzim_app` (restricted grants) | 0006 |
| `audit` | Audit, security audit, evidence | `fundzim_app` (INSERT/SELECT + `legal_hold`) | 0003 |
| `kyc` | C3 identity and KYB data | `fundzim_kyc` only | 0010 |
| `risk` | Limits, holds, signals, monitoring | `fundzim_app` | 0008 |
| `compliance` | Cases, screening, STR, restrictions | `fundzim_compliance` (+ RLS) | 0013 |
| `recon` | Reconciliation and settlement | `fundzim_app` | 0017 |

Roles, default privileges and RLS: `0018_grants.sql` (ADR-022).

## 2. Table catalogue by module

`AO` = append-only (triggers + grants). `M` = mutable (with `updated_at`, often `version`). `R` = reference data
changed only by migrations. `AO*` = append-only except named bookkeeping/set-once columns.

### 2.1 platform (`app`) — 0002

| Table | Purpose | Kind |
|---|---|---|
| `currencies` | ISO 4217 registry (code, numeric, minor units, symbol, enabled, minor_units_verified) | R |
| `markets` | Market configuration (ZW: Africa/Harare, +263, default USD) | R |
| `idempotency_keys` | Client `Idempotency-Key` records: scope, key, request hash, stored response | M (frozen when COMPLETED) |
| `outbox_events` | Transactional outbox | AO* (dispatch columns) |
| `inbox_events` | Consumer dedupe `(consumer, event_id)` | AO (deletable by retention) |
| `feature_flags` | Audited flags, kill switches, policy switches | M |
| `feature_flag_changes` | One row per flag version, maker-checker for `requires_approval` flags (baseline I-3) | AO |
| `status_transitions` | Allowed state-machine edges for every guarded machine (0001) | R |
| `queue.*` | River tables | library |

### 2.2 audit (`audit`) — 0003

| Table | Purpose | Kind |
|---|---|---|
| `audit_events` | Accountability log, hash-chained | AO |
| `security_audit_events` | Separate hash chain for security events | AO |
| `evidence_records` | Evidence metadata + SHA-256 + classification + retention class | AO* (`legal_hold`) |
| `evidence_holds` | Two-person legal hold / release history | AO |

### 2.3 users (`app`) — 0004

| Table | Purpose | Kind |
|---|---|---|
| `users` | Account (`account_kind` USER/STAFF), status, KYC level/status mirror | M |
| `user_profiles` | Display name, locale, time zone, avatar | M |
| `user_emails` | Emails; one verified owner per normalised address | M |
| `user_phone_numbers` | E.164 numbers; one verified owner per number | M |

### 2.4 auth (`app`) — 0004

| Table | Purpose | Kind |
|---|---|---|
| `authentication_identities` | Primary factors per user (PHONE_OTP, EMAIL_OTP, PASSWORD, WEBAUTHN); staff: PASSWORD/WEBAUTHN only | M |
| `password_credentials` | Argon2id hashes (history kept) | AO* |
| `otp_challenges` | Purpose-bound OTP challenges (HMAC only) | M (frozen when consumed) |
| `sessions` | Opaque sessions (SHA-256 of token), USER/STAFF | M |
| `mfa_methods` | WebAuthn credentials, encrypted TOTP seeds | M |
| `recovery_codes` | Hashed MFA recovery codes | AO* |
| `roles` | Staff roles (9) with SoD conflict list | R |
| `permissions` | Granular permissions | R |
| `role_permissions` | Role → permission matrix | R |
| `role_assignments` | Active staff roles (from approved requests) | AO* (revocation) |
| `role_assignment_requests` | Maker-checker grant/revoke requests | M |
| `break_glass_grants` | Time-boxed (≤ 1 h), justified, approved emergency permission | AO* (revocation set once) |
| `staff_conflict_declarations` | Staff-declared relationships to subjects (object-level SoD) | AO* (withdrawal set once) |
| `security_events` | Authentication telemetry | AO |

### 2.5 storage (`app`) — 0005

| Table | Purpose | Kind |
|---|---|---|
| `stored_objects` | Object metadata: bucket class, random keys, SHA-256, scan status, classification | M (identity immutable) |
| `upload_sessions` | Presigned upload grants | M (final once not PENDING) |

### 2.6 ledger (`ledger`) — 0006 (see [ledger-schema.md](ledger-schema.md))

`ledger_accounts`, `ledger_transactions`, `ledger_entries`, `ledger_balances` (projection),
`ledger_posting_rules`, `ledger_posting_batches`, `ledger_adjustments`, `ledger_periods`,
`ledger_invariant_runs`, `ledger_posting_rule_lines` (rule → allowed account classes).

### 2.7 fees and psp (`app`) — 0007

| Module | Tables |
|---|---|
| fees | `fee_schedules`, `fee_schedule_versions`, `fee_schedule_change_requests` |
| psp | `payment_providers`, `provider_capabilities`, `provider_accounts`, `provider_webhook_inbox`, `provider_health_events` |

### 2.8 risk (`risk`) — 0008

`limits`, `limit_change_requests`, `holds`, `hold_events`, `risk_signals`, `risk_assessments`,
`monitoring_rules`, `monitoring_alerts`, `hold_release_requests` (maker-checker hold release, I-3).

### 2.9 organisations (`app`) — 0009

| Table | Purpose | Kind |
|---|---|---|
| `organisations` | Platform organisation account (display name, type, account status) | M |
| `organisation_roles` | ORG_ADMIN, ORG_MEMBER (baseline I-1) | R |
| `organisation_members` | Personal accounts in an org; ≥ 1 active ORG_ADMIN | M |
| `organisation_invitations` | Invitations (token SHA-256) | M |
| `organisation_verifications` | Projection of KYB level/status from `kyc` events | M (forward-only) |

### 2.10 kyc (`kyc`) — 0010

`verification_profiles`, `profile_events`, `kyc_cases`, `kyc_checks`, `kyc_documents`, `kyc_decisions`,
`identities`, `consents`, `kyb_organisations`, `kyb_cases`, `kyb_checks`, `organisation_persons`,
`beneficial_owners`, `representative_authorities`, `fundraising_authorities`, `beneficiary_evidence`,
`gate_policies`, `vendor_callback_inbox`.

### 2.11 notifications (`app`) — 0011

| Table | Purpose | Kind |
|---|---|---|
| `notification_templates` | Versioned templates; immutable once ACTIVE | M |
| `notification_jobs` | One message to one recipient; `dedupe_key` unique | M (guarded machine) |
| `notification_attempts` | One row per provider call | AO |
| `notification_preferences` | Opt-outs for non-mandatory categories | M |
| `notification_suppressions` | Bounces, complaints, STOP, invalid destinations | AO* (lift) |

### 2.12 beneficiaries (`app`) — 0012

| Table | Purpose | Kind |
|---|---|---|
| `beneficiaries` | Who the money is for; verification status | M (guarded machine) |
| `beneficiary_relationships` | Relationship and authority basis to the owner | AO* (supersession) |
| `beneficiary_verifications` | Verification decisions (four eyes where required) | AO |
| `institution_payees` | Verified hospitals, schools, funeral parlours | M |

### 2.13 compliance (`compliance`) — 0013

`compliance_cases`, `compliance_case_events`, `compliance_case_links`, `screening_requests`,
`sanctions_screening_results`, `screening_hits`, `str_reports`, `compliance_restrictions`,
`screening_callback_inbox` (I-25), `screening_list_sources`, `screening_suppressions`.

### 2.14 campaigns (`app`) — 0014

| Table | Purpose | Kind |
|---|---|---|
| `campaign_categories` | Categories with default risk tier | R-ish (config) |
| `campaign_review_policies` | Versioned review policies; immutable once PUBLISHED | M |
| `campaigns` | Campaign aggregate (status, visibility, settlement model, goal currency) | M (guarded machine) |
| `campaign_versions` | Content snapshots at submission / material edits | AO |
| `campaign_goals` | Goal versions (amount + currency) | AO |
| `campaign_media` | Images (public media bucket only) | M |
| `campaign_updates` | Owner updates | M |
| `campaign_beneficiaries` | Campaign ↔ beneficiary link (one primary) | AO* (unlink) |
| `campaign_reviews` | Review per submission, pinned to policy version | M (final once DECIDED) |
| `campaign_status_history` | Every lifecycle transition | AO |
| `campaign_reports` | Public abuse reports | M |
| `campaign_moderation_actions` | Staff content actions | AO |

### 2.15 payments (`app`) — 0015 (see [payment-schema.md](payment-schema.md))

`donations`, `payment_intents`, `payment_attempts`, `payment_transactions`, `payment_provider_references`,
`payment_events`, `refund_requests`, `refund_transactions`, `payment_disputes`, `chargebacks`.

### 2.16 payouts (`app`) — 0016

`payout_destinations`, `payout_destination_verifications`, `payout_requests`, `payout_attempts`,
`payout_approvals`, `payout_reservations`, `payout_provider_references`, `payout_events`, `payout_failures`,
`payout_reversals`, `payout_eligibility_decisions`, `recovery_cases`, `recovery_case_events`,
`payout_destination_override_requests` (I-3).

### 2.17 reconciliation (`recon`) — 0017

`reconciliation_sources`, `reconciliation_imports`, `reconciliation_runs`, `reconciliation_items`,
`reconciliation_matches`, `reconciliation_discrepancies`, `reconciliation_resolutions`, `settlement_batches`,
`settlement_items`.

### 2.18 admin

No tables (composes other modules' services).

## 3. Table counts

Base tables in the drafts as loaded by the validator (`npm run validate`), excluding `queue.*`:

| Schema | Tables |
|---|---|
| `app` | 86 (incl. `status_transitions`) |
| `audit` | 4 |
| `compliance` | 11 |
| `kyc` | 18 |
| `ledger` | 10 |
| `recon` | 9 |
| `risk` | 9 |
| **Total** | **147** |

By domain:

| Domain | Module(s) | Schema | Tables |
|---|---|---|---|
| Platform | platform | app | 8 (`currencies`, `markets`, `idempotency_keys`, `outbox_events`, `inbox_events`, `feature_flags`, `feature_flag_changes`, `status_transitions`) |
| Audit & evidence | audit | audit | 4 |
| Identity | users, auth | app | 4 + 14 |
| Storage | storage | app | 2 |
| Ledger | ledger | ledger | 10 |
| Fees & providers | fees, psp | app | 3 + 5 |
| Risk | risk | risk | 9 |
| Organisations | organisations | app | 5 |
| KYC/KYB | kyc | kyc | 18 |
| Notifications | notifications | app | 5 |
| Beneficiaries | beneficiaries | app | 4 |
| Compliance | compliance | compliance | 11 |
| Campaigns | campaigns | app | 12 |
| Payments | payments | app | 10 |
| Payouts | payouts | app | 14 |
| Reconciliation | reconciliation | recon | 9 |

## 4. High-level ERD

```mermaid
erDiagram
    USERS ||--o{ USER_EMAILS : has
    USERS ||--o{ USER_PHONE_NUMBERS : has
    USERS ||--o{ SESSIONS : has
    USERS ||--o{ ROLE_ASSIGNMENTS : "staff holds"
    ROLES ||--o{ ROLE_ASSIGNMENTS : granted
    USERS ||--o{ ORGANISATION_MEMBERS : joins
    ORGANISATIONS ||--o{ ORGANISATION_MEMBERS : has
    ORGANISATIONS ||--|| ORGANISATION_VERIFICATIONS : "KYB projection"
    USERS ||--o{ CAMPAIGNS : owns
    ORGANISATIONS ||--o{ CAMPAIGNS : owns
    USERS ||--o{ BENEFICIARIES : declares
    ORGANISATIONS ||--o{ BENEFICIARIES : declares
    INSTITUTION_PAYEES ||--o{ BENEFICIARIES : serves
    CAMPAIGNS ||--o{ CAMPAIGN_BENEFICIARIES : links
    BENEFICIARIES ||--o{ CAMPAIGN_BENEFICIARIES : linked
    CAMPAIGNS ||--o{ CAMPAIGN_GOALS : "goal versions"
    CAMPAIGNS ||--o{ CAMPAIGN_STATUS_HISTORY : transitions
    CAMPAIGNS ||--o{ DONATIONS : receives
    DONATIONS ||--|| PAYMENT_INTENTS : "paid by"
    CAMPAIGNS ||--o{ PAYOUT_REQUESTS : "pays out"
    PAYOUT_DESTINATIONS ||--o{ PAYOUT_REQUESTS : to
    CURRENCIES ||--o{ CAMPAIGN_GOALS : denominates
    CURRENCIES ||--o{ LEDGER_ACCOUNTS : denominates
    LEDGER_TRANSACTIONS ||--|{ LEDGER_ENTRIES : balances
    LEDGER_ACCOUNTS ||--o{ LEDGER_ENTRIES : posts
    KYC_VERIFICATION_PROFILES }o--|| USERS : "kyc → app FK"
    KYB_ORGANISATIONS }o--|| ORGANISATIONS : "kyc → app FK"
    RISK_HOLDS }o--o| CAMPAIGNS : "subject (owner_type, id)"
    AUDIT_EVENTS }o--o{ EVIDENCE_RECORDS : references
```

The ledger never references domain tables: account owners are `(owner_type, owner_id)` without FKs.
Campaigns never store a raised amount; it is derived per currency from `ledger_entries`.

## 5. Cross-schema foreign-key rules

| From → To | Allowed? | Notes |
|---|---|---|
| `kyc` → `app` (users, organisations, stored_objects) | Yes | KYC rows attach to core identities |
| `risk`, `compliance`, `recon` → `app` reference tables | Yes | e.g. `app.users`, `app.currencies` |
| `ledger` → `app.currencies` | Yes, **only** this | Ledger stays domain-agnostic |
| `audit` → `app` | No FKs to users | Actor ids are plain uuids (system/provider actors; anonymisation) |
| `app` → `kyc` | **Never** | Opaque `*_ref uuid` columns, validated by the `kyc` service |
| `app` → `audit` | No FKs | `audit_event_id` columns are plain uuids (same-transaction write) |
| `app` → `risk` / `compliance` / `ledger` | No FKs | `risk_hold_id`, `ledger_transaction_id`, `*_case_ref` are plain uuids |

Module ownership inside `app` (only the owner reads/writes) is a code rule enforced by the Stage 3
query-ownership test; DB triggers enforce row invariants and read only tables of the same module or platform reference tables (`app.currencies`).
