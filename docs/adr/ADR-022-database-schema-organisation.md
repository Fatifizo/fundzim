# ADR-022: Database schema organisation and roles

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2

## Context

DATABASE §7 named `public/app`, `ledger`, `audit` and `kyc` schemas, and Stage 1 added `risk` and
`compliance`. The final core schema name and whether `compliance` needs its own role were left to Stage 2.
Module ownership (ADR-021) must be enforceable, and so must the sensitivity boundaries (C3 KYC, restricted
STR cases).

## Decision

1. **Schemas follow control boundaries, not modules:** `app` (core), `queue` (job library), `ledger`,
   `audit`, `kyc`, `risk`, `compliance` and `recon`. `public` is revoked from `PUBLIC` and unused.
2. **Roles:**
   - `fundzim_migrator` owns every object.
   - `fundzim_app` is the runtime role.
   - `fundzim_kyc` is the only role with access to `kyc`.
   - **`fundzim_compliance`** is new: it is the only role with full access to `compliance`. Rows of
     restricted STR cases are additionally protected by row-level security.
   - `fundzim_readonly` reads approved views only.

   `fundzim_app` gets a single narrow view in `compliance` (`v_payout_blocking_cases`) for eligibility
   check EC-13.
3. Append-only tables are protected by grants (INSERT/SELECT only) **and** `app.forbid_mutation()` triggers.
4. Cross-schema foreign keys run only from `kyc`, `risk`, `compliance`, `recon` and `ledger` into `app`
   reference tables, never from `app` into `kyc`. `ledger` references only `app.currencies`.
5. Module ownership inside `app` is enforced by tests (sqlc query files per module), not by roles. Per-module
   roles were judged not worth the connection-pool cost at this size.

## Consequences

### Positive
- C3 and STR data are isolated at the database-role level.
- A compromised app role cannot read KYC attributes or restricted compliance cases.

### Negative / costs
- Three connection pools (app, kyc, compliance).
- The RLS policy relies on a session setting that the compliance module sets after its permission check. Its
  limits are documented in [data-protection-architecture.md](../security/data-protection-architecture.md).

### Follow-up work
- Update DATABASE §7–§8 (done in Stage 2). Grants design: `design/sql/0018_grants.sql`.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| One schema per module (19 schemas) | No extra protection, because one app role touches them all; it only adds noise |
| Everything in `public` | Weakens grant management and default-privilege hygiene |
| Compliance under `fundzim_app` | STR confidentiality (tipping-off, LR-008) would then depend on application code alone |

## Security implications

Strengthens the isolation of compliance data. Break-glass access remains individually named and audited (DATABASE §8).

## Financial implications

None directly. Ledger grants are unchanged from DATABASE §8.

## Related

ADR-004, ADR-009, ADR-021; [design-principles.md](../database/design-principles.md),
[schema-overview.md](../database/schema-overview.md); LR-008, LR-012.
