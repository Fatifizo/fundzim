# ADR-041: Secret management — Vault KV v2 API (OpenBao by default) for provider credentials

- **Status:** Proposed (Stage 9, 2026-10-13)
- **Deciders:** Technical lead (Stage 9); owner acceptance pending; hosting decision deferred to Stages 18/20
- **Related:** ADR-039 §6 (secret references), ADR-040; [secret-management.md](../stage-9/secret-management.md);
  KI-S8-04

## Context

Stage 8 stores provider credentials as `secretref://…` **references** in `provider_accounts` and resolves them from
environment variables (`psp.EnvResolver`). Environment variables are acceptable for the simulator's generated test
keys but not for real provider credentials: no access control per secret, no versioning, no rotation trail, no
audit of reads, and values copied into every process and compose file. Stage 9 must choose a production-grade secret
manager and integrate it before any real credential exists. Production hosting (cloud provider, KMS) is undecided
(ARCHITECTURE: "Production hosting/topology, CDN, KMS, secret manager | 18/20").

## Options considered

| Option | For | Against |
|---|---|---|
| Cloud secret manager (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) | Managed, KMS-backed | Hosting not chosen; binds the code to one cloud now |
| HashiCorp Vault | Mature KV v2 API, AppRole, audit devices, policies | Business Source Licence since 2023 |
| **OpenBao** (Linux Foundation fork of Vault, MPL-2.0) | Same KV v2 and AppRole HTTP API as Vault; self-hostable anywhere; open licence; runs in compose and CI | Must be operated (unseal, HA storage, backups) unless a managed Vault-compatible service is chosen |
| Keep environment variables | No work | Fails the requirements above |

## Decision

1. FundZim resolves provider secrets through the **Vault KV version 2 HTTP API**. The default implementation and the
   local/CI service is **OpenBao 2.4.1** (`openbao/openbao:2.4.1`). HashiCorp Vault or a managed Vault-compatible
   service can be substituted without code changes. A cloud-native manager can be added later behind the same
   `psp.SecretResolver` interface (Stage 18/20) by a new ADR.
2. `SECRETS_PROVIDER=env|kv2`. **Production refuses `env`** and requires `kv2` with an `https://` address and
   **AppRole** authentication (static tokens are refused in production). Development and CI may use a token.
3. Reference mapping: `secretref://<path>` → `GET /v1/<SECRETS_KV2_MOUNT>/data/<path>`, field `value`. Paths are
   environment-scoped: an account in environment `E` may only reference `secretref://fundzim/<lower(E)>/…` (DB CHECK
   and resolver check). Production and sandbox credentials therefore live under different paths, which the secret
   manager's policies separate (the production AppRole cannot read `fundzim/sandbox/*` and vice versa).
4. Resolution is cached in memory for `SECRETS_KV2_CACHE_TTL` (default 60 s, max 15 min). On a failed refresh the
   resolver **fails closed**: no stale value is served after the TTL. The provider is then not routable and its
   health is UNAVAILABLE.
5. The resolver tracks each secret's KV version. A version change is audited as `psp.provider.credential_rotated`
   (PROVIDER_CREDENTIAL_ROTATED) with the reference and versions only, never the value. Rotation is performed in the
   secret manager; webhook keys keep the Stage 8 current/previous slots for overlap.
6. Values never appear in logs, errors, metrics, audit metadata, API responses or the database.

## Consequences

- KI-S8-04 is closed for the integration; operating a production OpenBao/Vault (HA storage, unseal/auto-unseal via
  KMS, backups, audit device, policies) is a Stage 18/20 hosting task and a production blocker until done.
- Compose and CI gain an OpenBao **dev-mode** service (in-memory, HTTP, root token from `.env`) seeded with the
  simulator's generated test keys. Dev mode is never acceptable outside local/CI.
- The env resolver stays for unit tests and for local runs without the secret service.
