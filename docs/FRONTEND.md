# FundZim — Frontend Standards (apps/web)

> **Status:** Stage 0 standard; **implementation state as of Stage 3:** `apps/web` is a development preview,
> no longer the unmodified scaffold. It has the homepage, How it works, About and Contact pages, draft
> placeholders for Privacy and Terms, "coming soon" pages for later features (explore, start, login, register,
> dashboard, campaign pages), shared layout and UI components, a same-origin `/api/v1` route-handler proxy, a
> typed API client, string-based money formatting, `/healthz`, security headers, and Vitest/axe and Playwright
> tests. There are no accounts, campaigns, donations or payments, and the site is `noindex`. Details:
> [apps/web/README.md](../apps/web/README.md) and [stage-3/implementation.md §12](stage-3/implementation.md).
> The public fundraising UI is built in Stage 7, sharing optimisation in Stage 16, the admin portal in Stage 14.

Related: [ARCHITECTURE.md](ARCHITECTURE.md) · [MONEY.md](MONEY.md) · [SECURITY.md](SECURITY.md) ·
[PRIVACY.md](PRIVACY.md) · [TESTING.md](TESTING.md) · `apps/web/AGENTS.md`

> **Before writing Next.js code:** read `apps/web/AGENTS.md`. This Next.js version has breaking changes
> relative to older documentation; the authoritative docs are in `apps/web/node_modules/next/dist/docs/`.

---

## 1. Role of the web app: presentation only

- `apps/web` renders UI and calls the Go API. It has **no database access, no business or financial logic,
  and no secrets** beyond public configuration.
- Every decision that matters — authorisation, campaign state, amounts, fees, payment status, KYC level — is
  made by the API. The web app displays what the API returns and never computes financial results
  (no fee calculation, no balance arithmetic, no currency totals).
- The browser reaches the API **same-origin** at `/api/v1/` through the reverse proxy; first-party cookies,
  no CORS.
- Server-side code in Next.js (Server Components, route handlers, server actions) may call the API on behalf
  of the user, forwarding the session cookie and `X-Request-ID`. It must not cache user-specific responses
  in shared caches.
- Public campaign pages are server-rendered from API data so they are fast and produce correct OpenGraph
  previews.
- The staff/admin portal (Stage 14) is separated from the public site (separate host or strictly isolated
  route group, decided in Stage 14) with staff authentication and stricter CSP.

---

## 2. Mobile-first

Design target: a mid-range Android phone on a congested 3G/4G connection, often opened from a WhatsApp link.

- Design at 360 px wide first; scale up. No horizontal scroll at 320 px.
- Primary actions (Donate, Share) reachable with one thumb; sticky donate button on campaign pages.
- Forms: minimal fields, correct `inputmode`/`autocomplete` (`tel`, `email`, `one-time-code`, `decimal`),
  phone input defaulting to +263 while supporting international numbers.
- Donation flow: amount → method → confirm → provider step → status page. Status page polls the API (with
  backoff) for authoritative state; it never treats the redirect itself as success (PAYMENTS.md).
- Mobile money flows involving handset approval (USSD/push) show clear "approve on your phone" guidance and
  a pending state that survives the user switching apps.

---

## 3. PWA-ready plan (implemented Stage 7/16)

- Web app manifest (name, icons, theme colour, `display: standalone`), installable.
- Service worker with a conservative strategy:
  - Cache static assets (versioned) and an offline fallback page.
  - Campaign pages: stale-while-revalidate **only for public content**, with visible "last updated" time.
  - **Never cache** authenticated API responses, payment/payout pages, KYC flows, or staff pages.
  - **Never queue** financial actions offline for later replay. Donations require online confirmation;
    an offline user is told to retry when connected.
- No push notifications until consent design is approved (LR-023).

---

## 4. Low-bandwidth and image strategy

- Images uploaded by campaign owners are processed server-side (Stage 6): re-encoded, EXIF stripped,
  resized into responsive variants (e.g. 320/640/1024/1600 px) in WebP/AVIF with JPEG fallback, served from
  the `public-media` CDN.
- Use `next/image` (or equivalent per the bundled Next.js docs) with explicit `width`/`height` or aspect
  ratio to avoid layout shift, `sizes` set correctly, lazy loading below the fold, a single prioritised hero
  image.
- Low-quality placeholders (blurred tiny image or dominant colour).
- Respect `Save-Data` and `prefers-reduced-data` where supported: smaller images, no autoplay video.
- No autoplaying video; video embeds are click-to-load.
- Self-host fonts with `font-display: swap` and subsetting; prefer system font stack for body text.
- Minimise client JavaScript: Server Components by default; client components only for interactivity.
  No heavy UI libraries without justification.
