# Stage 18 — API security

Scope: every `/api/v1` route (the OpenAPI contract `api/openapi/fundzim-v1.yaml` is checked against the router by
`TestEveryRouteHasAPolicyAndIsInOpenAPI`; Stage 18 adds six security routes).

| Risk | Control | Evidence |
|---|---|---|
| Authentication bypass | every route has a policy; non-public routes denied without a session | router tests; ZAP API scan |
| Authorization bypass / BOLA | permission per route; object ownership checks in services; staff routes 404 for non-staff | module ITs (security-testing.md §3) |
| Mass assignment | handlers decode into explicit structs; unknown JSON fields refused (`DecodeJSON` disallows unknown fields) | httpx tests |
| Injection | parameterised SQL; no dynamic SQL from input (filters are allow-lists); CSV export formula-safe | gosec, recon tests |
| Excessive data exposure | response structs per audience (owners see coarse reason categories; staff see more with permission); no secrets, KYC values or full account numbers in responses | module tests |
| Unbounded queries | limits on every list (cursor pagination, maximum page sizes, report row caps) | API tests |
| Rate-limit bypass | per-IP (trusted proxy only) and per-account policies; never fail open | ratelimit tests; DR exercise |
| Unsafe error responses | stable codes, generic messages, cause logged (scrubbed) never returned; **Stage 18:** dependency outages answer 503 + Retry-After | `TestDependencyOutageIs503` |
| Replay | idempotency keys (unique constraints) on every financial mutation; webhook inbox dedupe; single-use tokens | payments/payouts/idempotency tests |
| Insecure uploads | application-security.md §2 | storage tests |
| CSRF | HMAC token + Origin + Sec-Fetch-Site | `TestCSRFMiddleware` |
| Optimistic concurrency | `version` / `If-Match` (428/412) on mutable records | campaign, recon, alert tests |

## DAST

Disposable environment: the local compose stack (development data, no real provider). OWASP ZAP 2.17.0
(`ghcr.io/zaproxy/zaproxy@sha256:5ccfff9a…`), host network, 2026-10-23:

- **API** (`zap-api-scan.py`, OpenAPI import, safe mode `-S` = passive rules only, target `http://127.0.0.1:8080`):
  FAIL 0, WARN 0, PASS 120; informational: 659 4xx answers (expected: unauthenticated requests to protected routes),
  `no-store` on responses.
- **Web** (`zap-baseline.py`, 3-minute spider, passive): FAIL 0, WARN 8, PASS 60 — credentials in URL from a GET form
  fallback (fixed, F-S18-13), 500 responses while the crawler was rate-limited (F-S18-16), COEP/CORP missing on documents
  (F-S18-17), informational items.

No active or authenticated scan was run (KI-S18-21); that belongs to the penetration test (incident-response.md §5).
