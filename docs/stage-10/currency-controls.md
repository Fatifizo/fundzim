# Stage 10 — Currency controls

- **USD only.** A journal or account may use a currency only if `app.currencies.minor_units_verified` is true.
  Only USD (2 minor units, ISO 4217) is verified. ZWG is seeded `minor_units_verified = false` (LR-043, PCR-018):
  the ledger refuses it in Go (`LEDGER_CURRENCY_NOT_POSTABLE`) and in the database (account trigger and journal
  check). The ledger does not guess ZWG's precision.
- **One currency per journal** (L14): header, every line and every account must match (composite foreign keys on
  `(id, currency)` plus the deferred trigger).
- **No conversion, no netting across currencies.** Balances, the trial balance and reports are per currency; there
  is no FX rate table and no FX rule. The trial balance endpoint takes a `currency` parameter.
- **Integers only.** `amount_minor bigint`, `> 0`, sums checked against overflow in Go (`AMOUNT_TOO_LARGE` beyond
  2^62) and computed as `numeric`/`bigint` in SQL. JSON amounts are digit strings; the web UI formats them with
  integer arithmetic (`apps/web/src/lib/ledger/money.ts`), never `Number` division.
- Enabling ZWG needs LR-043/PCR-018 answers, a migration setting `minor_units_verified`, and an ADR (MONEY.md).
