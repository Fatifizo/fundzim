# apps/web — FundZim web frontend

Next.js 16.4 (App Router; every document rendered per request for the nonce CSP — `cacheComponents`/PPR off) · React 19 · TypeScript (strict,
`noUncheckedIndexedAccess`) · Tailwind CSS v4.

This app is **presentation only**: it renders pages and calls the Go API at `/api/v1/`. It never talks to the
database, never contains business or financial logic, and never holds secrets. Standards:
[docs/FRONTEND.md](../../docs/FRONTEND.md). Read [AGENTS.md](AGENTS.md) before writing Next.js code — this
Next.js version has breaking changes relative to older documentation (e.g. `error.tsx` receives `retry`, not
`reset`; request-time data must sit inside `<Suspense>` under `cacheComponents`).

**Stage 3 status (Stage 4 adds authentication, below):** development preview. Homepage, How it works, About and Contact have real (honest) content.
Every future feature route shows a "Coming soon — under development (Stage N)" page. There are no accounts,
campaigns, donations or payments, and the site is `noindex` throughout.

```bash
npm ci              # install from lockfile (Node 24)
npm run dev         # http://localhost:3000 (API expected at http://127.0.0.1:8080)
npm run lint        # ESLint (includes a rule banning Number()/parseFloat on amount_minor)
npm run typecheck   # next typegen && tsc --noEmit (route types generated first; works on a fresh checkout)
npm test            # Vitest + React Testing Library + axe-core (jsdom)
npm run test:watch
npm run build       # production build, output: standalone
npm run test:e2e    # Playwright: e2e/serve.mjs builds, starts the mock auth API and two standalone servers
                    # (API down / API = mock), runs e2e/ (Chromium desktop + Pixel 7). E2E_SKIP_BUILD=1 reuses .next
```

First E2E run on a machine: `npx playwright install chromium` (no sudo needed for the headless shell).

## Environment variables

| Variable | Where read | Required | Notes |
|---|---|---|---|
| `API_BASE_URL` | Server only, **at request time** (Route Handler proxy, Server Components) and at server start (validation) | **Production: yes.** Dev/test default `http://127.0.0.1:8080` | Origin only (`http://api:8080`), no path/credentials. In production (`NODE_ENV=production`) a missing/invalid value makes the server **exit on start** (`src/instrumentation.ts`) and the proxy answers `503 SERVICE_UNAVAILABLE`; there is no silent fallback. Not needed at build time. |
| `WEB_TRUSTED_PROXY_CIDRS` | Server only, at start (validated) and per request | No (default empty) | Comma-separated CIDRs of reverse proxies **in front of** the web server. Empty: the TCP peer is the client and client-supplied `X-Forwarded-For` is ignored. Invalid → the server exits on start in production. |
| `PUBLIC_SITE_URL` | Server only, at request time | No (production: recommended) | Canonical site origin only (`https://fundzim.example`; no path/query). Source of every absolute URL: canonical links, Open Graph/Twitter URLs and images, JSON-LD, share links, `robots.txt` sitemap line and `sitemap.xml`. When unset or invalid these are left out — never guessed from request headers — and share buttons are not offered (a note says why in non-production builds). |
| `SITE_INDEXING` | Server only, at request time | No | `allow` lets search engines index the public pages (only together with a valid `PUBLIC_SITE_URL`). Anything else keeps the development-preview behaviour: every page `noindex` and `robots.txt` `Disallow: /`. Private pages are always `noindex`. |
| `PORT`, `HOSTNAME` | Standalone server | No | Defaults `3000` / `0.0.0.0` in the container. |
| `NEXT_PUBLIC_*` | Inlined into the browser bundle **at build time** | — | Public by definition. None are used in Stage 3. Never put a secret in a `NEXT_PUBLIC_` variable. |

## Same-origin API access

The browser always calls relative `/api/v1/...`. `src/app/api/v1/[...path]/route.ts` forwards those requests to
`${API_BASE_URL}/api/v1/...`.

