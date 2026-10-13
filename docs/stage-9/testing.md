# Stage 9 — Testing

All runs on 2026-10-13 against the development stack (PostgreSQL 17, Valkey, Garage, Mailpit, ClamAV, **OpenBao 2.4.1
dev mode**), under `flock /tmp/claude-1000/fundzim-stack.lock`. CI results are in the completion report.

## 1. New tests

| Layer | Tests | Covers |
|---|---|---|
| Unit — secret store | `internal/platform/secretstore/kv2_test.go` (fake KV v2 server) | TTL cache; **fail closed** after TTL when the store is down; rotation callback with versions only; weak, deleted, missing, malformed and path-traversal references refused; values never in errors; AppRole login and re-login after a revoked token; bad AppRole refused; redirects never followed (token cannot leak); config validation; health |
| Unit — psp | `internal/psp/execution_test.go` | Every observation class → error category; UNKNOWN_OUTCOME / PROVIDER_TIMEOUT are status-query-only; unknown category fails safe; credential scope (cross-environment references refused); execution-environment gates for simulator and real providers in every environment; `LiveExecutionEnabled` stays false |
| Unit — payments | `internal/payments/evidence_test.go` | Evidence classification: FIRST, CONSISTENT, PROGRESSION, CONTRADICTS (failure after success, amount or currency change); late success is not double-escalated |
| Unit — config | `internal/platform/config/config_test.go` | Execution environment rules per `APP_ENV`; old `PAYMENTS_PROVIDER_ENVIRONMENT` refused; allowlist validation; simulator never allow-listed in production; secret manager rules (env refused in production, https, AppRole only, pairs) |
| Integration | `tests/integration/payments_stage9_test.go` (8 tests) | **Real OpenBao**: resolve, rotation → PROVIDER_CREDENTIAL_ROTATED audit without values, AppRole with a sandbox-only policy refused a production path, wrong token / unreachable store fail closed, app resolves the simulator's keys from OpenBao, secret-manager outage → no intent and health UNAVAILABLE. **Conformance suite** against the simulator (19/19 PASS). **PROVIDER_SANDBOX isolation** with an in-test fake provider: test payment marking, evidence environment, simulator unusable, allowlist, no production enablement. **DB isolation**: no PRODUCTION intents/payments, simulated ⇔ LOCAL_SIMULATOR, environment immutable, cross-environment secrets refused, evidence append-only and environment-checked, error category set once. **Startup gate** refuses PRODUCTION and provider execution without the secret manager (audited `denied`). **Contradictory evidence** escalated (recon + outbox) without changing the payment. **Error categories and audit events** (PROVIDER_RESPONSE_RECEIVED, PAYMENT_INITIATED_SANDBOX, PAYMENT_STATUS_RECONCILED, WEBHOOK_VERIFIED, WEBHOOK_REJECTED, REFUND_REQUESTED_SANDBOX; unknown endpoints not audited). **Provider controls, health, availability** (step-up, permissions, outage → UNAVAILABLE + routing refused + 409 on repeat, disable/enable audited, public availability shape, ZWG none, DISABLED deployment, no bypass routes). **Concurrency**: 8 simultaneous outage declarations → exactly 1 wins; 10 concurrent identical deliveries → 1 inbox row, 1 evidence row, 1 SUCCEEDED transition |
| Integration (changed) | `TestPaymentsPostingRequestsExactlyOnce` | Stricter: a simulated payment must produce **no** ledger-seam request (Stage 8 allowed at most one) |
| Web unit | `src/lib/payments/normalise.test.ts` (Stage 9 block) | Unknown health ⇒ UNKNOWN, unknown contract basis ⇒ DOCUMENTED_BUT_UNVERIFIED, unknown evidence consistency ⇒ CONTRADICTS, money as digit strings |
| Mock E2E | `e2e/staff-payments.spec.ts` (Stage 9 test, both projects) | Provider detail, contract basis, error categories, health events, outage declaration with required reason and step-up, resolve, health page via nav, evidence table, `/admin/payments/transactions`, axe, CSP |
| CI guard | `scripts/check-secrets.sh` | Fails if any workflow references `${{ secrets.* }}` (verified by planting one) |

