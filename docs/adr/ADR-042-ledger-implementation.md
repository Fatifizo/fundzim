# ADR-042: Ledger implementation — environment-designated databases, dedicated posting role, asynchronous payment boundary, versioned rules

- **Status:** Proposed (Stage 10, 2026-10-13)
- **Deciders:** Technical lead (Stage 10); owner acceptance pending; accounting review pending (KI-S10-02)
- **Amends:** [ADR-023](ADR-023-ledger-posting-architecture.md) §7 (posting inside the caller's transaction)
- **Related:** ADR-006, ADR-014, ADR-023, ADR-039, ADR-040; [ledger-architecture.md](../stage-10/ledger-architecture.md)

## Context

Stage 2 designed the ledger (ADR-006/014/023, `design/sql/0006_ledger.sql`). Stage 10 implements it while no payment
provider is selected (ADR-040), every payment is a test payment and production execution is blocked. The Stage 10
brief adds requirements the Stage 2 design did not cover: strict isolation of simulated activity from any production
ledger ("not a single boolean flag"), versioned posting rules, account status (ACTIVE / FROZEN / CLOSED), evidence
classes on every journal, a record of every source event considered for posting, a reconciliation foundation, and a
posting path that payments and webhooks cannot write to directly.

## Decision

1. **Environment designation per database.** `ledger.ledger_environment` is a singleton row written by migration
   (`TEST` here). Every account, journal, source reference and reconciliation reference carries `environment`, and a
   trigger refuses any value other than the database's. Test and production ledgers therefore never share a database;
   a PRODUCTION designation is a future migration in a separate database under its own ADR. In Stage 10 three
   independent controls refuse production: CHECK `ck_*_no_production_stage10` on every table, the Go constant
   `ledger.ProductionPostingEnabled = false`, and the payment-side guards of Stage 9. Synthetic journals are further
   bound by CHECK to `environment = TEST` and `evidence_class = SIMULATED`.
2. **Dedicated posting role `fundzim_ledger`.** Only this role may INSERT into ledger tables; `fundzim_app` and
   `fundzim_worker` have no ledger privileges at all. No role has UPDATE on history or DELETE/TRUNCATE (triggers
   also forbid them). The role writes audit events and outbox events only through the existing SECURITY DEFINER
   gateways, which now accept the `ledger.*` prefix from it. The API and worker open a separate pool
   (`DATABASE_LEDGER_URL`) for the ledger module.
3. **Asynchronous payment boundary (amends ADR-023 §7).** Because the ledger posts on its own connection and role, it
   cannot run inside the payments transaction. Payments never call the ledger and webhooks never touch ledger
   tables. The ledger consumes the outbox event `payments.payment_succeeded` and records a
   `ledger_source_references` row with a decision (`POSTED`, `NOT_ELIGIBLE`, `BLOCKED`, `REJECTED`). In Stage 10
   every decision is NOT_ELIGIBLE (test payment) or BLOCKED (production posting disabled). Missing postings are
   detected by reconciliation (MISSING_POSTING) instead of being prevented by one transaction; this trade-off is
   accepted because the outbox delivers at least once and posting is idempotent by key. `payments.LedgerPoster`
   stays nil at runtime.
4. **Versioned posting rules.** `ledger_posting_rules` is keyed by (code, version); a journal records the version
   and must use the current one. A rule change is a migration that adds a version, never an edit.
5. **Account status.** ACTIVE → FROZEN → ACTIVE, and ACTIVE/FROZEN → CLOSED (status machine `ledger_account`). The
   entry trigger reads the account row `FOR SHARE`: FROZEN accounts accept only adjustment-backed journals
   (approved maker-checker); CLOSED accepts nothing; closing requires a zero balance.
6. **Evidence class and source on every journal.** `evidence_class` ∈ SIMULATED, PROVIDER_REPORTED,
   PROVIDER_VERIFIED, SETTLEMENT_VERIFIED, MANUALLY_REVIEWED, REJECTED; `source_system` ∈ payments, reconciliation,
   staff, synthetic_fixture. Evidence is never elevated automatically.
7. **Reconciliation references** are their own table (OPEN → INVESTIGATING → RESOLVED; detection fields immutable;
   one unresolved row per type and source). Settlement reconciliation is reported NOT_PERFORMED until a statement
   source exists.
8. **The Stage 2 design endpoints** for ledger balance, transaction, invariant runs and adjustments in the OpenAPI
   contract are replaced by the implemented `/api/v1/admin/ledger/**` endpoints.

## Consequences

- A compromised application role cannot read or write the ledger; a compromised ledger role still cannot post an
  unbalanced journal, edit history, post into a closed period or account, or post a synthetic journal into anything
  but a TEST ledger. A database superuser or storage administrator is **not** constrained by these controls
  (KI-S10-05).
- One more database credential to manage (`POSTGRES_LEDGER_PASSWORD`).
- Payment postings are eventually consistent with payments; reconciliation is the backstop.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Post inside the payments transaction (ADR-023 §7 as written) | Requires `fundzim_app` to hold ledger INSERT rights, so a payments bug or compromise could write ledger rows directly; contradicts the brief ("payment webhooks must not directly insert accounting journals") |
| A single `is_test` boolean on journals | Explicitly rejected by the brief; a boolean can be flipped by one bad write. Database designation + CHECK + constant + separate database is layered |
| SECURITY DEFINER posting procedure | Kept as an option (ADR-023); the role separation gives the same isolation with Go-testable logic |
