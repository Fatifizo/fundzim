# Migration Plan (Stage 3 onward)

**Stage 2 — design only.** This plan derives the executable goose migrations from the Stage 2 drafts in
[`design/sql/`](../../design/sql/) and maps each to the [ROADMAP.md](../ROADMAP.md) stage that first needs it.
Mechanics (naming, transactions, locking, approval) are in [migration-strategy.md](migration-strategy.md).
Nothing in this plan authorises starting a stage; each stage still starts only when the user asks.

Conventions: names below omit the `YYYYMMDDHHMMSS_` prefix, which is assigned when the file is written. Within a
stage the order is the order listed. Each migration creates the table(s) **with** their constraints, indexes,
triggers, seed rows, `app.status_transitions` edges and grants from `0018_grants.sql`.

Rules for the plan:

1. A table is created in the earliest stage whose code writes it, never earlier "to be ready".
2. **Staged FKs (baseline I-16).** The drafts show the end state. When a column or FK targets a table created
   in a later stage, the earlier migration creates the column **nullable and without the FK**. The target
   stage adds the FK `NOT VALID`, runs `VALIDATE CONSTRAINT`, and then `SET NOT NULL` where the end state
   requires it (after a validated `CHECK (col IS NOT NULL)`). Data before Stage 20 is non-production only
   (Gates A–C), so no production backfill is involved. The complete list is §"Staged FKs and columns" below.
3. Second-reviewer migrations (migration-strategy §9) are marked **(2R)**.

---

## Stage 3 — Core Platform Foundation

Scope exactly as baseline I-18. Source: `0001_foundation.sql`, `0002_platform.sql`, `0003_audit.sql` (the two
chains only), grants from `0018_grants.sql`.

| # | Migration | Tables / objects | Notes |
|---|---|---|---|
| 3.1 | `create_roles_and_schemas` **(2R)** | roles `fundzim_migrator`, `fundzim_app`, `fundzim_worker` (I-15: app privileges + EXECUTE on worker-only routines), `fundzim_kyc`, `fundzim_compliance`, `fundzim_readonly` (LOGIN and secrets by infrastructure); schemas `app`, `queue`, `ledger`, `audit`, `kyc`, `risk`, `compliance`, `recon`; `REVOKE ALL ON SCHEMA public FROM PUBLIC`; default privileges | Role creation may live in IaC; the migration asserts they exist and applies the Stage 3 grants |
| 3.2 | `create_shared_functions` **(2R)** | `app.forbid_mutation`, `set_updated_at`, `keep_created_at`, `guard_transition`, `forbid_column_change`, `allow_only_column_changes`, `set_once_columns`, `bump_version`; `app.status_transitions` | Guard functions are security controls |
| 3.3 | `create_platform_currencies_markets` | `app.currencies` (+ guard; seed USD, ZWG unverified), `app.markets` (seed ZW) | |
| 3.4 | `create_platform_idempotency_keys` | `app.idempotency_keys` | |
| 3.5 | `create_platform_outbox_inbox` | `app.outbox_events`, `app.inbox_events` | |
| 3.6 | `create_feature_flags` | `app.feature_flags`, `app.feature_flag_changes` (seed `campaign.individual_for_others.enabled = false`). `requested_by`/`approved_by` are plain `uuid` (no FK); `approved_by` FK added in Stage 4 | Kill switches as data |
| 3.7 | `river_migrations_vX` | `queue.*` | River migrator at the pinned version |
| 3.8 | `create_audit_events` **(2R)** | `audit.chain_append`, `audit.verify_chain`, `audit.audit_events`, `audit.security_audit_events`; INSERT/SELECT grants | Stage 3 acceptance: rejects UPDATE/DELETE/TRUNCATE as the app role |

No users, auth, storage or evidence tables in Stage 3 (audit actor ids are plain uuids, so system actors work).

## Stage 4 — Authentication & Identity

Source: `0004_users_auth.sql`, `0011_notifications.sql` (subset).

