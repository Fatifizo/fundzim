# apps/api — Go API and worker

**Status: not started (Stage 3 — Core Platform Foundation).** This directory intentionally contains no code yet.

Planned shape (see [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md), [ADR-001](../../docs/adr/ADR-001-modular-monolith.md), [ADR-002](../../docs/adr/ADR-002-go-backend.md)):

- `apps/api/cmd/api/` — single binary. Runs as the HTTP API (`/api/v1/`, `/healthz`, `/readyz`) or, with a
  flag, as the background worker (Postgres-backed job queue, outbox/inbox processing).
- Wiring only: configuration, dependency injection, HTTP server, graceful shutdown. Business logic lives in
  `internal/` modules, never here.
- One Go module rooted at the repository root so `apps/api` and `internal/` share it.
