# Stage 18 — Application security (web, uploads, Go)

## 1. Next.js application

| Concern | Finding / implementation |
|---|---|
| Rendering | Server Components fetch the API at `API_BASE_URL` (server-only); every document rendered per request with a nonce CSP (`src/proxy.ts`, `src/lib/security/csp.ts`: `strict-dynamic`, no `unsafe-inline` outside development, `frame-ancestors 'none'`, `base-uri 'none'`) |
| Server actions / API routes | no server actions; one route handler: the `/api/v1` proxy (hop-by-hop headers stripped, X-Forwarded-For rewritten, body caps, timeouts) |
| XSS | React escaping; no `dangerouslySetInnerHTML` with user content; campaign text rendered as text; ZAP 10031 (user-controllable attribute) is informational: values reflected into attributes are escaped |
| CSRF | API-enforced (HMAC token + Origin); the web reads the CSRF cookie (not HttpOnly by design) to send the header |
| Sensitive hydration | only `/me` data the user may see; no secrets in `NEXT_PUBLIC_*`; no session token in JavaScript (HttpOnly cookie) |
| Browser storage | none for secrets or financial state (Stage 16 analytics use no storage) |
| Redirects | `next` parameters are validated same-origin paths |
| Error boundaries | `error.tsx`, `global-error.tsx`, `not-found.tsx`; production shows no stack or digest detail. Under API rate limiting the server render returns HTTP 500 with the generic page (F-S18-16) |
| Source maps | not served in production builds (Next default `productionBrowserSourceMaps: false`) |
| Dependencies | **Stage 18: Next.js 16.4.0 → 16.4.2** for GHSA-2464-fhr4-w9xw and GHSA-673q-mjm6-r5xr (CRITICAL) and CVE-2026-94482 (HIGH), found by Trivy; `npm audit --omit=dev` 0 |
| Credential forms | **Stage 18 (F-S18-13):** every auth/account form is `method="post"`; without JavaScript a submission no longer puts the password into the URL (ZAP 10024 showed `/login?email=…&password=…`) |

## 2. File uploads

| Path | Purpose | Controls |
|---|---|---|
| Campaign media | `CAMPAIGN_IMAGE` | size limit, signature sniffing (JPEG/PNG only), quarantine → ClamAV → re-encode (strips metadata) → public bucket; derivatives only |
| KYC documents | `KYC_DOCUMENT`, `BENEFICIARY_EVIDENCE` | JPEG/PNG/PDF only, ClamAV, private-kyc bucket, SSE-C per object, 60-second download tickets, `fundzim_kyc` pool, audited access |
| Reconciliation statements | `RECONCILIATION_REPORT` | CSV/JSON text only (markup refused), ClamAV before parsing, private evidence bucket, SHA-256 dedupe, all-or-nothing parser, formula neutralisation on export |
| Support / administrative evidence | evidence records reference stored objects; same pipeline as compliance documents |

Uploaded content is never executed or rendered as HTML; filenames are never used as identities or paths (object ids);
a scan error never promotes an object (FAILED_SCAN, retried). `fundzimctl verify objects` (Stage 18) re-reads objects and
checks their SHA-256: 6 602 verified, 0 integrity failures, 0 missing on the development stack.

## 3. Go services

gosec v2.29.0 over 283 files / 84 777 lines: 9 findings, all reviewed and annotated (`#nosec` with a reason): two
`math/rand` uses for retry jitter (G404), six MD5 uses that are the S3 SSE-C key checksum mandated by the protocol and
deterministic seed identifiers matching SQL `md5()::uuid` (G401/G501), one scenario-name constant (G101). CI now fails on
any new gosec finding. govulncheck: 0 reachable vulnerabilities (Go 1.27.3; GO-2026-6809 fixed in Stage 17). SQL is
parameterised everywhere (archtest also enforces table ownership); no shell execution; file access only to embedded
migrations and configured CA files.