| # | Migration | Tables |
|---|---|---|
| 4.0 | `create_users` | `app.users` (+ `user_account` edges), `app.user_profiles` (avatar column without FK); FK `feature_flag_changes.approved_by` added (NOT VALID + VALIDATE) |
| 4.1 | `create_user_contacts` | `app.user_emails`, `app.user_phone_numbers` |
| 4.2 | `create_auth_credentials` | `app.mfa_methods`, `app.authentication_identities`, `app.password_credentials`, `app.recovery_codes` |
| 4.3 | `create_auth_otp_sessions` | `app.otp_challenges`, `app.sessions` |
| 4.4 | `create_rbac` **(2R)** | `app.roles`, `app.permissions`, `app.role_permissions` (seeds incl. BUSINESS_APPROVER), `app.role_assignment_requests` (+ edges), `app.role_assignments` |
| 4.5 | `bootstrap_super_admins` **(2R)** | One-off ceremony migration (two named people, maker ≠ checker), recorded in `security_audit_events` |
| 4.6 | `create_security_events` | `app.security_events` (partitioned monthly from the start) |
| 4.7 | `create_notifications_core` | `app.notification_templates`, `app.notification_jobs` (+ edges), `app.notification_attempts`; seed OTP/security templates | Needed because `auth` delivers OTPs through `notifications` (fakes only until Stage 15) |

## Stage 5 — KYC & Verification

Source: `0005_storage.sql`, `0003_audit.sql` (evidence), `0009_organisations.sql`, `0010_kyc.sql`,
`0013_compliance.sql` (screening subset).

| # | Migration | Tables |
|---|---|---|
| 5.1 | `create_storage` | `app.stored_objects`, `app.upload_sessions`; staged FK `user_profiles.avatar_object_id` added |
| 5.2 | `create_evidence` **(2R)** | `audit.evidence_records`, `audit.evidence_holds` (+ legal-hold triggers; UPDATE grant on `legal_hold` only) |
| 5.3 | `create_organisations` | `app.organisations`, `app.organisation_roles` (ORG_ADMIN, ORG_MEMBER), `app.organisation_members`, `app.organisation_invitations`, `app.organisation_verifications` |
| 5.4 | `create_kyc_schema_tables` **(2R)** | `kyc.verification_profiles`, `profile_events`, `kyc_cases`, `kyc_checks`, `kyc_documents`, `kyc_decisions`, `identities`, `consents`, `gate_policies`, `vendor_callback_inbox` |
| 5.5 | `create_kyb_tables` **(2R)** | `kyc.kyb_organisations`, `kyb_cases`, `kyb_checks`, `organisation_persons`, `beneficial_owners`, `representative_authorities`, `fundraising_authorities` |
| 5.6 | `create_screening` **(2R)** | `compliance.screening_list_sources`, `screening_requests`, `sanctions_screening_results`, `screening_hits`, `screening_callback_inbox` (sanctions screening hook) |

`kyc.beneficiary_evidence` is created in Stage 6 (5.x has no beneficiaries yet).

## Stage 6 — Campaign Engine

Source: `0012_beneficiaries.sql`, `0014_campaigns.sql`, `0008_risk.sql` (holds subset), `0004` (conflict
declarations), `0010` (beneficiary evidence).

| # | Migration | Tables |
|---|---|---|
| 6.1 | `create_beneficiaries` | `app.institution_payees`, `app.beneficiaries` (+ edges), `app.beneficiary_relationships`, `app.beneficiary_verifications` (+ latest-decision FK) |
| 6.2 | `create_kyc_beneficiary_evidence` **(2R)** | `kyc.beneficiary_evidence` |
| 6.3 | `create_campaign_reference` | `app.campaign_categories` (seed), `app.campaign_review_policies` |
| 6.4 | `create_campaigns` | `app.campaigns` (+ 23 `campaign` edges, lifecycle and history triggers), `app.campaign_versions`, `app.campaign_goals`, `app.campaign_beneficiaries`, `app.campaign_status_history` |
| 6.5 | `create_campaign_review_and_moderation` | `app.campaign_reviews`, `app.campaign_reports`, `app.campaign_moderation_actions` (+ unfreeze maker-checker trigger and `campaign_status_history.moderation_action_id`) |
| 6.6 | `create_campaign_media_updates` | `app.campaign_media`, `app.campaign_updates` |
| 6.7 | `create_staff_conflict_declarations` | `app.staff_conflict_declarations` (reviewer conflict checks on claim) |
| 6.8 | `create_risk_holds` **(2R)** | `risk.holds`, `risk.hold_events`, `risk.hold_release_requests` (freeze/suspend place holds; the ledger move joins in Stage 10). `holds.alert_id` is created without its FK; the FK to `risk.monitoring_alerts` is added `NOT VALID` + validated in Stage 13 |

## Stage 7 — Public Fundraising Experience

No new tables (campaign reports already exist). Read models for public pages, if any, are views.

## Stage 8 — Payment Abstraction Layer

Source: `0007_fees_psp.sql` (psp), `0015_payments.sql`.

