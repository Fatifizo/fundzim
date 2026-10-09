# Stage 7 interface contracts (public experience)

Binding for every Stage 7 stream. Decisions: ADR-038 (caching, featured, reports, analytics, trust indicators,
canonical URLs, API consistency). Stage 6 contracts (docs/stage-6/interface-contracts.md) still apply unless changed
here.

## 1. Streams and ownership

| Stream | Owns |
|---|---|
| Lead | Go backend: consistency changes, new public endpoints, migration `20261010090000_public_experience.sql`, wiring |
| F (frontend) | `apps/web/**` except `apps/web/e2e-real/**` and `apps/web/playwright.real.config.ts` |
| E (real-stack E2E) | `apps/web/e2e-real/**`, `apps/web/playwright.real.config.ts`, the real-stack CI job, any test-only provisioning scripts under `scripts/e2e/` |
| P (performance) | `tests/perf/**`, docs/stage-7/performance.md measurements |
| OpenAPI | `api/openapi/fundzim-v1.yaml` |

## 2. API consistency changes (ADR-038 §7) — compatibility

| Area | Before (Stage 6) | Now |
|---|---|---|
| Path parameter | media/updates `{id}` | `{campaign_id}` (URLs unchanged) |
| Missing/unparsable `If-Match` | 422 `IF_MATCH_REQUIRED` / 400 `PRECONDITION_REQUIRED` / 422 `VALIDATION_FAILED` | **428 `PRECONDITION_REQUIRED`** |
| Stale `If-Match` | 409 `CAMPAIGN_STATE_CHANGED` / `MEDIA_STATE_CHANGED` / `VERSION_CONFLICT` | **412 `PRECONDITION_FAILED`** |
| Owner campaign list `GET /campaigns/mine` | array | `{items, next_cursor}` (cursor = `created_at|id`, newest first, `limit` ≤ 100) |
| Staff list `GET /admin/campaigns` | array | `{items, next_cursor}` |
| Media list `GET /campaigns/{campaign_id}/media`, staff `…/media/all` | `{media}` | `{items, next_cursor: null}` |
| Owner updates list | `{updates, next_cursor}` | `{items, next_cursor}` |
| Other 409s (status changed by someone else, lock races) | unchanged | unchanged |

The web client is updated in the same change.

## 3. New and changed public endpoints (all `Public` policy, rate limited by `global.ip` plus the listed policy)

