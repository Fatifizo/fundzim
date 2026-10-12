# Stage 7 — Public fundraising experience: what was implemented

Branch `stage-7/public-experience` (from `stage-6/campaigns` at `ca898ae`). Decisions: [ADR-038](../adr/ADR-038-public-experience-caching-reporting-and-api-consistency.md).
Contracts: [interface-contracts.md](interface-contracts.md). Starting point and its conditions:
[prerequisite-assessment.md](prerequisite-assessment.md).

Stage 7 turns the Stage 6 public read API into a public experience: home page, discovery, category pages, campaign
pages with trust indicators, sharing, SEO, a minimal PWA, public reporting with a staff triage queue, staff-picked
featured campaigns and first-party aggregate analytics. **There are still no donations, payments, payouts, totals or
progress bars.** Every campaign page shows a disabled "Donations are not yet available." control. Nothing here is a
claim of regulatory approval.

Detail documents: [public-experience.md](public-experience.md) · [campaign-discovery.md](campaign-discovery.md) ·
[social-sharing.md](social-sharing.md) · [seo.md](seo.md) · [accessibility.md](accessibility.md) ·
[security-review.md](security-review.md) · [performance.md](performance.md) · [testing.md](testing.md) ·
[known-issues.md](known-issues.md). Next stage: [STAGE-7-TO-STAGE-8.md](../stage-handover/STAGE-7-TO-STAGE-8.md).

## 1. Remediation carried from Stage 6 (prerequisite assessment)

| Item | What was done |
|---|---|
| A — staff pages never exercised against the real API (KI-S6-19) | New real-stack Playwright suite `apps/web/e2e-real` (staff moderation in the browser, public pages, reports, featured, axe + CSP) and a CI job "Real-stack E2E (compose + Playwright)" |
| B — third-party individual fundraising | Unchanged: `campaign.individual_for_others.enabled = false` (LR-046 – LR-048) |
| D — API inconsistencies (KI-S6-08/09/10) | Campaign family standardised (ADR-038 §7): `{campaign_id}` path parameter, 428 `PRECONDITION_REQUIRED` / 412 `PRECONDITION_FAILED` for `If-Match`, lists as `{items, next_cursor}` |
| E — public data safety | Integration test walks every public response recursively against an allow-list of keys |
| Caching defect (found) | `httpx.WriteJSON` overwrote every handler's `Cache-Control` with `no-store`, so Stage 6's "cacheable" public headers never took effect. Fixed: a handler's policy is kept (the security-headers middleware still defaults everything to `no-store`); **errors are always `no-store` and lose any `ETag`** |
| Retry-After defect (found) | `Retry-After` was always 5 s. Fixed: `httpx.WriteError` reads an optional `RetryAfter() time.Duration` from the error (rounded up to whole seconds); the campaigns `rateLimited` and identity `limitedErr` errors carry the limiter's real wait. Errors without it keep the 5 s fallback |
| Route collision (found) | `GET /public/campaigns/by-code/{code}` collided with `/public/campaigns/{slug}/updates` in the router; renamed `GET /public/campaign-codes/{public_code}` |
| C — formal acceptance of Stages 1–6 | Cannot be done by engineering; still NOT_VERIFIED (KI-S7-01) |

## 2. Backend (`internal/campaigns`, module `campaigns`; no new module)

- **Public read** (`publicx.go`, `public.go`): categories with exact live counts, category by slug, featured list,
  canonical-slug resolution and short codes, trust indicators, sitemap data, conditional responses (strong `ETag`
  over the response body, `If-None-Match` → 304).
- **Search** (`public.go`): Postgres full-text search (`simple` configuration) over a stored `search_tsv` column on
  `app.campaign_versions`, keyset cursors, exact total capped at 1 000, `q` ≤ 100 characters, control characters
  refused (422). Each search runs in a transaction with `SET LOCAL statement_timeout = '2s'` and
  `plan_cache_mode = force_custom_plan`; a timeout returns 503 `SEARCH_TIMEOUT`.
