# Development Seed Data Strategy

> **Status (Stage 3):** there is **no seed command** and no development sample data. The only rows in a fresh
> database are the reference data inserted by migrations (§1). This document fixes the rules that a future
> `fundzimctl seed` must follow, so that sample data can never be mistaken for real people or real money.

Related: [migrations.md](migrations.md) · [configuration.md](configuration.md) ·
[DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md) · [PRIVACY.md](../PRIVACY.md) ·
[local-environment-design.md §9](local-environment-design.md)

---

## 1. What exists today: reference data via migrations

Reference data is needed in **every** environment and is therefore part of the schema history, inserted by
migrations (never by a seed tool):

| Table | Rows | Migration |
|---|---|---|
| `app.currencies` | `USD` (840, 2 minor units, enabled, `minor_units_verified = true`); `ZWG` (924, 2 minor units, enabled, **`minor_units_verified = false`** — sandbox/test only until LR-043 / PCR-018 are answered) | `20261008120100_platform.sql` |
| `app.markets` | `ZW` (Zimbabwe, default currency USD, `Africa/Harare`, `+263`, `en-ZW`, enabled) | `20261008120100_platform.sql` |
| `app.feature_flags` + `app.feature_flag_changes` | `campaign.individual_for_others.enabled = false` (POLICY flag, requires approval, `legal_ref` LR-046/047/048/068, PD-27), with its creation change row by the all-zero system actor | `20261008120100_platform.sql` |

Reference rows use deterministic IDs where other rows refer to them, so every environment has identical IDs.
Changing reference data means a new migration (most of these tables are reference-only for the runtime role).

Why there is no seed command yet: Stage 3 has no domain tables to seed. Users arrive in Stage 4, organisations
and KYC in Stage 5, campaigns in Stage 6, payments in Stage 8 and the ledger in Stage 10. A seed tool written
now would have nothing to insert.

## 2. Rules for the future seed tool

When the first stage with domain tables needs sample data (expected Stage 4 for users), it adds
`fundzimctl seed` with these properties. They are requirements, not suggestions.

### 2.1 Where it may run

- **Refuses to run** unless `APP_ENV` is `development` or `test`. `staging` and `production` are refused
  with a non-zero exit, before connecting to the database (same pattern as `fundzimctl migrate down`).
- Connects as a runtime role through the application's own services (so constraints, triggers, audit and
  grants apply), never as the migrator or a superuser, and never by raw `INSERT`s that bypass invariants.
- Never runs automatically: not in `docker compose up`, not in `fundzim-migrate`, not in CI except in an
  explicitly named test job on a throwaway database.
- Idempotent: running it twice does not duplicate data (deterministic synthetic IDs or natural keys).

### 2.2 Synthetic identities only

- **No real personal data, ever** — not a colleague's phone number, not "my own test account", not data
  copied from production (there is none, and there never will be a copy in development).
- E-mail addresses use the reserved `.invalid` top-level domain, e.g. `donor-001@example.invalid`,
  `owner-002@example.invalid` (RFC 2606 / RFC 6761: never deliverable).
- Phone numbers: do **not** invent "test ranges" for Zimbabwean networks; no range has been verified as safe.
  Until a range documented as reserved for testing is confirmed and recorded with its source, seed data
  stores phone numbers as clearly invalid values that fail E.164 validation for real delivery, or leaves them
  unset where the schema allows. Local OTP delivery uses the `log` SMS fake and Mailpit only.
- Names are obviously fictional and labelled, e.g. `Test Donor 001`, `Sample Organisation (SYNTHETIC)`.
- No identity-document numbers, images or selfies. KYC sample cases (Stage 5) use the fake verification
  vendor and placeholder files that are clearly not documents (e.g. a generated image with the word
  "SYNTHETIC").
- Every seeded record is **marked synthetic**: a recognisable prefix in human-readable fields and, where the
  schema has a metadata/notes column, `"synthetic": true`. If a table has no such column, the deterministic
  synthetic ID range is documented here so rows can be identified.

### 2.3 No financial records that look real

- The seed tool **never creates donations, payment intents, payment events, ledger journals or entries,
  payouts, refunds or reconciliation records.** Financial history is append-only; synthetic rows would be
  undeletable and indistinguishable from real ones in reports.
- Financial flows in development are produced only by exercising the real code paths against the **sandbox
  payment provider** (Stage 8), which marks its transactions as sandbox by construction.
- No balances are seeded anywhere (balances are derived from the ledger; CLAUDE.md rule 5).

### 2.4 Staff accounts and roles

- Seeded staff accounts (Stage 4+) exist only in `development`/`test`, use `.invalid` addresses, and still
  require MFA enrolment; the seed never bypasses MFA or maker-checker.
- The production super-admin bootstrap is a separate, audited ceremony migration
  ([migration-plan.md](../database/migration-plan.md) Stage 4 row 4.5) — never the seed tool.

## 3. Resetting development data

Append-only tables (audit events, feature-flag changes, later ledger and payment events) **cannot** be cleaned
row by row — the triggers refuse `UPDATE`/`DELETE`/`TRUNCATE` even for cleanup. The only way to remove rows
from them is to discard the database:

```bash
make reset                     # asks you to type "reset"; then: docker compose down -v --remove-orphans
# without make:
docker compose down -v --remove-orphans
docker compose up -d --build   # recreates roles, re-runs migrations (reference data only)
```

This deletes **all** local data: the Postgres volume (database, roles), both Garage volumes (objects) — the
Valkey container keeps no data (`--save "" --appendonly no`). Your `.env` is kept, so the same generated
passwords are applied to the fresh volume.

Note: the integration tests insert synthetic rows (an audit event per runtime role, a briefly-lived outbox
event, storage probe objects that they delete) into the **local development database** when run against the
compose stack. The audit rows cannot be removed except by a reset. They contain no personal data
(`action = platform.integration_test.ran`, actor `system`).
