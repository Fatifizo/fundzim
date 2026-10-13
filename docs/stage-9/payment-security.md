# Stage 9 — Payment security: execution isolation and the production kill switch

## 1. Execution environments

One per process (`PAYMENTS_EXECUTION_ENVIRONMENT`); never per request; never from the browser.

| Environment | Who may execute | Account environment | Payment marking | Allowed `APP_ENV` |
|---|---|---|---|---|
| `DISABLED` | nobody | — | — | any (production default) |
| `LOCAL_SIMULATOR` | the simulator (needs `PAYMENTS_SANDBOX_ENABLED`) | SANDBOX | `is_simulated = true`, `test_payment = true` | not production |
| `PROVIDER_SANDBOX` | allow-listed real providers with `sandbox_status = ENABLED` | SANDBOX | `is_simulated = false`, `test_payment = true` | not production; needs `SECRETS_PROVIDER=kv2` |
| `PRODUCTION` | selected + ENABLED providers while live execution is on | PRODUCTION | `test_payment = false` | production only; **startup refused** while `psp.LiveExecutionEnabled = false` |

Database enforcement (`migrations/20261013090000_payment_execution_isolation.sql`): `execution_environment` on
donation intents and payments (immutable; simulated ⇔ LOCAL_SIMULATOR); a payment's environment equals its donation
intent's and matches its account's environment (trigger); **`ck_*_no_production_stage9`: no PRODUCTION donation
intent or payment can exist**; evidence rows carry the payment's environment (trigger).

**A sandbox transaction can never be represented as a real donation**: API views carry `execution_environment` and
`test_payment`; outbox events `payments.payment_succeeded` / `payments.payment_status_changed` carry
`test_payment: true`; the ledger seam (`payments.LedgerPoster`) is called only for PRODUCTION payments, so no test
payment can be posted by Stage 10.

## 2. Production execution kill switch (layers)

| # | Layer | Where | Effect |
|---|---|---|---|
| 1 | Global flag in code | `psp.LiveExecutionEnabled = false` (constant) | No real provider is usable in PRODUCTION; changing it needs an ADR, legal sign-off and acceptance (CLAUDE.md) |
| 2 | Maker-checker kill switch | feature flag `payments.live_execution.enabled` (off) | Must also be on; cannot override layer 1 |
| 3 | Environment validation | config: PRODUCTION only in production, simulator and provider sandbox refused in production | Misconfiguration fails at load |
| 4 | Provider allowlist | `PAYMENTS_PROVIDER_ALLOWLIST`; the simulator is refused in a production allowlist | A provider not listed is never usable, its webhooks are rejected |
| 5 | Credential environment validation | DB CHECK + `CredentialScopeOK` + secret-manager policies | A sandbox account cannot use production credentials or vice versa |
| 6 | Production startup refusal | `psp.StartupGate` (API and worker) | `PRODUCTION` while layer 1 is off, or provider execution without the secret manager → process exits with `payment execution startup refused` |
| 7 | Audit | `psp.execution_gate.evaluated` (security stream, every start; `denied` when refused), `psp.provider.configured` on change | Evidence of every gate evaluation |
| 8 | No frontend override | No request parameter, header or body field selects the environment, provider or simulation; the public button stays disabled | — |
| 9 | No admin bypass | Staff controls can enable SANDBOX execution, disable, or declare outages only; production `status = ENABLED` needs `selection_status = SELECTED` and an ADR (DB CHECK) and a migration; no route sets a payment status (tests assert 404/405) | — |
| 10 | Database | no PRODUCTION rows (CHECK); provider ENABLED requires selection (CHECK) | Last line if code were wrong |

## 3. Other controls (unchanged from Stage 8, still in force)

Required idempotency keys; payment row locks; `apply()` as the only state path; source checks in Go and SQL; UNKNOWN
never becomes FAILED by assumption; append-only history and evidence; inbox dedupe; rejection log without payloads;
redirect allow-list; masked/HMAC'd payer data; step-up for `payment.operate` and `refund.approve`; no PIN, PAN, CVV
or sensitive authentication data accepted anywhere; no PCI DSS compliance claimed.
