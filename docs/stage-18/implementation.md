# Stage 18 — Security hardening, compliance controls, resilience and recovery: implementation

Status: **complete; awaiting acceptance; non-production.** Decision record: [ADR-050](../adr/ADR-050-security-hardening-and-recovery.md).
Next stage: [STAGE-18-TO-STAGE-19](../stage-handover/STAGE-18-TO-STAGE-19.md). Production readiness: **BLOCKED**
([production-readiness.md](production-readiness.md)).

## 1. Documents

| Topic | Document |
|---|---|
| Prerequisites, Stage 17 verification, governance | [prerequisite-assessment.md](prerequisite-assessment.md) |
| Security architecture, trust boundaries, data flows | [security-architecture.md](security-architecture.md) |
| Threat model (STRIDE) | [threat-model.md](threat-model.md) |
| Control register | [security-control-register.md](security-control-register.md) |
| Authentication, MFA, sessions | [authentication-security.md](authentication-security.md) |
| Authorization, tenant isolation, privileged access | [authorization-review.md](authorization-review.md) |
| Secrets inventory, secret manager, rotation | [secrets-management.md](secrets-management.md) |
| Cryptography | [cryptography.md](cryptography.md) |
| Host hardening baseline | [infrastructure-hardening.md](infrastructure-hardening.md) |
| Network, TLS, reverse proxy, service authentication | [network-security.md](network-security.md) |
| PostgreSQL | [database-security.md](database-security.md) |
| Containers | [container-security.md](container-security.md) |
| Web, uploads, Go | [application-security.md](application-security.md) |
| API and DAST | [api-security.md](api-security.md) |
| Dependencies, SBOM, image scanning, provenance, CI/CD | [supply-chain-security.md](supply-chain-security.md) |
| Audit integrity and performance, retention, monitoring, alerting | [security-monitoring.md](security-monitoring.md) |
| Incident response, runbooks, penetration-test package | [incident-response.md](incident-response.md) |
| Backups, restore drill, failure drills | [backup-recovery.md](backup-recovery.md) |
| Disaster recovery, RPO/RTO, HA, worker resilience | [disaster-recovery.md](disaster-recovery.md) |
| Deployment architecture, environments, releases, migrations, rollback | [deployment-security.md](deployment-security.md) |
| Privacy | [privacy-review.md](privacy-review.md) |
| Compliance evidence | [compliance-evidence.md](compliance-evidence.md) |
| Tests and results | [security-testing.md](security-testing.md) |
| Findings register and known issues | [known-issues.md](known-issues.md) |
| Production readiness | [production-readiness.md](production-readiness.md) |

## 2. Code map

| Area | Files |
|---|---|
| Migration | `migrations/20261023090000_security_hardening.sql`: `audit.verify_chain_range`, `audit.chain_head`, `audit.chain_checkpoints`, `audit.chain_verifications`, `audit.retention_policies` (seeded), `app.ops_security_alerts` (+ events; machine `ops_security_alert`), `app.ops_backup_runs`, permissions `security.alert.view/manage`, payment status-poll index, runtime grants |
| Audit verification | `internal/audit/chain.go` (Verifier: FULL in parallel ranges, INCREMENTAL from MAC checkpoints; OpsSnapshot); `internal/app/auditchain.go` (worker jobs); worker config `AUDIT_CHECKPOINT_KEY`, `AUDIT_VERIFY_INTERVAL`, `AUDIT_FULL_VERIFY_INTERVAL` |
| Poll audit dedupe | `internal/payments/attempts.go` (`completeCall`) |
| Keys | `internal/platform/config` (`secretstore` provider, `ResolveManaged`, previous keys, key separation), `internal/platform/crypto` (AEAD previous keys, `OpenWithStatus`), `internal/storage/service.go`, `scan.go` (SSE-C masters by key id), `internal/app/keys.go`, `identity.go`, `verification.go` |
| Authentication fix | `internal/auth/me.go` (F-S18-01); `apps/web/src/components/account/change-password-form.tsx` |
| Security signals and alerts | `internal/auth/security_signals.go`, `internal/operations/security.go` + `http.go` routes, `internal/app/security.go` (worker scan), `operations.go` (sources) |
| Hardening | `internal/platform/config` (staging rules), `internal/app/routes.go` + `worker.go` (readiness categories), `internal/platform/logging` (scrubber), `internal/platform/httpx/envelope.go` (503 on dependency outage) |
| Operator CLI | `apps/api/cmd/fundzimctl/verify.go` (`verify audit|ledger|objects`), `backup.go` (`backup record`), `main.go` (migration session lock); `internal/app/verify.go`; `storage.SampleClean` |
| Backups / DR / host | `scripts/backup/{backup.sh,manifest.py,restore-drill.sh,drill_user.py,failure-drills.sh}`, `scripts/dr/exercise.sh`, `scripts/host/baseline-check.sh`, `deploy/docker/postgres/init/30-backup-role.sh` |
| Containers | `compose.yaml` (hardening, segmentation, digests, Valkey password off argv), `deploy/docker/api.Dockerfile`, `web.Dockerfile` (digests, upgrade, npm removed) |
| CI | `.github/workflows/ci.yml` (gosec; images job: SBOM, Trivy gate, checksums, provenance, artifacts) |
| Web | `apps/web/src/app/admin/security/**`, `src/components/admin/operations/security.tsx`, `src/lib/operations/{api,types,errors,navigation}.ts`, forms `method="post"`, Next 16.4.2 |
| API contract | `api/openapi/fundzim-v1.yaml` (tag "Security operations (Stage 18)", 6 paths, 8 schemas) |
| Tests | `internal/archtest/financial_gates_test.go`, `internal/deploytest/deploy_test.go`, `internal/operations/security_test.go`, `internal/platform/secretstore/tls_test.go`, config/crypto/storage/logging/httpx/app unit tests, `tests/integration/security_{gates,auth,audit,keys,alerts,migration,perf}_test.go`, web `security-alerts.test.tsx`, `change-password-form.test.tsx`, `credential-forms.test.ts`, E2E `staff-security.spec.ts` |

