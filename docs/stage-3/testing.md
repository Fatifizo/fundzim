# Stage 3 Testing and Validation Record

Everything below was **executed on 2026-10-08** on the development machine (Linux x86_64, Go 1.27.1, Node 24.21.0,
Docker 29.7.2, Compose v5.4.0). Nothing is reported that was not run. The GitHub Actions workflow has **not run
yet** (the branch has not been pushed), so CI results are not claimed.

## 1. Test inventory

| Suite | Location | How to run | Needs |
|---|---|---|---|
| Go unit tests | `internal/**/*_test.go` | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` (`make test-go`) | nothing |
| Go fuzz tests | `internal/platform/money` | `go test -run=^$ -fuzz=<Fuzz…> -fuzztime=15s ./internal/platform/money/` | nothing |
| Integration | `tests/integration` (build tag `integration`) | `set -a; . ./.env; set +a; FUNDZIM_IT_API_URL=… FUNDZIM_IT_WEB_URL=… go test -tags integration ./tests/integration/...` (`make test-integration`) | running compose stack |
| Web unit + accessibility | `apps/web/src/**/*.test.ts(x)` (Vitest, Testing Library, axe-core) | `npm --prefix apps/web test` | nothing |
| Web end-to-end | `apps/web/e2e` (Playwright, desktop + Pixel 7 Chromium) | `npm --prefix apps/web run test:e2e` | Chromium (installed via Playwright) |
| OpenAPI contract lint | `api/openapi/fundzim-v1.yaml` | `make openapi-lint` | network (npx) |
| Route ↔ contract parity | `internal/app/app_test.go` (`TestEveryRouteHasAPolicyAndIsInOpenAPI`) | part of Go unit tests | nothing |
| SQL design drafts | `design/sql` | `make db-validate` | nothing |

## 2. Results

| Check | Command | Result |
|---|---|---|
| Go format | `gofmt -l apps internal migrations tests` | clean (no files) |
| Go vet | `go vet ./apps/api/... ./internal/... ./migrations/...` and `go vet -tags integration ./tests/...` | clean |
| Go unit tests | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | **PASS** — 8 packages with tests; 54 top-level tests, 69 including subtests; 0 failures |
| Go race detector | `go test -race …` | **NOT RUN locally** — needs cgo/a C compiler, not installed (KI-02). CI runs it. |
| Fuzz: `FuzzParseNeverPanicsOrRounds` | 15 s | PASS (≈3.4 million executions, no failure) |
| Fuzz: `FuzzUnmarshalNeverPanics` | 15 s | PASS (≈0.5 million executions, no failure) |
| govulncheck v1.8.0 | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./…` | **No vulnerabilities found** |
| Web lint | `npm run lint` | PASS (exit 0) |
| Web type check | `npm run typecheck` (strict, `noUncheckedIndexedAccess`) | PASS (exit 0) |
| Web unit tests | `npm test` | **PASS — 8 files, 100 tests** |
| Web production build | `npm run build` | PASS — 17 routes |
| Web E2E | `npm run test:e2e` | **15 passed, 1 skipped** (a mobile-only test in the desktop project, by design), including axe with colour contrast, security headers, and hydration under the CSP |
| npm audit (production deps) | `npm audit --omit=dev --audit-level=high` | **0 vulnerabilities** |
| npm audit (all deps) | `npm audit --audit-level=high` | 5 high, all in the dev-only `eslint-config-next` chain (KI-03) — reported, not suppressed |
| OpenAPI lint | Redocly CLI 2.54.3 | **Valid**, 0 errors, 2 warnings (`operation-4xx-response` on `/healthz`, `/readyz`, intentional) |
| SQL design drafts | `node run.mjs && node catalogue.mjs` | **ALL CHECKS PASSED** (666 cases), catalogue OK |
| CI workflow lint | actionlint v1.7.12 | clean |
| Secret scan (pattern) | `scripts/check-secrets.sh` | no secrets found |
| Secret scan (gitleaks 8.30.1, full git history) | `gitleaks detect --source .` | **No leaks found** (run after the five Stage 3 commits, with `.gitleaks.toml` and `.gitleaksignore`) |

## 3. Docker and live-stack validation

