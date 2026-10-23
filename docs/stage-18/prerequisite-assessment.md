# Stage 18 — Prerequisite assessment

Assessed 2026-10-23 on branch `stage-18/security-hardening` (created from `stage-17/reconciliation` at `f5e9d00`).
Statuses: `SATISFIED`, `SATISFIED_WITH_CONDITIONS`, `BLOCKED`, `NOT_VERIFIED`. A documented feature was not treated
as operational until code, configuration or a run showed it. The audit used four read-only reviews (authentication and
RBAC; configuration, TLS, secrets and cryptography; audit-chain performance; containers and infrastructure); their
findings are F-S18-01…F-S18-20 in [known-issues.md](known-issues.md).

## 1. Stage 17 handover and governance (brief §3)

| Check | Evidence (2026-10-23) | Status |
|---|---|---|
| Stage 17 head | `origin/stage-17/reconciliation` = `f5e9d00` | SATISFIED |
| Remote CI | GitHub Actions run 39807904154 on `f5e9d00`: `completed / success`, 8 jobs (read from the public API) | SATISFIED |
| Stage 17 documentation | 19 documents in `docs/stage-17`, handover `STAGE-17-TO-STAGE-18.md` | SATISFIED (one wrong legal reference corrected, below) |
| Reviewed pull request | GitHub API: 0 pull requests for `stage-17/reconciliation` (none for any stage branch) | NOT_VERIFIED (no review exists) |
| Merged | `f5e9d00` is not an ancestor of `origin/main`; `main` is still `a552de2` (Stage 0) | BLOCKED (owner) |
| Owner acceptance | `docs/governance/stage-acceptance-register.md`: Stages 0–17 `NOT_RECORDED` | BLOCKED (owner; never recorded by engineering) |
| Stage 17 issues tracked | KI-S17-01…20 in `docs/stage-17/known-issues.md`; KI-S17-12 (audit verification time) is the Stage 18 input | SATISFIED |
| Legal references | KI-S17-06 and three Stage 17 documents cited **LR-076** for retention; LR-076 is *pool funding by FundZim*. Corrected to LR-012 with a note (the register and handover too) | SATISFIED (corrected) |

## 2. Financial safety gates (brief §4)

