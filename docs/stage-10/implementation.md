# Stage 10 — Implementation

Immutable double-entry ledger, posting engine, accounting integrity, reversals, reconciliation foundation and
financial operations UI. **Non-production engineering work**: the ledger database is designated TEST, production
posting is disabled, no real funds are processed and every journal is a synthetic TEST_ONLY fixture.

Start here: [prerequisite-assessment.md](prerequisite-assessment.md) · [ledger-architecture.md](ledger-architecture.md)
· [chart-of-accounts.md](chart-of-accounts.md) · [journal-model.md](journal-model.md) ·
[posting-engine.md](posting-engine.md) · [posting-rules.md](posting-rules.md) · [currency-controls.md](currency-controls.md)
· [idempotency.md](idempotency.md) · [reversals.md](reversals.md) · [balance-calculation.md](balance-calculation.md) ·
[reconciliation.md](reconciliation.md) · [security-review.md](security-review.md) ·
[financial-integrity-tests.md](financial-integrity-tests.md) · [performance.md](performance.md) ·
[known-issues.md](known-issues.md) · handover [STAGE-10-TO-STAGE-11.md](../stage-handover/STAGE-10-TO-STAGE-11.md) ·
decision [ADR-042](../adr/ADR-042-ledger-implementation.md).

## 1. What was built

| Area | Artefacts |
|---|---|
| Schema | `migrations/20261013120000_ledger.sql` (Stage 2 draft ported + environment designation, versioned rules, account status, evidence/source columns, source and reconciliation references, permissions, runtime grants for `fundzim_ledger`, gateway updates); Down restores the previous gateways |
| Role | `fundzim_ledger` (created by `deploy/docker/postgres/init/20-ledger-role.sh`; existing volumes: run it with `docker compose exec`); config `DATABASE_LEDGER_URL` (must differ from every other database URL) |
| Engine | `internal/ledger` ([ledger-architecture.md](ledger-architecture.md) §2) |
| Wiring | `internal/app/ledger.go` (pool, payment-facts adapter, routes, outbox consumer `ledger.payments`, jobs); `internal/payments/facts.go` (read-only facts) |
| API | 26 staff endpoints under `/api/v1/admin/ledger` (OpenAPI tag "Ledger (Stage 10)") |
| UI | `/admin/finance/ledger` (overview, pending adjustments, integrity), `/accounts`, `/accounts/[id]`, `/journals`, `/journals/[id]`, `/trial-balance`, `/reconciliation`, `/exceptions` — every page shows the ledger environment banner and TEST_ONLY labels |
| Jobs | `ledger.maintenance` (5 s: periods, fold, adjustment expiry), `ledger.integrity` (hourly), `ledger.reconcile` (15 min) |
| Tests | [financial-integrity-tests.md](financial-integrity-tests.md); perf harness `tests/perf/ledger` |

## 2. Permissions

| Permission | Roles | Step-up | Use |
|---|---|---|---|
| `ledger.view` | FINANCE, COMPLIANCE | no | read everything ledger |
| `ledger.account.manage` | FINANCE | yes | freeze / unfreeze / close |
| `ledger.period.close` | FINANCE | yes | request / approve / abort close (maker ≠ checker) |
| `ledger.reconciliation.operate` | FINANCE | yes | run reconciliation and integrity; work exceptions |
| `ledger.adjustment.create` / `.approve` (Stage 4) | FINANCE | yes | maker / checker of reversals, corrections, manual journals |
| `ledger.synthetic.post` | ADMIN | no | TEST_ONLY fixtures; also needs `LEDGER_SYNTHETIC_FIXTURES_ENABLED=true` (refused in production) and a TEST ledger |

## 3. Audit events and outbox events

Audit (same transaction as the change): `ledger.account.created`, `ledger.journal.posted`, `ledger.journal.rejected`,
`ledger.adjustment.requested|approved|rejected|withdrawn|expired|posted`,
`ledger.account.frozen|unfrozen|closed`, `ledger.period.opened|close_requested|close_aborted|closed`, `ledger.integrity.checked`,
`ledger.reconciliation.run|investigating|resolved`, `ledger.source.evaluated`.

