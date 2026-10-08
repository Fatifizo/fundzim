# Database Design Principles

**Stage 2 — design only.** These principles turn [DATABASE.md](../DATABASE.md) (Stage 0 standards) into the
concrete patterns used by every SQL draft in [`design/sql/`](../../design/sql/). The drafts are
**non-executable**: they are validated only in an in-memory PGlite (`design/sql/validate`). Executable
migrations start in Stage 3 under `/migrations` (goose, [ADR-028](../adr/ADR-028-migration-strategy.md)).

Contract: [design-baseline.md](../stage-2/design-baseline.md) fixes schemas, table names, owners, state names and
file numbering. Where this document and the baseline differ, the baseline wins.

Related: [schema-overview.md](schema-overview.md), [migration-strategy.md](migration-strategy.md),
[MONEY.md](../MONEY.md), [DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md), [PRIVACY.md](../PRIVACY.md),
[ADR-022](../adr/ADR-022-database-schema-organisation.md), [ADR-024](../adr/ADR-024-payment-intent-and-db-guarded-state-machines.md).

---

## 1. Namespace strategy

Schemas follow **control and sensitivity boundaries**, not modules (baseline §4, ADR-022):

| Schema | Holds | Runtime role |
|---|---|---|
| `app` | Core domain tables of all modules not listed below, platform tables | `fundzim_app` |
| `queue` | River job tables (library-managed, pinned version) | `fundzim_app` |
| `ledger` | Ledger tables | `fundzim_app`, restricted grants |
| `audit` | Audit, security audit, evidence records/holds | `fundzim_app` INSERT/SELECT (+ UPDATE of `evidence_records.legal_hold`) |
| `kyc` | KYC/KYB tables with C3 data | `fundzim_kyc` only |
| `risk` | Limits, holds, signals, monitoring | `fundzim_app` |
| `compliance` | Cases, screening, STR preparation, restrictions | `fundzim_compliance` (+ RLS for STR) |
| `recon` | Reconciliation and settlement | `fundzim_app` |

- `public` is revoked from `PUBLIC` and unused.
- Inside `app`, **module ownership** is enforced by tests (each module's sqlc query files may reference only
  its own tables), not by roles. Every table has exactly one owner (baseline §5).
- **Cross-schema foreign keys** run only from `kyc`, `risk`, `compliance`, `recon` and `ledger` into `app`
  reference tables. **Never `app` → `kyc`.** Where `app` must point at a `kyc` row (for example
  `beneficiaries.kyc_person_ref`, `campaigns.fundraising_authority_ref`), the column is an opaque `uuid` with
  **no FK**, named `*_ref`, and the `kyc` module validates it through its service interface.
- `audit` tables carry actor and target ids **without** FKs to `app.users`, so a system/provider actor can be
  recorded and audit rows never block anonymisation.

## 2. Identifiers

- Every PK is `id uuid`, a **UUIDv7 generated in Go** (`platform/ids`). No DB default: an id generated
  before INSERT makes the write idempotent and lets one transaction reference rows it is about to create.
- **Reference-data seeds** (roles, permissions, categories, org roles, seeded flags) use deterministic
  `md5('<kind>:<code>')::uuid` ids so every environment has the same ids. They are not v7; that is
  acceptable for a few hundred static rows, and their creation time is not sensitive.
- Public identifiers are separate columns with their own unique constraints: `campaigns.public_code`
  (10 chars Crockford base32, `CHECK (public_code ~ '^[0-9A-HJKMNP-TV-Z]{10}$')`), `slug` (not unique; the URL
  is `/c/{slug}-{public_code}`).
- **IDs are never authorisation.** Every access is checked server-side (IDOR defence, SECURITY §5.4).

## 3. Money and minor units

```sql
amount_minor bigint  NOT NULL CHECK (amount_minor > 0),
currency     char(3) NOT NULL REFERENCES app.currencies (code),
```

- Always the pair. No `numeric`, `real`, `double precision` or `money` column anywhere in a base table. The
  test `no_float_numeric_or_money_type_columns_anywhere` (core_platform_test.sql) scans the whole catalogue and
  fails on any such column, and `every_amount_column_has_a_currency_column` fails on an orphan amount.
- Every amount has a sign `CHECK`. Signed amounts only where documented (ledger uses direction + positive
  amount).