| Gate | Evidence | Status |
|---|---|---|
| Go constants (11) | `psp`, `payouts` `LiveExecutionEnabled`; `ledger.ProductionPostingEnabled`; `fees.ProductionFeesEnabled`; `risk.ScreeningProductionEnabled`; `notifications.LiveMessagingEnabled`, `MarketingEnabled`; `discovery.ProductionReferralsEnabled`; three `reconciliation` gates — all `false`. New regression test `internal/archtest` `TestFinancialGateConstantsStayOff` | SATISFIED |
| Database CHECKs (20) | `ck_*_no_production_stage{9,10,11,15}`, `ck_*_test_only_stage{11,12,13,15,17}`. New tests: `TestFinancialGateChecksSurviveMigrations` (no later migration drops one; detector self-test) and `TestSecurityFinancialGatesInDatabase` (present and validated, kill switches off, ledger TEST, no PRODUCTION rows) | SATISFIED |
| History immutability by grants | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory`: no runtime role (app, worker, kyc, compliance, ledger, backup) may DELETE/TRUNCATE ledger, audit or recon tables; only `fundzim_ledger` writes the ledger; posted journals/entries are not updatable even by it | SATISFIED |
| Unknown payout never resubmitted; RATE_LIMITED / DEFINITELY_NOT_SENT budgeted | Stage 17 IT `TestReconciliationRateLimitedPayoutIsBudgeted`, Stage 15 `payouts_retry_budget_test.go` (re-run in the full suite) | SATISFIED |
| Maker-checker, holds, restrictions | unchanged code paths; their integration tests re-run (security-testing.md) | SATISFIED |

## 3. Platform prerequisites

| Area | Current implementation | Missing / risk | Remediation (Stage 18) | Owner / dependency | Status |
|---|---|---|---|---|---|
| Go services, middleware | per-route policy (router panics without one), CSRF HMAC, security headers, body limits, timeouts | readiness returned raw driver errors (F-S18-05); errors from libraries reached logs unredacted (F-S18-07); a DB outage answered 500 | category-only readiness; free-text scrubber; 503 on dependency outage | engineering | SATISFIED |
| Next.js | per-request nonce CSP, server-only API base, no browser storage of secrets | Next 16.4.0 had two CRITICAL advisories (Trivy); credential forms fell back to GET without JavaScript (ZAP) | Next 16.4.2; `method="post"` on every auth/account form | engineering | SATISFIED |
| PostgreSQL | least-privilege roles, grants from the catalogue, RLS on compliance tables | no backup role; no TLS locally (`sslmode=disable`; `verify-full` required outside local) | read-only `fundzim_backup` (BYPASSRLS) | engineering; production TLS = infrastructure | SATISFIED_WITH_CONDITIONS |
| Valkey | cache/rate limits only, fallback never fails open | password on the command line | password via a tmpfs config file, non-root, read-only | engineering | SATISFIED |
| Garage | three buckets, three key pairs, SSE-C per object | single node, no backup | snapshot + block backup and restore drill | infrastructure (replication) | SATISFIED_WITH_CONDITIONS |
| Docker / compose | non-root images, loopback ports | all capabilities, writable roots, flat network, tag-only images | hardening + segmentation + digests + `internal/deploytest` | engineering | SATISFIED (development topology) |
| Reverse proxy / TLS | none in the repo; HSTS delegated to a future proxy | no TLS termination exists | documented requirements (network-security.md); production refuses plain HTTP URLs | infrastructure | BLOCKED (no production infrastructure) |
| GitHub Actions | `contents: read`, SHA-pinned actions, gitleaks, govulncheck | no SAST, SBOM, image scan, provenance | gosec, syft, Trivy, checksums + provenance artifact; workflow policy test | engineering | SATISFIED (branch protection = owner) |
| Authentication, sessions | Argon2id 64 MiB, `__Host-` cookies, rotation, TOTP with replay protection, staff MFA mandatory | staff step-up bypass via password change (F-S18-01, HIGH); no WebAuthn; no MFA reset procedure | bypass fixed and tested; gaps recorded | engineering / owner | SATISFIED_WITH_CONDITIONS |
| Authorization, tenant isolation | permission per route, object checks, maker-checker in Go + DB | break-glass has no workflow or audit tag (F-S18-04) | recorded; break-glass stays DB-only and alerts when active | engineering (Stage 19+) | SATISFIED_WITH_CONDITIONS |
| Ledger, payouts, reconciliation boundaries | Stage 10–17 gates and guards | — | regression tests above | — | SATISFIED |
| Audit logging | two hash chains, append-only triggers | full verification 40 s and growing ~120 k rows/day (KI-S17-12) | parallel + checkpointed verification; repeated polls not re-audited | engineering | SATISFIED |
| Secrets, keys | OpenBao dev server (provider credentials), keys in `.env` | production could not start (`kms` not implemented); no rotation | `secretstore` key provider; previous-key overlap | infrastructure (HA secret manager) | SATISFIED_WITH_CONDITIONS |
| Backups / DR | none | no backup, no restore drill | encrypted backup, drill, failure drills, DR exercises | infrastructure (off-host copies, PITR) | SATISFIED_WITH_CONDITIONS |
| Deployment scripts / IaC | none | — | reference architecture (deployment-security.md); no provisioning (brief §66) | infrastructure | BLOCKED (no production infrastructure) |
| Monitoring and alerting | Stage 14 derived alerts, metrics endpoint | no persisted alerts, no security signals | security scan + persisted alerts | engineering; paging channel = Stage 20 | SATISFIED_WITH_CONDITIONS |
| Existing security tests | identity, CSRF, cookies, rate limits, archtest, config refusals | no gate regression, deploy-config, workflow-policy, backup, DR tests | added (security-testing.md) | engineering | SATISFIED |

## 4. Conclusion

Stage 18 can proceed as a non-production engineering stage. It cannot change any governance status: Stages 0–17 remain
unaccepted and unmerged, and production financial operations remain blocked.
