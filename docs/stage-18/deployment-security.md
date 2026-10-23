# Stage 18 — Deployment security, environments and release management

Everything in §1 is **proposed** infrastructure: none of it is deployed or verified. No paid resource was provisioned and
nothing was deployed (brief §66, §69).

## 1. Production reference architecture (proposed)

| Layer | Proposal | Security requirements |
|---|---|---|
| Ingress | CDN/WAF + TLS-terminating reverse proxy (two instances) | TLS 1.2+/1.3, HSTS, request/header size limits, bot and rate controls, health-checked upstreams, access logs to the log store |
| Web | 2+ stateless containers (read-only, non-root, no capabilities) on the edge network | API base on the private network only |
| API | 2+ stateless containers, `TRUSTED_PROXY_CIDRS` = the proxies only | internal listener on the private network; `FIELD_ENCRYPTION_PROVIDER=secretstore`; AppRole per process |
| Worker | 2 containers (River leases coordinate) | no inbound traffic except the internal listener |
| PostgreSQL | managed, TLS (`verify-full`), PITR (WAL archive), one synchronous replica, encrypted volume, separate backup role | private subnet; admin from the management network only |
| Object storage | multi-node S3-compatible store or provider object storage with SSE-C, versioning, object lock for backups | per-bucket credentials, private buckets |
| Cache | Valkey with TLS and a password (optional for correctness) | private subnet |
| Secrets | OpenBao/Vault HA (raft), audit device on, AppRole, auto-unseal with a KMS when one is selected | private subnet; snapshots off-host |
| Monitoring | metrics scrape of internal listeners, central logs, alert routing/paging | separate zone; read-only |
| Backup | nightly encrypted dumps + WAL, object snapshots, off-host immutable copies; monthly restore drill | split key custody |
| Administration | bastion/VPN with MFA, named accounts, session recording | — |
| Compute sizing (starting point, to be load-tested in Stage 19) | API 2 × (2 vCPU, 1 GiB), worker 2 × (2 vCPU, 1 GiB), web 2 × (1 vCPU, 768 MiB), PostgreSQL 4 vCPU / 16 GiB | Argon2id memory: 64 MiB × 4 concurrent hashes per API process |

## 2. Infrastructure as code

The repository has none (only the development compose file). Requirements for Stage 20: version-controlled,
parameterised per environment, secrets referenced never embedded, `plan` reviewed before `apply`, drift detection,
rollback by re-applying the previous version, and `internal/deploytest`-style policy checks on the rendered
configuration.

## 3. Environment separation

| Rule | Enforcement today |
|---|---|
| Test credentials never in production | production refuses `SECRETS_PROVIDER=env`, static OpenBao tokens and `local` keys; simulators refused (`PAYMENTS_SANDBOX_ENABLED`, `RISK_SCREENING_SANDBOX_ENABLED`, `NOTIFICATIONS_SIMULATOR_ENABLED`, `LEDGER_SYNTHETIC_FIXTURES_ENABLED`, `MALWARE_SCANNER=dev`, `dev_*` senders) |
| Test providers never in live workflows | `PAYMENTS_EXECUTION_ENVIRONMENT=PRODUCTION` refused at startup while `psp.LiveExecutionEnabled=false`; payouts only DISABLED/LOCAL_SIMULATOR |
| No synthetic production financial data | CHECKs refuse PRODUCTION rows; ledger database designated TEST |
| No production secrets in test jobs | CI has no repository secrets and no environments (tested) |
| No cross-environment database access | one database URL per role, validated distinct; `verify-full` outside development |
| No accidental production deployment | no deployment pipeline exists; `migrate down` refused outside development/test |
| Staging is production-like | Stage 18: debug logs, plain-HTTP secret manager and http public URLs refused in staging |

## 4. Production configuration validation (startup gates)

`config.Load` refuses unsafe settings and names variables only (never values): missing secrets, development keys,
TLS requirements unmet, debug logging, simulators and test providers, `RATE_LIMIT_ENABLED=false`, http URLs, wrong cookie
names, weak Argon2 memory, missing storage credentials or shared buckets. Stage 18 adds: the `secretstore` key provider
(references only; resolution failure stops startup), previous-key validation, staging hardening, the worker's checkpoint
key. Proven: a hardened production configuration validates (`TestProductionKeysFromSecretManager`), and every unsafe
variant tested is refused (`TestProductionRefusesUnsafeSettings`, `TestStagingIsProductionLike`, payments/secrets/
notifications rule tests). The DR exercise showed an invalid release exits 1 before serving.

## 5. Release management (requirements; no release pipeline exists)

A release is a commit SHA with: green CI (all jobs, including SAST, image scan, SBOM), the supply-chain evidence
artifact, migrations reviewed (forward-only in production), a verified backup and restore drill younger than the
release, a written rollback plan, an approval by someone other than the author, and deployment evidence (who, when,
which digests). Stage 18 builds are not promoted anywhere.

## 6. Migration safety and rollback

| Kind | Procedure |
|---|---|
| Application rollback | redeploy the previous image digests (stateless tiers); allowed only if the schema is backward compatible (expand → migrate → contract across releases) |
| Database | forward-only in production (ADR-028); a bad migration is fixed by a new migration. Downs exist for development and are tested by the CI round trip on a populated database; they never delete records other tables reference (Stage 17 lesson; Stage 18 Downs drop only their own new tables) |
| Configuration | versioned; roll back to the previous version; invalid configurations stop at startup |
| Secret rotation rollback | keep the previous key as a previous key (overlap) until the new one is proven; never delete a key that still protects data |
| Financial state | never rolled back by deleting or editing posted records: corrections are maker-checker reversals and adjustments |

Tested: up/down/up round trips of the Stage 18 migration on the development database (several cycles), a failing
migration leaves nothing (`TestSecurityFailedMigrationLeavesNothing`); concurrent `migrate up` runs are serialised by the
PostgreSQL advisory session lock `fundzimctl` takes since Stage 18 (`TestSecurityConcurrentMigrationsSerialise`: two
concurrent runs both succeed, the migration is applied once).
