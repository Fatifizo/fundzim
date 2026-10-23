# Stage 18 — Network security, TLS and the reverse proxy

## 1. Security zones

| Zone | Members | Inbound allowed from | Implemented where |
|---|---|---|---|
| Public ingress | TLS-terminating proxy / WAF | Internet 443 | proposed (no proxy in the repository) |
| Edge | web | proxy | compose network `edge` (web 172.28.1.10, API) |
| Application | API, worker | web (API only), proxy (webhooks → API) | compose `default` |
| Data | PostgreSQL, Valkey, Garage, ClamAV | API, worker | compose `default` (production: separate subnet, security groups) |
| Secrets | OpenBao | API, worker | compose `default` (production: separate subnet, mTLS) |
| Monitoring | metrics scrapers, log shippers | application internal listeners | proposed |
| Backup | backup storage | backup job only | proposed (object lock) |
| Administration | bastion / VPN | named administrators | proposed |

**Implemented and verified (development):** the web container is only on `edge` and cannot resolve or reach any data
service (checked from inside the container: DNS `EAI_AGAIN` for postgres, redis, secrets, storage, clamav; direct IP
connect to PostgreSQL timed out); the API is on both networks. Every published development port binds to 127.0.0.1;
database and secret-manager ports are never public. `TestComposeNetworkSegmentation` keeps it so.

## 2. Reverse proxy and HTTP

| Concern | Implementation |
|---|---|
| Trusted proxy headers | the API trusts `X-Forwarded-For` only from `TRUSTED_PROXY_CIDRS` (development: exactly the web container `172.28.1.10/32`; nothing by default), walks right to left skipping trusted hops, stops on malformed entries, caps at 16 hops, ignores `X-Real-IP`/`Forwarded`; the web proxy replaces the header with one observed address and only trusts `WEB_TRUSTED_PROXY_CIDRS` |
| Client IP in tests | integration tests present their own address through the trusted loopback peer (a test-only route); production code never trusts an arbitrary header |
| Body limits | API 1 MiB default (max 64 MiB), upload routes 10 MiB; web proxy caps 1 MiB / ~10.25 MiB |
| Timeouts | public server: header 5 s, read 15 s, write request timeout + 5 s, idle 120 s, 64 KiB headers |
| Security headers | API: nosniff, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, CSP `default-src 'none'`, CORP same-origin, Permissions-Policy, no-store, HSTS outside development; web: nonce CSP, nosniff, DENY, COOP, Permissions-Policy |
| CORS | none by default; `*` refused; https origins only; credentials never allowed |
| CSRF | HMAC token bound to the session, Origin and `Sec-Fetch-Site` checks |
| Open redirects | login `next` parameters are validated same-origin paths (Stage 3); provider redirect URLs must be https on a host the adapter declares (Stage 8) |

## 3. TLS

Nothing in the repository terminates TLS; the development stack is plain HTTP on loopback. Production configuration
refuses: database URLs without `sslmode=verify-full`, `redis://` (needs `rediss://`), http S3 endpoints, http OpenBao
(Stage 18: outside development and test), http `APP_PUBLIC_URL`, `PUBLIC_SITE_URL` and sandbox webhook URLs (Stage 18),
`SMTP_TLS=none`. Clients verify certificates (no `InsecureSkipVerify`). The proxy must offer TLS 1.2+ (1.3 preferred),
modern AEAD ciphers only, HSTS with includeSubDomains, automatic certificate renewal with expiry monitoring (alert at
21 and 7 days), OCSP stapling. Certificate-expiry behaviour: the secret-manager client refuses an expired certificate, a
certificate for another name and a certificate from an unknown CA, and accepts a valid one from the configured private
CA (`TestTLSRefusesExpiredWrongNameAndUnknownCA`, Stage 18). A renewal failure must page before expiry (monitoring
requirement, Stage 20); renewal itself is a property of the not-yet-existing proxy.

## 4. Service-to-service authentication

Today: per-role passwords over (production) TLS; AppRole for OpenBao; per-bucket S3 keys. No request is trusted because
of its source network: the API authorizes every request by session and permission; webhooks are verified by signature;
the worker's internal endpoints expose no mutation. Mutual TLS between services, short-lived database credentials
(OpenBao database secrets engine) and network policies are production work (EXTERNALLY_PENDING).