**Why not `rewrites()`:** `next.config.ts` is evaluated at `next build` and serialised into the standalone server,
so a rewrite destination would bake in the build-time `API_BASE_URL`. The Route Handler reads the env per
request, so one image works in every environment. In deployed environments the reverse proxy may route
`/api/v1` directly to the API instead (ARCHITECTURE §3).

Proxy behaviour: fixed upstream origin (path + query only); strips hop-by-hop headers and client-supplied
`Forwarded`/`X-Forwarded-*`/`X-Real-IP`, then sets `X-Forwarded-For` to exactly one address — the client this
process observed (see "Client address" below); forwards
cookies, `Origin` and a validated `X-Request-ID` (generated if absent/malformed); 1 MiB body cap
(`413 PAYLOAD_TOO_LARGE`) — except exactly `POST /api/v1/verification/documents` (Stage 5 identity-document
uploads go through the API, ADR-035), which gets 10 MiB + 256 KiB multipart overhead and a 180 s timeout;
30 s upstream timeout otherwise; never retries; passes redirects and multiple `Set-Cookie`
through; upstream failure → error envelope `SERVICE_UNAVAILABLE` (503, or 504 on timeout) with
`retryable: true` only for GET/HEAD.

### Client address (security review F-02 / KI-04)

Next.js 16 gives Route Handlers no socket information. Its only related behaviour
(`next/dist/server/base-server.js`) is `req.headers['x-forwarded-for'] ??= socket.remoteAddress` — it fills
`X-Forwarded-For` **only if the client did not send one**, so the header a handler sees is client-controlled.
Therefore `src/lib/net/peer-stamp.ts` (installed from `src/instrumentation.ts`) wraps
`http.Server.prototype.emit('request')` and, before Next.js sees the request, deletes every `x-fz-peer-*`
header and records the real TCP peer under a **random per-process header name** (128 bits; a client cannot
guess it). `src/lib/net/client-ip.ts` then applies the API's rule one hop earlier: peer not in
`WEB_TRUSTED_PROXY_CIDRS` → the peer is the client; peer trusted → right-most untrusted `X-Forwarded-For`
entry (malformed entry stops the walk). Requests arriving before the stamp is installed (server start-up) or
with an unparsable peer forward **no** `X-Forwarded-For` (the API then sees the web container — coarse, never
spoofable). Used by the `/api/v1` proxy and by server-side session calls. The API must trust `X-Forwarded-For`
only from the web container's address (`TRUSTED_PROXY_CIDRS`). Tests: `src/lib/net/client-ip.test.ts`,
`src/__tests__/api-proxy-route.test.ts`, and E2E (`e2e/auth.spec.ts`, spoofed headers against the real server).

## Authentication (Stage 4)

Contract: `docs/stage-4/interface-contracts.md` §4. Pages: `/register`, `/login`, `/login/mfa`,
`/verify-email`, `/forgot-password`, `/reset-password`, `/staff/accept-invitation`, `/dashboard`,
`/settings/{profile,security,mfa,sessions}`.

- **Sessions** are HttpOnly cookies set by the API; JavaScript never sees them. Browser storage is banned by
  ESLint (`localStorage`/`sessionStorage`/`indexedDB`).
- **CSRF:** `browserApi` reads `__Host-fz_csrf`/`fz_csrf` and sends `X-CSRF-Token` on POST/PUT/PATCH/DELETE
  (`credentials: "same-origin"`); callers cannot override it.
- **Protected pages** call `requireUser(path)` (`src/lib/auth/session.ts`): GET `/api/v1/auth/session` at
  `API_BASE_URL` with only the FundZim auth cookies and the client address forwarded; no valid session →
  `307 /login?next=<path>`. `src/proxy.ts` additionally redirects cookie-less requests before rendering
  (defence in depth only) and marks `/dashboard` and `/settings/*` `Cache-Control: private, no-store`.
  Signed-in users visiting `/login` or `/register` go to their destination.
- **`next`** is validated by `safeNextPath` (`src/lib/auth/safe-redirect.ts`): same-site path only, no `//`,
  backslashes, control characters or schemes, never back into `/login`, `/register` or `/api`.