- Third-party scripts (analytics, chat widgets) are prohibited on the public site by default; each requires
  an ADR-level justification covering privacy, CSP and performance.

---

## 5. Performance budgets (public pages, mobile, slow 4G)

| Metric | Budget |
|---|---|
| Largest Contentful Paint (p75, field) | ≤ 2.5 s |
| Interaction to Next Paint (p75) | ≤ 200 ms |
| Cumulative Layout Shift (p75) | ≤ 0.1 |
| Time to First Byte, campaign page (p75) | ≤ 800 ms |
| JavaScript transferred, campaign page (compressed) | ≤ 170 KB initial |
| Total transfer, campaign page first view | ≤ 1 MB including images |
| Lighthouse mobile performance score (CI) | ≥ 90 on campaign and home pages |

Budgets are enforced in CI from Stage 7 (Lighthouse CI) and revisited with real field data.

---

## 6. Accessibility — WCAG 2.2 AA target

FundZim targets WCAG 2.2 Level AA where reasonably achievable. We do not claim conformance until audited.

| Area | Requirements |
|---|---|
| Keyboard | Every interactive element reachable and operable by keyboard in logical order; no keyboard traps; visible skip-to-content link; custom widgets follow WAI-ARIA Authoring Practices. |
| Focus | Always-visible focus indicator meeting WCAG 2.2 *Focus Appearance* guidance (≥2 px outline, ≥3:1 contrast); focused element never obscured by sticky headers/donate bars (2.4.11); focus moved to dialog on open and restored on close; focus moved to error summary on failed submit. |
| Screen readers | Semantic HTML first (landmarks, headings in order, lists, buttons vs links); accessible names for icon buttons; `aria-live="polite"` for donation status updates and progress changes; images have meaningful `alt` (campaign owners prompted to provide it; decorative images `alt=""`). |
| Contrast | Text ≥ 4.5:1 (≥ 3:1 for large text); UI components and graphical objects (progress bars, input borders, focus rings) ≥ 3:1. Colour is never the only carrier of meaning (status badges include text). |
| Forms & labels | Every input has a visible `<label>`; required fields marked in text; instructions before inputs; `autocomplete` attributes; no placeholder-as-label; accessible authentication (no cognitive tests; allow paste and password managers; OTP field supports autofill) per 3.3.8. |
| Errors | Inline error text associated via `aria-describedby`; error summary at top with links to fields; messages say what went wrong and how to fix it; preserve user input on error; no time-outs on forms without warning and extension. |
| Touch targets | Minimum 24×24 CSS px (WCAG 2.2 2.5.8) with spacing; **project target 44×44 px** for all primary controls. No drag-only interactions (2.5.7). |
| Motion | Respect `prefers-reduced-motion`; no flashing content. |
| Zoom/reflow | Usable at 200% zoom and 320 px reflow without loss of content. |
| Language | `lang` attribute set per page and per passage when mixed languages. |
| Money | Amounts read correctly by screen readers (formatted strings with currency name available, e.g. visually "US$50.00", accessible "50 US dollars" where ambiguity exists; ZiG labelled "ZiG"). |

Verification: axe-core in component and E2E tests (zero serious/critical violations), manual keyboard and
screen-reader (TalkBack on Android, VoiceOver on iOS, NVDA on Windows) walkthrough of primary journeys before
each release from Stage 7.

---

## 7. Money display and input (see [MONEY.md](MONEY.md))

- The API returns `{"amount_minor": "10000", "currency": "USD"}`. `amount_minor` is a **string** and stays a
  string or `BigInt` in TypeScript. **Never** `Number(amount_minor)`, `parseFloat`, or `/ 100`.
- Formatting: convert the minor-unit string to an exact decimal string using the currency's minor-unit
  count from the API/currency table (string manipulation or BigInt), then format with `Intl.NumberFormat`
  using the exact decimal *string* input (supported in modern engines) or locale-aware string grouping — never
  via floating-point division. Shared helper in a single `money` module with property tests.
- Currency display: USD as "US$" or "USD" per design system; ZWG displayed as **"ZiG"** (code stays `ZWG`
  in data). Always show the currency; never show a bare number as money.
