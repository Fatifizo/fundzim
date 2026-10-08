# tests — cross-cutting test suites

Unit tests live next to the code they test (`*_test.go`; `apps/web/src/**/*.test.ts(x)`; web E2E in
`apps/web/e2e/`). This directory holds suites that span the whole system. Strategy:
[docs/TESTING.md](../docs/TESTING.md).

## `integration/` (Stage 3)

Build tag `integration`; runs against the **real local services** from `compose.yaml` as the real database
roles. Covers: migrations (apply, re-run no-op, version; optional destructive down/up round trip), app-role
readiness queries, audit tables append-only for `fundzim_app` and `fundzim_worker`, hash-chain verification
being worker-only, reference-data and DDL privilege boundaries, outbox immutability, Redis connectivity and
mandatory TTL, storage access per class, cross-bucket credential denial, no anonymous access to private
buckets, and optional black-box API and web → API checks.

```bash
docker compose up -d --build           # the stack must be running
make test-integration
# without make:
set -a; . ./.env; set +a
FUNDZIM_IT_API_URL=http://127.0.0.1:8080 FUNDZIM_IT_WEB_URL=http://127.0.0.1:3000 \
  go test -tags integration -count=1 -v ./tests/integration/...
```

Environment variables: [docs/development/configuration.md §8.2](../docs/development/configuration.md).
`FUNDZIM_IT_DESTRUCTIVE=1` (migration down/up) is for throwaway databases only (CI). The tests use synthetic
data only, but the audit rows they insert cannot be deleted (append-only); reset the local volumes to clear them.

Planned later (layout per TESTING.md): `api/` (contract and deny-by-default tests), `architecture/` (import
graph), `financial/` (ledger invariants, idempotency, concurrency, failure injection), `perf/`, `security/`.