- **Errors** map stable codes to fixed copy (`src/lib/auth/errors.ts`); server messages are never shown.
  Generic messages where the contract forbids account enumeration. `AUTHENTICATION_REQUIRED` → login with
  `next`; `STEP_UP_REQUIRED` → step-up dialog (`<dialog>` + `showModal`, password or TOTP; TOTP only for staff)
  then one retry; `RATE_LIMITED` → shows the `Retry-After` wait.
- **Tokens** from email links (verify, reset, staff invitation) are removed from the address bar on load
  (`history.replaceState`), kept in memory only; those pages send `Referrer-Policy: no-referrer`.
- **MFA:** QR code rendered in the browser as inline SVG with `qrcode-generator@2.0.4` (MIT, zero deps); the
  TOTP secret/URI live only in component state during enrolment and are dropped on confirm/cancel. Recovery
  codes are shown once (copy / download as a local text file).

## Verification (Stage 5)

Contract: `docs/stage-5/interface-contracts.md` §7; vocabularies ADR-034; documents and access tickets ADR-035.

| Area | Pages | Main components |
|---|---|---|
| User | `/dashboard/verification` (level, status, progress, gates, next step), `/identity` (KYC details → review → submit, information requests, decision), `/documents`, `/beneficiaries`, `/payout-destinations` | `src/components/verification/*` |
| Organisations | `/dashboard/organisations` (list + create, KI-S4-08), `/dashboard/organisations/[id]/verification` (KYB: details, persons, documents, history; ORG_ADMIN edits, ORG_MEMBER read-only, others 404) | `organisations.tsx` |
| Staff | `/admin/verification` (+ `/kyc`, `/kyb`, `/beneficiaries`, `/payout-destinations`), `/admin/verification/cases/[caseId]`, `/admin/compliance/cases` (+ `[caseId]`) | `src/components/admin/*` |

- **Access.** User pages call `requireUser`. Staff pages call `requireStaff()` (anything but a staff session →
  404, never a login redirect) and load data with `serverGetOr404`, so a staff member without the API
  permission also gets a 404. `src/proxy.ts` marks `/admin/*` `private, no-store` but does not redirect it.
  Every page sets `robots: noindex`.
- **Documents.** Upload with progress via `XMLHttpRequest` (`src/lib/verification/upload.ts`; same CSRF and
  request-id rules as `browserApi`, never retried). Scan status (UPLOADED/QUARANTINED/SCANNING/CLEAN/REJECTED/
  FAILED_SCAN) is polled with backoff (1.5 s → 20 s, stop after 10 min) and announced in a polite live region.
  Viewing: `POST …/access` (through step-up when required) → the returned URL is accepted only if it is this
  document's same-origin `/content` path → full-page navigation (the API answers as an attachment).
- **Sensitive values.** ID and account numbers are write-only inputs (`autocomplete="off"`); only the masked
  values from the API are displayed. A reviewer's revealed ID number lives in component state for 60 s.
  Nothing goes into URLs (queue filters only) or browser storage.
- **Requirements** arrive as `{document_types: [alternatives], satisfied}` from the backend (contract:
  `{document_type, sides, satisfied}`); `normaliseRequirements` (`src/lib/verification/labels.ts`) accepts both.
  Staff payloads go through `src/lib/verification/normalise.ts` (KYC/KYB `review` block, opaque queue cursor).
- **Ownership shares** are entered as percentages and sent as integer `ownership_bp`
  (`src/lib/verification/basis-points.ts`: string parsing, no floating point, invalid input rejected).
- **Errors** (`src/lib/verification/errors.ts`) map every documented Stage 5 code (and fall back to the Stage 4
  mapping); `SUBMISSION_INCOMPLETE` details become an error summary linking to the field or document section.
- **Payout accounts** always show that payouts are not available; the three checks (format, ownership,
  compliance) are shown separately and `PROVIDER_CONFIRMATION_REQUIRED` is explained, never presented as
  confirmed.
- **Tests:** `src/lib/verification/*.test.ts`, `src/__tests__/verification-components.test.tsx`,
  `e2e/verification.spec.ts` against the mock API (`e2e/mock-api/verification.mjs`, control endpoints under
  `/api/v1/__mock/`).

## Campaigns (Stage 6)

