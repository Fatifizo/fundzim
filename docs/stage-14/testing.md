# Stage 14 — Testing

All results below were produced in this session on 2026-10-16 (development machine; compose stack rebuilt from the
Stage 14 tree). Remote CI is reported in the completion report.

| Suite | Command | Result |
|---|---|---|
| Go unit (vet, gofmt) | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | 278 top-level tests passed, 0 failed |
| Go race | same, `-race`, in `golang:1.27.2` | passed |
| Go integration | `go test -tags integration ./tests/integration/...` (under the stack lock) | 169 passed, 0 failed, 12 skipped (opt-in perf and environment-gated); `TestOperations*` 8, `TestRisk*` 12, `TestPayments*` 25, `TestPayout*` 9, `TestFee*` 12 |
| Web unit (vitest) | `npm test` | all files passed (incl. `operations-components.test.tsx`, 18 tests) |
| Web lint / typecheck / build | `npm run lint`, `npm run typecheck`, Playwright's production build | clean |
| E2E (mock API, desktop + Pixel 7, axe) | `npx playwright test` | 189 passed, 1 skipped (pre-existing) |
| OpenAPI | Redocly 2.54.3 lint | valid; 2 pre-existing warnings |
| Migrations | down ×2 / up ×2 on the development database | both Stage 14 migrations round-trip |
| Flake investigation (KI-S13-20) | `TestPublicExperienceEndpoints -count=3` | 3/3 passed; also passed in the full run |

## Stage 14 tests

| Test | Covers |
|---|---|
| `TestOperationsOverviewSourcesAndPermissions` | every module snapshot runs against the real schema; sources, natures, permissions on every metric; BLOCKED gates; per-role metric visibility (SUPPORT, FINANCE); non-staff 404; `/me` permissions staff-only; health components |
| `TestOperationsApprovalCenterRoutesToDomain` | checker decidable, maker `MAKER`, SUPPORT excluded; 4 concurrent domain decisions → exactly 1 succeeds; item leaves the queue |
| `TestOperationsSupportCases` | lifecycle, assignee eligibility, escalation keeps ownership, resolution note, reopen, history; 5 concurrent same-version updates → 1 ok / 4 stale; SUPPORT denied risk, verification and ledger routes; history immutable |
| `TestOperationsExceptionTriage` | real TEST screening ERROR → exception; triage, `OPS_EXCEPTION_DOMAIN_OPEN`, WAITING_EXTERNAL, history; SUPPORT cannot read history; stored view permission; unknown ref refused |
| `TestOperationsIncidentsAndAlerts` | alert-key dedupe, full lifecycle, post-incident note, SUPPORT view-only, new incident after closure |
| `TestOperationsStaffGovernance` | directory without email; detail; role inventory; self session revocation refused; revoked session → 401; access review separation of duties; REVOKE_RECOMMENDED files a dual-controlled request; decisions final in the database |
| `TestOperationsAuditCenter` | search filters and cursor; network fields hidden; stream permissions; audit UPDATE refused; export range bound, step-up, CSV, `audit.exported` recorded; FINANCE cannot export |
| `TestOperationsReportsSearchAndSLA` | report sources and per-role visibility; range validation; search permissions; SLA maker-checker (database refuses self-approval) and support `due_at` |
| `TestRiskRuleFaultIsolation` | KI-S13-11 end to end |
| Unit: `TestCollectBoundsSlowAndPanickingSources` (failure injection), `TestApprovalsVisibilityMakersAndPartialFailure`, `TestLifecyclesAreClosed` | source timeout and panic isolation; maker visibility; machines closed |
| Vitest | permission-aware navigation, breadcrumbs, risk action → route permission (reads `internal/risk/http.go`), lifecycle edges equal the Go maps, normalisers, metric/approval/source components (axe), action dialog with step-up, forms, task indicator failure state |
| E2E `staff-operations.spec.ts` | SUPPORT command center, navigation and support case lifecycle; COMPLIANCE approvals, triage refusal, audit search and export download, SLA proposal; SECURITY_ADMIN/SUPER_ADMIN directory, session revocation, access review decision |

## Performance (measured, `FUNDZIM_IT_PERF=1 go test -run TestOperationsPerf`)

measured 2026-10-16T08:21:58Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls on the server's composition (app.NewOperations); dataset: 412742 audit events, 5 support cases.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| snapshot: identity | 1 | 50 | 17.7 | 24.9 | 35.4 | 54 | 0 |
| snapshot: campaigns | 1 | 50 | 1.4 | 3.2 | 11.5 | 579 | 0 |
| snapshot: kyc | 1 | 50 | 1.5 | 2.8 | 8.0 | 547 | 0 |
| snapshot: beneficiaries | 1 | 50 | 0.7 | 1.2 | 1.6 | 1376 | 0 |
| snapshot: compliance | 1 | 50 | 1.5 | 2.7 | 16.3 | 535 | 0 |
| snapshot: risk | 1 | 50 | 2.2 | 3.7 | 23.2 | 353 | 0 |
| snapshot: payments | 1 | 50 | 1.3 | 3.6 | 10.4 | 597 | 0 |
| snapshot: providers | 1 | 50 | 215.3 | 233.8 | 240.8 | 5 | 0 |
| snapshot: ledger | 1 | 50 | 9.0 | 15.0 | 21.0 | 101 | 0 |
| snapshot: payouts | 1 | 50 | 2.1 | 3.4 | 21.1 | 393 | 0 |
| snapshot: fees | 1 | 50 | 1.9 | 2.9 | 15.1 | 472 | 0 |
| command center overview (all module snapshots), c=1 | 1 | 100 | 293.4 | 335.8 | 368.7 | 3 | 0 |
| command center overview (all module snapshots), c=8 | 8 | 100 | 385.5 | 486.2 | 542.2 | 20 | 0 |
| viewer permissions (EffectivePermissions) | 8 | 500 | 1.3 | 3.3 | 20.6 | 4426 | 0 |
| approval center (all modules) | 8 | 200 | 272.0 | 336.5 | 365.4 | 29 | 0 |
| exception center (all modules) | 8 | 200 | 277.3 | 352.7 | 414.9 | 28 | 0 |
| platform health (probes measured now) | 8 | 100 | 87.1 | 127.8 | 136.0 | 84 | 0 |
| global search (uuid) | 8 | 200 | 12.3 | 30.7 | 67.3 | 540 | 0 |
| support case queue (50) | 8 | 300 | 0.4 | 0.9 | 1.9 | 16794 | 0 |
| reports (30 days) | 8 | 50 | 269.4 | 347.1 | 394.3 | 27 | 0 |
