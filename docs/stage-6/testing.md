# Stage 6 — Testing

> Environment: Ubuntu host, Go 1.27.2 (`go.mod` `go 1.27.2`), Node 24.21.0, local compose stack (PostgreSQL 17.11,
> Valkey, Garage, ClamAV 1.5.3, Mailpit, API and worker containers rebuilt on current code). Integration tests run the
> API in-process against the shared development database; scanning, media processing, restriction enforcement and
> notifications run in the worker container. Only commands actually run are reported; final counts and the final CI
> status are in the Stage 6 completion report.

## 1. Summary

| Suite | Command | Result |
|---|---|---|
| Go unit + architecture | `go test -count=1 ./apps/api/... ./internal/... ./migrations/...` | final run: **194 top-level tests passed, 0 failed** (with `-race`, in the golang:1.27.2 container) |
| Race detector | `-race` is not runnable locally (no cgo); run in the `golang:1.27.2` container: `docker run … golang:1.27.2 go test -race …` | found a real data race in a notifications test helper (fixed, F-S6-02); the GitHub Actions `-race` unit job then passed |
| Integration | `set -a; . ./.env; set +a; FUNDZIM_IT_PERF=1 FUNDZIM_IT_API_URL=… FUNDZIM_IT_WEB_URL=… go test -tags integration -count=1 -timeout 40m ./tests/integration/...` | first full run: 95 PASS, 2 FAIL, 2 SKIP. Both failures passed in isolation (§4) and the causes were fixed or isolated. **Final full run (after the migration round trip, on rebuilt containers): 97 PASS, 0 FAIL, 2 SKIP** (320 s; skips: the scale-test-profile and container-stop tests) |
| Route ↔ contract | `TestEveryRouteHasAPolicyAndIsInOpenAPI` | pass after the Stage 6 OpenAPI update (60 operations) |
| OpenAPI lint | `npx @redocly/cli@2.54.3 lint api/openapi/fundzim-v1.yaml --config api/openapi/redocly.yaml` | valid; the 2 pre-existing `/healthz` `/readyz` warnings |
| Web | `npm --prefix apps/web run lint`, `typecheck`, `test`, `test:e2e` | reported by the frontend stream: lint clean, typecheck passes from a clean checkout, Vitest 325 passed (30 files), Playwright 107 passed / 1 skipped (pre-existing mobile-only skip). Final, re-run by the lead after the real-backend alignment on a clean `.next`: lint clean, typecheck passes, **Vitest 331 passed (30 files)**, **Playwright 107 passed / 1 skipped** |
| Migrations | goose up / down / up per migration (each stream) | clean; final round trip of all six Stage 6 migrations (down ×6, up, up = no pending) clean |
| Secrets, dependencies | `scripts/check-secrets.sh`, gitleaks, govulncheck, npm audit | check-secrets: none; gitleaks on staged changes: none (history scan in the completion report); govulncheck: 0 reachable; `npm audit --omit=dev`: 0 (dev-only lint chain KI-03 unchanged) |

## 2. New unit tests

- `internal/campaigns`: Go edge list = migration edge list (`FROZEN` reserved, `ARCHIVED` terminal); content safety
  (HTML tags and entities, `javascript:`/`vbscript:`/`data:` URLs, zero-width/bidi/NUL characters, invalid UTF-8,
  normalisation, "Our data: 12 families" allowed); exact money parsing (decimals, signs, leading zeros, exponents,
  overflow, full-width digits refused); slugs (Unicode folding, markup, word-boundary truncation) and 2 000 random
  public codes; the restriction matrix (generic reason only); tiers are never silently downgraded.
- `internal/campaigns/media`: 8 tests — EXIF/GPS stripped with orientation applied (all 8 orientations), PNG text/time/
  eXIf/iTXt stripped, decompression-bomb headers refused (PNG and JPEG), polyglot/garbage/truncated files refused, type
  mismatch refused, derivative size cap, edge list = migration.
- `internal/campaigns/updates`: 11 tests — XSS payloads including `<img onerror>`, `javascript:` links, Unicode tricks
  (zero-width, bidi, full-width `<`, entities), control-character handling, character-based lengths, policy fails
  closed, edge list = migration.
- `internal/kyc`, `internal/compliance`, `internal/platform/featureflags`: age-attestation rules, restriction levels
  (`LevelForDecision`, `Max`), feature-flag reader.

## 3. Integration tests added in Stage 6

