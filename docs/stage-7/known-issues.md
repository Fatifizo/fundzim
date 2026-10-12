# Stage 7 — Known issues

## 1. Stage 6 issues carried

| Stage 6 issue | Status after Stage 7 |
|---|---|
| KI-S6-08 `If-Match` errors differ | **RESOLVED** for the campaign family (428/412, ADR-038 §7); Stage 5 verification and compliance APIs unchanged — KI-S7-14 |
| KI-S6-09 path parameter names differ | **RESOLVED** (`{campaign_id}` throughout the campaign family) |
| KI-S6-10 review queue ignores `?status=` | RESOLVED in Stage 7 (campaign-status filter) |
| KI-S6-18 search uses `simple`, no Shona/Ndebele handling | OPEN — KI-S7-08 |
| KI-S6-19 staff pages tested only against the mock API | **RESOLVED with conditions** — real-stack suite in `apps/web/e2e-real` (47 / 47 passed in two local runs; CI: [testing.md](testing.md) §6) |
| KI-S6-23 Stage 1–5 acceptance not recorded | OPEN — KI-S7-01 |
| KI-S6-01 – 07, 11 – 17, 20 – 22 | Unchanged (OPEN or ACCEPTED as recorded in [stage-6/known-issues.md](../stage-6/known-issues.md)) |

## 2. Stage 7 issues

| ID | Issue | Impact | Status |
|---|---|---|---|
| KI-S7-01 | Formal acceptance of Stages 1–6 is not recorded; Stage 7 acceptance is pending | Process gap | OPEN — owner |
| KI-S7-02 | `global.ip` (default 40 requests / 2 s per IP) also counts public campaign image requests; an image-heavy page, fast scrolling through results, or many visitors behind one carrier-grade NAT can hit it | Images or pages may fail with 429 for shared-IP mobile users | OPEN — consider exempting or separately limiting public media, and measuring real traffic |
| KI-S7-03 | In the local compose stack, browser traffic reaches the API from the Docker gateway address, so every local client shares per-IP budgets (registration, reports, login) | Development and real-stack E2E only (slow runs, cross-suite interference; host lock required) | ACCEPTED (local only) |
| KI-S7-04 | No manual accessibility audit (screen readers, keyboard-only, zoom, text spacing); the WCAG 2.2 AA criteria listed in [accessibility.md](accessibility.md) §3 are unverified | Possible barriers for disabled users | OPEN — before public launch |
| KI-S7-05 | Core Web Vitals were measured in the lab only (`apps/web/scripts/cwv.mjs`, [performance.md](performance.md) §8); no field data (no real-user monitoring), no Lighthouse run, and no web performance budget enforced in CI | Real-device performance unknown; regressions are not caught automatically | OPEN |
| KI-S7-06 | Link previews (WhatsApp, Facebook, X) and search-engine crawling/rich results are unverified: no publicly reachable deployment exists | Previews may render differently than expected | OPEN — verify on the first public preview deployment |
| KI-S7-07 | `/privacy` and `/terms` are draft placeholders | No privacy notice for reports (contact email) and analytics | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-022, LR-035) |
| KI-S7-08 | English-only UI; search uses the `simple` configuration (no stemming, no Shona/Ndebele handling); campaign text has no per-campaign `lang` | Search quality; screen-reader pronunciation of Shona/Ndebele text | OPEN — Stage 16 |
| KI-S7-09 | (resolved during Stage 7) The real-stack suite documented `Retry-After` as a fixed default; the API now sends the limiter's real wait (`RetryAfter()`) and the suite comment says so | — | RESOLVED |
| KI-S7-10 | ROADMAP Stage 7 items not built: donation-flow UI against payment mocks with "awaiting confirmation" states, receipts UI, QR codes | Carried forward; donation UI belongs with the payment work | OPEN — see [STAGE-7-TO-STAGE-8.md](../stage-handover/STAGE-7-TO-STAGE-8.md) |
| KI-S7-11 | (resolved during Stage 7) Completed staff picks were still listed in the public featured list; the query now lists only ACTIVE/PAUSED picks, matching the pick rule | — | RESOLVED |
| KI-S7-12 | Sitemap is one file capped at 50 000 campaigns (no sitemap index); the response is about 3.25 MB at 26 854 campaigns; covering index R6 not applied | Above the cap campaigns are missing from the sitemap | OPEN |
| KI-S7-13 | Retention of reports, reporter contact emails and event counters is not defined; reporters get no follow-up notification; no triage SLA | Data kept indefinitely | OPEN — **LEGAL_REVIEW_REQUIRED** (LR-012) |
| KI-S7-14 | Stage 5 verification and compliance APIs keep their own `If-Match` and list conventions (ADR-038 §7) | Client mapping differs between API families | OPEN — recorded follow-up |
| KI-S7-15 | Event counters have no staff view or export | Counters are collected but not yet usable | OPEN |
| KI-S7-16 | Category counts and sitemap data are cached for 300 s | A suspended campaign may stay in the sitemap or a count for up to five minutes (its page is 404 immediately) | ACCEPTED (ADR-038 §1) |
| KI-S7-17 | Reports have no CAPTCHA or proof-of-work (honeypot, dedupe and rate limits only) | Distributed report spam possible | ACCEPTED — reports never change status; revisit with abuse data |
| KI-S7-18 | The real-stack suite shares the development database with the Go integration tests and needs the host lock `flock /tmp/claude-1000/fundzim-stack.lock`; a run with an empty registration budget takes 12+ minutes | Developer friction | ACCEPTED (CI jobs have their own stacks) |
| KI-S7-19 | Only Chromium (desktop and Pixel 7 emulation) is tested; no Firefox/WebKit, no real devices | Browser-specific defects possible | OPEN |
| KI-S7-20 | Staff review-queue query start-up (performance.md R6, staff-only) not optimised | Slower staff queue at scale | OPEN — low priority |
| KI-S7-21 | Campaign image width variants are 320, 640 and 1024 px; between about 720 and 768 px viewport width at DPR 2 the browser picks the 1024 px variant where a ~768 px one would do | Some extra bytes on mid-size screens | OPEN — consider a 768 px variant |
| KI-S7-22 | The real-stack suite answers the staff step-up dialog when the API asks for it but does not force a step-up (a fresh staff session is inside the step-up window) | The step-up dialog path is covered by unit and mock tests, not proven in every real-stack run | OPEN |
| KI-S7-23 | Report **dismissal** is not exercised in the real-stack suite (escalation is, through to the compliance case); dismissal against the real API and database is covered by `TestPublicExperienceEndpoints` (dismiss → `DISMISSED`, no escalation event, no status change, 409 on a second triage) and the staff UI dismiss path by the mock suite (`e2e/staff-public.spec.ts`) | The staff UI dismiss path is not proven end to end against the real stack | OPEN |
