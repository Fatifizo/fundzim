# Stage 10 — Ledger architecture

Decisions: [ADR-006](../adr/ADR-006-double-entry-ledger.md), [ADR-014](../adr/ADR-014-accounting-separated-from-custody.md),
[ADR-023](../adr/ADR-023-ledger-posting-architecture.md) (design) and [ADR-042](../adr/ADR-042-ledger-implementation.md)
(implementation). Normative background: [../LEDGER.md](../LEDGER.md),
[../ledger/settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md),
[../architecture/ledger-invariants.md](../architecture/ledger-invariants.md).

## 1. Principles

1. **Double entry.** Every journal has at least two lines, one currency, and debits equal credits. PostgreSQL checks
   this at COMMIT (deferred constraint trigger); Go checks it first.
2. **Immutability.** Journals and entries are INSERT-only: no role has UPDATE or DELETE on them, triggers forbid
   UPDATE/DELETE/TRUNCATE, and entries can only be added in the transaction that created their journal (L11, by
   `created_txid`). Corrections are reversals and replacement journals.
3. **Balances are derived.** `ledger_balances` is a projection maintained by SECURITY DEFINER functions and the
   fold job; it can always be recomputed from entries (`v_balance_drift` must be empty). No API or role can set a
   balance.
4. **Money is integers.** `amount_minor bigint > 0` plus `currency`; direction is a separate column. JSON carries
   amounts as digit strings. No floating point anywhere (Go, SQL, TypeScript).
5. **Accounting is not custody.** Account names describe claims and obligations (for example
   `asset:psp_clearing` = a claim on a provider, `liability:campaign_unsettled` = what is owed to a campaign once
   settled). The ledger makes no statement that FundZim owns, holds or controls settlement cash (LR-001, LR-002,
   LR-003 open).
6. **Isolation of test activity.** The database is designated TEST or PRODUCTION; every record must match
   (ADR-042 §1). Stage 10 databases are TEST; PRODUCTION is refused by CHECK.
7. **Least privilege.** Only `fundzim_ledger` writes; `fundzim_app` has no ledger privilege (ADR-042 §2).

## 2. Module

`internal/ledger` (imports only `internal/platform/*` and `internal/audit`; enforced by `internal/archtest`).

| File | Responsibility |
|---|---|
| `ledger.go` | Chart of accounts, account codes, posting/line types, errors, environment and rule caches, Go validation |
| `post.go` | `Post` (inside a caller transaction) and `PostOnce` (own transaction with retry); idempotency; locking; audit; outbox |
| `accounts.go` | Account status changes; accounting periods (open, request close, approve close, abort) |
| `balances.go` | Fold of DEFERRED projections; account balance; trial balance |
| `adjustments.go` | Maker-checker reversals, corrections and manual journals |
| `integrity.go` | Integrity checker (L1, L2, L4, L6, L7, L9, L10, L13, L15) |
| `reconcile.go` | Payments consumer (source references), reconciliation run, exceptions |
| `synthetic.go` | TEST_ONLY synthetic fixtures |
| `reports.go` | Journal register, journal detail, accounts, overview, source references |
| `http.go` | Staff API under `/api/v1/admin/ledger` |
| `jobs.go` | River workers: maintenance (5 s), integrity (hourly), reconcile (15 min) |

The composition root (`internal/app/ledger.go`) opens a separate pool on `DATABASE_LEDGER_URL` (role
`fundzim_ledger`), warms the environment and rule caches, wires the payment-facts adapter (payments → ledger, read
only), registers routes, the outbox consumer `ledger.payments` and the jobs.

## 3. Data flow

```
payments (fundzim_app) ── outbox: payments.payment_succeeded ──▶ ledger.payments consumer (fundzim_ledger)
                                                                   └─ ledger_source_references (NOT_ELIGIBLE / BLOCKED)
staff UI ──▶ /api/v1/admin/ledger/** ──▶ ledger.Service ──▶ ledger tables (fundzim_ledger)
                                               ├─ audit (audit.record_gateway, same transaction)
                                               └─ outbox (ledger.journal_posted, …; same transaction)
jobs: maintenance (periods, fold, adjustment expiry) · integrity (hourly) · reconcile (15 min)
```

Payments never call the ledger, webhooks never touch ledger tables, and the ledger never calls a provider.

## 4. Tables (schema `ledger`)

| Table | Kind | Notes |
|---|---|---|
| `ledger_environment` | singleton, immutable | TEST in Stage 10 |
| `ledger_posting_rules`, `ledger_posting_rule_lines` | reference (migration-only) | versioned (code, version); account patterns per rule |
| `ledger_accounts` | status-bearing | ACTIVE / FROZEN / CLOSED; identity columns immutable |
| `ledger_periods` | status-bearing | OPEN / CLOSING / CLOSED; one per month and currency |
| `ledger_transactions` | append-only | journal header |
| `ledger_entries` | append-only | lines |
| `ledger_balances` | projection | written only by SECURITY DEFINER functions |
| `ledger_adjustments` | status-bearing | maker-checker |
| `ledger_invariant_runs` | append-only | integrity results |
| `ledger_source_references` | append-only | every source event considered for posting and the decision |
| `ledger_reconciliation_references` | status-bearing | exceptions; detection fields immutable |

Views: `v_trial_balance`, `v_balances_current`, `v_balance_drift`, `v_pool_integrity`.