- Minor units come from the registry `app.currencies` (MONEY §2): `USD` 840/2 `US$`, `ZWG` 924/2 `ZiG`.
  `minor_units` is frozen once `minor_units_verified = true` (trigger `app.currencies_guard`). ZWG is seeded
  `minor_units_verified = false` (LR-043, PCR-018) and so is sandbox-only.
- Rates and percentages are integer **basis points** (`*_bp integer`).
- PostgreSQL silently *rounds* a numeric literal assigned to a `bigint` column (`INSERT … VALUES (10.5)` stores
  11). The defence is in layers: Go uses `int64` only (lint bans floats in money packages), JSON
  `amount_minor` is a string of digits, pgx binds `int64`, and the DB rejects decimal strings
  (`'5000.50'` → `invalid input syntax for type bigint`, test `decimal_goal_amount_rejected`).
- **Aggregates always `GROUP BY currency`.** Views may expose `SUM(bigint)` (typed `numeric`, still exact);
  base tables never store floats.
- **No stored balances outside the ledger.** Campaign "raised" figures are derived per currency from ledger
  postings (test `no_raised_or_balance_column_on_campaign_tables`).

## 4. Time

- `timestamptz` only, UTC sessions. Business time is `occurred_at` (from the injected clock); write time is
  `created_at`/`recorded_at` (`DEFAULT now()`); ledger time is `posted_at`. They are never overwritten.
- `created_at` is immutable (`app.keep_created_at()` or a column guard); `updated_at` exists only on mutable
  tables and is maintained by `app.set_updated_at()`.
- Calendar concepts (campaign end date in Harare) are stored as an instant plus the zone used
  (`ends_at`, `ends_at_time_zone`).
- Expiry CHECKs that are fixed security rules are constraints (OTP TTL ≤ 5 minutes, break-glass ≤ 1 hour).
  Tunable values (session timeouts, upload size maxima) are configuration, not CHECKs.

## 5. Constraints

- **NOT NULL by default**; every nullable column has a stated reason.
- **Enumerations** are `text` + `CHECK (col IN (…))`, `UPPER_SNAKE` values matching the API. Exception: the
  audit vocabulary (`actor_type`, `outcome`) keeps AUDIT.md's lower-case values.
- **Cross-column CHECKs** express the business rule locally, for example
  `CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL AND response_status_code IS NOT NULL))`.
- **Partial unique indexes** express "at most one active X": one verified owner per phone number
  (`uq_user_phone_numbers_verified_e164 … WHERE verified_at IS NOT NULL AND deleted_at IS NULL`), one live OTP
  per destination and purpose, one primary beneficiary per campaign, one open review per campaign.
- **Kind-restricted composite FKs.** To force "this user must be a STAFF account" without a trigger, the child
  carries a constant column and references a composite unique key:

  ```sql
  user_account_kind text NOT NULL DEFAULT 'STAFF' CHECK (user_account_kind = 'STAFF'),
  FOREIGN KEY (user_id, user_account_kind) REFERENCES app.users (id, account_kind)
  ```

  The same pattern pins `stored_objects.bucket_class = 'PUBLIC_MEDIA'` for campaign media and avatars, ties a
  role assignment to the exact approved request (`(request_id, user_id, role_id, granted_by, approved_by)`),
  and ties an approved campaign version to its campaign.
- **Maker-checker** is a CHECK wherever both actors are on the row: `approved_by <> requested_by`
  (`role_assignment_requests`, `role_assignments`, `evidence_holds`, `feature_flag_changes`,
  `break_glass_grants`, `campaign_review_policies`, `notification_templates`, unfreeze rows of
  `campaign_status_history`).
- **Deferred constraint triggers** check invariants that span rows at COMMIT:
  every campaign status change has a history row; every flag version has a change row; an active organisation
  keeps at least one active `ORG_ADMIN`.
- Naming (DATABASE §10): `pk_<table>`, `fk_<table>_<col>`, `uq_<table>_<cols>`, `ck_<table>_<rule>`,
  `ix_<table>_<cols>`, `trg_<table>_<purpose>`. Tests assert on these names, so they are stable API.

## 6. Mutability patterns