- **Reports** (ADR-038 §3): `POST /public/campaigns/{slug}/reports`; staff queue, dismiss and escalate. Escalation
  emits `campaigns.report_escalated`; the compliance consumer opens a `CAMPAIGN_REVIEW` case (severity S2, source
  `REPORT`). A report never changes a campaign's status.
- **Featured** (ADR-038 §2): staff picks (`campaign.feature`, new permission, ADMIN and COMPLIANCE, sensitive,
  step-up) then recently published live `PUBLIC` campaigns.
- **Events** (ADR-038 §4): `POST /public/events` increments `app.public_event_counters`.
- **Rate limits** (new policies in `internal/platform/ratelimit/policies.go`): `public.report.ip` 5/hour,
  `public.report.campaign` 50/hour, `public.event.ip` 120/minute, all protective; every public request also passes
  `global.ip` (default 40 per 2 s).
- **API consistency** across the campaign family (core, media, updates, featured, reports) — see contracts §2.

## 3. Data model

| Migration | Contents |
|---|---|
| `20261010090000_public_experience.sql` | `app.campaign_reports` (status machine `campaign_report`: `OPEN → DISMISSED / ESCALATED`, guarded by `app.guard_transition`; per-campaign/reporter/day dedupe constraint; report content immutable, only triage columns change once; no delete), `app.campaign_featured` (one active pick per campaign; removal keeps history; no delete), `app.public_event_counters` (day, type, subject, channel, count; checks restrict types, channels and subject characters), permission `campaign.feature` granted to ADMIN and COMPLIANCE |
| `20261010090100_public_search_performance.sql` | Index of live `PUBLIC` campaigns by approved version, versions by category, stored generated `search_tsv` + GIN index (replaces the expression index; snapshots' `content_sha256` unchanged), drops an unused index. Measured in [performance.md](performance.md) |

Reporter identity is an HMAC (`reporter_key`) of the signed-in user id or of the client IP, used for dedupe and never
shown. An optional contact email is stored as a blind index plus AEAD ciphertext and is never returned by any list
(staff see only `has_contact`).

## 4. Frontend (`apps/web`)

Routes: `/`, `/campaigns`, `/campaigns/[slug]`, `/c/[code]`, `/categories`, `/categories/[slug]`, `/how-it-works`,
`/safety`, `/about`, `/contact`, `/privacy` and `/terms` (both marked "DRAFT — placeholder only",
LEGAL_REVIEW_REQUIRED), `/sitemap.xml`, `/robots.txt`, `/manifest.webmanifest`; `/explore` redirects to
`/campaigns`. Staff: `/admin/campaigns/reports` and `/admin/campaigns/featured`. New components include
`share-controls`, `report-dialog`, `trust-indicators`, `campaign-card`, `category-card`, `json-ld`,
`track-event` and `register-service-worker`; libraries `lib/share`, `lib/seo`, `lib/site`, `lib/analytics`.
Campaign text is always rendered as escaped text nodes (no `dangerouslySetInnerHTML` for user content). Details in
[public-experience.md](public-experience.md) and `apps/web/README.md` "Public experience (Stage 7)".

## 5. Configuration

| Variable (web) | Effect |
|---|---|
| `PUBLIC_SITE_URL` | Origin used for canonical, Open Graph, share and sitemap URLs. Must be an origin (no path); absent or invalid → those URLs are omitted, never derived from request headers |
| `SITE_INDEXING` | `allow` (with a valid `PUBLIC_SITE_URL`) makes public pages indexable; anything else → `robots.txt` `Disallow: /` and `noindex` on every page |

## 6. Audit and events

New audit actions: `campaign.report_received`, and the staff actions `campaign.report_dismissed`,
`campaign.report_escalated`, `campaign.featured` and `campaign.unfeatured`. New outbox event `campaigns.report_escalated`. Event counters are not audit
records and hold no personal data.

## 7. Not in Stage 7

No donation flow, receipts, payment UI, QR codes, totals, donor lists or progress. No third-party scripts, cookies
for analytics, or tracking pixels. No WhatsApp Business API. See [known-issues.md](known-issues.md).