| Test | Covers |
|---|---|
| `TestCampaignLifecycleEndToEnd` | age gate → BASIC → draft; exact money (float/decimal/ZWG refused); XSS and unsafe links refused; If-Match required/stale; IDOR (404, not listed); submission reasons and recorded refusal; KYC → beneficiary (foreign one refused, disclosure without consent refused) → cover approved by the worker → submit (idempotent) → content frozen; KYC reviewer and support refused; ineligible assignee; request changes with owner-visible message only; approve and publish; public page without private data and with "Donations are not yet available."; search; slug enumeration 404; live edit → re-review → public only after approval; pause/resume, suspend (404 publicly; owner cannot resume), reactivate, `GOAL_REACHED` refused, complete, archive; one history row per version; audit actions; email |
| `TestCampaignSelfReviewFourEyesAndPrivacy` | linked reviewer refused in Go and by the DB trigger; HIGH tier pending approval (202); first approver lacking `campaign.decide.high` refused; COMPLIANCE second approval; owner has no admin route; beneficiary not disclosed without consent; four-eyes record in the DB |
| `TestCampaignComplianceRestrictionEnforcement` | real compliance case → SUSPEND proposed (maker) → self-approval refused → second officer approves → worker suspends the live campaign; creation refused with `ACCOUNT_RESTRICTED` (no reason disclosed); reactivation refused; reopen + CLEARED lifts the restriction but the campaign stays SUSPENDED; staff reactivation then succeeds |
| `TestCampaignConcurrency` | 6 concurrent creates with one Idempotency-Key → 1 campaign; 5 concurrent edits with one If-Match → 1 success, 4 × 409; concurrent duplicate submissions → 1 SUBMITTED history row; approve vs reject → exactly one decision; publish vs suspend → consistent final state, never public when suspended |
| `TestCampaignFailureInjection` | restrictions reader unavailable → submission fails closed, status unchanged; session revoked mid-flow → 401; reviewer session gone → refused, campaign still queued |
| `TestCampaignSecurityRechecks` | CSRF-less submit refused and inert; beneficiary swap during review refused; KYC revoked after approval → owner and staff publication refused, refusals recorded; suspended campaign removed from search; unlisted reachable by link but not listed |
| `TestCampaignPerformanceSample` | opt-in (`FUNDZIM_IT_PERF=1`), §5 |
| Campaign media (5, stream M) | upload → scan → re-encode → APPROVED with metadata stripped in served bytes; malware verdict → REJECTED; scanner outage never approves, then recovers; IDOR and validation; a private object can never be linked |
| Campaign updates (5, stream U) | direct vs pre-moderated publishing (incl. RESTRICTED/SUSPENDED/OFFBOARDED owners); suspended campaign refused; IDOR; public listing/hiding; linked moderator refused in the API and in SQL |
| Remediation (7, stream R) | age attestation append-only and BASIC grant/withdrawal; DECLINED blocks BASIC; restrictions applied/lifted through real cases with maker-checker and event payloads; staff link happy path, wrong email, reused/expired token, double link, non-staff caller; KYC decision trigger refuses a linked identity |

## 4. Failures seen and their resolution

- `TestStaffPersonalLink` (first full run): the shared staff fixture's super admins had been revoked by
  `TestIdentityRBACStaffMakerChecker`'s bootstrap reset earlier in the same run. The fixture now re-bootstraps its
  admins when they were revoked; the test passes.
- `TestWorkerCrashedJobIsRescued` (first full run): a 30 s deadline expired while the worker container was busy with
  other tests' jobs; it passed in isolation in 22 s. Recorded as a load-sensitive test (KI-S6-20).

## 5. Performance sample

Local stack, API in-process, sequential requests (concurrency 1), rate limiting off, shared development database with
about 53 campaigns (17 live):

| Operation | n | p50 | p95 |
|---|---|---|---|
| `POST /campaigns` (create draft) | 30 | 11 ms | 18 ms |
| `PATCH /campaigns/{id}` (edit draft) | 30 | 10 ms | 16 ms |
| `GET /campaigns/mine` | 50 | 2.2 ms | 4.1 ms |
| `GET /campaigns/{id}/eligibility` | 50 | 5.4 ms | 7.6 ms |
| `GET /public/campaigns?q=` (search) | 50 | 1.9 ms | 5.0 ms |
| `GET /admin/campaigns/review` (queue) | 50 | 2.2 ms | 5.3 ms |

These are development numbers on a tiny dataset; they say nothing about production capacity. Publication and public
page retrieval were exercised in the lifecycle test but not timed separately.

## 6. Not tested / limits

- Local `-race` (no cgo; run in a container and on CI instead).
- Two-replica limiter and Valkey-stop tests (not re-run in Stage 6).
- PostgreSQL outage and worker outage were not injected directly; the restriction-reader failure and scanner outage
  stand in for dependency failures, and the outbox retry behaviour is covered by the Stage 3/4 worker tests.
- Staff admin pages against the real API: **not smoke-tested** (no staff session was created on the shared dev DB); they are covered by mock-API e2e and by the backend integration tests of the same endpoints. Owner and public flows were smoke-tested on the real stack.
