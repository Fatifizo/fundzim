# ADR-031: Data access and HTTP stack (pgx + sqlc, stdlib router)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (decision); pinned versions Stage 3

## Context

DATABASE §1 prefers explicit SQL ("pgx with hand-written SQL, sqlc preferred"). ARCHITECTURE §14 deferred the
router to Stage 3, but the Stage 3 blueprint needs the choice now.

## Decision

1. **pgx v5** is the driver and pool (one pool per DB role: app, kyc, compliance). **sqlc** generates typed
   Go code from hand-written SQL. Each module has its own `queries/` directory, and a CI test checks that a
   module's queries reference only the tables it owns (ADR-021).
2. **HTTP:** Go standard library `net/http` with `ServeMux` method and path patterns, and our own middleware
   chain (ARCHITECTURE §5). No web framework.
3. The transaction helper (`platform/db.WithTx`) retries on SQLSTATE 40001 and 40P01 with bounded attempts.
   This is safe only because operations are idempotent.

## Consequences

### Positive
- Minimal dependencies, explicit SQL that is reviewable for financial correctness, compile-time typed queries.

### Negative / costs
- More hand-written SQL than with an ORM, and middleware written in-house. Both are acceptable.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| GORM / ent | Hidden queries and auto-migration; conflicts with DATABASE §1 |
| chi / echo / gin | Unneeded since Go 1.22 ServeMux patterns; more supply-chain surface |
| database/sql + lib/pq | pgx has better types (numeric, timestamptz), COPY support, and River integration |

## Security implications

Parameterised queries only (SECURITY §8). A smaller dependency tree.

## Financial implications

Explicit SQL makes locking (`FOR UPDATE`, lock order) visible in review.

## Related

ADR-002, ADR-004, ADR-021, ADR-025; [go-module-design.md](../architecture/go-module-design.md).
