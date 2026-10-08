# Stage 2 Design Baseline (canonical decisions)

**Stage 2 — design only.** This document fixes the cross-cutting decisions that every other Stage 2
document follows: module names, the dependency graph, schema namespaces, table names and ownership,
state names, migration file order and ADR numbers. Where a Stage 2 document disagrees with this baseline,
the baseline wins and the other document is a defect. Changing the baseline needs an ADR.

Inputs: Stage 0 docs ([ARCHITECTURE.md](../ARCHITECTURE.md), [DATABASE.md](../DATABASE.md),
[LEDGER.md](../LEDGER.md), [PAYMENTS.md](../PAYMENTS.md), [SECURITY.md](../SECURITY.md)), Stage 1 specifications
and the [Stage 1 → Stage 2 handover](../stage-handover/STAGE-1-TO-STAGE-2.md).

---

## 1. Repository layout (preserves Stage 0)

The Stage 2 brief suggests `apps/api/internal/...`. Stage 0 (ARCHITECTURE §4, CLAUDE.md) already fixed
**one Go module at the repository root** with `apps/api/cmd/*` entrypoints and `internal/<module>`. Stage 2
keeps the Stage 0 layout; nothing is gained by moving it, and moving it would contradict an accepted decision.

```
go.mod                         # module path chosen in Stage 3 (placeholder: github.com/Fatifizo/fundzim)
apps/api/cmd/api/              # HTTP mode entrypoint
apps/api/cmd/worker/           # worker mode entrypoint (same binary build, different main)
apps/api/cmd/fundzimctl/       # operator CLI: migrate, invariant checks (Stage 3+)
apps/web/                      # Next.js (presentation only)
internal/<module>/             # domain modules (§2)
internal/app/                  # composition root: wiring, HTTP router, job registration. Nothing imports it.
api/openapi/fundzim-v1.yaml    # OpenAPI 3.1 contract (source of truth for the HTTP API)
migrations/                    # executable goose migrations (start in Stage 3; empty now)
design/sql/                    # NON-EXECUTABLE schema drafts (Stage 2), validated in-memory only
```

There is no `pkg/` directory: nothing is intended for import by other repositories.

## 2. Modules (final list)

Stage 0 modules are kept. Stage 2 adds two: `psp` and `beneficiaries` (ADR-021).

| Module | Responsibility (summary) | New in Stage 2? |
|---|---|---|
| `platform` | Shared kernel: config, DB pool/tx helper, `money`, `ids`, `clock`, HTTP envelope/errors, logging, crypto (envelope encryption, HMAC, blind index), outbox writer, job client, idempotency store, maker-checker helper library (code only) | — |
| `audit` | Audit events, security audit events, evidence records (metadata + hash) and evidence holds | evidence moved here (§5) |
| `users` | User accounts (user and staff), profiles, emails, phone numbers | — |
| `auth` | Credentials, OTP, sessions, MFA, roles, permissions, role assignments, authorisation evaluation, security events. Called "identity" in the Stage 2 brief. | — |
| `storage` | Object metadata, upload sessions, quarantine → scan → promote, presigned URLs | — |
| `ledger` | Accounts, journals, entries, projections, named posting rules, adjustments, periods, invariant runs | — |
| `fees` | Fee schedules (versioned), fee calculation. Never posts. | — |
| `psp` | Provider registry, capability registry, provider adapter interfaces (`PaymentProvider`, `PayoutProvider`, `RefundProvider`, `ProviderWebhookVerifier`, `ProviderReconciliationSource`), sandbox provider, webhook inbox, provider health | **new** (was "shared provider interface package") |
| `risk` | Limits registry, holds (all hold types), risk signals, assessments, monitoring rules and alerts | holds and limits owned here (§5) |
| `organisations` | Organisations, members, org roles, invitations, verification status projection | — |
| `kyc` | KYC and KYB cases, checks, documents, decisions, identities (C3), beneficial owners, consents, fundraising authorities, beneficiary evidence | — |
| `notifications` | Templates, jobs, attempts, preferences, suppressions | — |
| `beneficiaries` | Beneficiaries, relationships, beneficiary verification decisions (no C3), institution payees | **new** (split from `campaigns`) |
| `compliance` | Compliance cases and events, screening requests/results/hits, STR preparation, compliance restrictions | — |
| `campaigns` | Campaigns, categories, goals, media refs, updates, campaign↔beneficiary links, reviews, review policies, status history, reports, moderation, versions, freeze/unfreeze orchestration | — |
| `payments` | Donations, payment intents, attempts, provider transactions, references, events, refunds, disputes, chargebacks | — |
| `payouts` | Payout destinations (+ verifications), payout requests, attempts, approvals, reservations, references, events, failures, reversals, eligibility decisions, recovery cases | — |
| `reconciliation` | Sources, imports, runs, items, matches, discrepancies, resolutions, settlement batches/items | — |
| `admin` | Staff HTTP handlers composed from other modules' public services. No tables, no business rules. | — |

