# Stage 18 — Secrets management

Values are never printed in this document, in reports or in logs; only names.

## 1. Inventory

| Secret | Where it lives (development) | Production source | Rotation |
|---|---|---|---|
| PostgreSQL role passwords (migrator, app, worker, kyc, compliance, ledger, readonly, backup, superuser) | `.env` (generated) | infrastructure secret store / managed DB IAM | §4.1 |
| Valkey password | `.env` → tmpfs config file in the container (never argv) | secret store | §4.1 |
| S3 key pairs (public, KYC, evidence) | `.env`, Garage | secret store | §4.1 |
| Garage RPC secret, admin token | `.env` | infrastructure | restart with new values |
| OpenBao root token (dev only) / AppRole role id + secret id | `.env` / — | AppRole (static tokens refused in production) | AppRole secret-id rotation |
| CSRF secret | `.env` | `secretref://` (Stage 18) | §4.3 (sessions reset) |
| Field keys (app, KYC, compliance) | `.env` hex | `secretref://` (Stage 18) | §4.2 overlap |
| Blind-index keys (app, KYC), document-ticket key | `.env` hex | `secretref://` | §4.3 (re-index) |
| SSE-C master key | `.env` hex | `secretref://` | §4.2 overlap |
| Audit checkpoint key (worker only) | `.env` hex | `secretref://` | §4.4 |
| Provider credentials and webhook secrets (sandbox) | OpenBao KV v2 only | OpenBao | CURRENT/PREVIOUS (Stage 9) |
| SMTP credentials | `.env` | secret store | §4.1 |
| Backup encryption key | key file outside the backup directory (mode 600) | split custody, outside the platform | new key per backup generation |
| CI credentials | none (no repository secrets; `contents: read`) | — | — |

Hard-coded secrets: none (`scripts/check-secrets.sh`, gitleaks over history in CI). `.env.example` holds placeholders
only; stale and misleading names were removed (F-S18-11).

## 2. Secret-manager integration (provider-neutral)

The KV v2 client (`internal/platform/secretstore`) works with OpenBao or Vault: TLS 1.2+, optional private CA, no
redirects, bounded cache, version tracking with a rotation callback, AppRole login with refresh at 80 % of the lease.
**Stage 18:** with `FIELD_ENCRYPTION_PROVIDER=secretstore` every application key is a `secretref://` reference resolved
once at startup (`config.Security.ResolveManaged`, `internal/app/keys.go`); a reference that does not resolve stops the
process with the variable name only (tested against the compose OpenBao: success, rotation through a new KV path,
missing key, invalid credential, unreachable server). Outside development the address must be https and production
needs an AppRole. Measured: resolve 1.2 ms p50 uncached, 1.5 µs cached.

Production requirements still open (EXTERNALLY_PENDING): a persistent, highly available secret manager with its audit
device enabled, sealed-key custody, backups of its storage and documented unseal/recovery (backup-recovery.md §6). The
development OpenBao is in-memory and loses everything on restart (DR exercise: re-seeded in 3.3 s).

## 3. Exposure prevention

Repository: `check-secrets.sh` + gitleaks (history) in CI. Images: no secret build arguments (`TestDockerfilesPinnedAndUnprivileged`),
`.env` never copied. Browser bundle: only `NEXT_PUBLIC_*` values (none secret). Logs: key redaction plus free-text
scrubbing of error/panic texts (`TestErrorTextScrubbed`). Error responses: code and message only; readiness reports
categories (F-S18-05). Test artifacts: drill credentials live in the scratch directory (mode 600) and are never
committed. Documentation examples use placeholders.

## 4. Rotation procedures

### 4.1 Credentials with a single active value (database roles, Valkey, S3, SMTP)
1. Create the new value in the secret store. 2. For PostgreSQL: `ALTER ROLE … PASSWORD` (or a second role and grant
switch for zero downtime). 3. Roll the processes (they read credentials at startup). 4. Revoke the old value. 5. Record
the change (audit event / change ticket). Evidence for this stage: none performed against production (no production).

### 4.2 Keys with overlap (field keys, SSE-C master)
1. Store the new key; set it as the active key and list the old key in `*_PREVIOUS_KEYS`. 2. Roll the processes: new
data uses the new key, old data stays readable (`OpenWithStatus` reports which rows need re-encryption). 3. Re-encrypt
(KI-S18-03: no job yet). 4. Remove the old key from the list only when nothing uses it — a removed key makes its data
unavailable (tested). Evidence: `TestAEADRotationOverlap`, `TestSSEMasterRotation`, IT rotation through OpenBao.

### 4.3 Keys without overlap (blind indexes, CSRF, document tickets)
Blind indexes need a re-index (lookups compare HMACs); the CSRF secret change invalidates every session's token (users
sign in again); document tickets live 60 seconds. Plan these as maintenance windows (KI-S18-04).

### 4.4 Audit checkpoint key
Checkpoints are tied to the key fingerprint; after rotation the next run is a full verification and new checkpoints use
the new key (old ones are ignored, never trusted).

### 4.5 Webhook secrets
CURRENT and PREVIOUS are both accepted by key id; promote the new secret to CURRENT, keep the old as PREVIOUS until the
provider has switched, then remove it.

## 5. Revocation and compromise

Compromised key: rotate (above), revoke at the source, review audit events for its use, and follow the incident runbook
"Exposed API secret" (incident-response.md §3.2).