| Method and path | Response | Cache |
|---|---|---|
| `GET /public/categories` | `{items: [{code, slug, name, description, display_order, sensitive, live_campaigns}]}` (`live_campaigns` = count of live PUBLIC campaigns, exact) | `public, max-age=300` |
| `GET /public/categories/{slug}` | one category (same shape); 404 `CATEGORY_NOT_FOUND` if unknown or inactive | `public, max-age=300` |
| `GET /campaign-categories` | unchanged; documented as a compatibility alias of the list | `public, max-age=300` |
| `GET /public/campaigns` | as Stage 6, plus `category` may be a code **or** a category slug; `total` (exact count, capped: `{value, capped: bool}`, cap 1000); `q` ≤ 100 chars, control characters refused (422); statement timeout 2 s (→ 503 `SEARCH_TIMEOUT`); items gain `organiser {type, display_name}` and `trust` | `public, no-cache` + ETag |
| `GET /public/campaigns/featured?limit=` (≤ 12, default 6) | `{items: [card + selection: STAFF_PICK|RECENTLY_PUBLISHED]}` | `public, no-cache` + ETag |
| `GET /public/campaigns/{slug}` | as Stage 6, plus `canonical_slug`, `public_code`, `trust {organiser_identity_verified, organisation_verified, beneficiary_verified, campaign_reviewed}`, `category_name`; a non-canonical slug whose trailing public code matches a public campaign returns that campaign (web redirects to `canonical_slug`) | `public, no-cache` + ETag |
| `GET /public/campaign-codes/{public_code}` (not under `/public/campaigns/`: it would collide with `{slug}/updates`) | `{canonical_slug}`; 404 if not public | `public, no-cache` |
| `GET /public/campaigns/{slug}/updates` | as Stage 6 (U's handler); `{items, next_cursor}` | `public, no-cache` + ETag |
| `GET /public/campaigns/{slug}/media/{media_id}` | as Stage 6; cache changed to `public, no-cache` + ETag | |
| `POST /public/campaigns/{slug}/reports` | body `{reason, details?, contact_email?, website?}` (`website` is a honeypot: must be empty); 202 `{received: true}` for accepted **and** for silently-dropped duplicates/honeypot hits; 404 if not public; 422 validation; 429 when limited. Policies `public.report.ip` (5/hour), `public.report.campaign` (50/hour) | `no-store` |
| `GET /public/sitemap` | `{campaigns: [{slug, updated_at}], categories: [{slug, updated_at}]}` live PUBLIC campaigns only, cap 50 000 | `public, max-age=300` (sitemaps tolerate minutes of lag; pages themselves 404 immediately) |
| `POST /public/events` | body `{type: CAMPAIGN_PAGE_VIEW|CAMPAIGN_SHARE_CLICK|CAMPAIGN_SEARCH|CATEGORY_VIEW, slug?, category?, channel?: WHATSAPP|FACEBOOK|X|EMAIL|COPY_LINK|WEB_SHARE}` → 204; unknown slugs/categories silently ignored; policy `public.event.ip` (120/minute) | `no-store` |

Report reasons: `MISLEADING_INFORMATION`, `IMPERSONATION`, `SUSPICIOUS_ACTIVITY`, `INAPPROPRIATE_CONTENT`,
`PRIVACY_CONCERN`, `OTHER` (details required, 10–2000 chars, for OTHER). Details are plain text (Stage 6 content
rules).

## 4. Staff endpoints (new)

| Method and path | Permission |
|---|---|
| `GET /admin/campaigns/reports?status=OPEN|ESCALATED|DISMISSED&cursor=` | `campaign.review` |
| `POST /admin/campaigns/reports/{report_id}/dismiss` · `/escalate` (`{reason_code, note}`) | `campaign.review` |
| `GET /admin/campaigns/featured` · `POST /admin/campaigns/{campaign_id}/feature` (`{position, note}`) · `DELETE /admin/campaigns/{campaign_id}/feature` | `campaign.feature` (new; ADMIN, COMPLIANCE; step-up) |

Escalation emits `campaigns.report_escalated` (compliance opens a case: CAMPAIGN primary subject). Report contact
emails are never returned in staff lists (only `has_contact: bool`).

## 5. Web routes (F)

`/`, `/campaigns`, `/campaigns/[slug]`, `/c/[code]` (redirect), `/categories`, `/categories/[slug]`,
`/how-it-works`, `/safety`, `/about`, `/contact`, `/privacy`, `/terms`, `/sitemap.xml`, `/robots.txt`,
`/manifest.webmanifest`. Canonical and Open Graph absolute URLs use `PUBLIC_SITE_URL` (never request headers).
WhatsApp: `https://wa.me/?text=<encoded>`; Facebook: `https://www.facebook.com/sharer/sharer.php?u=`; X:
`https://x.com/intent/post?text=&url=`; email: `mailto:?subject=&body=`. No third-party scripts.

## 6. Real-stack E2E (E)

Runs Playwright against the compose stack (`fundzim-web` at `http://localhost:3000`, API, worker, Postgres,
Mailpit, ClamAV). Provisioning uses the real API and Mailpit; the only direct database operations allowed are the
test-environment fixtures already used by Go integration tests (revoking super admins before the bootstrap
ceremony, linking a staff account for the linked-identity test). TOTP is computed in the test (RFC 6238).