Contract: `docs/stage-6/interface-contracts.md` (§3, §5, §6, §9); ADR-036 (lifecycle, publication), ADR-037
(age attestation, BASIC_VERIFIED, restrictions, staff links).

| Area | Pages | Main code |
|---|---|---|
| Owner | `/dashboard/campaigns` (list, organisation filter), `/new` (8-step wizard), `/[id]` (overview, feedback, eligibility, lifecycle), `/[id]/edit`, `/media`, `/beneficiaries`, `/updates`, `/settings` (visibility, pause/resume/complete/cancel/archive) | `src/components/campaigns/*`, `src/lib/campaigns/*` |
| Verification | `/dashboard/verification/age` (self-declaration, `?next=` validated), `/dashboard/account/staff-link?token=` (confirm), optional age checkbox on `/register` | `age-attestation.tsx`, `staff-link-confirm.tsx` |
| Staff | `/admin/campaigns/review` (queue), `/admin/campaigns/review/[id]` (detail + actions; API `GET /admin/campaigns/{id}/review`), `/admin/campaigns` (search), `/admin/campaigns/updates` (moderation), `/admin/account/personal-link` | `src/components/admin/campaign-*.tsx`, `update-moderation.tsx`, `personal-link.tsx` |
| Public | `/campaigns` (search, category, sort, cursor), `/campaigns/[slug]` (published version only; anything else is the app 404) | `src/app/campaigns/*`, `src/lib/campaigns/public.ts` |

- **Money.** Goals are typed as decimal strings and converted to `amount_minor` digit strings with string/BigInt
  arithmetic (`src/lib/campaigns/money.ts`); currencies and minor units come from `GET /campaign-currencies`.
  No totals, raised amounts or progress bars exist anywhere; the public donate control is disabled.
- **Text.** Campaign text is plain text rendered through React text nodes (`StoryText`: paragraphs on blank
  lines, no links, control/bidi characters stripped). `dangerouslySetInnerHTML` is not used.
- **Media** (internal/campaigns/media): kinds `COVER` | `GALLERY`, JPEG/PNG only, alt text 3–250, a required
  `depicts_minor` field (images of children are refused, LR-070), one cover at a time (remove it to replace:
  409 `COVER_ALREADY_EXISTS`). Staff list: `GET /admin/campaigns/{id}/media/all`; previews use the owner
  `…/content` route, which staff with `campaign.view` may read.
- **Shapes** follow the Go handlers (`src/lib/campaigns/normalise.ts` documents each); older readings are still
  accepted where harmless. `/explore` and `/start` redirect to `/campaigns` and `/dashboard/campaigns/new`.
- **Uploads** go only to the API through the proxy; the large-body allow-list (`src/lib/api/upload-routes.ts`)
  is exactly `POST /api/v1/verification/documents` and `POST /api/v1/campaigns/{uuid}/media`.
- **Images** use plain `<img>` from same-origin API paths (`next/image` emits inline styles the CSP blocks).
- **Staff actions** open a dialog with a reason code and a 3–5000 character note; `STEP_UP_REQUIRED` goes
  through `StepUpProvider`. Second approval is never offered when `pending_decided_by` is the current user.
  The compliance restriction indicator is a yes/no flag only.
- **Tests:** `src/lib/campaigns/*.test.ts(x)`, `src/__tests__/public-campaign-page.test.tsx`,
  `src/lib/api/upload-routes.test.ts`, `e2e/campaigns.spec.ts` against `e2e/mock-api/campaigns.mjs`.

## Public experience (Stage 7)

Contract: `docs/stage-7/interface-contracts.md` (§2 consistency, §3 public endpoints, §5 routes); ADR-038.

