# ADR-021: Domain module boundaries, dependency graph and table ownership

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2 architecture session); pending project-owner acceptance of Stage 2
- **Stage:** 2

## Context

ADR-001 fixed a modular monolith, and ARCHITECTURE §4.2 listed modules and an allowed-dependency table. Stage 1
added concepts that did not fit cleanly:
- beneficiaries separate from campaign owners (ADR-016);
- unified holds (payout-eligibility-and-controls §5);
- an evidence store (ADR-019);
- a limits registry;
- provider capabilities that matter to both payments and payouts.

The Stage 1 handover (§3) left four ownership questions open. The Stage 0 table also allowed dependencies that
would form cycles once those concepts were added: for example `compliance` placing holds that `payouts` reads,
while `payouts` reads compliance cases.

## Decision

1. The module list and the compile-time dependency graph are those in
   [stage-2/design-baseline.md §2–§3](../stage-2/design-baseline.md). The graph is layered and acyclic. CI
   enforces it from Stage 3 with an import-graph test.
2. Two modules are added:
   - **`psp`**: provider registry, capability registry, adapter interfaces, sandbox provider and webhook inbox.
     It replaces the Stage 0 "shared provider interface package" between `payments` and `payouts`.
   - **`beneficiaries`**: beneficiaries, relationships, beneficiary verification decisions and institution
     payees. These are split out of `campaigns`.
3. Ownership resolutions:
   - **`risk`** owns all holds (`risk.holds`, `risk.hold_events`) and the limits registry. It is a leaf
     module, so every module can place or check holds without cycles.
   - **`audit`** owns `evidence_records` and `evidence_holds`. Callers store the object and pass a reference
     plus hash.
   - **`payouts`** owns `payout_destinations`, application-encrypted with a dedicated key class.
4. `organisations` no longer imports `kyc`. It keeps a projection fed by events. Decisions that need live
   verification state (payout eligibility, campaign submission) call `kyc` directly.
5. `payments` and `payouts` never import each other. Cross-effects travel as outbox events.
6. Every table has exactly one owning module, listed in the baseline §5. A module's SQL may reference only its
   own tables (Stage 3 sqlc ownership test).

## Consequences

### Positive
- Acyclic by construction. Hold checks have a single source (EC-12). The evidence store is usable from every
  domain.

### Negative / costs
- More modules (19). Some reads that were joins become service calls or projections.
- Event-driven effects such as recovery-case creation are eventually consistent (seconds).

### Follow-up work
- Update ARCHITECTURE §4.2 and DATABASE §7 to point to the baseline (done in Stage 2).
- Stage 3: write the import-graph and query-ownership tests.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Holds in `payouts` (Stage 1 handover suggestion) | `compliance`, `risk` and `payments` place holds, so they would all have to import `payouts`, creating cycles |
| Holds in `compliance` (DATABASE §7) | `payments` would then depend on `compliance` for dispute holds; it also mixes restricted STR data with routine holds |
| Evidence in `compliance` | Non-compliance modules (kyc, payments, reconciliation) create evidence and would have to depend on `compliance` |
| One schema and one module for "financial" | Loses ownership clarity; the ledger must stay domain-agnostic (ADR-006) |

## Security implications

Confidential compliance holds sit in `risk` but carry a `confidential` flag. Owner-facing DTOs never expose
their reason (LR-008). Restricted STR data stays in `compliance` under its own role (ADR-022).

## Financial implications

None to journals. The freeze transaction now posts the ledger move from `campaigns` in the same DB transaction
that places the `CAMPAIGN_FREEZE` hold, which keeps LEDGER §6.8 atomic.

## Related

ADR-001, ADR-007, ADR-016, ADR-017, ADR-019, ADR-022, ADR-029; [design-baseline.md](../stage-2/design-baseline.md);
[dependency-rules.md](../architecture/dependency-rules.md); [dependency-matrix.md](../architecture/dependency-matrix.md).