| # | Migration | Tables |
|---|---|---|
| 8.1 | `create_psp_registry` **(2R)** | `app.payment_providers`, `app.provider_capabilities`, `app.provider_accounts`, `app.provider_health_events` |
| 8.2 | `create_provider_webhook_inbox` **(2R)** | `app.provider_webhook_inbox` |
| 8.3 | `create_payments` **(2R)** | `app.donations`, `app.payment_intents` (+ edges), `app.payment_attempts`, `app.payment_transactions`, `app.payment_provider_references`, `app.payment_events` |
| 8.4 | `create_refunds_disputes` **(2R)** | `app.refund_requests`, `app.refund_transactions`, `app.payment_disputes`, `app.chargebacks` |

## Stage 9 — Zimbabwe Payment Integrations

Reference-data migrations only: provider rows and capability versions per contracted sandbox provider
**(2R)**. No new tables.

## Stage 10 — Double-Entry Financial Ledger

Source: `0006_ledger.sql`. All **(2R)**.

| # | Migration | Tables |
|---|---|---|
| 10.1 | `create_ledger_reference` | `ledger.ledger_posting_rules`, `ledger.ledger_posting_rule_lines` (seed), `ledger.ledger_periods` |
| 10.2 | `create_ledger_core` | `ledger.ledger_accounts`, `ledger.ledger_transactions`, `ledger.ledger_entries` (balance trigger), `ledger.ledger_balances`, `ledger.ledger_posting_batches` |
| 10.3 | `create_ledger_controls` | `ledger.ledger_adjustments`, `ledger.ledger_invariant_runs` |
| 10.4 | `create_settlement_tables` | `recon.reconciliation_sources`, `recon.reconciliation_imports`, `recon.reconciliation_items`, `recon.reconciliation_matches` (`run_id` nullable, no FK until Stage 17), `recon.settlement_batches`, `recon.settlement_items` (baseline I-17, ADR-030: settlement matching must exist before payouts) |
| 10.5 | `add_payment_ledger_fks` | Staged FKs from payments to `ledger.ledger_transactions` (see list) |

## Stage 11 — Withdrawals & Payouts

Source: `0016_payouts.sql`, `0008_risk.sql` (limits).

| # | Migration | Tables |
|---|---|---|
| 11.1 | `create_risk_limits` **(2R)** | `risk.limits`, `risk.limit_change_requests` (proposed regulatory limits seeded as PROPOSED, never active) |
| 11.2 | `create_payout_destinations` **(2R)** | `app.payout_destinations`, `app.payout_destination_verifications`, `app.payout_destination_override_requests` (baseline I-3) |
| 11.3 | `create_payouts` **(2R)** | `app.payout_requests`, `app.payout_eligibility_decisions`, `app.payout_approvals`, `app.payout_reservations`, `app.payout_attempts`, `app.payout_provider_references`, `app.payout_events`, `app.payout_failures`, `app.payout_reversals` |
| 11.4 | `create_recovery_cases` **(2R)** | `app.recovery_cases`, `app.recovery_case_events` |

## Stage 12 — Fees & Platform Revenue

Source: `0007_fees_psp.sql` (fees): `app.fee_schedules`, `app.fee_schedule_versions`,
`app.fee_schedule_change_requests` **(2R)** (BUSINESS_APPROVER approves); staged FK
`payment_intents.fee_schedule_version_id` added and set `NOT NULL`. Until then fee previews read a config
default and post nothing.

## Stage 13 — Trust, Fraud & Risk Engine

Source: `0008_risk.sql`, `0013_compliance.sql`.

| # | Migration | Tables |
|---|---|---|
| 13.1 | `create_risk_signals` | `risk.risk_signals`, `risk.risk_assessments`, `risk.monitoring_rules`, `risk.monitoring_alerts` |
| 13.2 | `create_compliance_cases` **(2R)** | `compliance.compliance_cases`, `compliance_case_events`, `compliance_case_links`, `compliance_restrictions`, `screening_suppressions` (references cases); FK `risk.holds.alert_id` added |
| 13.3 | `create_str_reports` **(2R)** | `compliance.str_reports` + RLS policies (LR-008) |

## Stage 14 — Administration & Operations Portal

| # | Migration | Tables |
|---|---|---|
| 14.1 | `create_break_glass_grants` **(2R)** | `app.break_glass_grants` |

## Stage 15 — Notifications & Communications

| # | Migration | Tables |
|---|---|---|
| 15.1 | `create_notification_preferences_suppressions` | `app.notification_preferences`, `app.notification_suppressions` (+ FK `notification_jobs.suppression_id`) |