| Route | What it shows | Data |
|---|---|---|
| `/` | Hero, featured campaigns (staff picks then recently published; selection explained, not an endorsement), categories with live counts, four steps (step 4: "Receive support when payments become available"), trust and safety, call to action; Organization JSON-LD | `GET /public/campaigns/featured`, `GET /public/categories` |
| `/campaigns` | Search (`q` ≤ 100, control characters stripped), category (code or slug), sort, keyset pagination ("Next page" / "Back to the first page"), result count from `total` ("1000+" when capped); results stream behind a skeleton; empty and error states | `GET /public/campaigns` |
| `/categories`, `/categories/[slug]` | Category cards with live counts; one category's description, count, sensitivity note and listing | `GET /public/categories[/{slug}]` |
| `/campaigns/[slug]` | Approved version: cover, story (escaped paragraphs, no links), photos, updates with their photos, trust indicators (only when true, with "Verified identity does not guarantee the campaign's claims"), disabled "Donations are not yet available.", share, report; WebPage JSON-LD. A non-canonical slug → 308 to `canonical_slug` | `GET /public/campaigns/{slug}`, `…/updates` |
| `/c/[code]` | 308 to the canonical campaign URL; unknown → 404 | `GET /public/campaign-codes/{code}` |
| `/safety`, `/how-it-works`, `/about`, `/contact`, `/privacy`, `/terms` | Static; privacy and terms stay marked DRAFT (LEGAL_REVIEW_REQUIRED) and `noindex` | — |
| `/robots.txt`, `/sitemap.xml`, `/manifest.webmanifest` | See below | `GET /public/sitemap` |

- **Freshness.** Campaign data is fetched per request with `cache: 'no-store'` (ADR-038 §1); there is no server cache to
  invalidate when a campaign is suspended.
- **Cards** (`src/components/campaigns/campaign-card.tsx`): approved cover (lazy, fixed 640×360, `aspect-video`),
  title, summary, category, organiser, goal with currency, status, and only the true trust indicators. No totals,
  donor counts or progress bars anywhere.
- **Sharing** (`src/lib/share/links.ts`, `share-controls.tsx`): WhatsApp `wa.me/?text=` ("Support this FundZim
  campaign:\n<title>\n<summary ≤140>\nRead the story: <canonical URL>"), Facebook sharer, X intent, `mailto:` (all
  percent-encoded), Copy link (polite live region; selectable fallback when the clipboard is refused) and the Web
  Share API when present. Only public, approved fields; URLs only from `PUBLIC_SITE_URL`.
- **Reports** (`report-dialog.tsx`, `src/lib/campaigns/report.ts`): native `<dialog>`, the six contract reasons,
  details (required 10–2000 for OTHER), optional email, hidden honeypot `website`; `POST /public/campaigns/{slug}/reports`
  (never retried). The confirmation says the team will review it; nothing promises removal.
- **SEO** (`src/lib/seo/*`): per-page title/description/canonical/Open Graph/Twitter via `pageMetadata`; the generic
  image is `public/og/fundzim-og.png`. JSON-LD only as Organization (home) and WebPage (campaign) — never
  DonateAction, amounts, ratings, reviews or certifications; serialised with `<`, `>`, `&`, U+2028/9 escaped and
  rendered with the request nonce (`src/components/seo/json-ld.tsx`, the only `dangerouslySetInnerHTML`).
  `robots.txt` disallows `/dashboard`, `/admin`, `/login`, `/register`, `/settings`, `/staff`, `/verify-email`,
  `/reset-password`, `/forgot-password` and `/api` when indexing is allowed (`Disallow: /` otherwise).
- **Analytics** (`src/lib/analytics/events.ts`): fire-and-forget `POST /api/v1/public/events` with
  `credentials: "omit"` for campaign page views, share clicks (channel), searches (never the query text) and category
  views. Nothing is sent with Do Not Track or Global Privacy Control. No cookies, identifiers or third parties.
- **PWA.** `src/app/manifest.ts`; icons in `public/icons/` drawn by `scripts/generate-icons.mjs` (node:zlib only;
  re-run after changing the mark). `public/sw.js` (registered in production builds only) caches **only** hashed
  `/_next/static/*` assets (cache-first, capped) and the static `/offline.html` + `/offline.css`; it never intercepts
  online navigations and never caches API responses, pages, media, documents, auth, dashboard or admin. When the
  browser reports it is offline, navigations get the offline page (its own strict CSP, no scripts). The worker's own
  CSP allows only same-origin fetches.
