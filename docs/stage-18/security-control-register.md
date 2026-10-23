# Stage 18 — Security control register

Status meanings: **IMPLEMENTED** (code/config exists) · **TESTED** (an automated test proves it) · **OPERATIONALLY_VERIFIED**
(observed working on the running development stack or in CI in this stage — never production) · **EXTERNALLY_PENDING**
(depends on infrastructure, a provider, a person or a legal decision) · **FAILED** · **NOT_APPLICABLE**. Owners are
roles. This register drives the Stage 18 gate (production-readiness.md).

## Financial safety (C-FIN)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-FIN-01 | No live payment, payout, posting, fee, screening, messaging, referral or settlement operation | 11 Go constants (`internal/{psp,payouts,ledger,fees,risk,notifications,discovery,reconciliation}`) | compile-time `false` | `TestFinancialGateConstantsStayOff` | CI run | Technical lead | TESTED | none |
| C-FIN-02 | No PRODUCTION money rows | 20 CHECKs `ck_*_no_production_*`, `ck_*_test_only_*` | migrations | `TestFinancialGateChecksSurviveMigrations`, `TestSecurityFinancialGatesInDatabase` | dev DB: 0 PRODUCTION intents/payouts | Technical lead | TESTED | none |
| C-FIN-03 | Kill switches off | `payments/payouts.live_execution.enabled` | feature flags | `TestSecurityFinancialGatesInDatabase` | — | Technical lead | TESTED | — |
| C-FIN-04 | Financial history cannot be rewritten by runtime roles | grants (`app.apply_runtime_grants`), append-only triggers | migrations | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory`, `TestSecurityBackupRoleIsReadOnly` | — | DBA | TESTED | superuser can still disable triggers |
| C-FIN-05 | Gate drift is detected at runtime | security scan rule `FINANCIAL_GATE_NOT_BLOCKED` (CRITICAL) | worker, every minute | `TestSecurityFindings`, `TestSecurityAlertsLifecycle` | live scan on the dev stack | Security admin | OPERATIONALLY_VERIFIED | — |
| C-FIN-06 | Unknown payout never resubmitted; RATE_LIMITED/DEFINITELY_NOT_SENT budgeted | payouts attempts guard, dispatcher | `PAYOUTS_SUBMIT_RETRY_BUDGET` | `payouts_retry_budget_test.go`, `TestReconciliationRateLimitedPayoutIsBudgeted` | — | Finance lead | TESTED | — |
| C-FIN-07 | Maker-checker on money, roles, rules, alerts' escalation path | Go + CHECK + triggers | — | module ITs | — | Compliance | TESTED | — |

## Identity and access (C-IAM)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-IAM-01 | Strong password storage | `auth/passwords` Argon2id | 64 MiB, t=3, p=2, ≤ 4 concurrent | `passwords_test.go` | login p50 153 ms (perf sample) | Security admin | TESTED | — |
| C-IAM-02 | Enumeration-safe login, reset, resend | `auth/account.go` | — | `TestIdentityLoginGenericErrorsAndCSRF` | — | Security admin | TESTED | `ACCOUNT_SUSPENDED` after a correct password |
| C-IAM-03 | Brute-force limits that never fail open | `platform/ratelimit` | policies.go | `TestDistributedFallbackNeverFailsOpen` | cache-loss DR scenario: login still 401 | Security admin | OPERATIONALLY_VERIFIED | no lockout (by design) |
| C-IAM-04 | Mandatory MFA for staff, TOTP replay protection | `auth/mfa.go`, staff invitation | — | `TestIdentityMFAEnrollLoginRecovery` | — | Security admin | TESTED | no WebAuthn (F-S18-12) |
| C-IAM-05 | Step-up for sensitive actions, TOTP-only for staff | `auth/middleware.go`, permission flag | `STEP_UP_MAX_AGE` 10 min | `TestAuthorizeMatrix`, `TestSecurityStaffPasswordChangeNeedsTOTPStepUp` | — | Security admin | TESTED | — |
| C-IAM-06 | Hardened sessions and CSRF | `auth/sessions.go`, `middleware.go` | `__Host-`, Secure, Lax; staff 15 min / 12 h | `TestCookiesAreHardened`, `TestCSRFMiddleware`, `TestIdentitySessionExpiry` | — | Security admin | TESTED | — |
| C-IAM-07 | Every route has a policy; non-staff get 404 | `platform/httpx/router.go` | — | `TestRouteWithoutPolicyPanics`, `TestEveryRouteHasAPolicyAndIsInOpenAPI` | — | Technical lead | TESTED | — |
| C-IAM-08 | Role changes dual-controlled, nobody decides own access | auth + DB | — | `TestIdentityRBACStaffMakerChecker` | — | Security admin | TESTED | — |
| C-IAM-09 | Linked staff/personal identities excluded from self-decisions | `app.actor_identities` | — | remediation ITs | — | Compliance | TESTED | — |
| C-IAM-10 | Break-glass bounded | `app.break_glass_grants` CHECKs | ≤ 1 h, no role/ledger perms | DB constraints | alert `BREAK_GLASS_ACTIVE` | Security admin | IMPLEMENTED | no workflow, no audit tag (F-S18-04) |
| C-IAM-11 | Phishing-resistant MFA for privileged staff | — | — | — | — | Security admin | EXTERNALLY_PENDING | KI-S18-06 |

## Secrets and cryptography (C-KEY)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-KEY-01 | Keys from the secret manager outside development | `config.Security.ResolveManaged`, `app/keys.go` | `FIELD_ENCRYPTION_PROVIDER=secretstore` | `TestProductionKeysFromSecretManager`, `TestResolveManagedKeys`, IT `TestSecurityKeysFromSecretManager` | resolved from the dev OpenBao | Security admin | TESTED | dev OpenBao not durable |
| C-KEY-02 | Rotation with overlap | `crypto.AEAD` previous keys, storage masters | `*_PREVIOUS_KEYS` (≤ 3) | `TestAEADRotationOverlap`, `TestSSEMasterRotation`, `TestPreviousKeysValidated` | — | Security admin | TESTED | no re-encryption job (KI-S18-03) |
| C-KEY-03 | One key per purpose | config validation, worker config | — | config tests, `TestLoadWorkerConfig` | — | Security admin | TESTED | — |
| C-KEY-04 | Provider credentials only as `secretref://` | Stage 9 CHECK, resolver | — | Stage 9 ITs | — | Security admin | TESTED | — |
| C-KEY-05 | Production refuses env secrets, static tokens, plain HTTP | `config.loadSecrets` | — | `TestSecretsManagerRules`, `TestStagingIsProductionLike` | — | Security admin | TESTED | — |
| C-KEY-06 | Secrets never in logs or errors | logging redaction + scrubber, readiness categories | — | `TestErrorTextScrubbed`, readiness test, config "errors never contain values" | — | Technical lead | TESTED | pattern-based |
| C-KEY-07 | Secrets not committed / not in images | `check-secrets.sh`, gitleaks, Dockerfile arg test | CI | `TestDockerfilesPinnedAndUnprivileged` | CI secret scanning job | Technical lead | OPERATIONALLY_VERIFIED | — |
| C-KEY-08 | KMS/HSM envelope encryption | — | — | — | — | Owner | EXTERNALLY_PENDING | KI-S18-02 |

