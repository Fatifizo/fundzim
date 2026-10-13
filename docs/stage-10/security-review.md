# Stage 10 — Ledger security review

Self-review by the implementing engineer (no external review has taken place).

## 1. Database privileges (verified by `TestLedgerApplicationRoleHasNoLedgerAccess` and `TestLedgerDatabaseInvariantsDirectSQL`)

| Role | Ledger privileges |
|---|---|
| `fundzim_migrator` | owner (DDL; `rebuild_balance`) |
| `fundzim_ledger` | SELECT; INSERT on append-only tables; INSERT/UPDATE on status-bearing tables (accounts, periods, adjustments, reconciliation references — every UPDATE guarded by triggers and status machines); SELECT + UPDATE on `ledger_balances` only to lock rows (direct UPDATE refused by trigger unless the owner sets the maintenance flag) |
| `fundzim_app`, `fundzim_worker` | **none** (no USAGE on schema `ledger`) |
| `fundzim_kyc`, `fundzim_compliance` | none |
| `fundzim_readonly` | SELECT on the ledger views |

No role has DELETE or TRUNCATE; triggers also forbid UPDATE/DELETE/TRUNCATE on journals, entries, source references,
invariant runs and the environment row.

## 2. Threats and controls

| Threat | Control |
|---|---|
| Unbalanced or rule-breaking journal (bug or compromised ledger role) | Deferred trigger at COMMIT (L1, L2, L7, L12, L14); Go validation first |
| Editing or deleting history | INSERT-only grants + mutation triggers + L11 seal (`created_txid`) |
| Simulated payment in a production ledger | Database environment designation + CHECKs + `ProductionPostingEnabled=false` + payment-side guards; synthetic ⇒ TEST by CHECK |
| Payments/webhooks writing journals | `fundzim_app` has no ledger privilege; consumer records decisions only; a TEST ledger refuses any `source_system = payments` journal (Go + CHECK `ck_ledger_transactions_no_payment_postings_in_test`, added after an integration test was found posting such journals — KI-S10-17) |
| Overdrawing an obligation | Row lock + sufficiency check + CHECK on the projection |
| Direct balance edit | Guard trigger; balances written only by SECURITY DEFINER functions |
| Unauthorised staff posting | No free posting endpoint; maker-checker with step-up; self-approval refused; requests expire |
| Synthetic fixtures in production | `LEDGER_SYNTHETIC_FIXTURES_ENABLED` refused by config in production; TEST ledger required; ADMIN-only permission |
| Hiding imbalances in suspense | Each journal balances on its own; suspense balances are exceptions |
| Replay / duplicate posting | Unique idempotency key; L6; reconciliation |
| Information exposure | All ledger endpoints are staff-only (`ledger.view` minimum); no public endpoint; no withdrawable balance exposed; TEST_ONLY labels in UI |
| Sensitive data in ledger | Journals hold ids, amounts and codes only; metadata is a string map without personal data; audit metadata passes the sensitive-key filter |

## 3. Limits (stated, not hidden)

- **A database superuser, the table owner or anyone with access to storage or backups can alter ledger data.**
  Immutability here is against the application roles. Stronger tamper evidence (hash-chaining journals, external
  anchoring, WORM backups) is not implemented (KI-S10-05).
- `ledger.rebuild_balance` is owner-only and changes only the projection, never journals.
- The `fundzim_ledger` credential is an environment variable in development (KI-S9-04 applies to production).
- Audit events are written through the existing hash-chained audit gateway; verification of the chain is
  `audit.verify_chain`.