- **Core Web Vitals (lab).** `node scripts/cwv.mjs --runs 5` measures LCP, CLS, an INP proxy, TTFB and bytes with
  Playwright Chromium against a running stack. The profiles are mobile (360×800, 4× CPU, ~1.6 Mbps / 150 ms) and
  desktop, cold and warm. Results and method: `docs/stage-7/performance.md` §8. In result grids, the first card's
  cover loads eagerly with `fetchpriority="high"` (`CampaignGrid priorityFirst`); every other cover is lazy.
- **Responsive images.** Public images use the API's JPEG width variants (`?w=320|640|1024`) via `srcset` and
  layout-specific `sizes` (`publicImageSources` / `IMAGE_SIZES` in `src/lib/campaigns/paths.ts`). The detail page
  offers only the widths listed in `media[].variants`. Cards offer all three, because the API falls back to the full
  image for a missing width. The full derivative (up to 8000 px) is never a `srcset` candidate. Open Graph uses the
  1024 px variant when present.
- **Mobile.** Mobile-first; the main navigation collapses into a disclosure menu below 1024 px (icon-only button
  below 400 px so the header fits at 320 px). E2E checks no horizontal overflow at 320 and 375 px.
- **API consistency (contract §2).** Owner/staff/media/update lists are `{items, next_cursor}` (owner list paginates
  with "Next page"); media and update edits send `If-Match`; 428 `PRECONDITION_REQUIRED` and 412
  `PRECONDITION_FAILED` have their own copy (the Stage 6 409 codes are still understood).
- **Staff pages** (contract §4; `requireStaff` + `serverGetOr404`, so users and staff without the permission get
  404): `/admin/campaigns/reports` (`campaign.review`; Open / Escalated / Dismissed tabs, keyset "Next page"; reason,
  details as escaped text, whether a contact email was given — never the email — whether the reporter was signed in,
  the campaign's current status and a link to its review page; Dismiss / Escalate dialogs with reason code and a
  3–5,000 character note; the page states that a report never suspends a campaign by itself) and
  `/admin/campaigns/featured` (`campaign.feature`; picks by position, `shown: false` marked "Hidden from the public",
  add by campaign id — pre-filled from the review page's "Feature on the home page" link — with position 1–100 and a
  3–500 character note, remove with a reason; step-up whenever the API asks). Components:
  `src/components/admin/{report-queue,featured-manager}.tsx`; E2E: `e2e/staff-public.spec.ts`.
- **Tests:** `src/lib/{share,site,seo,analytics}/*.test.ts`, `src/lib/campaigns/{report,public-normalise,errors-stage7}.test.ts`,
  `src/__tests__/{home-page,public-campaign-page,campaign-card,share-controls,report-dialog,service-worker}.test.*`,
  `e2e/public.spec.ts` against the extended mock (`e2e/mock-api/campaigns.mjs`; control endpoints `__mock/feature`,
  `__mock/reports`, `__mock/events`, `__mock/update`).

## API client (`src/lib/api/`)

- `types.ts` — envelope (`ApiSuccess<T>`, `ApiErrorBody`), `Money` (`amount_minor: string`), health/ready/version DTOs.
- `errors.ts` — `ApiError` (`code`, `status`, `requestId`, `retryable`, `details`, `retryAfterMs`) and client-only
  `NetworkError` (`NETWORK_ERROR`), `TimeoutError` (`TIMEOUT`), `AbortedError` (`REQUEST_ABORTED`); non-envelope
  responses (e.g. HTML 502) → `INVALID_RESPONSE`. These client codes are not API catalogue codes.