## 3. Dependency graph (compile-time imports)

A module may import only the modules listed. Everything else is forbidden. Events through the outbox create
**no** compile-time dependency. The graph is acyclic by construction (layers); Stage 3 adds an import-graph
test that enforces this table.

| Layer | Module | May import |
|---|---|---|
| 0 | `platform` | — |
| 1 | `audit` | platform |
| 2 | `users` | platform, audit |
| 2 | `storage` | platform, audit |
| 2 | `ledger` | platform, audit |
| 2 | `fees` | platform, audit |
| 2 | `psp` | platform, audit |
| 2 | `risk` | platform, audit |
| 3 | `organisations` | platform, audit, users |
| 3 | `notifications` | platform, audit, users |
| 4 | `auth` | platform, audit, users, notifications (synchronous OTP delivery only) |
| 4 | `kyc` | platform, audit, storage, users, organisations |
| 5 | `beneficiaries` | platform, audit, users, organisations, kyc |
| 5 | `compliance` | platform, audit, users, organisations, kyc, risk, storage |
| 6 | `campaigns` | platform, audit, users, organisations, kyc, beneficiaries, storage, risk, ledger, fees, compliance (restriction checks, read-only) |
| 7 | `payments` | platform, audit, campaigns, fees, ledger, risk, psp, storage, compliance (restriction checks, read-only) |
| 7 | `payouts` | platform, audit, users, organisations, kyc, beneficiaries, compliance, campaigns, ledger, risk, psp, fees, storage |
| 8 | `reconciliation` | platform, audit, ledger, psp, payments, payouts, storage |
| 9 | `admin` | any module's public service package |
| — | `internal/app` | everything (composition root) |

Notes on deliberate choices:

- `organisations` no longer imports `kyc` (Stage 0 allowed a status query). It keeps a verification-status
  **projection** fed by `kyc` events. Decisions that need the live status (for example payout eligibility)
  call `kyc` directly.
- `campaigns` imports `ledger` only to post the freeze/unfreeze journals inside the freeze transaction
  (LEDGER §6.8), and `fees` only to preview fees for display.
- `payments` and `payouts` never import each other. Cross-effects (for example a lost dispute creating a
  recovery case in `payouts`) travel as outbox events.
- `risk` is a leaf so that every module can place or read holds and limits without cycles.
- `auth` imports `notifications` only for synchronous OTP delivery: the code is passed in memory to the SMS/email
  sender and never written to the outbox, jobs or logs (only its HMAC is stored).
