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
npm run typecheck   # tsc --noEmit
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
(`413 PAYLOAD_TOO_LARGE`); 30 s upstream timeout; never retries; passes redirects and multiple `Set-Cookie`
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
