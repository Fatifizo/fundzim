# Stage 18 — Security testing and results

Every result below was produced in this session (2026-10-23) on branch `stage-18/security-hardening` on the development
host (16 vCPU Xeon 8160, 30 GiB, Ubuntu 25.10, Docker; compose PostgreSQL 17.11), integration and real-stack runs under
`flock /tmp/claude-1000/fundzim-stack.lock`. No test was skipped, disabled or weakened to pass; skips listed are opt-in
performance samples and environment-dependent tests that were skipped before Stage 18.

**Remote CI (observed).** `57e95e5`: secret scanning failed — gitleaks flagged a fake 64-hex test value in
`internal/app/worker_test.go`; replaced by a value built at run time and the historical fingerprint recorded in
`.gitleaksignore` with a justification. **`7ae778f`: run 39961618431 succeeded — all 8 jobs** (Go incl. gosec and
deploytest, Web, mock E2E, real-stack E2E, integration incl. the destructive migration round trip, container images
with SBOMs, Trivy gate and provenance — artifact `supply-chain-evidence-7ae778f…`, contracts, secret scanning).

## 1. Suites

| Suite | Command | Result |
|---|---|---|
| gofmt / vet | `gofmt -l apps internal migrations tests`; `go vet` (default, `integration`, `perf`, payments overlay) | clean |
| Go unit, race | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` in `golang:1.27.3` | **39 packages ok**, no data race |
| Go integration (full) | `go test -tags integration -count=1 -timeout 90m ./tests/integration/...` | **206 passed, 1 failed, 14 skipped**. The failure, `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings` (`LEDGER_PERIOD_STATE`), passed on rerun: accounting-period state shared on the development database (seen in Stage 17 too; KI-S18-24). All 11 `TestSecurity*` that run passed (the 12th is the opt-in perf sample) |
| Web | lint, typecheck, `vitest run`, build, `npm audit --omit=dev` | clean; **61 files, 578 tests**; build ok; 0 vulnerabilities |
| Mock E2E | `npm run test:e2e` (desktop + Pixel 7) | **221 passed, 1 skipped** (pre-existing) — includes `staff-security.spec.ts` (4) |
| Real-stack E2E | `npm run test:e2e:real` against the hardened stack | **64 passed, 0 failed** |
| OpenAPI | Redocly lint; `TestEveryRouteHasAPolicyAndIsInOpenAPI` | valid (2 pre-existing warnings); routes covered |
| Secrets | `scripts/check-secrets.sh` | §8 |

## 2. Stage 18 automated tests (brief §80)

| # | Area | Tests |
|---|---|---|
| 1 | Authentication | identity ITs; `TestSecurityStaffPasswordChangeNeedsTOTPStepUp`; auth unit tests |
| 2 | MFA enforcement | `TestIdentityMFAEnrollLoginRecovery`, staff invitation, step-up tests; web `change-password-form.test.tsx` |
| 3 | Sessions | `TestCookiesAreHardened`, `TestIdentitySessionsRotationLogoutAndIDOR`, `TestIdentitySessionExpiry` |
| 4 | RBAC | `TestIdentityRBACStaffMakerChecker`, `TestAuthorizeMatrix`, alert API permissions (`TestSecurityAlertsLifecycle`), web navigation tests |
| 5 | Tenant isolation | organisation, sessions, payments, payouts, campaigns, notifications, reconciliation ITs (authorization-review.md §3) |
| 6 | Financial authorization | maker-checker ITs (payouts, ledger, fees, recon, risk) |
| 7 | Secrets redaction | `TestErrorTextScrubbed`, readiness category test, config "errors never contain values", key-resolution errors (`TestResolveManagedKeys`, IT) |
| 8 | TLS configuration | `TestTLSRefusesExpiredWrongNameAndUnknownCA`; config TLS refusals (`TestStagingIsProductionLike`, production tests) |
| 9 | API security | `TestDependencyOutageIs503`, httpx header/limit tests, ZAP API scan |
| 10 | File uploads | storage sniffing/pipeline tests; `credential-forms.test.ts` (no credential in URLs) |
| 11 | Containers | `TestComposeContainersAreHardened`, `TestComposeNetworkSegmentation`, `TestDockerfilesPinnedAndUnprivileged` |
| 12 | Dependencies | govulncheck, npm audit, Trivy gate (CI) |
| 13 | Ledger integrity | `TestLedger*`; `fundzimctl verify ledger` in the restore drill |
| 14 | Payout safety | `TestPayout*`, retry budget, RATE_LIMITED |
| 15 | Reconciliation integrity | `TestReconciliation*` |
| 16 | Audit integrity | `TestSecurityAuditChainVerification`, `TestSecurityAuditChainTamperDetection`, `TestAuditHashChainVerifiesAndIsWorkerOnly`, `TestIdentityAuditChainsIntact` |
| 17 | Backup integrity | `scripts/backup/failure-drills.sh` (7 scenarios) |
| 18 | Restore | `scripts/backup/restore-drill.sh` |
| 19 | Worker recovery | DR exercise (worker killed, database restart) |
| 20 | Infrastructure configuration | `internal/deploytest` (mutation-checked) |
| 21 | CI permissions | `TestWorkflowLeastPrivilege` |
| 22 | Production configuration gates | `TestProductionKeysFromSecretManager`, `TestProductionRefusesUnsafeSettings`, `TestStagingIsProductionLike`, `TestPreviousKeysValidated`, `TestLoadWorkerConfig`; DR "invalid configuration release" |

Financial safety regression (brief §82): `TestFinancialGateConstantsStayOff`, `TestFinancialGateChecksSurviveMigrations`
(+ detector self-test `TestGateStateDetectsDroppedCheck`), `TestSecurityFinancialGatesInDatabase`,
`TestSecurityRuntimeRolesCannotRewriteFinancialHistory`, `TestSecurityBackupRoleIsReadOnly`,
`TestSecurityRetentionRegistryInventsNoPeriods`, plus the Stage 8–17 suites in the full run (unknown payouts, ledger
immutability, reservations, maker-checker, restricted campaigns, messaging disabled).

## 3. Failure injection (brief §81)

| Scenario | Test / run | Result |
|---|---|---|
| Expired certificate | `TestTLSRefusesExpiredWrongNameAndUnknownCA` | refused (also wrong name, unknown CA) |
| Invalid signing key | webhook signature tests (Stage 8/9); backup wrong key | refused |
| Missing secret | IT `TestSecurityKeysFromSecretManager` (missing reference) | startup refused, variable named only |
| Revoked service credential | same IT (invalid token) | refused |
| Database outage | DR exercise | 503 readiness and reads, recovery < 1 s after restart |
| Cache outage | DR exercise | API ready, login still 401 |
| Object-storage outage | DR exercise | 503 readiness, recovery < 1 s |
| Worker crash | DR exercise | resumes after restart |
| Queue backlog | DR exercise (outbox unchanged while the worker is down) | no loss |
| Corrupt backup | failure drills | refused |
| Partial restore | truncated dump | refused, nothing restored |
| Failed migration | `TestSecurityFailedMigrationLeavesNothing` | no object, no version row |
| Concurrent migration | `TestSecurityConcurrentMigrationsSerialise` | applied once |
| Duplicate webhook / conflicting provider response | Stage 8/9/17 tests (inbox dedupe, contradiction log, CONFLICTING_EVIDENCE) | rerun in the full suite: pass |
| Unauthorized financial operation | maker-checker and permission tests | refused |
| Audit-chain corruption | `TestSecurityAuditChainTamperDetection` (modified row, removed row) | detected |

## 4. Audit verification measurements

1 314 358 rows: plpgsql single pass 40.1 s; set-based 40.4 s (rejected); parallel ranges 13.2 s on 8 connections;
incremental after a checkpoint 8 ms; worker full run 12.5 s on 4 connections; restored copy 6.2 s on 8. The two
integration tests that timed out in Stage 17 now pass (`TestIdentityAuditChainsIntact` 2.2 s,
`TestComplianceMakerCheckerAndSubjectConflicts` 16.2 s).

## 5. Backup, restore and DR (final build)

Backup `fundzim-development-20261023T131704Z-ab4184` (614 MB encrypted dump, 52 MB storage, 72 kB config; every file mode
600): PostgreSQL 85.1 s, storage 6.3 s, configuration 0.2 s. Restore drill **PASSED**, 178.7 s of recovery steps
(restore 135.1 s; audit 1 323 127 rows INTACT in 6.2 s on 8 connections; ledger L1–L15 passed; 6 848 objects verified,
198 under throwaway test keys, 0 integrity failures, 0 missing; 78/78 reconciliation evidence objects; API ready; drill
user authenticated, wrong password refused). Failure drills **PASSED** (7 scenarios refused). Both recorded with
`fundzimctl backup record`. The earlier drill on `…090624Z-cfb044` (173.0 s) is in backup-recovery.md §5. DR exercise:
disaster-recovery.md §3 (final-build rerun result recorded there).

## 6. Performance and capacity (brief §73, §84; FUNDZIM_IT_PERF=1)

| Measurement | Workers | p50 | p95 | p99 | Throughput |
|---|---|---|---|---|---|
| Login (Argon2id 64 MiB) | 1 | 153 ms | 166 ms | 166 ms | 6.6 /s |
| Login | 8 | 401 ms | 547 ms | 547 ms | 18.4 /s (bounded by 4 concurrent hashes) |
| `GET /me` (session, permissions, CSRF) | 10 | 2.4 ms | 4.6 ms | 13.1 ms | 3 496 /s |
| `GET /version` (public, no session) | 10 | 0.22 ms | 0.90 ms | 1.3 ms | 32 527 /s |
| Secret resolve (uncached) | 4 | 1.2 ms | 2.6 ms | 14.6 ms | 2 408 /s |
| Secret resolve (cached) | 4 | 1.5 µs | 2.4 µs | 70 µs | — |
| Audit event write (own transaction) | 1 | 2.0 ms | 3.5 ms | 5.0 ms | 452 /s |
| Audit event write | 8 | 13.6 ms | 17.5 ms | 42.0 ms | 553 /s (one chain lock serialises writers) |
| Security scan | 1 | 77 ms | 108 ms | — | — |
| Full chain verification, one connection | 1 | 39.5 s for 1 318 293 rows | | | |

Authorization overhead: `/me` (session lookup + permission load + CSRF) vs `/version` ≈ 2.2 ms at p50. Earlier stages'
capacity samples (public browsing, search, campaign creation, financial APIs, reconciliation, notifications) are in
their testing documents and were not re-measured; Stage 19 owns load and endurance.

## 7. Scans

gosec v2.29.0: 0 issues (9 reviewed `#nosec`). govulncheck v1.8.0: 0 reachable. Trivy v0.75.0 (final images): API 5
UNKNOWN; web 1 CRITICAL / 56 HIGH / 87 MEDIUM / 83 LOW, **0 fixable CRITICAL/HIGH** (gate passes). syft v1.54.1: SBOMs
API 1 097, web 3 511, source 118 components. ZAP 2.17.0: web baseline FAIL 0 / WARN 8; API safe scan FAIL 0 / WARN 0.

## 8. Secrets scan

`scripts/check-secrets.sh` before the commit (completion report). gitleaks is not installed on this host; the CI
secret-scanning job runs it over the full history.