## 3. Brief sections → delivery

| § | Requirement | Delivered | Notes |
|---|---|---|---|
| 2–3 | Audit, Stage 17 handover | prerequisite-assessment.md | no PR, not merged, not accepted; LR-076 correction |
| 4, 82 | Financial gates + regression tests | archtest + DB tests | |
| 5–7 | Architecture, threat model, control register | three documents | |
| 8–12 | Authentication, MFA, RBAC, PAM, sessions | review + F-S18-01 fix | WebAuthn, MFA reset, break-glass workflow open |
| 13–16 | Secrets inventory, manager, rotation, exposure | `secretstore`, overlap, scrubber | HSM/KMS not selected |
| 17–19 | Cryptography, TLS, service authentication | documents + TLS failure test | no TLS termination exists |
| 20–22 | Database, tenant isolation, object storage | backup role, grants tests, verify objects | |
| 23–26 | Containers, host, network, proxy | compose hardening + tests, host check, segmentation | production network = Stage 20 |
| 27–36 | API, web, uploads, payments, ledger, payouts, reconciliation, risk, notifications, discovery | reviews, DAST, fixes (Next, forms, 503) | |
| 37–41 | Dependencies, SBOM, image scanning, provenance, CI/CD | CI jobs + workflow tests | signing pending |
| 42–45 | Security tests, SAST, DAST, pentest package | security-testing.md, incident-response.md §5 | no pentest performed |
| 46–49 | Audit coverage, integrity, performance, retention | ADR-050 §3–4, registry | |
| 50–53 | Monitoring, alerting, incident response, runbooks | security scan, alerts, 10 runbooks | no paging |
| 54–59 | Backups, security, consistency, object storage, secrets recovery, restore tests | scripts + drills | off-host copies = infrastructure |
| 60–64 | DR, RPO/RTO, exercises, HA, workers | disaster-recovery.md | HA not claimed |
| 65–72 | Deployment, IaC, environments, startup validation, releases, migrations, rollback, availability | deployment-security.md, session lock, failed-migration test | nothing provisioned or deployed |
| 73, 84 | Capacity and security performance | security-testing.md §6 | development host only |
| 74 | Rate limiting | review + DR (Valkey loss) + perf | |
| 75–78 | Privacy, data subjects, compliance evidence, regulatory dependencies | three documents | |
| 79 | Documentation | this folder, ADR-050, handover, updates | |
| 80–81 | Automated and failure-injection tests | security-testing.md | |
| 85–86 | CI gate, remote CI | ci.yml; completion report | |
| 87–88 | Findings | known-issues.md §1 | no open CRITICAL/HIGH |
| 92 | Stage 19 preparation | STAGE-18-TO-STAGE-19.md | |