| Pattern | Mechanism | Examples |
|---|---|---|
| **Append-only** | `app.forbid_mutation()` BEFORE UPDATE OR DELETE (row) and BEFORE TRUNCATE (statement) **and** INSERT/SELECT grants | `audit_events`, `security_audit_events`, `evidence_holds`, `campaign_status_history`, `campaign_versions`, `campaign_goals`, `beneficiary_verifications`, `campaign_moderation_actions`, `notification_attempts`, `security_events`, `feature_flag_changes` |
| **Append-only except bookkeeping** | `app.allow_only_column_changes('col', …)` (all other columns frozen) | `outbox_events` (dispatch columns), `evidence_records` (`legal_hold` only, with its own trigger), `password_credentials`, `recovery_codes`, `role_assignments` (revocation), `beneficiary_relationships` (supersession) |
| **Set-once columns** | `app.set_once_columns('col', …)` (NULL → value, then frozen) | `break_glass_grants` revocation, `staff_conflict_declarations` withdrawal |
| **Immutable identity on a mutable row** | `app.forbid_column_change('col', …)` | `users.account_kind`, `campaigns.owner_*`, `sessions.token_hash/absolute_expires_at`, `stored_objects.content_sha256` |
| **Mutable aggregate + append-only history** | `version integer` bumped by `app.bump_version()`; state guarded; history row in the same transaction (deferred check) | `campaigns` + `campaign_status_history`; `users`, `organisations`, `beneficiaries` + their audit events / decision rows |
| **Final state freezes the row** | small guard trigger | `idempotency_keys` once `COMPLETED`, `otp_challenges` once consumed, `campaign_reviews` once `DECIDED`, `upload_sessions` once not `PENDING`, published review policies/templates |

The helper functions live in `0001_foundation.sql` (`forbid_mutation`, `set_updated_at`, `keep_created_at`,
`guard_transition`) and `0002_platform.sql` (`forbid_column_change`, `allow_only_column_changes`,
`set_once_columns`, `bump_version`).

**Optimistic concurrency.** Users edit aggregates with `UPDATE … WHERE id = $1 AND version = $2`; the trigger
sets `version = OLD.version + 1`, so a caller can never skip or reuse a version. Zero rows updated → the API
returns `CONFLICT` and the client reloads.

## 7. State machines and transition guards

```sql
status text NOT NULL DEFAULT '<initial>' CHECK (status IN (...)),
CREATE TRIGGER trg_<t>_guard_status BEFORE INSERT OR UPDATE OF status ON <t>
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('<machine>');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES ('<machine>', '', '<initial>'), ...;
```

- `from_status = ''` marks the allowed initial states, so a row cannot be **created** in a later state
  (test `campaign_cannot_be_created_active`).
- Machines defined in the files in this document's scope: `campaign` (23 rows: initial + the 22 PRODUCT §6.2
  edges), `beneficiary_verification` (8), `user_account` (7), `role_assignment_request` (5),
  `notification_job` (11). `stored_objects.scan_status` uses a dedicated guard because its column is not
  named `status`.
- Rules the edge list cannot express are a second BEFORE trigger (for example `FROZEN → COMPLETED` only if
  the campaign had completed before the freeze; see [campaign-schema.md](campaign-schema.md) §4).
- The Go state machine is the primary implementation; the Stage 6 test suite checks that the Go edge list and
  `app.status_transitions` are identical, in both directions.

## 8. Encryption columns

| Data | Storage form | Key class |
|---|---|---|
| C3 identifiers (ID numbers, DOB) | `*_ciphertext bytea` + `*_key_id text` (+ `*_bidx bytea` HMAC blind index) — **in `kyc` only** | per-subject data key wrapped by KMS |
| Payout account numbers | Same shape, in `app.payout_destinations` (payouts) | `payout-destination` |
| TOTP seeds (C4) | `mfa_methods.totp_secret_ciphertext` + `totp_secret_key_id` | `mfa-secret` |
| Passwords | Argon2id PHC string (`CHECK (password_hash ~ '^\$argon2id\$…')`) | — |
| OTP codes | `code_hmac` = HMAC-SHA-256(server key, code ‖ challenge id), 32 bytes | `otp` HMAC key (`hmac_key_id`) |
| Session / invitation tokens | `token_hash` = SHA-256(token), 32 bytes, `UNIQUE` | — |
| Recovery codes | `code_hash` keyed SHA-256 | `recovery-code` |
| Contact blind indexes outside `users` | `destination_hmac bytea` (OTP challenges, suppressions, guest reporters) | `contact-bidx` |

