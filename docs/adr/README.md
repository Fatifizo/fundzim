# Architecture Decision Records (ADRs)

An ADR records one significant architectural decision: the context that forced it, what was decided, what it
costs, and what was rejected. ADRs are how FundZim avoids "silent" architecture changes — if the reasoning is
not written down here, the decision has not been made.

## When an ADR is required

Write an ADR (before or alongside the change, never after the fact) when a change:

- adds, removes or replaces a core technology (language, framework, database, queue, storage, identity provider);
- changes module boundaries, data ownership, or which module may write a table;
- touches how money is represented, stored, calculated, rounded, converted or posted to the ledger;
- changes payment, webhook, idempotency, payout or reconciliation behaviour;
- changes security boundaries: authentication, session model, authorisation model, encryption, key management,
  KYC data handling, secret management;
- changes data classification, retention or cross-border data flows;
- introduces new infrastructure (e.g. a message broker, a second database, Kubernetes) or a new deployable unit;
- reverses or materially amends an existing ADR.

Bug fixes, refactors inside one module, and routine dependency upgrades do not need an ADR.

## Process

1. Copy `ADR-000-template.md` to `ADR-NNN-short-kebab-title.md` using the next unused number. Numbers are never reused.
2. Set **Status: Proposed** and open it for review with the change that depends on it.
3. Financial, security or KYC-affecting ADRs need review from the technical lead **and** a second engineer;
   anything with regulatory impact must also list the relevant `LEGAL_REVIEW_REQUIRED` items (`LR-xxx`) from
   the register in [`../compliance/open-legal-questions.md`](../compliance/open-legal-questions.md).
4. On approval set **Status: Accepted** and update the index below.
5. Accepted ADRs are not rewritten. To change a decision, write a new ADR that supersedes it, then set the old
   one to **Superseded by ADR-NNN** (the only edit allowed to an accepted ADR, plus typo fixes).

## Status lifecycle

| Status | Meaning |
|---|---|
| Proposed | Under discussion; must not be relied on yet. |
| Accepted | In force. Code and documentation must conform. |
| Superseded | Replaced by a later ADR (linked). Kept for history. |
| Deprecated | No longer relevant (e.g. the subsystem was removed) and not replaced. |

## Index

| ADR | Title | Status |
|---|---|---|
| [ADR-000](ADR-000-template.md) | Template | — |
| [ADR-001](ADR-001-modular-monolith.md) | Modular monolith architecture | Accepted |
| [ADR-002](ADR-002-go-backend.md) | Go backend | Accepted |
| [ADR-003](ADR-003-nextjs-frontend.md) | Next.js frontend (presentation layer only) | Accepted |
| [ADR-004](ADR-004-postgresql.md) | PostgreSQL as the authoritative database | Accepted |
| [ADR-005](ADR-005-exact-money-representation.md) | Integer minor-unit money representation | Accepted |
| [ADR-006](ADR-006-double-entry-ledger.md) | Double-entry ledger requirement | Accepted (amended by ADR-014) |
| [ADR-007](ADR-007-payment-provider-abstraction.md) | Payment provider abstraction | Accepted (amended by ADR-020) |
| [ADR-008](ADR-008-s3-object-storage.md) | S3-compatible object storage | Accepted |
| [ADR-009](ADR-009-kyc-storage-separation.md) | Sensitive KYC storage separation | Accepted |
| [ADR-010](ADR-010-multi-currency.md) | Multi-currency architecture | Accepted (amended by ADR-018) |
| [ADR-011](ADR-011-utc-time.md) | UTC internal time | Accepted |
| [ADR-012](ADR-012-container-first.md) | Container-first development | Accepted |
| [ADR-013](ADR-013-regulatory-operating-model.md) | Regulatory operating model: PSP-mediated (Model A) for the MVP | Accepted (provisional — subject to LR-001/LR-004 and provider confirmation) |
| [ADR-014](ADR-014-accounting-separated-from-custody.md) | Separation of financial accounting from custody | Accepted |
| [ADR-015](ADR-015-risk-based-identity-verification.md) | Risk-based identity verification | Accepted |
| [ADR-016](ADR-016-beneficiary-verification-before-payout.md) | Beneficiary verification before payout | Accepted |
| [ADR-017](ADR-017-payout-approval-segregation-of-duties.md) | Payout approval and segregation of duties | Accepted |
| [ADR-018](ADR-018-currency-isolation-and-fx.md) | Currency isolation and FX policy | Accepted |
| [ADR-019](ADR-019-regulatory-evidence-management.md) | Regulatory evidence management | Accepted |
| [ADR-020](ADR-020-payment-payout-state-model-revision.md) | Payment and payout state model revision | Accepted |
| [ADR-021](ADR-021-module-boundaries-and-ownership.md) | Domain module boundaries, dependency graph and table ownership | Accepted (Stage 2; pending owner acceptance of Stage 2) |
| [ADR-022](ADR-022-database-schema-organisation.md) | Database schema organisation and roles | Accepted (Stage 2) |
| [ADR-023](ADR-023-ledger-posting-architecture.md) | Ledger posting architecture | Accepted (Stage 2) |
| [ADR-024](ADR-024-payment-intent-and-db-guarded-state-machines.md) | Payment intent vs provider transaction; database-guarded state machines | Accepted (Stage 2) |
| [ADR-025](ADR-025-outbox-inbox-job-queue.md) | Transactional outbox, inbox and PostgreSQL job queue (River) | Accepted (Stage 2) |
| [ADR-026](ADR-026-api-versioning-contract-first.md) | API versioning and contract-first OpenAPI | Accepted (Stage 2) |
| [ADR-027](ADR-027-authentication-session-strategy.md) | Authentication and session strategy | Accepted (Stage 2) |
| [ADR-028](ADR-028-migration-strategy.md) | Database migration strategy (goose, forward-only) | Accepted (Stage 2) |
| [ADR-029](ADR-029-provider-capability-abstraction.md) | Provider capability abstraction (`psp` module) | Accepted (Stage 2) |
| [ADR-030](ADR-030-reconciliation-architecture.md) | Financial reconciliation architecture | Accepted (Stage 2) |
| [ADR-031](ADR-031-data-access-and-http-stack.md) | Data access and HTTP stack (pgx + sqlc, stdlib router) | Accepted (Stage 2) |
