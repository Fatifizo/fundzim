# Stage 9 — Implementation

Stage 9 (Zimbabwe payment provider integrations) on branch `stage-9/provider-integrations`. **No payment provider
is selected** ([ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md), DECISION_DEFERRED): there are no
merchant agreements, no sandbox credentials and no written provider confirmations, so no real adapter was written
and no provider is integrated or certified. Stage 9 delivers the **engineering preparation** and the **governance
artefacts**; production execution remains BLOCKED.

| Doc | Content |
|---|---|
| [prerequisite-assessment.md](prerequisite-assessment.md) | Prerequisites classified; acceptance NOT_VERIFIED; production BLOCKED |
| [legal-review.md](legal-review.md) | 43 LR items with their exact register definitions, grouped, all OPEN |
| [provider-confirmation-register.md](provider-confirmation-register.md) | PCR-001..007, 009, 010, 017, 018 per provider (CONFIRMED / PARTIALLY_CONFIRMED / UNCONFIRMED / NOT_APPLICABLE) |
| [provider-evaluation.md](provider-evaluation.md) · [provider-selection.md](provider-selection.md) | Gates, 2026-10-13 documentation re-check, DECISION_DEFERRED |
| [provider-capabilities.md](provider-capabilities.md) | Method mapping; mobile money, card, bank, ZIPIT/ZimSwitch assessments; currency rules |
| [provider-contracts.md](provider-contracts.md) | Contract bases (OFFICIAL / SANDBOX_OBSERVED / DOCUMENTED_BUT_UNVERIFIED / SIMULATED) |
| [sandbox-certification.md](sandbox-certification.md) | Conformance suite, results, per-provider checklist (all BLOCKED) |
| [webhook-security.md](webhook-security.md) | Audit, evidence, per-provider verification designs |
| [secret-management.md](secret-management.md) | ADR-041 integration, scoping, rotation, fail-closed behaviour |
| [payment-security.md](payment-security.md) | Execution environments, 10-layer kill switch |
| [reconciliation-evidence.md](reconciliation-evidence.md) | Evidence log, contradiction escalation |
| [testing.md](testing.md) · [known-issues.md](known-issues.md) | Tests, defects found, performance; issues |
| [../stage-handover/STAGE-9-TO-STAGE-10.md](../stage-handover/STAGE-9-TO-STAGE-10.md) | Handover |

## 1. What was built

| Area | Files |
|---|---|
| Migration | `migrations/20261013090000_payment_execution_isolation.sql`: `payment_providers.sandbox_status`; `ck_provider_accounts_secret_env`; `execution_environment` on donation intents and payments (immutable, simulated ⇔ LOCAL_SIMULATOR, environment guard trigger, `ck_*_no_production_stage9`); `payment_attempts.error_category` (set once); reconciliation reason `CONTRADICTORY_EVIDENCE`; append-only `app.payment_provider_evidence` (+ unique per provider event, environment guard). Down section restores Stage 8 |
| Secret manager | `internal/platform/secretstore` (KV v2 client); config `SECRETS_*` (`internal/platform/config`); wiring `internal/app/payments.go`; compose `fundzim-secrets` + `fundzim-secrets-init` (OpenBao 2.4.1 dev mode) |
| psp | `execution.go` (environments, allowlist, `Usable`, credential scope, startup gate, provider controls, rotation audit, contract bases), `errors.go` (10 categories + retry policy), `health.go` (derived health, PROVIDER_UNAVAILABLE on circuit open), `inbox.go` (WEBHOOK_VERIFIED / WEBHOOK_REJECTED audit), `admin.go` (provider detail, health view), `registry.go` (outage and credential checks in routing), `psptest/` (conformance suite), simulator `Contract()` and fail-closed `Limits` |
| payments | `evidence.go` (evidence log, contradiction escalation, error counts), `available.go` (method availability), `attempts.go` (error category, PROVIDER_RESPONSE_RECEIVED, PAYMENT_INITIATED_SANDBOX), `apply.go` (environment on events, PAYMENT_STATUS_RECONCILED, ledger seam only for PRODUCTION), `intents.go` (environment, `test_payment`), `refunds.go` (REFUND_REQUESTED_SANDBOX), `http.go` (8 routes) |
| API | `GET /api/v1/payment-methods/available`; `GET /admin/payments/providers/{id}`; `POST …/providers/{id}/enable-sandbox | disable | outage | outage/resolve`; `GET /admin/payments/health`; `GET /admin/payments/attempts/{id}/evidence`; OpenAPI updated (tags "… (Stage 9)", extended Stage 8 schemas) |
| Web (staff) | `/admin/payments/providers/[id]`, `/admin/payments/health`, `/admin/payments/transactions` (attempt list), provider list with health and contract basis, evidence table and error categories on the payment page, provider controls in `PaymentAction`, nav entry; mock API and E2E |
| CI | OpenBao service and seed in the integration and real-stack jobs; workflow-secrets guard in `scripts/check-secrets.sh` |
| Tests | see [testing.md](testing.md) |

## 2. Audit events (brief name → action)

| Brief event | Audit action | Stream |
|---|---|---|
| PROVIDER_CONFIGURED | `psp.provider.configured` (on change, at start) | business |
| PROVIDER_ENABLED_SANDBOX | `psp.provider.sandbox_enabled` | business |
| PROVIDER_DISABLED | `psp.provider.disabled` | business |
| PROVIDER_CREDENTIAL_ROTATED | `psp.provider.credential_rotated` | security |
| PAYMENT_INITIATED_SANDBOX | `payments.attempt.created` with `audit_event` | business |
| PROVIDER_RESPONSE_RECEIVED | `payments.provider.response_received` | business |
| WEBHOOK_VERIFIED | `psp.webhook.verified` (first dispatch claim) | business |
| WEBHOOK_REJECTED | `psp.webhook.rejected` (known provider, auth failures) | security |
| PAYMENT_STATUS_RECONCILED | `payments.payment.status_reconciled` (UNKNOWN → provider status) | business |
| PROVIDER_UNAVAILABLE | `psp.provider.unavailable` (circuit opened or staff-declared) | business |
| REFUND_REQUESTED_SANDBOX | `payments.refund.requested` with `audit_event` | business |
| (gate) | `psp.execution_gate.evaluated` | security |

Every record carries `audit_event` with the brief's name. No metadata contains a secret value.

## 3. Deviations from the brief

| Brief item | What was done | Why |
|---|---|---|
| Real adapters, payment initiation for confirmed sandbox providers, refund sandbox | Not built | No provider confirmed; no sandbox access; inventing contracts is forbidden |
| Provider sandbox certification | BLOCKED for every provider; simulator passes the suite | No access |
| Dev-only "SANDBOX TEST PAYMENT" UI (optional) | Not built | Simulated payments are created via the API and the staff sandbox panel; the public button stays disabled |
| `/admin/payments/transactions` | Same page as payment attempts | One row per payment with the provider's transaction state is the attempt list |
| WEBHOOK_VERIFIED in the ingest transaction | Moved to the first dispatch claim | 5× ingestion slowdown (testing.md §4) |

## 4. How to run locally

```bash
./scripts/dev-env-init.sh   # new machines (generates SECRETS_KV2_TOKEN too); existing .env: add the Stage 9 block of .env.example
docker compose up -d --build                     # starts fundzim-secrets and seeds it
flock /tmp/claude-1000/fundzim-stack.lock bash -c 'set -a; . ./.env; set +a; go test -tags integration -count=1 -run TestPayments ./tests/integration/'
```