Failure injection covered: secret-manager outage and wrong credentials; provider timeout after send, not sent,
unavailable, decline (simulator scenarios); malformed / unmapped provider status; contradictory late evidence;
amount mismatch; staff-declared outage.

## 2. Defects found by the new tests (fixed)

| Defect | Found by | Fix |
|---|---|---|
| Simulator `Limits` returned limits for any method (checked currency only) | conformance `limits_unknown_method_fail_closed` | Unknown methods fail closed |
| `AvailableMethods` filtered on production `status = ENABLED`, hiding provider-sandbox routes | `TestPaymentsProviderSandboxIsolation` | Filter on `status <> RETIRED`; the router decides usability |
| A re-dispatched webhook appended duplicate evidence rows | `TestPaymentsStage9Concurrency` | Unique `(payment, provider_event_id)` + `ON CONFLICT DO NOTHING` |
| Audit metadata keys containing "credential"/"secret" refused by the never-embed rule (API failed to start) | compose startup | Neutral key names (`references_resolve`, `key_store`, `reference`) |
| Startup gate needed the database at construction | `internal/app` unit tests | Refusal is DB-free; audit records best effort |
| WEBHOOK_VERIFIED in the ingest transaction cut ingestion ~5× (audit hash chain serialises writers) | perf §4 | Audited at first dispatch claim instead |

## 3. Suites run

| Suite | Result |
|---|---|
| Go unit (`./apps/api/... ./internal/... ./migrations/...`) with `-race` (golang:1.27.2 container) | PASS |
| Go integration (`./tests/integration/`, full) | PASS (see completion report for the final run) |
| Web: typecheck, lint, vitest (48 files, 475 tests) | PASS |
| Mock Playwright (158: 157 passed, 1 pre-existing skip) | PASS |
| Real-stack Playwright | see completion report |
| OpenAPI lint (redocly) | PASS (2 pre-existing warnings) |
| Migration down/up of `20261013090000` on the dev database | PASS (destructive full round trip runs in CI) |

## 4. Performance (measured 2026-10-13, 16 CPUs, `tests/perf/payments/stage9_perf_test.go`)

| Scenario | c | p50 ms | p95 ms | p99 ms | req/s |
|---|---:|---:|---:|---:|---:|
| Secret resolve, KV v2 uncached (each call reads OpenBao) | 1 / 8 | 1.0 / 1.2 | 1.6 / 2.9 | 2.7 / 3.9 | 918 / 5 690 |
| Secret resolve, KV v2 cached (TTL 60 s) | 8 | < 0.1 | < 0.1 | 0.2 | ~1.1 M |
| `GET /payment-methods/available` | 1 / 8 | 30.1 / 25.6 | 39.7 / 32.5 | 44.8 / 66.4 | 32 / 298 |
| `GET /admin/payments/providers/{id}` | 1 / 8 | 109 / 100 | 122 / 115 | 137 / 123 | 9 / 78 |
| `GET /admin/payments/health` | 1 / 8 | 91 / 79 | 104 / 91 | 111 / 102 | 11 / 99 |
| `GET /admin/payments/providers` | 1 / 8 | 130 / 111 | 142 / 126 | 152 / 139 | 8 / 71 |
| `psp.StartupGate` | 1 | 8.3 | 14.5 | 15.9 | — |
| Webhook ingest (after the audit fix) | 32 | 10.8 | 16.8 | 32.8 | 2 711 (Stage 8: 2 481) |
| Webhook ingest with WEBHOOK_VERIFIED in the ingest tx (rejected design) | 32 | 67.7 | 76.9 | 140.8 | 457 |
| Webhook dispatch (claim → apply incl. evidence → processed), 150 events | 4 dispatchers | 14.9 | 20.2 | 34.2 | 256 events/s (Stage 8: 157) |

Reading: the availability endpoint routes every method (7 router passes, each reading capabilities, accounts,
outage, circuit and secrets) — ~25–30 ms is acceptable for a page load but it should be cached for ~10 s before a
public checkout exists (KI-S9-12). Staff provider views derive health per request (~80–130 ms) — acceptable for
staff pages. Dispatch with evidence capture is not slower than Stage 8 (variance between runs dominates).
