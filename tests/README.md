# tests — cross-cutting test suites

**Status: no tests yet.** Unit and module integration tests live next to the code they test; this directory
holds suites that span the whole system. Strategy: [docs/TESTING.md](../docs/TESTING.md).

Planned (layout per TESTING.md): `e2e/` (Playwright), `api/` (black-box API contract tests), `integration/`
(cross-module integration), `financial/` (ledger invariants, idempotency, concurrency, failure injection
against the sandbox payment provider), `perf/` (k6), `security/` (authorisation matrix / IDOR, rate limits).
