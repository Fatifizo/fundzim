# Stage 20 — Security certification

**Area result: BLOCKED for production; PASS WITH CONDITIONS for the engineering controls that exist.** Stage 18's
findings were re-verified against the candidate code on 2026-10-25, not copied. No penetration test has taken place;
none is claimed.

## 1. Carried Stage 18 production conditions — current status

| Finding | Verification in Stage 20 | Status |
|---|---|---|
| F-S18-12 / KI-S18-06 — no phishing-resistant staff MFA | no WebAuthn/passkey code in `internal/` or `apps/web/src` | **OPEN** |
| F-S18-03 / KI-S18-06 — no MFA reset workflow | no reset code path; documented manual procedure only (Stage 18 authentication-security.md §5) | **OPEN** |
| F-S18-04 / KI-S18-07 — break-glass without workflow or audit tag | `app.break_glass_grants` is read by `auth/sessions.go:245`; no request/approve/revoke API; an alert fires while a grant is active (Stage 18) | **OPEN** |
| F-S18-05 / KI-S18-16 — internal metrics/readiness unauthenticated | unchanged; readiness returns categories only (fixed in Stage 18); listeners on the internal network | **OPEN (residual)** — acceptable only with evidenced network isolation in the real deployment |

## 2. Control assessment

| Control | Evidence | Result |
|---|---|---|
| Authentication, sessions, step-up | Stage 4/18 tests (all passed in the Stage 20 isolated run: `TestIdentity*`, `TestSecurityStaffPasswordChangeNeedsTOTPStepUp`) | PASS WITH CONDITIONS (WebAuthn, MFA reset) |
| Authorization and tenant isolation | `TestEveryRouteHasAPolicyAndIsInOpenAPI`, IDOR tests, `TestFinancialPayoutCrossTenantUnderConcurrency` (isolated run) | PASS |
| Secret management | `secretstore` provider, `secretref://` CHECKs, OpenBao tests (isolated run `TestSecurityKeysFromSecretManager`) | PASS WITH CONDITIONS (production secret manager not deployed) |
| KMS/HSM and key lifecycle | none selected (KI-S18-02); rotation overlap exists; no re-encryption job (KI-S18-03) | BLOCKED |
| Container vulnerabilities | Trivy gate in CI passes (no *fixable* CRITICAL/HIGH) on the candidate (run 40034115480); unfixed Debian CVEs in the web runtime image (KI-S18-10, Stage 18: 1 CRITICAL zlib/minizip, 56 HIGH) | decision required — §4 |
| Artifact signing / provenance | unsigned provenance record; no signing (KI-S18-08, F-S20-06) | FAIL for production |
| Dependency security | govulncheck and `npm audit` in CI (success on the candidate); gosec 0 issues (Stage 19) | PASS |
| Branch protection | **not configured** (F-S20-01, verified via the API) | FAIL |
| Backup and restore | encrypted backups, MAC manifest; restore drill repeated in Stage 20 (infrastructure-dr-readiness.md §3) | PASS WITH CONDITIONS (no off-host immutable copy, RPO/RTO unapproved) |
| Audit integrity | hash chain + MAC checkpoints, tamper/interruption tests (isolated run); not anchored externally (KI-S18-05) | PASS WITH CONDITIONS |
| Incident response | framework and 10 runbooks (Stage 18); no on-call, no paging (KI-S18-15) | BLOCKED |
| Security monitoring | persisted alerts from a scan each minute; delivery only inside the application | BLOCKED (no delivery destination) |
| Independent penetration test | package prepared; **no test performed** (KI-S18-11); DAST passive only (KI-S18-21) | NOT_VERIFIED |

## 3. Risk decisions required

Each open MEDIUM finding above needs either remediation or an explicit, recorded risk acceptance by an authorised
owner (none exists). A launch cannot proceed on an implicit acceptance.

## 4. Unpatched CRITICAL/HIGH container vulnerabilities

Stage 18 (KI-S18-10) recorded, for the web runtime image, 1 CRITICAL (zlib/minizip) and 56 HIGH Debian bookworm CVEs
**with no published fix**. Stage 20 re-scan: see §5. Exposure facts: the web container is a Next.js server on the
`edge` network only (no data-service access, `internal/deploytest`), read-only root, no capabilities. Whether any
vulnerable library code path is reachable from request handling has **not been analysed**; "no patch available" is not
evidence of harmlessness. Required: a risk decision (accept with compensating controls, or change the base image, e.g.
distroless/alpine, and re-scan).

## 5. Stage 20 container re-scan

Trivy 0.75.0 (the CI version), database of 2026-10-25, `--severity CRITICAL,HIGH`, against the images the disposable
stack ran (`fundzim/api:local`, `fundzim/web:local`, built from the candidate code):

| Image | CRITICAL | HIGH | With a published fix |
|---|---|---|---|
| `fundzim/api` (distroless, static Go binary) | 0 | 0 | — |
| `fundzim/web` (Next.js on Debian bookworm) | 1 — CVE-2023-45853 (`zlib1g` 1:1.2.13.dfsg-1, minizip) | 56 | **0** |

Identical to Stage 18 (KI-S18-10). The CI gate passes because it fails only on fixable CRITICAL/HIGH. Exposure analysis
and a risk decision are still required (§4); a base-image change is the engineering option.
