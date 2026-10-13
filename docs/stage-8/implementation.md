# Stage 8 — Implementation

Stage 8 (Payment Abstraction Layer) on branch `stage-8/payments`. Decisions: [ADR-039](../adr/ADR-039-payment-abstraction-donation-intents-and-sandbox-execution.md).
**Sandbox-only engineering:** no real payment, refund, ledger posting or balance exists; the production gate is
BLOCKED ([prerequisite-assessment.md](prerequisite-assessment.md), [legal-and-provider-dependencies.md](legal-and-provider-dependencies.md)).

| Doc | Content |
|---|---|
| [payment-architecture.md](payment-architecture.md) | Modules, glossary, payment and webhook flows, jobs, events, gates |
| [provider-interface.md](provider-interface.md) | Adapter interface, classification, routing, secrets, metrics, **§6 sandbox contract** |
| [donation-intents.md](donation-intents.md) | Lifecycle, eligibility, currency, limits, retries, donor API |
| [payment-state-machine.md](payment-state-machine.md) | 42 edges, ranks, `apply`, source rules, parking, UNKNOWN |
| [webhook-security.md](webhook-security.md) | Verification, replay protection, rotation, rejection log, dispatch |
| [idempotency.md](idempotency.md) | Keys, DB backstops, provider reference, concurrency |
| [refund-architecture.md](refund-architecture.md) | Refund requests (never executed) |
| [reconciliation-foundation.md](reconciliation-foundation.md) | Reconciliation references |
| [provider-capability-register.md](provider-capability-register.md) | Target rails (all UNKNOWN) and the sandbox rows |
| [security-review.md](security-review.md) · [testing.md](testing.md) · [performance.md](performance.md) · [known-issues.md](known-issues.md) | Review, tests, measurements, issues |
| [interface-contracts.md](interface-contracts.md) | API, jobs, events, errors, staff JSON shapes |
| [../stage-handover/STAGE-8-TO-STAGE-9.md](../stage-handover/STAGE-8-TO-STAGE-9.md) | Handover |

## 1. What was built

| Area | Files |
|---|---|
| Migrations | `migrations/20261012090000_psp_registry_and_inbox.sql` (registry, capabilities, accounts, inbox, rejections, health, simulator tables, sandbox seed, kill switch flag), `20261012090100_payments.sql` (donation intents, payments, provider calls, transactions, references, sessions, refund requests, reconciliation references, events, machines, permissions, runtime grants procedure with `payment_status_rank` allowed) |
| psp | `internal/psp/{psp,registry,health,inbox,admin,secrets}.go`, simulator `internal/psp/sandbox/{sandbox,scenarios,webhook}.go` |
| payments | `internal/payments/{payments,machine,apply,intents,attempts,poll,webhooks,refunds,recon,admin,http}.go` |
| Other modules | `internal/campaigns/donation.go` (`ACCEPT_DONATION`), `internal/risk/risk.go` (`MoneyLimits`), `internal/platform/ratelimit/policies.go` (4 policies), `internal/platform/config/config.go` (`Payments`), `internal/archtest` (graph and ownership) |
| Wiring | `internal/app/payments.go`, `routes.go`, `consumers.go`, `worker.go`, `verification.go` |
| Web (staff only) | `apps/web/src/app/admin/payments/**`, `src/components/admin/payments/*`, `src/lib/payments/*`, admin navigation, mock API `e2e/mock-api/payments.mjs`, `e2e/staff-payments.spec.ts` |
| API contract | `api/openapi/fundzim-v1.yaml` (34 Stage 8 operations with `x-fundzim-status`) |
| Config / local stack | `.env.example`, `compose.yaml` (`PAYMENTS_SANDBOX_ENABLED`, `SANDBOX_WEBHOOK_URL`, `SECRETREF_FUNDZIM_SANDBOX_*`) |
| Tests | unit tests in `internal/payments`, `internal/psp`, `internal/psp/sandbox`, config, ratelimit; integration `tests/integration/payments_test.go`; perf harness `tests/perf/payments/` |
| Governance | `docs/governance/stage-acceptance-register.md` (proposed; nothing accepted) |

## 2. Execution gates (ADR-039 §6)

`psp.LiveExecutionEnabled = false` (constant) · kill switch `payments.live_execution.enabled` off (maker-checker) ·
real provider cannot be ENABLED without selection + ADR (DB) · sandbox only with `PAYMENTS_SANDBOX_ENABLED=true` and
never in production (config) · no PRODUCTION sandbox account (DB) · simulated records pinned by FK · public donation
button disabled, no checkout.

## 3. Deviations from the brief and the Stage 2 design

All recorded in ADR-039: donation intents as the parent of payments (brief "payment attempts" = Stage 2 payments);
Stage 2 state names kept (VALIDATED / PROCESSING / RECONCILIATION_REQUIRED mapped); fees, ledger postings,
`donations`, `refund_transactions`, disputes and chargebacks deferred; refund requests cannot be executed; rejected
webhooks recorded without payloads (Stage 2: nothing stored); circuit breaker opt-in for the simulator; OpenAPI design
paths PAY-02..05 superseded.

## 4. How to run locally

```bash
./scripts/dev-env-init.sh            # new machines: generates the sandbox test secrets too
docker compose up -d --build          # PAYMENTS_SANDBOX_ENABLED=true comes from .env
flock /tmp/claude-1000/fundzim-stack.lock bash -c 'set -a; . ./.env; set +a; go test -tags integration -count=1 -run TestPayments ./tests/integration/'
```

Amounts select simulator scenarios by their last two minor-unit digits ([provider-interface.md §6](provider-interface.md)).
