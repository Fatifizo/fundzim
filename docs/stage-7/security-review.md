# Stage 7 — Security review (public experience)

Scope: everything Stage 7 exposes to anonymous visitors (public API, public pages, sharing, SEO, PWA, reports,
analytics) and the new staff pages. Controls from Stage 6 ([campaign-security.md](../stage-6/campaign-security.md))
still apply. Status words: **Mitigated** (control in place and tested), **Partly mitigated**, **Accepted risk**,
**Open**. None of this is a claim of certification or audit.

## 1. Threats and controls

| # | Threat | Control | Evidence | Status |
|---|---|---|---|---|
| 1 | Stored XSS through campaign text, updates or report details | Plain-text rules at the API (HTML, entities, script/data URLs, bidi and zero-width characters refused; DB check on report details); React text nodes only; no auto-linking; no `dangerouslySetInnerHTML` for user content | `TestCleanTextRefusesMarkupAndUnsafeContent`; Stage 6 tests; component tests | Mitigated |
| 2 | Script injection through JSON-LD | Built by our code from approved fields; `serializeJsonLd` escapes `<`, `>`, `&`, U+2028, U+2029; forbidden-key list; nonce | `json-ld.test.ts` | Mitigated |
| 3 | Script injection / third-party code | Per-request nonce CSP (ADR-033); no third-party scripts, fonts, pixels or share SDKs | `e2e/csp.spec.ts`; CSP violation checks in `e2e-real/a11y.spec.ts` | Mitigated |
| 4 | Private data in public responses (owner/beneficiary ids, emails, KYC, risk, review notes) | Explicit public DTOs; beneficiary name only with `DISPLAY_NAME` disclosure; recursive allow-list of keys over every public endpoint | `TestPublicExperienceEndpoints` (allow-list) | Mitigated |
| 5 | Stale public data (suspended or restricted campaign still visible) | `public, no-cache` + ETag for campaign state; SSR `cache: 'no-store'`; no server-side shared cache; errors always `no-store` with no `ETag` (a public 404 must not stick) | Integration test: after suspension the detail is 404 with `no-store` and no `ETag` even with `If-None-Match`, and the campaign is gone from search, featured, sitemap data and short-code lookup; `TestCachePolicyRespectedAndErrorsNeverCached` | Mitigated. Categories/sitemap may lag 300 s (counts and URL lists only, never content) — Accepted risk |
| 6 | **Caching defect found:** `httpx.WriteJSON` forced `no-store` on every response, silently defeating the documented public cache policies | WriteJSON keeps a handler's `Cache-Control` (the middleware default stays `no-store`); `WriteError` always sets `no-store` and deletes `ETag` | `TestCachePolicyRespectedAndErrorsNeverCached` | Fixed |
| 7 | **Retry-After defect found:** `Retry-After` was always 5 s regardless of the limiter's wait, so clients retried too early | Errors may implement `RetryAfter() time.Duration`; `WriteError` sends it rounded up to whole seconds; campaigns `rateLimited` and identity `limitedErr` implement it; others keep 5 s | `TestRetryAfterFromError`, `TestRetryAfterSeconds` | Fixed (the e2e-real README still describes the old behaviour — KI-S7-09) |
| 8 | Open redirect via canonical slug or short link | Redirect targets are built by `campaignPath()` from the slug the API returns; `/c/[code]` only resolves codes; `absoluteUrl` refuses `//` paths | Unit and E2E tests (canonical redirect, short link) | Mitigated |
| 9 | Host-header / URL poisoning of canonical, OG, share and sitemap URLs | Absolute URLs come only from `PUBLIC_SITE_URL` (origin only); never from `Host` or `X-Forwarded-*`; unset → URLs omitted | `url.test.ts`; E2E "without PUBLIC_SITE_URL" | Mitigated |
| 10 | Report abuse (flooding, harassment, take-down by mob) | Honeypot; per-IP (5/h) and per-campaign (50/h) limits, the IP limit checked before anything else; per-reporter per-campaign per-day dedupe in the database; identical 202 for accepted, duplicate and honeypot (no oracle); **a report never changes a campaign's status**; staff triage with reason and note; conflict-of-interest check on triage | Integration test (honeypot, dedupe, rate limit, status unchanged, triage, escalation) | Mitigated |
| 11 | Reporter privacy | Reporter key is an HMAC of user id or IP (never shown); optional contact email stored as blind index + AEAD ciphertext bound to the report id, never listed (`has_contact` only), never shown to owners; owners cannot reach the queue (404) | Integration test | Mitigated. Retention of reports and contact emails not defined — **LEGAL_REVIEW_REQUIRED** (LR-012) |
| 12 | Analytics collecting personal data | No cookies, `credentials: "omit"`, no IP or user id stored, no query text, DNT/GPC respected, whitelisted fields only, counters only, unknown slugs silently ignored (no existence oracle), 120/min per IP | `events.test.ts`, E2E "analytics: nothing is sent when Do Not Track or Global Privacy Control is on", integration counters test | Mitigated. Lawful basis and privacy-notice wording — **LEGAL_REVIEW_REQUIRED** (LR-035) |
| 13 | Counter inflation (fake page views/shares) | Rate limit per IP; counters are internal and never displayed, so inflation cannot mislead donors | — | Accepted risk |
| 14 | Search denial of service | `q` ≤ 100 characters, control characters refused, `limit` ≤ 50, count capped at 1 001 rows, `statement_timeout` 2 s, forced custom plans, `global.ip` limit; 503 `SEARCH_TIMEOUT` | `performance.md` §5 and §7.3 | Mitigated |
| 15 | Enumeration of non-public campaigns | Non-public is always 404 (detail, short codes, media, updates, reports); events for unknown slugs are silently ignored | Integration test (detail and short code after suspension; Stage 6 tests for media and updates) | Mitigated |
| 16 | Featured list used as endorsement or manipulation | Staff-only (`campaign.feature`, sensitive, step-up, ADMIN/COMPLIANCE), audited, history kept, only live PUBLIC campaigns shown, selection reason in each item, public copy says it is not an endorsement | Integration test; real-stack `featured.spec.ts` | Mitigated (see KI-S7-11 for a staff display mismatch) |
| 17 | Misleading trust signals | Indicators computed live, shown only when true; explanation and disclaimer; no other badges; no ratings or awards in JSON-LD | `public-normalise.test.ts`, `json-ld.test.ts`, integration test (trust values) | Mitigated |
| 18 | Misleading fundraising (implying donations work) | Disabled donate control and notices on home, how-it-works, safety; manifest description; share text invites reading only; no totals/progress | E2E "disabled donations" | Mitigated |
| 19 | Service worker serving stale or cross-user content | Caches only same-origin query-less `/_next/static/` and the offline page; never pages, API or media; navigations online go to the network; offline page has its own restrictive CSP; `/sw.js` served `no-cache` with `default-src 'none'; connect-src 'self'` | E2E "service worker" | Mitigated |
| 20 | Clickjacking | `frame-ancestors 'none'` (web CSP and API), `X-Frame-Options: DENY` on the API | Existing CSP tests | Mitigated |
| 21 | Tabnabbing via share links | `rel="noopener noreferrer"` on external links | `share-controls.test.tsx` | Mitigated |
| 22 | Crawling of private areas | `robots.txt` disallows private paths (all paths unless indexing allowed); private pages `noindex` | E2E robots tests | Mitigated (robots.txt is advisory; access control is the real control) |
| 23 | Rate-limit evasion via spoofed `X-Forwarded-For` | API honours forwarding headers only from trusted peers; the web proxy strips client-supplied forwarding headers and sets the client IP; SSR fetches forward the visitor's IP | `TestClientIPHonoursForwardedOnlyFromTrustedPeers` | Mitigated |
| 24 | Route ambiguity (`/public/campaigns/by-code/{code}` vs `{slug}/updates`) | Renamed to `/public/campaign-codes/{public_code}` | Integration test | Fixed |
| 25 | Optimistic-concurrency bypass | `If-Match` mandatory on campaign-family writes: 428 when missing/unparsable, 412 when stale | `TestRequireIfMatch`; updated Stage 6 integration tests | Mitigated |
| 26 | Staff pages exercised only against mocks (KI-S6-19) | Real-stack Playwright suite drives moderation, four-eyes, linked-account self-approval refusal, reports and featured in the browser | `apps/web/e2e-real` | Mitigated (47 / 47 locally, twice; [testing.md](testing.md)) |

## 2. Residual risks and open items

- `global.ip` (default 40 requests per 2 s per IP) also counts public image requests; a campaign page with many
  gallery images, or many visitors behind one carrier-grade NAT, may hit it (KI-S7-02).
- Locally, all browser traffic reaches the API from the Docker gateway address, so every client shares per-IP
  budgets (development only; KI-S7-03).
- Report and contact-email retention not defined (LR-012). Report triage SLA not defined.
- No CAPTCHA or proof-of-work on reports (honeypot and rate limits only).
- Sitemap and category counts may lag 300 s.
- Link-preview rendering by external services not verified (no public deployment).
