# ADR-001: Modular monolith architecture

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (implemented from Stage 3)

## Context

FundZim must orchestrate donations, a double-entry ledger, payouts, KYC, risk and audit for real people's money.
The team is small, no payment provider has been chosen, and the domain will change as regulatory questions are
answered. Several operations must be **atomic across domains**. For example, a confirmed payment must update the
payment record, post ledger entries, record fees and emit an outbox event in one database transaction.
Distributing these across services would turn simple ACID transactions into distributed sagas, adding failure
modes in exactly the place where correctness matters most.

At the same time, financial and KYC code needs hard internal boundaries so that, for example, a campaign
feature cannot write ledger rows or read identity documents.

## Decision

We will build the backend as a **modular monolith in Go**: one deployable API binary, which also runs in a
*worker mode* for background jobs, organised into domain modules under `internal/`:

`auth`, `users`, `organisations`, `campaigns`, `payments`, `ledger`, `payouts`, `fees`, `kyc`, `compliance`,
`risk`, `notifications`, `admin`, `audit`, `reconciliation`, `storage`, and `platform` (the shared kernel:
config, db, logging, ids, `money`, clock, HTTP envelope, errors).

Module rules:

1. Each module exposes a public service API (Go interfaces/types). Other modules call **only** that API, never
   another module's tables or internal packages.
2. A module owns its tables. **Only `ledger` writes ledger tables. Only `kyc` reads or writes KYC tables and
   private KYC objects.**
3. There are no circular imports. From Stage 3 an architecture test or lint rule fails the build on
   forbidden imports.
4. Financial modules (`payments`, `ledger`, `payouts`, `fees`, `reconciliation`) have the strictest boundaries.
   Changing their public API requires review by the technical lead.
5. Cross-module side effects that leave the process (notifications, provider calls) go through the
   transactional outbox.

## Consequences

### Positive
- Cross-domain financial operations stay inside one PostgreSQL transaction, which is simple, strongly
  consistent and testable.
- One build, one deploy and one log stream keep operations simple for a small team.
- Module boundaries keep the option of extracting a service later if there is ever a demonstrated need.

### Negative / costs
- Boundaries are enforced by convention and tooling, not by the network. Discipline and automated import
  checks are mandatory.
- The whole API scales as one unit. This is acceptable at the expected pilot load.
- A bad deploy affects all functionality. This is mitigated by tests, staged rollout and fast rollback.

### Follow-up work
- Stage 2: module dependency map and table ownership.
- Stage 3: skeleton, import-boundary check in CI, worker mode.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Microservices from day one | Distributed transactions across payments and ledger, more infrastructure, and higher operational cost with no demonstrated need. The master prompt prohibits them without one. |
| Unstructured monolith | Fast at first, but there is no protection against non-financial code mutating financial or KYC data. |
| Serverless functions | Cold starts, harder transactional guarantees, vendor lock-in, and poor fit for long-running reconciliation jobs. |

## Security implications
- Module boundaries are also **data-access boundaries**: the KYC schema uses its own DB role (ADR-009), and the
  application role cannot UPDATE, DELETE or TRUNCATE ledger or audit tables (ADR-006).
- A single process means a compromise of the API process reaches all modules' in-memory data. This is mitigated
  by least-privilege DB roles, separate storage credentials and secret scoping.

## Financial implications
Enables atomic posting of payment state, ledger entries and fees in one transaction, which is the core of
FundZim's financial integrity.

## Related
[ARCHITECTURE.md](../ARCHITECTURE.md), ADR-002, ADR-004, ADR-006, ADR-009, ADR-012.
