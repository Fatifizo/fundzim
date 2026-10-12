# Stage 7 — Testing

What was added, what it covers, and what was run. The final local runs (2026-10-12, on the final tree) are in §0;
remote CI is recorded in §6. No test was skipped, disabled or weakened to get a green run.

## 0. Final local runs (2026-10-12)

| Gate | Command | Result |
|---|---|---|
| Go format, vet, build | `gofmt -l …; go vet ./apps/api/... ./internal/... ./migrations/...; go build ./apps/api/cmd/...` | clean |
| Go unit, race detector | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` (in `golang:1.27.2`) | 29 packages ok; 201 tests + 60 subtests passed, 0 failed |
| Integration | `go test -tags integration -count=1 ./tests/integration/...` with `FUNDZIM_IT_API_URL`, `FUNDZIM_IT_WEB_URL`, `FUNDZIM_IT_PERF=1`, under the host lock | 99 passed, 0 failed, 2 skipped (env-gated: multi-replica API profile; stopping the Valkey container) |
| Web lint, typecheck | `npm run lint`, `npm run typecheck` | 0 errors |
| Vitest | `npx vitest run` | 440 / 440 passed (44 files) |
| Mock-API Playwright | `npm run test:e2e` | 147 passed, 1 skipped (see §5) |
| Real-stack Playwright | `npm run test:e2e:real`, twice, under the host lock, after rebuilding API/worker/web | 47 / 47 passed, both runs |
| Dependencies | `npm audit --omit=dev` | 0 vulnerabilities |
| OpenAPI | `redocly lint` | valid, 2 warnings (pre-existing) |
| Secrets | `scripts/check-secrets.sh`; gitleaks history | none; history clean (working-tree findings only in git-ignored `.env` and `e2e-real/.state/`) |

Two Go integration tests (`TestCampaignLifecycleEndToEnd`, `TestVerificationKYCEndToEnd`) read only the first page
of a staff queue and failed once the shared development database held more than one page of open reviews (left by
repeated real-stack runs). They now follow `next_cursor` over every page; the ID-number leak check covers every
page. The same first-page race existed in the real-stack queue walkers (a client-side "Next page" click returned
before the new page rendered): they now navigate to the link's `href`.

## 1. Go unit tests (new or changed)

| Test | Covers |
|---|---|
| `TestRequireIfMatch` (`internal/platform/httpx`) | `If-Match` parsing: quoted or bare positive integer; missing/unparsable → 428 `PRECONDITION_REQUIRED` |
| `TestCachePolicyRespectedAndErrorsNeverCached` (httpx) | `WriteJSON` keeps the handler's `Cache-Control`; errors are `no-store` and drop the `ETag` (the Stage 6 defect) |
| `TestRetryAfterFromError` (httpx) | `Retry-After` taken from an error's `RetryAfter()`, rounded up; 5 s fallback |
| `TestDefaultPolicyValues`, `TestPolicyValidate`, `TestRetryAfterSeconds` (`internal/platform/ratelimit`) | New policies `public.report.ip` 5/h, `public.report.campaign` 50/h, `public.event.ip` 120/min; validation; retry delay |
| `TestCleanTextRefusesMarkupAndUnsafeContent`, `TestSlugsAndPublicCodes` (`internal/campaigns`) | Plain-text rules used by reports and search; slug/public-code format used by canonical resolution |

## 2. Integration tests (`tests/integration`, real Postgres, Valkey, Garage, ClamAV, Mailpit)

`TestPublicExperienceEndpoints` (`public_experience_test.go`) — passed in the final run:

- API consistency: 428/412 for `If-Match`, `{items, next_cursor}` lists.
- Detail: canonical slug, public code, trust values, `category_name`; `public, no-cache` + ETag; `If-None-Match` → 304;
  wrong title part + right code resolves to the canonical slug; `/public/campaign-codes/{code}`.
- Search: totals, category by slug, validation (length, control characters).
- Categories (`public, max-age=300`) and featured (staff pick first, then recently published).
- Events: aggregate counters only, unknown subjects ignored, no query text.
- Reports: honeypot silently dropped, same-day duplicate silently accepted, campaign status unchanged, owner gets 404
  on the queue, escalation → compliance case (source `REPORT`), dismissal of a second report (`DISMISSED`, no
  escalation event, no status change, 409 on a second triage), per-IP rate limit (5/hour) with a separate client.
- Sitemap data; then suspension: detail 404 with `no-store` and no `ETag` despite `If-None-Match`, campaign gone from
  sitemap data, featured, search and short-code lookup; staff featured view shows `shown: false`.
- **Recursive allow-list**: every key of every public response (nested) must be in `publicAllowedKeys`; ids of owners,
  beneficiaries or reviewers, emails, KYC, risk, compliance, notes and documents fail the test.

Earlier Stage 6 integration tests were updated for ADR-038 §7 (428/412 instead of 422/409/400, `items` instead of
`media`/`updates`/arrays). Their assertions were changed to the new contract, not loosened.

## 3. Performance

At 50 000 campaigns (26 854 live PUBLIC) on a scratch database: plans, API latency and the 2 s search timeout, before
and after the R1–R5 changes (for example categories 922 → 29 ms and sitemap 1 054 → 185 ms, p50 sequential). Full
method, environment, caveats and numbers: [performance.md](performance.md). These are developer-VM measurements under
shared load, not capacity figures.

## 4. Web unit and component tests (Vitest)

New Stage 7 files include `lib/share/links.test.ts`, `lib/site/url.test.ts`, `lib/seo/robots.test.ts`,
`lib/seo/json-ld.test.ts`, `lib/analytics/events.test.ts`, `lib/campaigns/{public-normalise,report,staff-public,
errors-stage7}.test.ts`, and `__tests__/{home-page,public-campaign-page,campaign-card,share-controls,report-dialog,
service-worker,site-header,states}.test.tsx`. Component tests run axe-core (`src/test/axe.ts`, without
`color-contrast`, which jsdom cannot evaluate). Final run: 440 / 440 (§0).

## 5. Mock-API Playwright suite (`apps/web/e2e`, desktop and mobile Chromium)

New `e2e/public.spec.ts` and extended `site.spec.ts` / `staff-public.spec.ts`: home sections, discovery (search,
count, category, sort, keyset pagination, no query text in analytics), API-failure state, categories, detail
(canonical redirect, short link, trust, updates, metadata, JSON-LD, disabled donations), share links and copy
feedback, analytics off under DNT/GPC, report dialog (validation, honeypot, rate-limited message), robots and
sitemap with and without `PUBLIC_SITE_URL`, private pages `noindex`, axe on every public page, no horizontal overflow
at 320/375 px, the service worker. Some tests are guarded to one browser or viewport (`test.skip` for Chromium-only
clipboard/service-worker checks and the mobile-only menu test), which accounts for skips. Final run: 147 passed,
1 skipped (§0).

## 6. Real-stack Playwright suite (`apps/web/e2e-real`)

Against the running compose stack with nothing mocked (`playwright.real.config.ts`; projects `staff-desktop`,
`desktop-chromium`, `mobile-chromium`). Provisioning through the real API and Mailpit (bootstrap ceremony, invitations
with TOTP, maker-checker role grants, KYC approved by a real reviewer, ClamAV-scanned covers). Specs:
`staff-moderation.spec.ts` (review queue, assign, request changes, resubmission, approval with step-up, HIGH-tier four
eyes, publish, suspend/reactivate, self-approval refusal through a real staff link), `public.spec.ts`,
`report.spec.ts`, `featured.spec.ts`, `a11y.spec.ts` (axe + CSP on public and staff pages). Run locally under the
host lock `flock /tmp/claude-1000/fundzim-stack.lock` because it shares the development database with the Go
integration tests. A smoke run passed early in the stage; the suite then passed 29/29 twice locally before further
specs (featured, a11y) were added. Final: **47 / 47 passed in two consecutive runs** (2026-10-12; projects
staff-desktop, desktop-chromium, mobile-chromium; about 2.3 minutes each on a warm stack).

CI: new job "Real-stack E2E (compose + Playwright)" in `.github/workflows/ci.yml` (throwaway secrets, infrastructure,
migrations, ClamAV signatures, API/worker/web containers, the suite, logs on failure). Result: see the completion
report. Local runs are not CI evidence.

## 7. Not tested

- Manual screen-reader and keyboard audits (see [accessibility.md](accessibility.md) §3).
- Lighthouse / Core Web Vitals on a throttled mobile profile (KI-S7-05).
- Browsers other than Chromium (Firefox, Safari/WebKit), real low-end Android devices.
- Link previews in WhatsApp, Facebook and X, and search-engine crawling (no public deployment).
- Load on the public web tier (only API and database latency were measured).
