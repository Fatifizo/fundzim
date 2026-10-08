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
   anything with regulatory impact must also list the relevant `LEGAL_REVIEW_REQUIRED` items from
   [`../COMPLIANCE.md`](../COMPLIANCE.md).
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
| [ADR-006](ADR-006-double-entry-ledger.md) | Double-entry ledger requirement | Accepted |
| [ADR-007](ADR-007-payment-provider-abstraction.md) | Payment provider abstraction | Accepted |
| [ADR-008](ADR-008-s3-object-storage.md) | S3-compatible object storage | Accepted |
| [ADR-009](ADR-009-kyc-storage-separation.md) | Sensitive KYC storage separation | Accepted |
| [ADR-010](ADR-010-multi-currency.md) | Multi-currency architecture | Accepted |
| [ADR-011](ADR-011-utc-time.md) | UTC internal time | Accepted |
| [ADR-012](ADR-012-container-first.md) | Container-first development | Accepted |
