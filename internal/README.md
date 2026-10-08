# internal — Go application code

Module boundaries are specified in [docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) and
[design-baseline.md](../docs/stage-2/design-baseline.md) §3; each domain module is created in the stage that
implements it ([docs/ROADMAP.md](../docs/ROADMAP.md)). Empty module directories are deliberately not created.

What exists (Stage 3):

| Package | Responsibility |
|---|---|
| `app/` | Composition root: dependencies (`NewDeps`), readiness checks, routes, middleware chain, public and internal servers, graceful shutdown. Imported only by `apps/api/cmd/*` and tests |
| `platform/config` | Typed configuration from the environment, validation, production refusals, self-redacting `Secret` |
| `platform/logging` | `slog` logger (JSON/text) with key-based redaction and request-scoped loggers |
| `platform/errs` | Error kinds → HTTP status, stable error codes, client-safe messages |
| `platform/httpx` | Response envelope, router with mandatory route policies, middleware, client IP / request ID |
| `platform/health` | Bounded, concurrent readiness checks |
| `platform/db` | pgx v5 pool, schema-version check |
| `platform/cache` | Optional Redis client (never authoritative) |
| `platform/storage` | S3 client per storage class (three classes, three credentials, three buckets) |
| `platform/metrics` | Prometheus metrics for the internal listener |
| `platform/money` | Exact money type (int64 minor units + currency) |
| `platform/ids` | UUIDv7 |
| `platform/version` | Build metadata |

Not yet created: domain modules (`auth`, `users`, `organisations`, `campaigns`, `payments`, `ledger`, `payouts`,
`fees`, `kyc`, `compliance`, `risk`, `notifications`, `admin`, `audit`, `reconciliation`, `storage`) and the
platform packages for jobs, outbox, idempotency, clock and transactions (Stage 4 onward).

Rules (also in [CLAUDE.md](../CLAUDE.md)): modules call each other only through public service interfaces;
no module reads another module's tables; no import cycles; only `ledger` writes ledger tables; only `kyc`
touches KYC data. The import-graph architecture test that will enforce this is carried to Stage 4.
