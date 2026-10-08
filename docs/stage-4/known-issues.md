# Stage 4 Known Issues

Status: **OPEN** (owner stage named), **ACCEPTED** (deliberate, reason given), **BLOCKED** (needs an owner
action), **ENVIRONMENT** (property of the development machine). Security findings are detailed in
[security-review.md](security-review.md) (F-xx).

## 1. Stage 3 known issues — status after Stage 4

| Stage 3 ID | Status now |
|---|---|
| KI-01 worker/outbox/idempotency | **RESOLVED** (worker, outbox, idempotency middleware) |
| KI-02 no local `-race` | ENVIRONMENT (unchanged) — and CI is not running (KI-S4-01), so `-race` has not run on Stage 4 code |
| KI-03 dev-only npm advisories | OPEN (unchanged; `npm audit --omit=dev` = 0) |
| KI-04 web proxy client IP | **RESOLVED** (peer stamp + trusted-proxy rules in web and API) |
| KI-05 per-replica limiter | **RESOLVED** (Valkey GCRA, tested across two replicas) |
| KI-06 CSP `unsafe-inline` | **RESOLVED** (nonce CSP; ADR-033) |
| KI-07 integration rows in dev DB | ACCEPTED (now also accounts and staff; see KI-S4-09) |
| KI-08 middleware order vs ARCHITECTURE §5 | **RESOLVED** (ARCHITECTURE §5 updated with the Stage 4 order) |
| KI-09 PGlite vs PostgreSQL 17 | Stage 4 migrations ran on real PostgreSQL 17.11 |
| KI-10 Garage | ACCEPTED (unchanged) |
| KI-11 `config check` output | OPEN (low priority, unchanged) |
| KI-12 tracing export | OPEN — not done in Stage 4; moved to Stage 18 |
| KI-13 architecture tests | **RESOLVED** (`internal/archtest`) |
| KI-14 OpenAPI-generated web types | OPEN — still hand-written types; Stage 5 |
| KI-15 acceptance records | OPEN — Stages 1–3 acceptance still not recorded |

## 2. Stage 4 issues

| ID | Issue | Impact | Status / owner |
|---|---|---|---|
| KI-S4-01 | **CI not verified.** Branches `stage-3/core-platform` and `stage-4/identity-access` are pushed; the GitHub API lists **0 workflows and 0 runs** for the (public) repository, so Actions appears disabled. | No independent CI evidence; `-race`, the CI integration job and container builds have not run on GitHub | **BLOCKED** — owner: enable Actions (Settings → Actions) and re-run; record results |
| KI-S4-02 | **Production cannot start:** KMS for field encryption / blind-index keys is not implemented (config refuses `local` outside development/test); no real SMS provider (dev_mailpit refused in production). | By design | OPEN — Stage 18 (KMS), Stage 15 (SMS) |
| KI-S4-03 | Per-account login throttle counts successful attempts and can be used to delay a known user's sign-in for up to 15 minutes (F-02). | Targeted nuisance/DoS, no lockout | OPEN — Stage 13/18 |
| KI-S4-04 | Breached-password check is offline (top 10 000) only (F-04). | Weaker screening | ACCEPTED for Stage 4 |
| KI-S4-05 | Handler-level `429` responses lack `Retry-After` (F-07). | Clients must parse the message | OPEN — small fix |
| KI-S4-06 | Staff reactivation and break-glass grant APIs are not implemented (schema exists). Staff role **revocation** works. | Reactivating staff needs a later admin feature | OPEN — Stage 14 (admin portal), maker-checker |
| KI-S4-07 | `cacheComponents`/partial prerendering disabled for the nonce CSP (ADR-033): every page renders per request, no CDN-cacheable HTML. | Performance cost for public pages | ACCEPTED — revisit for Stage 7 public pages (e.g. hash-based CSP for static pages) |
| KI-S4-08 | Organisation invitations list on the dashboard is not built (API exists: `GET /me/organisation-invitations`). No organisation management UI. | Org features are API-only | OPEN — Stage 5/6 (with KYB and organisation campaigns) |
| KI-S4-09 | Identity integration tests create users/staff/orgs in the shared dev database and revoke existing SUPER_ADMIN assignments to re-run the bootstrap ceremony (F-15). | Dev data only | ACCEPTED — CI uses a throwaway DB; `make reset` locally |
| KI-S4-10 | `golang.org/x/crypto` advisory GO-2026-5932 (`openpgp`, not imported; no fix) (F-14). | None (not called) | ACCEPTED — govulncheck monitors |
| KI-S4-11 | OpenAPI `Organisation.org_type` enum from Stage 2 (`RELIGIOUS_BODY`, `COMMUNITY_GROUP`, …) differs from the implemented schema/database (`FAITH_BASED`, `COMMUNITY_BASED`, … per `docs/database/organisation-beneficiary-schema.md`). New Stage 4 schemas use the implemented values; the old schema remains for not-yet-built operations. | Contract inconsistency for later stages | OPEN — reconcile in Stage 5 (KYB) |
| KI-S4-12 | Session revocation on email change uses reason `PASSWORD_CHANGED` (F-09). | Imprecise label | OPEN — add `EMAIL_CHANGED` with the next identity migration |
| KI-S4-13 | Local databases that applied the Stage 4 identity migrations **before** the River migrations (earlier timestamps) are refused by goose ("missing out-of-order migrations"). Fresh and CI databases are unaffected. | Developer friction only (seen on a scratch DB) | ACCEPTED — `migrate down` to before `20261008130000` and up, or `make reset` |
| KI-S4-14 | Playwright auth tests run against a mock API (server-side session checks cannot be intercepted); the real path is covered by Go integration tests and a manual smoke. | Some UI ↔ API drift could slip through | OPEN — add a real-stack Playwright job when CI runs |
| KI-S4-15 | The web peer-address stamp depends on Next.js internals (F-12). | Breaks only on framework upgrade (fails closed) | OPEN — re-verify per upgrade |
| KI-S4-16 | Recompose note: after the compose network got a fixed subnet, existing local stacks may need `docker compose up -d --force-recreate` for service DNS names to resolve. | Developer friction | ACCEPTED (documented) |