- `client.ts` — `createApiClient({ baseUrl })` with typed `get/head/post/put/patch/delete`. Per-attempt timeout
  (`AbortSignal.timeout`, combined with the caller's signal via `AbortSignal.any`). `X-Request-ID` per logical
  request, reused across retries. **Retries only GET/HEAD** (max 2, exponential backoff with jitter) on network
  error, timeout, 502/503/504 or `retryable: true`; a `Retry-After` above 5 s stops retrying.
  POST/PUT/PATCH/DELETE are never retried automatically (unknown outcome ≠ failure; retry deliberately with the
  same `Idempotency-Key`). No auth tokens are handled; sessions (Stage 4) are HttpOnly cookies.
- `server.ts` — `getServerApi()` (imports `server-only`; build fails if imported from a Client Component).
- `config.ts` — `API_BASE_URL` validation.

**Generated API types (future):** endpoint DTOs should be generated from `api/openapi/fundzim-v1.yaml` (ADR-026)
when Stage 4 needs them, e.g. with a pinned `openapi-typescript` into `src/lib/api/generated.ts` and an
`npm run gen:api` script, keeping `types.ts` for the envelope. Not added in Stage 3: the contract is ~26k lines
and nothing in Stage 3 consumes endpoint DTOs.

## Money (`src/lib/money.ts`)

Formats `{ amount_minor: string, currency }` using string/BigInt only (never `Number`/`parseFloat`/`/100`):
validates the wire format (`^-?[0-9]{1,19}$`, no leading zeros, int64 range), USD → `US$1,250.00`,
ZWG → `ZiG 1,250.00`, plus screen-reader text ("1,250.00 US dollars"). Minor units default to 2 for both;
pass `minorUnits` from API currency config when available (ZWG minor units are unverified in the registry).

## Security headers

Set for every route in `next.config.ts` `headers()`: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy` (camera, microphone, geolocation,
payment, … disabled), `Cross-Origin-Opener-Policy: same-origin`; `X-Powered-By` removed. HSTS is set by the
TLS-terminating reverse proxy.

**CSP (closes F-04 / KI-06):** documents get a strict per-request policy from `src/proxy.ts`
(`src/lib/security/csp.ts`): `script-src 'self' 'nonce-…' 'strict-dynamic'`, `style-src 'self' 'nonce-…'`
(no `'unsafe-inline'` anywhere; Tailwind is a same-origin stylesheet and no inline `style` attributes are
rendered), `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`, `form-action 'self'`,
`connect-src 'self'`. `next dev` adds `'unsafe-eval'` and inline styles only. `/api/*` and `/healthz` get
`default-src 'none'`.

Decision and evidence: with `cacheComponents` (PPR) the build-time static shell contains framework
`<script>` tags **without** a nonce (observed: 10 un-nonced scripts on `/about`), which `'strict-dynamic'`
blocks; making the root layout dynamic under PPR fixed the nonces but a resumed render cannot change the
shell's HTTP status, so `notFound()` returned 200 and `redirect()` became client-side. `cacheComponents` and
`partialPrefetching` are therefore **off** and the root layout calls `connection()`: every document is
rendered per request. Trade-off: no static/CDN-cacheable HTML and more server CPU per page view (the site is
small, and authenticated pages are per-user anyway). The root `loading.tsx` was removed so redirects and 404s
keep real status codes. `e2e/csp.spec.ts` checks every page (anonymous and signed in) in the production build:
fresh nonce per response, every `<script>`/`<style>` carries it, no inline style attributes, zero
`securitypolicyviolation` events, hydration works, and an injected un-nonced inline script is blocked.

## Containers

`deploy/docker/web.Dockerfile`, **build context `apps/web`**:

```bash
docker build -f deploy/docker/web.Dockerfile -t fundzim-web:dev apps/web
docker run --rm -p 3000:3000 -e API_BASE_URL=http://api:8080 fundzim-web:dev
```

Multi-stage (`npm ci` → `next build` → standalone runtime on `node:24.21.0-bookworm-slim`), runs as the
unprivileged `node` user with root-owned, read-only app files (only `.next/cache` writable), `HEALTHCHECK` on
`/healthz` using `node` (no curl in slim images). `/healthz` is liveness only and never calls the API.

## Accessibility

Target WCAG 2.2 AA (no conformance claim until audited). Skip link, landmarks, visible focus ring (3 px),
44 px touch targets, `prefers-reduced-motion` honoured, disclosure-pattern mobile menu (Escape closes and
returns focus). Token contrast ratios are listed in `src/app/globals.css`. axe-core runs in unit tests
(jsdom; `color-contrast` disabled there) and in Playwright against the real build (contrast included).
Light theme only for now; dark mode is not shipped until it is designed and checked.
