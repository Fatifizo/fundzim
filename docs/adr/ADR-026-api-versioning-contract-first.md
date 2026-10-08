# ADR-026: API versioning and contract-first OpenAPI

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2

## Context

ARCHITECTURE §6 fixed `/api/v1`, the envelope, the error model, cursor pagination and idempotency headers.
The web app, future mobile clients and contract tests need a single machine-readable contract.

## Decision

1. `api/openapi/fundzim-v1.yaml` (OpenAPI **3.1**) is the source of truth for `/api/v1`. Changes to the HTTP
   surface change the spec first, in the same pull request.
2. **Versioning:** URL major version. Additive changes (new endpoints, new optional request fields, new
   response fields) stay in v1, and clients must ignore unknown fields. Breaking changes need `/api/v2` for
   the affected resources and a deprecation period announced through a `Deprecation` header.
3. **Enum evolution:** adding a value to a response enum (for example a new payment status) is treated as
   **potentially breaking**. Clients must handle unknown values with a safe default ("processing"), and new
   values are announced in release notes.
4. The spec is linted in CI (Redocly CLI, pinned). From Stage 3, contract tests check that responses
   conform to the spec. A route-registry test fails if a registered route is missing from the spec or lacks
   an authorization policy.
5. Server types are generated from or checked against the spec (generator choice in Stage 3). The spec never
   contains secret or internal-only fields; staff DTOs are separate schemas.

## Consequences

### Positive
- One contract for web, mobile and tests; safe evolution.

### Negative / costs
- The spec must be maintained, which the CI checks enforce.

### Follow-up work
- Stage 3: lint in CI and add response conformance tests.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Code-first spec generation | The spec drifts toward implementation details; reviewing contract changes is harder |
| Header/media-type versioning | Harder to route and cache; less visible |
| GraphQL | Excluded by ARCHITECTURE §10 without a demonstrated need |

## Security implications

Per-audience schemas (public/owner/staff) make accidental field exposure reviewable in the diff.

## Financial implications

Money is always `{amount_minor: string, currency}` in the contract, with no floats (ADR-005).

## Related

ADR-005, ADR-027; [api-design.md](../api/api-design.md), [error-model.md](../api/error-model.md),
[authorization-matrix.md](../api/authorization-matrix.md).