Length CHECKs (`octet_length(token_hash) = 32`) catch the mistake of storing a raw token: test
`raw_token_never_fits_token_hash`. Plaintext C4 values never enter the database; one-time codes are delivered
synchronously by `auth` through the `notifications` service and exist only in memory.

## 9. Deletion, anonymisation and crypto-shredding

- **No hard deletes by application code** of financial, audit, KYC or identity rows. `users`, `campaigns`,
  `organisations`, `beneficiaries`, `stored_objects` have `BEFORE DELETE` guards.
- **User-facing content** uses `deleted_at` (+ partial indexes `WHERE deleted_at IS NULL`); hard purge is a
  scheduled, audited retention job.
- **Anonymisation** (PRIVACY §8): contact and name columns are nullable *only* with a tombstone (for example
  `CHECK (email_normalized IS NOT NULL OR deleted_at IS NOT NULL)`; `beneficiaries.full_name` NULL only when
  `anonymised_at` is set). Financial rows keep internal ids and amounts.
- **Crypto-shredding**: C3 data in `kyc` and private objects are encrypted under per-subject data keys;
  destroying the key makes ciphertext (and backups) unreadable. Rows remain as proof of existence
  (`stored_objects.purged_at`, `evidence.purged` audit event).
- **Retention classes, not periods**: `retention_class IN ('KYC','FINANCIAL','AUDIT','CASE','CONSENT','OPERATIONAL')`
  (audit-evidence-model §7). Periods are **LEGAL_REVIEW_REQUIRED (LR-012, LR-034, LR-074)**; an unset period
  means *retain*. No period is hard-coded anywhere.
- **High-volume append-only telemetry** (`security_events`, later `audit_events`) is retained by dropping
  monthly partitions as `fundzim_migrator` (Stage 17/18), never by row DELETE.

## 10. Row-level security stance

- RLS is used only where a **database role boundary is not enough**: STR-restricted rows in `compliance`
  (ADR-022, `0018_grants.sql`).
- It is **not** used for user/organisation tenancy in `app`: the app connects with a single role, so RLS would
  depend on a session variable set by the same code it is meant to check. Tenancy is enforced by scoped
  repository queries (`WHERE owner_user_id = $actor` or membership joins) and IDOR tests (SECURITY §5.4).
- Revisit if a second, less-trusted role ever reads `app` directly (for example a reporting role), in which
  case masked views are preferred over RLS.

## 11. How invariants are enforced (summary)

| Invariant | DB enforcement | Test |
|---|---|---|
| No float money, currency always present | Column types + catalogue scans | `no_float_numeric_or_money_type_columns_anywhere`, `every_amount_column_has_a_currency_column` |
| Positive amounts | `CHECK (amount_minor > 0)` | `negative_goal_rejected`, `zero_goal_rejected` |
| Idempotent API calls | `UNIQUE (scope, key)`, completed rows frozen | `duplicate_idempotency_key_rejected` |
| Exactly-once consumer effect | `UNIQUE (consumer, event_id)` | `inbox_duplicate_delivery_rejected` |
| Audit immutability + tamper evidence | append-only triggers + hash chain + `audit.verify_chain()` | `audit_update_rejected`, `audit_tamper_detected_by_verifier` |
| Legal hold changes only via two-person hold rows | trigger on `evidence_records` + CHECK on `evidence_holds` | `evidence_legal_hold_without_hold_row_rejected` |
| Illegal state transitions impossible | `app.guard_transition` | `illegal_draft_to_active_rejected` |
| Every transition recorded | deferred history check | `status_change_without_history_rejected` |
| No self-approval | `CHECK (approved_by <> requested_by)` family | `role_request_self_approval_rejected`, `unfreeze_self_approved_rejected` |
| Role SoD conflicts | `roles.conflicting_role_codes` + trigger | `sod_conflicting_role_rejected` |
| Staff need MFA | `CHECK (kind <> 'STAFF' OR mfa_verified_at IS NOT NULL)` | `staff_session_without_mfa_rejected` |
| One verified owner per phone/email | partial unique indexes | `duplicate_verified_phone_rejected` |
| Goal currency fixed | trigger + goal currency check | `goal_currency_change_after_submission_rejected` |

Limitations of the in-memory validator: PGlite is single-connection, so concurrency (two sessions racing) and
grants under `SET ROLE` with real logins are tested against real PostgreSQL from Stage 3
([TESTING.md](../TESTING.md)).