## Infrastructure and network (C-INF)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-INF-01 | Least-privilege containers | `compose.yaml` x-hardened | cap_drop ALL, no-new-privileges, pids/mem limits, read-only app roots | `TestComposeContainersAreHardened` (mutation-checked) | stack healthy; inspect shows ro/caps/nnp | Platform engineer | OPERATIONALLY_VERIFIED | dev topology |
| C-INF-02 | Web cannot reach data services | networks `edge`/`default` | — | `TestComposeNetworkSegmentation` | DNS EAI_AGAIN, IP timeout from web | Platform engineer | OPERATIONALLY_VERIFIED | — |
| C-INF-03 | Images pinned by digest, non-root | Dockerfiles, compose | — | `TestDockerfilesPinnedAndUnprivileged` | CI "Images run as non-root" | Platform engineer | TESTED | — |
| C-INF-04 | TLS everywhere in production | config refusals | `verify-full`, `rediss`, https | config tests | — | Infrastructure | EXTERNALLY_PENDING | no proxy/certificates (KI-S18-01) |
| C-INF-05 | Host baseline | `scripts/host/baseline-check.sh` | read-only | run on dev host | 9 PASS / 6 WARN / 3 UNKNOWN | Infrastructure | IMPLEMENTED | production host does not exist |
| C-INF-06 | Database roles per purpose, RLS on compliance | `deploy/docker/postgres/init`, migrations | — | `TestReferenceDataAndPrivilegeBoundaries`, security ITs | — | DBA | TESTED | RLS only on compliance |