- Input: user-typed amounts are parsed from a decimal string into minor units with strict validation (digits,
  at most the currency's minor digits, no exponent, no negative, within min/max). Invalid input is rejected,
  not rounded. The API re-validates; the client is a convenience.
- Never sum, compare or convert USD and ZiG in the UI. Campaign pages show raised amounts **per currency**;
  the progress bar uses the goal-currency amount only. Any future indicative conversion must be labelled
  "indicative", show rate source and timestamp, and come from the API.
- Fees shown to donors are the values returned by the API (Stage 12), not calculated in the browser.

---

## 8. Frontend security

- **Secrets:** nothing secret in the client bundle. Only variables prefixed `NEXT_PUBLIC_` reach the browser,
  and they are public by definition — never name a secret `NEXT_PUBLIC_*`. Server-only modules import a
  server-only guard so accidental client imports fail the build.
- **CSP:** strict CSP with per-request nonces (`script-src 'nonce-…' 'strict-dynamic'`), no `unsafe-inline`
  scripts, no `unsafe-eval` in production, `object-src 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`,
  `form-action 'self'` plus explicitly allow-listed PSP hosted-payment origins. `connect-src` limited to self
  and required telemetry endpoints. Reporting via `report-to`.
  **Implemented in Stage 4** (`apps/web/src/proxy.ts`, `src/lib/security/csp.ts`, [ADR-033](adr/ADR-033-per-request-nonce-csp.md)):
  per-request nonce with `'strict-dynamic'`, no `unsafe-inline` for scripts or styles, `unsafe-eval` only in
  `next dev`; `cacheComponents` disabled so every page renders per request. `report-to` is not configured yet
  (Stage 18). The Stage 3 `unsafe-inline` deviation is closed.
- Other headers (set at proxy or Next.js level, verified in tests): HSTS (values and preload timing per [SECURITY.md](SECURITY.md) §11: preload once the domain is stable),
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy`
  disabling unused features (camera allowed only on KYC capture pages if used).
- **XSS:** React escaping by default; `dangerouslySetInnerHTML` prohibited except through a reviewed
  sanitiser for campaign rich text (allow-listed tags, no inline styles/scripts/URLs other than http(s)).
  User-supplied links get `rel="nofollow ugc noopener noreferrer"`.
- **CSRF:** state-changing calls include the CSRF token from the API and rely on SameSite cookies + Origin
  checks enforced by the API (SECURITY.md).
- **Auth state:** session is an HttpOnly cookie; tokens are never stored in `localStorage`/`sessionStorage`
  or exposed to JavaScript.
- **Payments:** card entry happens only on PSP-hosted pages/fields; FundZim pages never render inputs for
  PAN/CVV. Redirect return pages show "checking payment status" and query the API.
- **KYC capture:** documents upload directly to API endpoints (or presigned quarantine uploads issued by the
  API); never stored in browser storage; previews revoked after upload.
- **Dependencies:** minimal; lockfile committed; `npm audit` in CI; no CDN-loaded scripts.
- **Privacy:** no third-party trackers by default; anonymous donors never revealed in UI responses (the API
  does not send their identity to public/owner views).

---

## 9. Internationalisation readiness

- English first. Shona and Ndebele planned later; architecture must not block them.
- All user-facing strings go through a message catalogue from the first real UI (Stage 7); no hard-coded
  copy in components.
- Locale-aware formatting for dates (Africa/Harare default display timezone, user preference later), numbers
  and currency via `Intl`, with the money rules in §7.
- Avoid text in images; allow for string expansion (~30%) in layouts.
- `lang` attributes set per locale; URLs locale-neutral initially (locale routing decided in Stage 7).

---

## 10. Sharing, OpenGraph and WhatsApp previews (Stage 7 basics, Stage 16 optimisation)

- Campaign pages are server-rendered with complete metadata: `og:title`, `og:description`, `og:image`
  (1200×630, < 300 KB, JPEG/PNG for broad WhatsApp compatibility, absolute HTTPS URL), `og:url` (canonical),
  `og:type`, `twitter:card=summary_large_image`, canonical link.
- Preview text must not include sensitive information (no beneficiary health details beyond what the owner
  published, never donor identities).
- Suspended/frozen/cancelled campaigns return metadata reflecting their status so old shares don't solicit
  donations misleadingly.
- Share UI: WhatsApp share link (`https://wa.me/?text=…`), copy-link with confirmation announced to screen
  readers, native Web Share API where available, QR code generation (client-side, no third-party service),
  social links.
- Referral attribution via short share codes only where privacy-appropriate and consented (LR-023); no
  third-party tracking pixels.
- No WhatsApp messaging API integration until Stage 16 and only with an approved consent model.

---

## 11. Testing (see [TESTING.md](TESTING.md))

- Vitest + React Testing Library for components; axe checks in component tests.
- Playwright E2E with mobile viewport first, throttled network, axe scans per page.
- Money helper: property tests (fast-check) for parse/format round-trip per currency.
- Lighthouse CI budgets from §5.