| Check | What was done | Result |
|---|---|---|
| Compose file | `docker compose config --quiet` | valid |
| Full stack build and start | `docker compose up -d --build` from a fresh, empty volume set | **All services healthy**: postgres, redis (Valkey), storage (Garage), api, web; `fundzim-migrate` and `fundzim-storage-init` exited 0 |
| Migrations on PostgreSQL 17.11 | `fundzim-migrate` log | 4 migrations applied (foundation, platform, audit, runtime grants) |
| Storage initialisation | `fundzim-storage-init` log | layout applied, 3 keys imported, 3 buckets created, each key granted on exactly one bucket |
| Idempotent restarts | `docker compose stop/start fundzim-api` (re-ran migrate and storage-init) | both exited 0 with no changes |
| API endpoints | `curl` against `127.0.0.1:8080` | `/healthz` 200, `/readyz` 200, `/api/v1/health` 200, `/api/v1/ready` 200, `/api/v1/version` 200 (name, version, build, commit), unknown path 404 `ROUTE_NOT_FOUND` with `meta.request_id`, `POST /api/v1/health` 405 with `Allow: GET, HEAD`; security headers present |
| Internal listener | `127.0.0.1:9090/internal/readiness`, `/metrics` | all 6 checks OK (database, migrations, redis, 3 storage classes); `fundzim_build_info` and `fundzim_dependency_up` exported |
| Readiness with PostgreSQL stopped | `docker compose stop fundzim-postgres` | `/readyz` 503 `{"status":"unavailable"}` with `Retry-After: 5`; `/api/v1/ready` 503 `SERVICE_UNAVAILABLE` (retryable, no internal details); `/api/v1/health` still 200; the cause was logged server-side only; ready again (200) after restart |
| Graceful shutdown | `docker compose stop -t 25 fundzim-api` | logs show `shutting down` → `shutdown complete`; container stopped cleanly |
| Structured logging | API container logs | JSON lines with time, level, msg, service, env, version, request_id, correlation_id, operation, method, status, duration_ms |
| Secrets in logs | grep of all container logs | no passwords or secret keys; Garage logs access-key **IDs** (identifiers, not secrets) |
| Frontend | `curl 127.0.0.1:3000/` | 200; homepage served by the container with the "development preview" and "payments are not live" notices; CSP, `X-Frame-Options: DENY`, `nosniff` |
| Frontend → backend | `GET 127.0.0.1:3000/api/v1/version` (web proxy route → API) | 200 with FundZim version (integration test `TestFrontendReachesBackend`) |
| Integration suite | `go test -tags integration ./tests/integration/...` against the live stack | **12 tests PASS** (21 including subtests): migrations repeatable, readiness queries as app role, audit append-only as `fundzim_app` and `fundzim_worker`, hash chain verifies and `verify_chain` is worker-only, reference data and privilege boundaries, outbox immutability, Redis, storage per class, **cross-bucket denial for all 6 credential/bucket pairs**, no anonymous access to private buckets, running API, web→API |
| Destructive migration round trip | `FUNDZIM_IT_DESTRUCTIVE=1` (local, synthetic data only) | down to 0 and up again: PASS; status and version back at `20261008120300` |
| Production rollback refusal | `APP_ENV=production fundzimctl migrate down` | refused (exit 1): "production is forward-only (ADR-028)" |

## 4. Problems found by running (and fixed)

| Problem | How it was found | Fix |
|---|---|---|
| goose could not parse any migration (`SET` before `-- +goose Up`) | documentation review, then confirmed by running | Timeouts moved inside the Up section as `SET LOCAL` |
| Compose read an inline comment as the value of empty `.env` entries (`APP_VERSION=   # …`), breaking the image build's ldflags | `docker compose up --build` | Comments moved to their own line in `.env.example` |
| The migrator cannot grant role membership (`GRANT fundzim_app TO fundzim_worker`: permission denied) | first real migration run | Membership granted by the infrastructure init script; the migration now verifies it and fails closed |
| Garage admin API returns pretty-printed JSON; the init script's greps failed | storage-init exit 1 | Responses are compacted before parsing |
| `EMAIL_FROM` value not shell-safe when sourcing `.env` | sourcing `.env` for integration tests | Value quoted |
| `./...` descended into `apps/web/node_modules` (a dependency ships Go files) | `go test ./...` | Explicit package patterns in the Makefile and CI |
| Secret scanners flagged placeholders and fake test credentials | `check-secrets.sh`, gitleaks | Unambiguous placeholders, a narrow reviewed allowlist (`.gitleaks.toml`, `.gitleaksignore`), and `${INTERPOLATION}` treated like `<placeholder>` |

## 5. Not verified in Stage 3

- GitHub Actions runs (workflow not yet pushed; verified locally with actionlint only).
- `go test -race` locally (no C compiler; runs in CI).
- Container image vulnerability scanning (Trivy) — not set up (security-review F-11).
- Performance/load tests — out of scope (test-architecture §6).
