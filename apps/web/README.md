# apps/web — FundZim web frontend

Next.js 16.4 (App Router, `cacheComponents` + Partial Prerendering) · React 19 · TypeScript (strict,
`noUncheckedIndexedAccess`) · Tailwind CSS v4.

This app is **presentation only**: it renders pages and calls the Go API at `/api/v1/`. It never talks to the
database, never contains business or financial logic, and never holds secrets. Standards:
[docs/FRONTEND.md](../../docs/FRONTEND.md). Read [AGENTS.md](AGENTS.md) before writing Next.js code — this
Next.js version has breaking changes relative to older documentation (e.g. `error.tsx` receives `retry`, not
`reset`; request-time data must sit inside `<Suspense>` under `cacheComponents`).

**Stage 3 status:** development preview. Homepage, How it works, About and Contact have real (honest) content.
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
npm run test:e2e    # Playwright: builds, starts the standalone server, runs e2e/ (Chromium desktop + Pixel 7)
```

First E2E run on a machine: `npx playwright install chromium` (no sudo needed for the headless shell).

## Environment variables

| Variable | Where read | Required | Notes |
|---|---|---|---|
| `API_BASE_URL` | Server only, **at request time** (Route Handler proxy, Server Components) and at server start (validation) | **Production: yes.** Dev/test default `http://127.0.0.1:8080` | Origin only (`http://api:8080`), no path/credentials. In production (`NODE_ENV=production`) a missing/invalid value makes the server **exit on start** (`src/instrumentation.ts`) and the proxy answers `503 SERVICE_UNAVAILABLE`; there is no silent fallback. Not needed at build time. |
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
`Forwarded`/`X-Forwarded-*`/`X-Real-IP` (real client IP propagation is the reverse proxy's job); forwards
cookies, `Origin` and a validated `X-Request-ID` (generated if absent/malformed); 1 MiB body cap
(`413 PAYLOAD_TOO_LARGE`); 30 s upstream timeout; never retries; passes redirects and multiple `Set-Cookie`
through; upstream failure → error envelope `SERVICE_UNAVAILABLE` (503, or 504 on timeout) with
`retryable: true` only for GET/HEAD.

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

Set in `next.config.ts` `headers()` for every route: CSP, `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy` (camera,
microphone, geolocation, payment, … disabled), `Cross-Origin-Opener-Policy: same-origin`; `X-Powered-By`
removed. HSTS is set by the TLS-terminating reverse proxy.

**CSP deviation (tracked):** FRONTEND.md §8 asks for a nonce-based CSP. Nonces force fully dynamic rendering and,
per the bundled Next.js CSP guide, are incompatible with Partial Prerendering (`cacheComponents`). Stage 3
therefore uses a static CSP with `script-src 'self' 'unsafe-inline'` (Next.js inline bootstrap scripts), no
`'unsafe-eval'` in production, no third-party origins, `object-src 'none'`, `base-uri 'none'`,
`frame-ancestors 'none'`. Verified in the built app by the E2E suite (hydration works; no CSP violations).
Must be revisited before Stage 7 renders owner-supplied content (nonce via `proxy.ts` on dynamic routes, or
hashes).

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
