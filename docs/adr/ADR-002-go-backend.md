# ADR-002: Go backend

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (code starts Stage 3)

## Context

The backend is the system of record for campaigns, payments, ledger, payouts and KYC. It needs:

- a strong static type system;
- good concurrency for webhook bursts and background jobs;
- predictable performance;
- a small attack surface;
- a mature PostgreSQL ecosystem;
- easy containerisation;
- a hiring pool.

The master prompt proposes Go.

## Decision

We will implement the backend in **Go**, using the standard library where practical (`net/http`, `log/slog`,
`context`, `crypto/*`), and add only well-maintained dependencies.

- A single Go module at the **repository root** (`go.mod`), so `apps/api` (entrypoint `apps/api/cmd/api/main.go`)
  and `internal/` share one module and Go's `internal/` visibility rules protect domain packages. The module
  path is chosen in Stage 3.
- The Go toolchain version is **pinned in Stage 3** (the `go` and `toolchain` directives in `go.mod`, plus the
  CI image) to the current stable release at that time. Go is not installed on the development machine at
  Stage 0.
- Money is a dedicated type (`internal/platform/money`) wrapping `int64` minor units and a currency
  (ADR-005). `float32` and `float64` are banned in financial code, enforced by a lint rule from Stage 3.
- `govulncheck`, `go vet`, `staticcheck` (or golangci-lint) and race-enabled tests (`go test -race`) run in CI.

## Consequences

### Positive
- Static binaries produce small, non-root, distroless container images (ADR-012).
- Goroutines and contexts suit webhook ingestion, provider timeouts and worker queues.
- The strong standard library reduces dependency risk, which matters for a fintech supply chain.

### Negative / costs
- There are two languages in the repo (Go and TypeScript), so types are not shared between them. API contracts
  must be specified explicitly (an OpenAPI schema is planned for Stage 3) and generated or validated on both
  sides.
- Go has no built-in decimal type. We do not need one, because we use integer minor units, but a vetted
  rounding and allocation helper must be written and tested.

### Follow-up work
- Stage 3: pin the version, choose the module path, linters, project skeleton and the `money` package with
  property tests.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| TypeScript/Node.js backend (single language) | `number` is IEEE-754 floating point, so money correctness depends entirely on discipline. Weaker guarantees for CPU-bound work. Larger dependency trees and supply-chain surface. |
| Java/Kotlin (Spring) | Mature fintech ecosystem but heavier runtime and operational footprint for a small team. |
| Rust | Excellent safety, but slower development and a smaller hiring pool locally. |
| Next.js API routes as the backend | Mixes presentation and system-of-record logic. Rejected in ADR-003. |

## Security implications
Memory safety in practice, a small standard-library-centric dependency graph, and `govulncheck` in CI. Must
still guard against SQL injection by using parameterised queries only (no string-built SQL).

## Financial implications
Financial code gets a compile-time distinct `Money` type and a lint ban on floats in financial packages, which
removes a whole class of rounding bugs.

## Related
ADR-001, ADR-003, ADR-005, [ARCHITECTURE.md](../ARCHITECTURE.md), [DEVELOPMENT.md](../DEVELOPMENT.md).
