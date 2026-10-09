# Stage 5 Testing Evidence

All results below were produced in this session on 2026-10-09 on the development machine (Linux, Go
toolchain go1.27.2 — `go.mod` `go 1.27.1` + `toolchain go1.27.2`, Node 24.21.0; PostgreSQL 17.11, Valkey,
Garage v2, ClamAV 1.5.3 and Mailpit in containers). Nothing is reported as passing that was not run.
**GitHub Actions CI has not run** (KI-S5-01 / KI-S4-01), so the `-race` detector (CI only; no C compiler
locally) has **not** been executed for Stage 5 code. The final gate results below were re-run by the lead on
the committed code on 2026-10-09.

## 1. Summary

| Suite | Command | Result |
|---|---|---|
| Go unit + architecture tests | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | **162 top-level tests passed, 0 failed, 0 skipped** (25 packages with tests) |
| Integration (real stack) | `set -a; . ./.env; set +a; go test -tags integration -count=1 ./tests/integration/...` | Final run with `FUNDZIM_IT_PERF=1 FUNDZIM_IT_API_URL=… FUNDZIM_IT_WEB_URL=…`: **73 passed, 0 failed, 2 skipped** (148 s). The skips are `TestDistributedLoginLimitAcrossReplicas` (needs the scale-test profile) and `TestRateLimitFallbackWhenValkeyContainerStopped` (stops a container); neither was run in Stage 5 |
| Opt-in integration | `FUNDZIM_IT_PERF=1 FUNDZIM_IT_API_URL=http://127.0.0.1:8080 FUNDZIM_IT_WEB_URL=http://127.0.0.1:3000 … -run 'PerformanceSample\|TestRunningAPI\|TestFrontendReachesBackend'` | `TestIdentityPerformanceSample`, `TestRunningAPI`, `TestFrontendReachesBackend`, `TestVerificationPerformanceSample` **passed**. `TestDistributedLoginLimitAcrossReplicas` and `TestRateLimitFallbackWhenValkeyContainerStopped` were **not run** in Stage 5 |
| Migrations | `fundzimctl migrate down` × 7 then `migrate up` on the development database | all seven Stage 5 migrations rolled back and re-applied cleanly; a second `up` reports no pending migrations |
| Route ↔ contract | `TestEveryRouteHasAPolicyAndIsInOpenAPI` (in the unit run) | passes after 51 operations were added to `api/openapi/fundzim-v1.yaml` |
| Vulnerabilities | govulncheck | 0 reachable after the go1.27.2 toolchain bump (GO-2026-6617, see [security-review.md](security-review.md) F-S5-07); `npm audit --omit=dev`: 0; full `npm audit`: 5 high in the dev-only lint chain (unchanged KI-03) |
| Web (lint, typecheck, Vitest, Playwright) | `npm --prefix apps/web run lint`, `typecheck`, `test`, `test:e2e` | re-run by the lead: lint clean, typecheck OK, **Vitest 281 passed (20 files)**, **Playwright 83 passed / 1 skipped** (pre-existing mobile-only skip; includes the production build). A manual smoke run of the user KYC flow against the real stack (web container → API → worker → ClamAV) passed; staff pages are covered against the mock API only |
| Real-stack web smoke | through the web app against the compose API | register → email and phone verification → KYC draft → PDF/PNG upload scanned **CLEAN by ClamAV within ~5 s** → incomplete submit shows a field-level message → successful submit. Found one shape mismatch (requirements `{document_types[], satisfied}`), fixed in the UI. **Staff/admin pages were exercised against the mock API only** (KI-S5-08) |
| OpenAPI | `go test ./internal/app/` (every route has a policy and is in the spec); `npx @redocly/cli@2.54.3 lint` | pass; lint valid with the 2 pre-existing `/healthz` `/readyz` warnings |
| Secrets | `scripts/check-secrets.sh`; gitleaks | check-secrets: none; gitleaks (working tree): findings only in the git-ignored local `.env` (generated dev secrets); gitleaks on full git history (after the Stage 5 commits): no leaks |
| CI | GitHub Actions | **not running** (KI-S4-01); the workflow was extended (ClamAV service, Stage 5 tests) but has never executed |

## 2. New unit tests

| Package | Tests |
|---|---|
| `internal/kyc` | seeded v1 policy validates; unsafe policies refused (minor/restricted without four eyes, unknown document type, empty requirement, missing risk level, validity range, information-request expiry, missing `adult_age`); ID-number masking and normalisation; reason codes and level ordering; identity patch validation (markup, ISO-2, types, number format; **no** national-ID structure rule) |
| `internal/payouts` | mobile-wallet normalisation (valid Zimbabwean mobile; foreign, landline, short and junk refused); bank account + bank code; unknown rail |
| `internal/beneficiaries` | age calculation including the leap-year birthday edge (**found a real off-by-one bug, fixed**); text cleaning |
| `internal/platform/verifcase` | Go edge list equals the migration's `verification_case` rows |
| `internal/storage` | sniffing, multipart parsing, scanner (clamd protocol, dev scanner refusal), tickets (binding, expiry, tampering), service configuration |
| `internal/risk`, `internal/compliance` | model evaluation and fail-closed limits; state machine and decision rules |
| `internal/platform/config` | Stage 5 keys distinct, scanner policy, public storage credential optional but all-or-nothing |