## Stage 16 — Discovery, Sharing & WhatsApp Optimisation

Indexes for search/listing (`CREATE INDEX CONCURRENTLY`, `NO TRANSACTION`), referral attribution tables if
designed then (not in the Stage 2 catalogue).

## Stage 17 — Financial Reconciliation & Reporting

Source: `0017_reconciliation.sql`. All **(2R)**: `recon.reconciliation_runs`, `recon.reconciliation_discrepancies`,
`recon.reconciliation_resolutions` and the full matching scope (baseline I-17); staged FK
`reconciliation_matches.run_id` added and set `NOT NULL`. Retention jobs (evidence purge, partition drops)
become executable once LR-012 periods are approved.

## Stage 18 — Security Hardening

Partitioning of `audit.audit_events` if volume requires (chain per partition with daily anchors — a new
`chain_version`), audit anchor export, key-rotation re-index migrations (blind indexes).

---

## Staged FKs and columns (baseline I-16)

Produced by scanning every FK in the loaded drafts (`pg_constraint`) against the stage map above; these are all
FKs whose target table is created in a later stage than the referencing table.

| Column(s) | Table (created) | Target (created) | End state | Earlier stage | Target stage |
|---|---|---|---|---|---|
| `approved_by` | `app.feature_flag_changes` (3) | `app.users` (4) | nullable | `uuid`, no FK | FK NOT VALID + VALIDATE |
| `avatar_object_id`, `avatar_bucket_class` | `app.user_profiles` (4) | `app.stored_objects` (5) | nullable | columns, no FK | FK NOT VALID + VALIDATE |
| `suppression_id` | `app.notification_jobs` (4) | `app.notification_suppressions` (15) | nullable | column, no FK | FK NOT VALID + VALIDATE |
| `alert_id` | `risk.holds` (6) | `risk.monitoring_alerts` (13) | nullable | column, no FK | FK NOT VALID + VALIDATE |
| `fee_schedule_version_id` | `app.payment_intents` (8) | `app.fee_schedule_versions` (12) | **NOT NULL** | nullable, no FK | FK NOT VALID + VALIDATE, then `SET NOT NULL` |
| `ledger_reservation_txn_id`, `currency` | `app.refund_transactions` (8) | `ledger.ledger_transactions` (10) | **NOT NULL** | nullable, no FK | FK + VALIDATE, then `SET NOT NULL` |
| `ledger_settlement_txn_id`, `currency` | `app.refund_transactions` (8) | `ledger.ledger_transactions` (10) | nullable | no FK | FK NOT VALID + VALIDATE |
| `ledger_release_txn_id`, `currency` | `app.refund_transactions` (8) | `ledger.ledger_transactions` (10) | nullable | no FK | FK NOT VALID + VALIDATE |
| `ledger_transaction_id`, `currency` | `app.chargebacks` (8) | `ledger.ledger_transactions` (10) | nullable | no FK | FK NOT VALID + VALIDATE |
| `run_id` | `recon.reconciliation_matches` (10) | `recon.reconciliation_runs` (17) | **NOT NULL** | nullable, no FK | FK + VALIDATE, then `SET NOT NULL` |

Columns whose end state is NOT NULL are written by their stage's code as soon as the target exists; before that,
rows exist only in non-production environments. A CI check re-runs this scan against the migrations, so a new
cross-stage FK cannot be added without an entry here.

## Cross-check: every Stage 2 draft table has a home

| Draft | Stages |
|---|---|
| 0001 | 3 |
| 0002 | 3 |
| 0003 | 3 (audit chains), 5 (evidence) |
| 0004 | 4 (users, profiles, contacts, auth, RBAC, security events), 6 (conflict declarations), 14 (break-glass) |
| 0005 | 5 |
| 0006 | 10 |
| 0007 | 8 (psp), 12 (fees) |
| 0008 | 6 (holds, hold events, hold release requests), 11 (limits), 13 (signals, monitoring) |
| 0009 | 5 |
| 0010 | 5, 6 (beneficiary evidence) |
| 0011 | 4 (templates, jobs, attempts), 15 (preferences, suppressions) |
| 0012 | 6 |
| 0013 | 5 (list sources, screening), 13 (cases, restrictions, suppressions, STR) |
| 0014 | 6 |
| 0015 | 8 |
| 0016 | 11 |
| 0017 | 10 (sources, imports, items, matches, settlement batches/items), 17 (runs, discrepancies, resolutions) |
| 0018 | Every stage (grants travel with tables) |
