# ADR-003: Next.js frontend (presentation layer only)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (UI work begins Stage 7; admin portal Stage 14)

## Context

FundZim is mobile-first. Many users browse on Android handsets over intermittent or expensive data, and arrive
from WhatsApp links. Campaign pages must load fast and render rich link previews (OpenGraph), which requires
server-side rendering. The app should also become an installable PWA.

## Decision

We will use **Next.js with TypeScript, React and Tailwind CSS**.

- **Next.js 16.4** (App Router, `src/` directory, ESLint, Tailwind CSS v4, `@/*` import alias) was scaffolded
  with `create-next-app` and lives in **`apps/web`**. At Stage 0 it is the unmodified scaffold.
- Next.js is a **presentation layer only**:
  - no direct database access;
  - no business or financial logic;
  - no secrets beyond public configuration.
  All system-of-record logic lives in the Go API.
- The browser reaches the API **same-origin at `/api/v1/`** through a reverse proxy. This avoids CORS and
  keeps session cookies first-party.
- Public campaign pages are **server-rendered** by Next.js calling the API, for speed, SEO and
  WhatsApp/OpenGraph previews.
- Money is handled as `{amount_minor: string, currency}` and formatted with `Intl.NumberFormat` from an exact
  decimal string, never via float arithmetic (ADR-005).
- Accessibility targets WCAG 2.2 AA (see [FRONTEND.md](../FRONTEND.md)).
- The scaffold's `AGENTS.md` warns that this Next.js version has breaking changes. Contributors must read
  `node_modules/next/dist/docs/` before writing Next.js code.

## Consequences

### Positive
- SSR and static generation give fast first paint and rich social previews, plus built-in image optimisation
  for low bandwidth.
- A mature React ecosystem and accessible component libraries are available.
- The strict separation means a Next.js compromise or bug cannot directly corrupt financial data.

### Negative / costs
- There are two runtimes to operate (Node.js for web, Go for API).
- API contracts must be kept in sync across languages (OpenAPI plus generated types, from Stage 3).
- Next.js server components can tempt developers to add "just one" DB query or business rule. Code review and
  CLAUDE.md rules forbid this.

### Follow-up work
- Stage 3: reverse-proxy wiring and typed API client.
- Stage 7: PWA manifest and service worker, image strategy, CSP with nonces.
- Stage 14: decide whether the admin portal is a separate app or host.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Single-page app (Vite + React) only | No SSR, so poor WhatsApp/OpenGraph previews and slower first load on low-end phones. |
| Server-rendered Go templates | Fewer moving parts, but a weaker interactive/PWA story and a smaller UI talent pool. |
| Next.js as full-stack backend | Puts financial logic in a JavaScript runtime with float numbers, and blurs trust boundaries. Rejected in favour of ADR-002. |

## Security implications
- The web tier holds no DB credentials or provider secrets.
- XSS is mitigated by React escaping, a strict CSP (nonces), and no `dangerouslySetInnerHTML` on user content
  without sanitisation.
- Same-origin API avoids permissive CORS.
- The `NEXT_PUBLIC_*` variables are public by definition and must never contain secrets.

## Financial implications
The frontend displays and collects amounts but never decides financial outcomes. A browser redirect after
payment is **never** treated as confirmation (see ADR-007).

## Related
ADR-001, ADR-002, ADR-005, ADR-007, [FRONTEND.md](../FRONTEND.md), [ARCHITECTURE.md](../ARCHITECTURE.md).
