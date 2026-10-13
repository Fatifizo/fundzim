# Stage 10 — Journal model

## 1. Journal header (`ledger.ledger_transactions`)

| Column | Meaning |
|---|---|
| `id` | UUIDv7 |
| `posting_rule`, `posting_rule_version` | the rule and its current version ([posting-rules.md](posting-rules.md)) |
| `environment` | must equal the database designation (TEST in Stage 10) |
| `currency` | the single currency of every line (L14); must have verified minor units |
| `idempotency_key` | UNIQUE; deterministic per business event ([idempotency.md](idempotency.md)) |
| `source_system` | payments, reconciliation, staff, synthetic_fixture |
| `source_type`, `source_id`, `source_event_id` | the business object and event; `source_type` must be allowed by the rule |
| `evidence_class` | SIMULATED, PROVIDER_REPORTED, PROVIDER_VERIFIED, SETTLEMENT_VERIFIED, MANUALLY_REVIEWED, REJECTED |
| `occurred_at` / `posted_at` | business time / database commit time |
| `accounting_period_id` | set by trigger: the period of `occurred_at`, or the current OPEN period for an allowed prior-period posting |
| `reverses_transaction_id` | for REVERSAL journals; at most one reversal per journal (unique) |
| `adjustment_id` | the APPROVED maker-checker request for approval-requiring rules |
| `description`, `metadata` | free text and string map (no personal data; fixtures carry `{"fixture":"TEST_ONLY"}`) |
| `created_by_type`, `created_by_id` | SYSTEM component or STAFF user |
| `created_txid` | the creating database transaction (L11 seal) |

CHECKs bind: synthetic_fixture ⇒ TEST and SIMULATED; PRODUCTION ⇒ verified evidence (and PRODUCTION is refused in
Stage 10); REJECTED evidence is never postable.

## 2. Lines (`ledger.ledger_entries`)

`(transaction_id, line_no)` unique; `account_id`; `direction` DEBIT | CREDIT; `amount_minor bigint > 0` (no negative
or zero amounts — the sign is the direction); `currency` = journal currency = account currency.

## 3. Lifecycle

A journal has no draft state in the database: it is built in Go, validated, and inserted with its lines in one
transaction. At COMMIT the deferred trigger checks balance (L1), line count (L2), rule conformance (L12), reversal
mirroring (L7) and currency (L14). Committed journals are **POSTED** and immutable. A journal that is reversed
remains POSTED; its status in the API is derived (`reversed_by_transaction_id` set). Rejected attempts leave no journal; they are
audited `ledger.journal.rejected` and, for source events, recorded in `ledger_source_references`.