## 3. Integration tests added in Stage 5 (28)

**Verification** (`tests/integration/verification_test.go`; staff are created through the real bootstrap,
invitation and maker-checker role-grant flows; scanning runs in the worker container with real ClamAV):

| Test | Proves |
|---|---|
| `TestVerificationKYCEndToEnd` | prerequisites (phone); one open case; `If-Match`; ID number never echoed; incomplete submit refused; IDOR (foreign PATCH/submit/upload/document → 404); `text/html` refused; untrusted filename not reflected; no access before CLEAN; ticket download headers; ticket useless for another user, another session or without a ticket; idempotent resubmit; no uploads/removals after submit; SUPPORT denied queue and documents; queue/detail never show the number; assignee eligibility; unassigned reviewer denied documents and decisions; reviewer download audited exactly once; reveal: reviewer 403, COMPLIANCE with justification → audited; invalid reason code refused; approval → `IDENTITY_VERIFIED`; decision records "screening not performed"; worker mirrors the level; approval email delivered; ID number absent from audit metadata and outbox payloads |
| `TestVerificationSelfReviewAndDuplicateIdentity` | reviewer linked to the subject's personal account cannot be assigned (self or by others); duplicate ID number → ENHANCED, first approval `202`, same reviewer second approval 403, different reviewer → `409 DUPLICATE_IDENTITY_ACTIVE`; no self-decision rows exist |
| `TestVerificationConcurrentReviewActions` | 8 concurrent case creations → 1 × 201 + 7 × 409; assignment race; approve vs reject → exactly one decision; case events one per version; 6 concurrent submits → one SUBMITTED event; uploads racing after submission refused |
| `TestVerificationKYBRepresentativeAndIsolation` | outsider 404; member read-only and sees no persons/documents; unverified representative cannot submit; verified representative does **not** verify the organisation; approval → `ORG_KYB_VERIFIED` |
| `TestVerificationBeneficiaryFourEyes` | minor authority and age rules; IDOR; incomplete submit; COMPLIANCE lacks the permission; first approval 202 without approving; same reviewer refused; second reviewer approves; decided beneficiary not editable |
| `TestVerificationPayoutDestinationNeverVerifiedByFormatOrMock` | invalid wallet refused; float/unknown field refused; format-valid → UNVERIFIED; identifier masked; IDOR; dev mock recorded non-production and not PASS; approval needs name match and evidence; **direct SQL** cannot set VERIFIED or insert a non-production ownership PASS; documentary route → VERIFIED with `eligible_for_payout: false`; detail change → UNVERIFIED; notification email |
| `TestVerificationFailureInjection` | EICAR via real ClamAV → REJECTED, not downloadable, submit refused; expired ticket → 403; **storage outage** (unreachable endpoint) → upload fails ≥ 500 and nothing attached; reviewer with revoked session cannot act |
| `TestVerificationQueuePagination` | page size 1 through the merged queue with a forced timestamp tie: no skips, no repeats; invalid cursor 422. **Mutation-checked**: fails against the old timestamp-only cursor |
| `TestVerificationPerformanceSample` (opt-in) | latencies below |

**Storage** (`storage_pipeline_test.go`, 9): upload → scan → promote; EICAR rejected; scanner outage then
recovery; interrupted scan fenced; concurrent scans promote once; interrupted upload cleaned up; upload
rejections store nothing; stored-object DB guards; body-limit exemption only for the upload route.

**Compliance and risk** (`compliance_risk_test.go`, 10): gateway-only role; RLS hides STR cases; maker-checker
and subject conflicts; organisation member cannot decide; concurrent decisions exactly one wins; duplicate
delivery opens one case; signal dedupe and assessment; limits maker-checker and baseline; risk escalation
opens a compliance case through the outbox; HTTP workflow.

## 4. Performance sample (local stack, rate limiting off, single API in-process)

| Operation | n | p50 | p95 | max |
|---|---|---|---|---|
| `GET /admin/verification/cases?limit=50` | 50 | 4.7 ms | 7.9 ms | 44 ms |
| `GET /kyc/status` | 50 | 2.3 ms | 3.8 ms | 6.8 ms |
| `POST /verification/documents` (≈200 KB PDF) | 10 | 36 ms | 60 ms | 60 ms |

Scan latency to CLEAN in the real-stack web smoke was about 5 s (worker + ClamAV). These are development-
machine samples, not capacity figures; the only assertion is a 2 s p95 ceiling.

## 5. Not tested / limits

`-race` (CI not running; no local C compiler); two-replica limiter and Valkey-stop tests (not re-run in
Stage 5); staff/admin web pages against the real API; key rotation (not implemented); production ClamAV
deployment.