Outbox: `ledger.journal_posted`, `ledger.journal_reversed`, `ledger.account_frozen`, `ledger.account_status_changed`,
`ledger.period_closed`, `ledger.reconciliation_exception`. Payloads carry ids, rule, currency, environment and
amounts as strings — no personal data. No consumer exists yet.

## 4. Simulator and synthetic isolation

Layers (none of them a single boolean): (1) the database ledger environment row (TEST), checked by trigger on every
record; (2) CHECK `ck_*_no_production_stage10`; (3) CHECK synthetic ⇒ TEST + SIMULATED; (4)
`ledger.ProductionPostingEnabled = false`; (5) the payments consumer never posts a test payment and records the
decision; (6) payment-side Stage 9 guards (no PRODUCTION payment can exist); (7) `fundzim_app` has no ledger
privilege. A production ledger would be a **separate database** designated PRODUCTION by its own migration (ADR-042).

Synthetic fixtures (staff API `POST /api/v1/admin/ledger/synthetic-fixtures`, body `{"scenario": …}`; there is no UI control): `donations` (USD 10.00 +
25.00 captured, USD 0.50 provider fee), `partial_refund` (USD 100.00 captured, USD 30.00 refunded), `settlement`
(USD 100.00 captured, settled, released), `refund_5` (USD 25.00 captured, USD 5.00 refunded). Each run uses a fresh
synthetic campaign id and the synthetic provider id, metadata `{"fixture":"TEST_ONLY"}` and a TEST_ONLY
description. They are never donor contributions.

## 5. Data retention

Ledger journals, entries, source references, reconciliation references, invariant runs and their audit events are
financial records. Their retention period is **LEGAL_REVIEW_REQUIRED** (LR-012; KI-S10-07). No retention period is
assumed, nothing is purged and no purge function exists; the append-only grants and triggers would refuse one.
Synthetic TEST data lives only in TEST databases and is removed only by dropping that database.

## 6. Backup and recovery

See [financial-integrity-tests.md](financial-integrity-tests.md) §5 for the logical dump/restore test of the
development database. Production backup, point-in-time recovery and disaster recovery are not set up (KI-S10-06) and
nothing here is a DR certification.

## 7. Deviations from the brief

| Brief | Implementation | Why |
|---|---|---|
| Posting events named DONATION_CONFIRMED, SETTLEMENT_CONFIRMED, … | Stage 2 rule names (`DONATION_CAPTURED`, `SETTLEMENT_MATCHED`, …), mapped in [posting-rules.md](posting-rules.md) §1 | The design rules are already normative (ADR-023) |
| Journal lifecycle with draft states | Journals are created POSTED in one transaction; drafts exist only as PENDING_APPROVAL adjustments | A ledger row that can change is history that can change |
| In-transaction posting from payments (ADR-023 §7) | Asynchronous outbox consumer | ADR-042 §3: payments must not write ledger tables |
| Account numbering | Deterministic account codes, no numeric numbers | Codes are stable and self-describing |
| ROADMAP: every confirmed payment produces exactly one posting in test environments (Gate A) | Test payments are recorded NOT_ELIGIBLE and never posted, even to the TEST ledger; synthetic fixtures exercise the rules | Stage 9 handover and the Stage 10 brief (simulated payments must never reach a production ledger; no single-flag separation). Owner decision requested |
| Separate test database preferred | The development database is designated TEST; production must be a separate database | Production hosting is Stage 18/20 |

## 8. How to run locally

```bash
docker compose exec -T -e POSTGRES_LEDGER_PASSWORD="$POSTGRES_LEDGER_PASSWORD" fundzim-postgres \
  sh /docker-entrypoint-initdb.d/20-ledger-role.sh          # once, for a volume created before Stage 10
set -a; . ./.env; set +a; go run ./apps/api/cmd/fundzimctl migrate up
docker compose up -d --build
# as an ADMIN: POST /api/v1/admin/ledger/synthetic-fixtures {"scenario":"donations"}; FINANCE or COMPLIANCE staff
# then see the TEST_ONLY journals and balances under /admin/finance/**
```
