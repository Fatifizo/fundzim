# Real-stack E2E (Playwright against the running compose stack)

Browser tests against the **real** FundZim stack: `fundzim-web` at <http://localhost:3000>, the Go API, the
worker, PostgreSQL, Valkey, Garage, Mailpit (<http://127.0.0.1:8025>) and ClamAV. Nothing is mocked and
Playwright starts no server. Contract: `docs/stage-7/interface-contracts.md` §6. The mock-API suite in `../e2e`
is separate (`npm run test:e2e`).

## Run locally

```bash
./scripts/dev-env-init.sh                                   # once: .env with generated secrets
sg docker -c "docker compose up -d --build" < /dev/null     # full stack; wait until ClamAV is healthy
cd apps/web && source ~/.nvm/nvm.sh
npx playwright install chromium                             # once
flock /tmp/claude-1000/fundzim-stack.lock npm run test:e2e:real
```

**Hold the host lock** (also when rebuilding or restarting containers: a restart during a run fails it with
503s). The suite and the Go integration tests (`tests/integration`) share the development
database, and both run the super-admin bootstrap ceremony, which first revokes every active SUPER_ADMIN. Run
either one only while holding the exclusive lock `/tmp/claude-1000/fundzim-stack.lock` (`flock <lock> <command>`),
or each breaks the other mid-run. CI does not need it: each job has its own stack.

Use `http://localhost:3000`, not `127.0.0.1`: the API checks `Origin` against `APP_PUBLIC_URL`. Overrides:
`FUNDZIM_E2E_WEB_URL`, `FUNDZIM_E2E_MAILPIT_URL`, `FUNDZIM_E2E_PUBLIC_SITE_URL` (the web container's
`PUBLIC_SITE_URL`; defaults to the web URL).

## What it does

`global-setup.ts` provisions everything through the real API (via the web container's `/api/v1` proxy) and
Mailpit, using `support/`:

- **Staff fixture (persistent):** two SUPER_ADMINs from the real bootstrap ceremony
  (`scripts/e2e/bootstrap-admins.sh` → `go run ./apps/api/cmd/fundzimctl bootstrap-admins`), invitations accepted
  from Mailpit with TOTP enrolment (RFC 6238 in `support/totp.ts`, never reusing a time step), and REVIEWER,
  COMPLIANCE, KYC_REVIEWER and ADMIN holders granted through the maker-checker API (admin A requests, admin B
  approves). Kept in `.state/fixture.json` and re-validated every run; recreated when it no longer works.
- **Per run:** one owner (email link and phone OTP from Mailpit, age declaration, KYC documents uploaded as
  multipart and scanned CLEAN by ClamAV, approved by the KYC reviewer), one new REVIEWER for the linked-account
  test, and 22 campaigns with scanned covers: moderation fixtures (submitted), public fixtures (published,
  suspended, unlisted, draft; visible, hidden and pending-moderation updates) and a 13-campaign catalogue
  sharing one search token.

Every moderation step under test is done **in the browser** (`staff-moderation.spec.ts`): staff sign-in with
password + TOTP, the review queue, assign, start review, request changes, owner resubmission, approval with
step-up when the API asks, the HIGH-tier four-eyes flow (first approval, the first approver refused by UI and
API, COMPLIANCE second approval), owner and staff publication, suspension and reactivation with the public page
and discovery checked, and the self-approval refusal for a staff member linked through the real staff-link
ceremony (request in the staff UI, token confirmed from Mailpit by the personal account).
`staff-moderation.spec.ts` also covers four eyes when a COMPLIANCE officer gave the first approval
(SECOND_APPROVER_MUST_DIFFER). `public.spec.ts` (desktop and mobile Chromium) covers discovery, detail, sharing,
`/c/{code}` and wrong-title redirects, privacy, robots.txt (following `SITE_INDEXING`) and the sitemap.
`a11y.spec.ts` (desktop and mobile) runs axe (serious/critical) and CSP checks on public pages and the staff
reports and featured pages. Desktop only: `report.spec.ts` (report dialog → triage in the staff reports page,
contact email never shown, escalation) and `featured.spec.ts` (a COMPLIANCE officer features and removes a staff
pick in the UI; the home page follows).

Projects run in order: `staff-desktop` (staff moderation, once), then `desktop-chromium` and `mobile-chromium`.
They do not depend on each other, so a staff failure does not hide the public results; staff pages reuse the
sessions signed in through the UI and sign in again when those have expired.

## Direct database access

Only the test-environment fixtures allowed by contract §6, as scripts that refuse to run unless `APP_ENV` is
`development` or `test`:

- `scripts/e2e/reset-super-admins.sh` — revokes active SUPER_ADMIN assignments before the bootstrap ceremony,
  exactly as the Go helper `resetSuperAdmins`.
- `scripts/e2e/link-staff-account.sh` — fallback staff↔personal link (not used by the suite, which uses the
  real ceremony).

## Rate limits

Every request comes from one client address, so the API's per-IP limits apply to the whole suite **and to
everything else on the host** (other test suites, manual use). The limiter is GCRA: a burst of N, then a
continuous refill. A run registers **one** account (`auth.register.ip`: 5/hour, one more every 12 minutes) and
the desktop project submits **one** report (`public.report.ip`: 5/hour, same refill). When a budget is empty the
client waits for the time the API states in its error message (a rate-limited request is refused before it does
anything, so sending it again is safe) and the report spec retries every two minutes for up to 13 minutes. Expect
a run to take 12+ minutes when the registration budget is drained; a run with budget left takes about 3 minutes
(about 45 s of provisioning on a warm stack). Staff sessions are reused across runs, and spare unlinked reviewers
are created in batches, to stay inside `auth.login.ip` (20/5 min) and `auth.token.ip` (20/15 min).

## State

`.state/` is git-ignored (mode 0600): staff TOTP secrets and session cookies of synthetic test accounts, the
run's data, TOTP steps already used, browser storage states. Delete it to start over (the next run creates new
staff).