- `campaigns` and `payments` call `compliance` read-only to enforce `CREATE_CAMPAIGN` / `DONATE` restrictions.
- Modules that keep evidence or statement files (`compliance`, `payments`, `payouts`, `reconciliation`) import
  `storage` to write the object, then register it with `audit` (§6 #2).
- `compliance` never imports `payouts` or `payments`; it places holds through `risk` and records case links
  by ID.

## 4. Schema namespaces

`public` is not used (`REVOKE ALL ON SCHEMA public FROM PUBLIC`). Schemas follow **sensitivity and control
boundaries**, not one schema per module. Table ownership by module is recorded in §5 and enforced in Stage 3
by a query-ownership test (each module's sqlc query files may reference only its own tables).

| Schema | Holds | Who may touch it |
|---|---|---|
| `app` | Core domain tables of all modules not listed below, plus platform tables | `fundzim_app` |
| `queue` | Job-queue tables managed by the queue library (River) | `fundzim_app` (worker and API enqueue) |
| `ledger` | Ledger tables | `fundzim_app` with restricted grants (INSERT/SELECT; UPDATE only on `ledger_balances`) |
| `audit` | Audit events, security audit events, evidence records/holds | `fundzim_app` INSERT/SELECT (plus UPDATE of `evidence_records.legal_hold` only) |
| `kyc` | KYC/KYB tables with C3 data | `fundzim_kyc` only |
| `risk` | Limits, holds, signals, assessments, monitoring | `fundzim_app` |
| `compliance` | Cases, screening, STR preparation, restrictions | `fundzim_compliance` (new role, ADR-022). STR-restricted rows protected by row-level security. |
| `recon` | Reconciliation and settlement tables | `fundzim_app` |

## 5. Table catalogue and ownership

Every table has exactly one owning module. Only the owner reads or writes it. Names are unique across schemas.
"Brief name" maps the Stage 2 brief's suggested names where the final name differs.

### 5.1 `platform` (schema `app` unless noted)

| Table | Purpose | Brief name |
|---|---|---|
| `currencies` | ISO 4217 registry (MONEY §2) | — |
| `markets` | Market configuration (ZW first) | — |
| `idempotency_keys` | Client → API idempotency (scope, key, request hash, response) | `payment_idempotency_keys` |
| `outbox_events` | Transactional outbox | `outbox_events` |
| `inbox_events` | Consumer-side dedupe of outbox events (consumer, event_id) | `inbox_events` |
| `feature_flags` | Audited flags | — |
| `feature_flag_changes` | Append-only flag change history; maker-checker for flags marked `requires_approval` (I-3) | — |
| `queue.*` | River job tables (created by the library's migrations, pinned version) | — |

### 5.2 `audit` (schema `audit`)

`audit_events`, `security_audit_events`, `evidence_records`, `evidence_holds`.

Security audit events form a **separate hash chain** for security-relevant events (role grants, break-glass,
key rotation, MFA changes, KYC document views). `SECURITY_ADMIN` can read them without reading financial audit
events.

### 5.3 `users` (schema `app`)

`users` (with `account_kind` `USER` | `STAFF`), `user_profiles`, `user_emails`, `user_phone_numbers`.

### 5.4 `auth` (schema `app`)

`authentication_identities` (credential type per user: `PHONE_OTP`, `EMAIL_OTP`, `PASSWORD`, `WEBAUTHN`),
`password_credentials`, `otp_challenges`, `sessions`, `mfa_methods`, `recovery_codes`, `roles`, `permissions`,
`role_permissions`, `role_assignments`, `role_assignment_requests` (maker-checker), `break_glass_grants` (time-boxed, justified,
maker-checker), `staff_conflict_declarations`, `security_events`
(authentication telemetry: high volume, append-only, shorter retention; not the audit log).

### 5.5 `storage` (schema `app`)

`stored_objects`, `upload_sessions`.

### 5.6 `ledger` (schema `ledger`)

| Table | Brief name |
|---|---|
| `ledger_accounts` | `ledger_accounts` |
| `ledger_transactions` (one balanced journal per business event) | `ledger_journals` / `ledger_transactions` |
| `ledger_entries` | `ledger_entries` |
| `ledger_balances` (projection) | — |
| `ledger_posting_rules` (registry of allowed named rules) | — |
| `ledger_posting_rule_lines` (account patterns each rule may debit or credit) | — |
| `ledger_posting_batches` (optional grouping for bulk postings, e.g. a settlement batch) | `ledger_posting_batches` |
| `ledger_adjustments` (maker-checker requests for manual adjustments and reversals) | `ledger_adjustments` |
| `ledger_periods` | `ledger_periods` |
| `ledger_invariant_runs` | — |

`ledger_reconciliation_references` is not a ledger table: matches that reference ledger transactions live in
`recon.reconciliation_matches` (ledger stays domain-agnostic).

### 5.7 `fees` (schema `app`)

`fee_schedules`, `fee_schedule_versions`, `fee_schedule_change_requests`.

### 5.8 `psp` (schema `app`)

`payment_providers`, `provider_capabilities` (versioned), `provider_accounts` (configuration, with secret
**references** only), `provider_webhook_inbox`, `provider_health_events`.

`provider_webhook_inbox` replaces the brief's `payment_webhook_inbox`. One endpoint receives payment, refund,
dispute and payout events for a provider, so the inbox sits with `psp`. After verification, `psp` classifies
each event and dispatches it to the owning module's job (`payments` or `payouts`). Neither reads the other's
tables.

### 5.9 `risk` (schema `risk`)

`limits` (versioned limit records, REGULATORY / PROVIDER / INTERNAL_RISK), `limit_change_requests`, `holds`,
`hold_events`, `hold_release_requests` (maker-checker, I-3), `risk_signals`, `risk_assessments`, `monitoring_rules`, `monitoring_alerts`.

`transaction_limits` (brief name) = `risk.limits`.

### 5.10 `organisations` (schema `app`)

`organisations`, `organisation_roles`, `organisation_members`, `organisation_invitations`,
`organisation_verifications` (projection of KYB level/status, fed by `kyc` events; display and filtering only).

### 5.11 `kyc` (schema `kyc`)

| Table | Stage 1 conceptual name | Brief name |
|---|---|---|
| `verification_profiles` | `verification_profiles` | — |
| `profile_events` | `profile_events` | — |
| `kyc_cases` | `verification_attempts` + `reviews` | `kyc_cases` |
| `kyc_checks` | `check_results` | `kyc_checks` |
| `kyc_documents` | document metadata (object in `private-kyc`) | `kyc_documents` |
| `kyc_decisions` | review decisions | `kyc_decisions` |
| `identities` | `identities` (C3, encrypted, blind index) | — |
| `consents` | `consents` | — |
| `kyb_organisations` | `kyc.organisations` | — |
| `kyb_cases` | org verification attempts/reviews | `kyb_cases` |
| `kyb_checks` | `org_check_results` | — |
| `organisation_persons` | `org_persons` (directors, trustees, controllers) | — |
| `beneficial_owners` | BO subset of `org_persons` | `beneficial_owners` |
| `representative_authorities` | `org_representative_authorities` | — |
| `fundraising_authorities` | `fundraising_authorities` | — |
| `beneficiary_evidence` | `beneficiary_evidence` | — |
| `gate_policies` | `kyc_gate_policy` (versioned verification gates) | — |
| `vendor_callback_inbox` | KYC vendor callbacks (verified, deduplicated, append-only raw) | — |

### 5.12 `notifications` (schema `app`)

`notification_templates`, `notification_jobs`, `notification_attempts`, `notification_preferences`,
`notification_suppressions`.

### 5.13 `beneficiaries` (schema `app`)

`beneficiaries`, `beneficiary_relationships`, `beneficiary_verifications`, `institution_payees`.

### 5.14 `compliance` (schema `compliance`)

`compliance_cases`, `compliance_case_events`, `compliance_case_links`, `screening_requests`,
`sanctions_screening_results`, `screening_hits`, `str_reports`, `compliance_restrictions`,
`screening_callback_inbox` (screening-vendor callbacks), `screening_list_sources`, `screening_suppressions`.

Views granted to `fundzim_app` (the only `compliance` objects the app role can read):
`v_payout_blocking_cases` (subject, case id, `blocks_payouts`; no reasons or narrative) for EC-13, and
`v_screening_status` (subject, latest result, screened_at, list version; no hit details) for EC-15.
`v_active_restrictions` (subject, capability, expires_at; no reasons) so `campaigns` and `payments` check
`CREATE_CAMPAIGN` / `DONATE` restrictions **inside** their own transaction, without a gap.

`compliance_restrictions` are **capability** restrictions on a subject (for example `CREATE_CAMPAIGN`,
`DONATE`, `RECEIVE_PAYOUT`), with expiry and maker-checker. Payout-blocking is always expressed as a
`risk.holds` row (type `COMPLIANCE_HOLD`), so that payout eligibility has one place to check (EC-12). A
`RECEIVE_PAYOUT` restriction places that hold in the same transaction.

### 5.15 `campaigns` (schema `app`)

`campaign_categories`, `campaign_review_policies` (versioned), `campaigns`, `campaign_versions`,
`campaign_goals`, `campaign_media`, `campaign_updates`, `campaign_beneficiaries`, `campaign_reviews`,
`campaign_status_history`, `campaign_reports`, `campaign_moderation_actions`.

### 5.16 `payments` (schema `app`)

| Table | Purpose | Brief name |
|---|---|---|
| `donations` | Donor-facing record (campaign, donor or guest, anonymity, message, consent). 1:1 with a payment intent. | — |
| `payment_intents` | Canonical payment (Stage 1 `payments`; `payment_id` = `payment_intents.id`) | `payment_intents` |
| `payment_attempts` | Each outbound provider call (create, status query, cancel), with classification | `payment_attempts` |
| `payment_transactions` | Provider-side transaction record: raw provider status + mapped canonical status | `payment_transactions` |
| `payment_provider_references` | (provider, reference kind, reference) → intent; unique | `payment_provider_references` |
| `payment_events` | Append-only canonical status history | `payment_events` |
| `refund_requests` | Refund request workflow (maker-checker) | `refund_requests` |
| `refund_transactions` | Refund execution against the provider (Stage 1 `refunds`) | `refund_transactions` |
| `payment_disputes` | Dispute cases (Stage 1 `dispute_cases`) | `payment_disputes` |
| `chargebacks` | Involuntary reversals (card chargeback or provider reversal), one per completed loss | `chargebacks` |

### 5.17 `payouts` (schema `app`)

`payout_destinations`, `payout_destination_verifications`, `payout_destination_override_requests` (staff overrides, maker-checker, I-3), `payout_requests` (Stage 1 `payouts`),
`payout_attempts`, `payout_approvals`, `payout_reservations`, `payout_provider_references`, `payout_events`,
`payout_failures`, `payout_reversals`, `payout_eligibility_decisions` (Stage 1 `eligibility_decisions`),
`recovery_cases`, `recovery_case_events`.

Payout destination account numbers are C3. They are stored as application-encrypted ciphertext with a
dedicated key class (`payout-destination`) and an HMAC blind index. They are never in `kyc`, because payout
execution needs them and the `kyc` role boundary would otherwise be crossed on every payout.

### 5.18 `reconciliation` (schema `recon`)

`reconciliation_sources`, `reconciliation_imports`, `reconciliation_runs`, `reconciliation_items`,
`reconciliation_matches`, `reconciliation_discrepancies`, `reconciliation_resolutions`, `settlement_batches`,
`settlement_items`.

## 6. Ownership questions from the Stage 1 handover — resolved

| # | Question | Resolution | Supersedes |
|---|---|---|---|
| 1 | Holds | `risk` owns `risk.holds` + `risk.hold_events` (all hold types). Modules place and release holds through `risk`. | Stage 1 handover suggestion (payouts) and DATABASE §7 (`compliance` schema). Behaviour from [payout-eligibility-and-controls.md §5](../payments/payout-eligibility-and-controls.md) is unchanged. |
| 2 | Evidence records | `audit` owns `audit.evidence_records` + `audit.evidence_holds`. The caller stores the object (KYC objects via `kyc`, other evidence via `storage` in the private bucket's compliance prefix) and passes `storage_ref` + hash. | [refund-and-dispute-architecture.md §1](../payments/refund-and-dispute-architecture.md) (`compliance`) |
| 3 | Limits registry | `risk` owns `risk.limits` (as DATABASE §7 already said). | — |
| 4 | Payout destinations | `payouts` owns them in `app`, encrypted (§5.17). Beneficiary-side verification decisions are in `beneficiaries`; ownership/name-match checks are in `payout_destination_verifications`. | — |

All four are recorded in ADR-021.

## 7. Cross-cutting conventions

- **IDs:** UUIDv7 primary keys generated in Go (`platform/ids`). Campaigns also have `public_code` (10
  characters, Crockford base32, random, unique) used in `/c/{slug}-{public_code}`. Donors receive a
  human-readable `receipt_number` (random, unique, not enumerable).
- **Money:** `amount_minor bigint` + `currency char(3) REFERENCES app.currencies(code)`. Minor units come from
  the registry (`USD` 2, `ZWG` 2 per MONEY §2). Rates and percentages are integer basis points. JSON
  `amount_minor` is a string.
- **Time:** `timestamptz`, UTC. `occurred_at` (business), `recorded_at`/`created_at` (FundZim), `posted_at`
  (ledger).
- **State columns:** `status text` + `CHECK` on allowed values. Critical machines (campaigns, payment intents,
  refunds, disputes, payout requests, compliance cases) also get a transition-guard trigger backed by a
  `*_status_transitions` reference table, so illegal transitions are impossible even in direct SQL.
- **Mutable aggregate + append-only history:** every mutable financial aggregate has a `version` column and
  an append-only `*_events` or `*_history` table written in the same transaction.
- **Append-only tables** use the shared trigger function `app.forbid_mutation()` (BEFORE UPDATE OR DELETE per
  row; BEFORE TRUNCATE per statement), **and** grants.
- **Naming:** DATABASE §10 (`pk_`, `fk_`, `uq_`, `ck_`, `ix_`, `trg_`).
- **Encryption columns:** `*_ciphertext bytea` + `*_key_id text` (+ `*_bidx bytea` where duplicate detection
  is needed).

## 8. State names (authoritative)

| Machine | States | Source |
|---|---|---|
| Payment intent | `CREATED`, `PENDING`, `REQUIRES_ACTION`, `AUTHORISED`, `UNKNOWN`, `SUCCEEDED`, `FAILED`, `EXPIRED`, `CANCELLED`, `PARTIALLY_REFUNDED`, `REFUNDED`, `DISPUTED`, `CHARGED_BACK` | ADR-020, [transaction-lifecycle.md](../payments/transaction-lifecycle.md) |
| Provider transaction (`payment_transactions.mapped_status`) | Same vocabulary, plus `raw_status` kept verbatim | ADR-024 |
| Payout request | `PAYOUT_REQUESTED`, `PENDING_REVIEW`, `APPROVED`, `SUBMITTED`, `PROCESSING`, `UNKNOWN`, `COMPLETED`, `FAILED`, `REJECTED`, `CANCELLED`, `REVERSED` | ADR-020 |
| Refund request | `REQUESTED`, `PENDING_APPROVAL`, `ON_HOLD`, `APPROVED`, `EXECUTING`, `COMPLETED`, `FAILED`, `REJECTED`, `WITHDRAWN` | [refund-and-dispute-architecture.md](../payments/refund-and-dispute-architecture.md) |
| Refund transaction | `CREATED`, `PENDING`, `UNKNOWN`, `SUCCEEDED`, `FAILED` | ADR-024 |
| Dispute | `OPENED`, `EVIDENCE_REQUIRED`, `EVIDENCE_SUBMITTED`, `UNDER_PROVIDER_REVIEW`, `WON`, `LOST`, `ACCEPTED`, `EXPIRED` | Stage 1 |
| Campaign (lifecycle) | `DRAFT`, `SUBMITTED`, `UNDER_REVIEW`, `APPROVED`, `ACTIVE`, `COMPLETED`, `REJECTED`, `SUSPENDED`, `FROZEN`, `CANCELLED` | PRODUCT §6 |
| Campaign visibility | `PUBLIC`, `UNLISTED`, `HIDDEN` (separate column; e.g. `ACTIVE` + `PUBLIC` while a `PAYOUT_HOLD` blocks payouts) | Stage 2 |
| KYC level / status | `UNVERIFIED`, `BASIC_VERIFIED`, `IDENTITY_VERIFIED`, `PAYOUT_VERIFIED` / `ACTIVE`, `PENDING_REVIEW`, `REJECTED`, `SUSPENDED` | ADR-015 |
| KYB level | `ORG_UNVERIFIED`, `ORG_REGISTERED_VERIFIED`, `ORG_KYB_VERIFIED`, `ORG_PAYOUT_VERIFIED` (same status overlay) | Stage 1 |
| Compliance case | `OPEN`, `IN_PROGRESS`, `AWAITING_INFO`, `PENDING_APPROVAL`, `DECIDED`, `CLOSED`, `REOPENED` | Stage 1 |

**On the brief's `PROCESSING` payment state:** it is not added. "Provider acknowledged, outcome not final"
is `PENDING` (or `REQUIRES_ACTION` when the donor must act), as ADR-020 decided. Adding a synonym would
create two names for one meaning. `PROCESSING` exists for payouts only.

**Separation of publication and financial restriction:** campaign `status` and `visibility` describe
publication. Financial restrictions are `risk.holds` rows (payout holds, dispute holds and so on), plus the
ledger move to `campaign_held` when the campaign is `FROZEN`. A campaign can therefore be `ACTIVE` and
`PUBLIC` while its payouts are held.

## 9. Technology choices made in Stage 2

| Concern | Choice | ADR |
|---|---|---|
| Migration tool | **goose** (SQL files, `-- +goose NO TRANSACTION` for `CONCURRENTLY`, embeddable, forward-only in production) | ADR-028 |
| Job queue | **River** (PostgreSQL, `FOR UPDATE SKIP LOCKED`, transactional enqueue with pgx) in schema `queue` | ADR-025 |
| DB driver / queries | **pgx v5** + **sqlc** (hand-written SQL, generated typed code) | ADR-031 |
| HTTP router | Go standard library `net/http.ServeMux` (method + path patterns), no framework | ADR-031 |
| API contract | OpenAPI 3.1, contract-first; Go server types generated or hand-checked against it in Stage 3 | ADR-026 |
| Auth | Opaque server-side sessions (Stage 0), OTP primary factor for users, WebAuthn/TOTP for staff | ADR-027 |

Exact library versions are pinned in Stage 3, not here.

## 10. Draft SQL artifacts

`design/sql/` holds **non-executable design drafts**: they are not migrations, are never run against a real
database, and goose does not read them. They are validated only in an in-memory PGlite instance
(`design/sql/validate`). Load order:

| File | Content |
|---|---|
| `0001_foundation.sql` | Roles (NOLOGIN placeholders), schemas, shared trigger functions (`app.forbid_mutation`, `app.set_updated_at`, `app.guard_transition`) |
| `0002_platform.sql` | currencies, markets, idempotency, outbox, inbox, feature flags |
| `0003_audit.sql` | audit, security audit, evidence |
| `0004_users_auth.sql` | users + auth tables |
| `0005_storage.sql` | storage |
| `0006_ledger.sql` | ledger |
| `0007_fees_psp.sql` | fees, psp |
| `0008_risk.sql` | risk |
| `0009_organisations.sql` | organisations |
| `0010_kyc.sql` | kyc |
| `0011_notifications.sql` | notifications |
| `0012_beneficiaries.sql` | beneficiaries |
| `0013_compliance.sql` | compliance |
| `0014_campaigns.sql` | campaigns |
| `0015_payments.sql` | payments |
| `0016_payouts.sql` | payouts |
| `0017_reconciliation.sql` | reconciliation |
| `0018_grants.sql` | grants, default privileges, RLS policies |
| `tests/*.sql` | Invariant tests (`-- @case <name> expect=ok|error:<text>`) |

Cross-schema foreign keys: allowed from `kyc`, `risk`, `compliance`, `recon` and `ledger` to `app` reference
tables, and from `recon` to `ledger` (matches and resolutions reference the journals they confirm or create) (for example `app.users`, `app.currencies`). **Never** from `app` into `kyc`. `ledger` references only
`app.currencies` (it stays domain-agnostic: owners are `(owner_type, owner_id)` without a foreign key).

## 11. ADR numbers (Stage 2)

| ADR | Title |
|---|---|
| ADR-021 | Domain module boundaries, dependency graph and table ownership |
| ADR-022 | Database schema organisation and roles |
| ADR-023 | Ledger posting architecture |
| ADR-024 | Payment intent vs provider transaction; database-guarded state machines |
| ADR-025 | Transactional outbox, inbox and PostgreSQL job queue (River) |
| ADR-026 | API versioning and contract-first OpenAPI |
| ADR-027 | Authentication and session strategy |
| ADR-028 | Database migration strategy (goose, forward-only) |
| ADR-029 | Provider capability abstraction (`psp` module) |
| ADR-030 | Financial reconciliation architecture |
| ADR-031 | Data access and HTTP stack (pgx + sqlc, stdlib router) |

## 12. Decisions taken during Stage 2 integration

| # | Question | Decision |
|---|---|---|
| I-1 | Organisation financial role | MVP organisation roles are `ORG_ADMIN` and `ORG_MEMBER` only. Organisation financial actions (payout request, destination change) are `ORG_ADMIN` only. An `ORG_FINANCE` role can be added later as data (`organisation_roles`) without schema change. |
| I-2 | Guest donors | A guest checkout gets a **donation access token** (random, shown once, stored as SHA-256 in `app.donations.access_token_hash`). It grants read-only access to that one donation and receipt, and lets the donor request a refund. There is no guest session. |
| I-3 | Homes for pending maker-checker objects | Per-module request tables (no generic table): `risk.hold_release_requests`; `app.payout_destination_override_requests` (payouts); dispute acceptance via `app.payment_disputes` maker/checker columns (`accept_requested_by`, `accept_approved_by` CHECK ≠); kill-switch and feature-flag changes via `app.feature_flag_changes` (`requested_by`, `approved_by` CHECK ≠ for flags marked `requires_approval`). The shared code lives in `platform/makerchecker`. |
| I-4 | "Business owner" approver | New staff role **`BUSINESS_APPROVER`**. It holds approval-only permissions: fee schedule changes, write-offs above threshold, campaign review policy changes and limit changes owned by the business. No operational permissions. |
| I-5 | Who sees KYC status | [identity-data-protection.md](../security/identity-data-protection.md) wins over SECURITY §5.2 (the more specific Stage 1 rule). ADMIN and SUPER_ADMIN do not see KYC status by default. |
| I-6 | Staff first factor | Password (Argon2id) **plus** a mandatory phishing-resistant second factor (WebAuthn, or TOTP as fallback). No email or SMS OTP for staff. |
| I-7 | Duplicate payment while a previous one is `UNKNOWN` | Adopted: creating a new payment for the same donor, campaign and amount while one is `UNKNOWN` returns `409 PAYMENT_OUTCOME_UNKNOWN` and tells the donor not to pay again. The donor can override this explicitly after a configurable delay (INTERNAL_RISK limit). |
| I-8 | Guest retries (no session to scope idempotency keys) | Accepted as designed in [api-design.md](../api/api-design.md) §18. Guest idempotency keys are scoped to the key plus the exact request hash. A replay re-issues the donation access token and revokes the previous one. |
| I-9 | Remaining maker-checker homes | Campaign unfreeze and cancel-from-FROZEN → `campaign_moderation_actions` (maker and checker columns). Write-off → `recovery_cases` state `WRITE_OFF_PENDING`. KYC/KYB second approval → `kyc_decisions`. Compliance decision proposals → `compliance_case_events` with case state `PENDING_APPROVAL`. |
| I-10 | `BUSINESS_APPROVER` separation of duties | It cannot be combined with FINANCE, COMPLIANCE or SUPER_ADMIN for the same person. The role-grant workflow rejects the combination (enforced in Stage 4). |
| I-11 | Refund API shape | The donor path is keyed by `donation_id` (the donor-facing identifier). Withdrawal is `POST …/withdraw` (a state change, not a deletion). This refines Stage 1 refund-and-dispute-architecture §7. |
| I-12 | New permissions and audit actions introduced by the API design | Accepted as the Stage 4 seed input. The permission catalogue in [authorization-matrix.md](../api/authorization-matrix.md) and the action names in [api-design.md](../api/api-design.md) are added to the closed catalogues in Stage 4. |
| I-13 | Writes to `audit` and the outbox from the `kyc` and `compliance` pools | Through `SECURITY DEFINER` functions (`audit.append_event`, `audit.append_security_event`, `app.enqueue_outbox`) owned by `fundzim_migrator`, with a fixed `search_path`. Those roles get no direct privileges on `audit.*` or `app.outbox_events`. |
| I-14 | `compliance` placing holds in its own transaction | `risk` module code runs on the caller's compliance-pool transaction. The compliance role has minimal privileges on `risk.holds`, `risk.hold_events` and `risk.monitoring_alerts` (INSERT/SELECT plus the set-once release UPDATE). Ownership stays with the `risk` code. |
| I-15 | Worker-only routines | New role **`fundzim_worker`** for the worker pool: `fundzim_app` privileges plus EXECUTE on worker-only routines (for example `ledger.fold_deferred_balances`). The API pool cannot run them. |
| I-16 | Staged foreign keys (review H1) | The SQL drafts show the **end state**. When a column or FK targets a table created in a later stage, the earlier migration creates the column **nullable without the FK**. The later stage adds the FK as `NOT VALID`, validates it, and then sets `NOT NULL` where the end state requires it. Pre-Stage-20 data is non-production only (Gates A–C), so no production backfill is involved. The list is in [migration-plan.md](../database/migration-plan.md). |
| I-17 | Settlement tables before payouts (review H2) | `recon.reconciliation_sources`, `reconciliation_imports`, `reconciliation_items`, `reconciliation_matches`, `settlement_batches` and `settlement_items` are created in **Stage 10** (ADR-030). Discrepancies, resolutions, runs and the full matching scope follow in Stage 17. |
| I-18 | Stage 3 table scope (review H3) | Stage 3 creates only the foundation: 0001 (roles, schemas, shared functions), the platform tables (`currencies`, `markets`, `idempotency_keys`, `outbox_events`, `inbox_events`, `feature_flags`, `feature_flag_changes`, with actor columns as `uuid` without FK until Stage 4), `audit.audit_events`, `audit.security_audit_events`, the River schema and grants. Users and auth come in Stage 4; storage and evidence records in Stage 5. |
| I-19 | Private-bucket credentials (review H4) | **Three** object-storage credentials: `public-media` (whole public bucket); `private-kyc` scoped to the `kyc/` prefix and used only by `kyc`; `private-evidence` scoped to the `compliance/` and `reports/` prefixes of the private bucket and used only by `storage` for evidence and statement files. Neither private credential can read the other's prefix. |
| I-20 | Who approves payouts (review H5) | Only `FINANCE` holders of `payout.approve` (ADR-017, operational-controls §3). `DUAL` means two **distinct** FINANCE approvers, neither of them the requester. COMPLIANCE never approves payouts. |
| I-21 | `FAILED` sources (review M1) | Database CHECKs restrict `→ FAILED` to authoritative sources: payments `WEBHOOK`/`POLL`/`RECON`/`SYNC`, plus `INTERNAL` only from `CREATED` (pre-submit rejection), plus `STAFF` only with a provider-evidence record (Stage 1 transaction-lifecycle §7.3 step 6, maker-checker); payouts `SYNC`/`WEBHOOK`/`POLL`/`RECON`/`STAFF`. `SYSTEM` (for example a timeout handler) can never write `FAILED`. |
| I-22 | Synchronous payout completion (review M3) | Accepted. An authoritative synchronous provider response may complete a payout (`SYNC` source). This refines Stage 1 Y10 and is recorded in ADR-024. |
| I-23 | Permission names (review M7) | `recovery.write_off.approve` (not `ledger.write_off.approve`) and a separate `security_audit.read` for the security audit chain. |
| I-24 | Bulk refunds (review M8) | `POST /api/v1/admin/refund-requests/bulk`. Maker COMPLIANCE (`refund.bulk.request`), checker FINANCE (`refund.approve`), plus `BUSINESS_APPROVER` (`refund.bulk.approve`) when the batch total exceeds a configured limit. The manifest hash is recorded, and each refund stays individually idempotent. |
| I-25 | Screening vendor callbacks (review M9) | `POST /api/v1/webhooks/screening/{vendor}` → `compliance.screening_callback_inbox` (catalogue entry WHK-03). |
