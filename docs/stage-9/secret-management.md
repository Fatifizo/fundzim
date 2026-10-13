# Stage 9 — Secret management

Decision: [ADR-041](../adr/ADR-041-secret-management.md). Code: `internal/platform/secretstore` (KV v2 client),
`internal/app/payments.go` (`newSecretResolver`), `internal/psp/execution.go` (scope checks, rotation audit).

## 1. Integration

| Item | Value |
|---|---|
| API | Vault KV version 2 over HTTPS (OpenBao 2.4.1 in compose/CI; HashiCorp Vault or a compatible service interchangeable) |
| Reference mapping | `secretref://<path>` → `GET /v1/<SECRETS_KV2_MOUNT>/data/<path>`, field `value` (≥ 32 bytes) |
| Authentication | AppRole (`POST /v1/auth/approle/login`; token re-obtained at 80 % of its lease and once after a 403) — **required in production**; static token for development/CI only |
| Transport | `https://` required in production; optional private CA (`SECRETS_KV2_CA_FILE`); TLS ≥ 1.2; redirects never followed (the token cannot leak to another host) |
| Cache | In memory per process, `SECRETS_KV2_CACHE_TTL` (default 60 s, 1 s – 15 min). **Fail closed**: after the TTL a failed refresh removes the value; nothing stale is served |
| Version tracking | Each resolve records the KV version; a higher version than seen before triggers `psp.provider.credential_rotated` (PROVIDER_CREDENTIAL_ROTATED) with reference, versions, affected accounts — **never the value** |
| Health | `GET /v1/sys/health` (200 = initialised, unsealed, active) → `secret_manager` on `/admin/payments/health` |
| Errors | `secretstore.ErrUnavailable` with path and HTTP status only |
| Configuration | `SECRETS_PROVIDER=env|kv2`, `SECRETS_KV2_ADDR`, `SECRETS_KV2_MOUNT`, `SECRETS_KV2_TOKEN`, `SECRETS_KV2_ROLE_ID`, `SECRETS_KV2_SECRET_ID`, `SECRETS_KV2_CA_FILE`, `SECRETS_KV2_CACHE_TTL`, `SECRETS_KV2_TIMEOUT` |

Config refusals (`internal/platform/config`): `SECRETS_PROVIDER=env` in production; plain HTTP in production; a static
token in production; half an AppRole; token and AppRole together; `PROVIDER_SANDBOX` or `PRODUCTION` execution
without `kv2`.

## 2. Credential environment separation

- DB CHECK `ck_provider_accounts_secret_env`: an account in environment `E` may only reference
  `secretref://fundzim/<lower(E)>/…`.
- `psp.CredentialScopeOK` repeats the rule at runtime; routing and `EnableSandbox` require it.
- Secret-manager policies must mirror it: the production AppRole reads only `secret/data/fundzim/production/*`, the
  staging AppRole only `secret/data/fundzim/sandbox/*`. The integration test proves an AppRole with the sandbox
  policy is refused a production path by OpenBao itself.

## 3. What happens when secrets do not resolve

Routing skips the provider (no payment is created); donation intents fail with `PAYMENT_METHOD_UNAVAILABLE`;
health becomes `UNAVAILABLE / CREDENTIALS_UNAVAILABLE`; existing payments stay in their state and are resolved later
by status queries. Tested in `TestPaymentsSecretManagerOpenBao`.

## 4. Local and CI

`compose.yaml` runs `fundzim-secrets` (OpenBao **dev mode**: in-memory, HTTP, root token `SECRETS_KV2_TOKEN` from
`.env`) and `fundzim-secrets-init`, which copies the simulator's three generated test keys from `.env` into
`secret/fundzim/sandbox/{api,webhook-current,webhook-previous}` via stdin. API and worker resolve from OpenBao
(`SECRETS_PROVIDER=kv2`); the keys are no longer in their environment. Dev mode is never acceptable outside local/CI.

## 5. Rotation procedure (provider credentials)

1. Write the new value as a new KV version (`bao kv put -mount=secret fundzim/<env>/<provider>/<name> value=-`).
2. For a webhook key: put the new key in the `webhook-current` path and keep the old one in `webhook-previous` for
   the provider's overlap window, then remove it.
3. Within one cache TTL every process resolves the new version and audits PROVIDER_CREDENTIAL_ROTATED.
4. Revoke the old key at the provider.

## 6. Production readiness of secret management

The **integration** is implemented and tested against a real OpenBao. **Operating** a production secret manager is
not done: HA storage, auto-unseal with a KMS, backups, an audit device, policies per AppRole and secret-id delivery
to the workloads are Stage 18/20 hosting work. Until then production remains BLOCKED (it is blocked anyway).