## Application and API (C-APP)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-APP-01 | Security headers, CSP nonce | httpx, `apps/web/src/proxy.ts` | — | httpx header tests, E2E CSP checks | ZAP baseline PASS 60 | Technical lead | OPERATIONALLY_VERIFIED | COEP/CORP on documents (F-S18-17) |
| C-APP-02 | Credentials never in URLs | auth/account forms | `method="post"` | `credential-forms.test.ts` | ZAP finding closed by fix | Technical lead | TESTED | — |
| C-APP-03 | Upload safety | `storage` sniffing, ClamAV, quarantine, SSE-C | per purpose | `sniff_test.go`, storage pipeline IT | `verify objects`: 6 602 verified, 0 integrity failures | Technical lead | OPERATIONALLY_VERIFIED | — |
| C-APP-04 | Dependency outage answered 503 | httpx `dependencyDown` | — | `TestDependencyOutageIs503` | DR exercise | Technical lead | TESTED | — |
| C-APP-05 | Static analysis | gosec v2.29.0 | CI | CI step | 0 issues, 9 reviewed `#nosec` | Technical lead | OPERATIONALLY_VERIFIED | — |
| C-APP-06 | Dynamic analysis (passive) | ZAP 2.17.0 | baseline + API safe mode | — | web FAIL 0 / WARN 8; API FAIL 0 / WARN 0 | Security admin | OPERATIONALLY_VERIFIED | no active/authenticated scan (KI-S18-21) |

## Supply chain (C-SUP)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-SUP-01 | Least-privilege workflow | `.github/workflows/ci.yml` | `contents: read`, SHA-pinned, no secrets, no environments | `TestWorkflowLeastPrivilege` | CI | Technical lead | TESTED | branch protection NOT_VERIFIED (KI-S18-09) |
| C-SUP-02 | Vulnerability scanning | govulncheck, npm audit, Trivy | CI gates | CI | local Trivy: 0 fixable CRITICAL/HIGH | Technical lead | OPERATIONALLY_VERIFIED | unfixed Debian CVEs (KI-S18-10) |
| C-SUP-03 | SBOM | syft v1.54.1 CycloneDX 1.7 | CI artifact | CI | local: api 1 097, web 3 511, source 118 components | Technical lead | OPERATIONALLY_VERIFIED | — |
| C-SUP-04 | Provenance and checksums | CI images job | unsigned record | CI | artifact | Technical lead | IMPLEMENTED | unsigned (KI-S18-08) |

## Audit, monitoring and recovery (C-OPS)

| ID | Objective | Component / location | Configuration | Test coverage | Operational evidence | Owner | Status | Residual |
|---|---|---|---|---|---|---|---|---|
| C-OPS-01 | Tamper-evident audit with practical verification | `audit/chain.go`, `verify_chain_range`, checkpoints | 15 min incremental, 24 h full | `TestSecurityAuditChainVerification`, `…TamperDetection` | worker FULL 1.3 M rows 12.5 s INTACT | Security admin | OPERATIONALLY_VERIFIED | not anchored externally (KI-S18-05) |
| C-OPS-02 | Persisted security alerts with lifecycle | `operations/security.go`, worker scan | 1 min | `TestSecurityFindings`, `TestSecurityAlertsLifecycle`, E2E | live alerts raised/cleared on the dev stack | Security admin | OPERATIONALLY_VERIFIED | no paging (KI-S18-15) |
| C-OPS-03 | Encrypted, verified backups | `scripts/backup/backup.sh` | key file outside the set | `failure-drills.sh` | backups taken 2026-10-23 | Infrastructure | OPERATIONALLY_VERIFIED | on-host only |
| C-OPS-04 | Restore drill | `restore-drill.sh` | disposable containers | — | PASSED, 173 s recovery | Infrastructure | OPERATIONALLY_VERIFIED | — |
| C-OPS-05 | Backup evidence and alerts | `fundzimctl backup record`, `app.ops_backup_runs` | — | IT (backups route), unit rules | recorded runs | Infrastructure | OPERATIONALLY_VERIFIED | — |
| C-OPS-06 | Retention without invented periods | `audit.retention_policies` | 17 legal-review families, 3 operational | `TestSecurityRetentionRegistryInventsNoPeriods` | — | Legal / DPO | TESTED | periods LEGAL_REVIEW_REQUIRED |
| C-OPS-07 | Off-host immutable backups, PITR | — | — | — | — | Infrastructure | EXTERNALLY_PENDING | KI-S18-01 |
| C-OPS-08 | Independent penetration test | — | — | — | — | Owner | EXTERNALLY_PENDING | KI-S18-11 |
