# ADR-033: Per-request nonce CSP; Next.js `cacheComponents` disabled

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 4)
- **Stage:** 4

## Context

FRONTEND.md §8 requires a strict CSP with per-request nonces and no `unsafe-inline`. Stage 3 shipped a static
policy with `script-src 'unsafe-inline'` because nonces force request-time rendering, which conflicts with
Partial Prerendering under Next.js `cacheComponents` (Stage 3 KI-06, security review F-04). Stage 4 adds
authenticated pages (account security, MFA enrolment with a TOTP secret on screen), so XSS impact grows.

In this Next.js version, a build-time page shell ships framework scripts without a nonce; a nonce policy
blocks them. Forcing individual pages dynamic while keeping `cacheComponents` made `notFound()` answer 200 and
turned server `redirect()` into client-side redirects.

## Decision

1. `src/proxy.ts` generates a 128-bit nonce per request and sets
   `script-src 'self' 'nonce-…' 'strict-dynamic'`, `style-src 'self' 'nonce-…'`, `object-src 'none'`,
   `base-uri 'none'`, `frame-ancestors 'none'`, `form-action 'self'`, `frame-src 'none'`; no `unsafe-inline`
   anywhere; `unsafe-eval` (and inline styles) only under `next dev`.
2. `cacheComponents` and `partialPrefetching` are **disabled**; the root layout renders every page per request.
   The root `loading.tsx` is removed so redirects and 404s keep real status codes.
3. `e2e/csp.spec.ts` asserts a fresh nonce on every page, nonce on every script/style tag, no inline style
   attributes, zero violations, working hydration, and that an injected non-nonced script is blocked.

## Consequences

- Positive: XSS defence matches FRONTEND.md §8 before user content arrives (Stage 7); correct HTTP status codes.
- Negative: no static/CDN-cacheable HTML; every page costs a server render. Acceptable at current scale.
  Revisit for Stage 7 public campaign pages (options: hash-based CSP for fully static pages, edge caching of
  anonymous pages with per-response nonce rewriting, or a later Next.js release that nonces PPR shells).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep `unsafe-inline` (Stage 3) | Weak XSS protection on account-security pages |
| Hash-based CSP | Next.js inline bootstrap scripts vary per render; brittle |
| Nonces + `cacheComponents` with per-page `dynamic` | Broke `notFound()`/`redirect()` status codes (observed) |

## Related

FRONTEND.md §8, ADR-003, Stage 3 KI-06 / F-04, docs/stage-4/known-issues.md KI-S4-07.
