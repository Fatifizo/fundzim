# ADR-005: Integer minor-unit money representation

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (money package Stage 3)

## Context

IEEE-754 floating-point numbers cannot represent most decimal fractions exactly. For example,
`0.1 + 0.2 != 0.3`. In JavaScript every `number` is a float and integers are only exact up to 2^53. Rounding
errors in fees, refunds or payout totals become ledger imbalances and reconciliation mismatches. FundZim must
also handle more than one currency (USD and ZiG), so an amount without its currency is meaningless.

## Decision

A monetary amount is a **signed 64-bit integer number of minor units, always paired with an ISO 4217 currency
code**.

- **Go:** `money.Money{amountMinor int64; currency Currency}` in `internal/platform/money`.
  - Add and Sub work only on the same currency and return an error otherwise.
  - Arithmetic is overflow-checked.
  - Allocations (fee splits, partial refunds) use the **largest-remainder method**, so parts always sum
    exactly to the whole.
  - Percentage fees are computed in integer basis points and rounded half-up to the minor unit **at one
    documented point**, with a documented rule for assigning the remainder.
  - `float32` and `float64` are banned in financial code.
- **SQL:** `amount_minor BIGINT NOT NULL` + `currency CHAR(3) NOT NULL REFERENCES currencies(code)`. There is no
  `NUMERIC`-with-float drift, and no `REAL` or `DOUBLE PRECISION` money columns.
- **JSON:** `{"amount_minor": "10000", "currency": "USD"}`. `amount_minor` is a **string of digits**, to avoid
  JavaScript precision loss.
- **TypeScript:** amounts are handled as string or `BigInt` and formatted with `Intl.NumberFormat` from an exact
  decimal string. Never divide by 100 as a float.
- **User input:** a decimal string is parsed to minor units with strict validation. Input with more fractional
  digits than the currency allows is **rejected, not rounded**.
- The `currencies` table defines `minor_units`. Initial rows: USD (840, 2) and ZWG (924, 2).

Example: USD 100.00 is stored as `amount_minor = 10000, currency = 'USD'`.

## Consequences

### Positive
- Exact arithmetic everywhere, and the ledger can be checked with exact equality.
- Currency mismatches fail loudly at compile or run time instead of silently producing wrong totals.

### Negative / costs
- Every boundary (API, UI, provider adapters, CSV exports) needs explicit conversion code and tests.
- Provider APIs that use decimal strings or floats must be converted carefully in adapters. A float coming from
  a provider is parsed as a decimal string, never via float arithmetic.
- int64 caps amounts at about 9.22 × 10^18 minor units. This is far above any realistic campaign, but overflow
  is still checked.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| float/double | Inexact. Prohibited by the master prompt. |
| Arbitrary-precision decimal library (e.g. `shopspring/decimal`) | Exact, but adds a dependency and allows sub-minor-unit values to leak in. Integers are simpler and map 1:1 to SQL `BIGINT`. A decimal library may be used **only** for intermediate FX or rate calculations by a future ADR. |
| `NUMERIC(19,4)` in SQL | Exact, but invites fractional minor units and diverges from the Go and JSON representation. |
| JSON number for amounts | Precision loss in JavaScript beyond 2^53, and some clients parse as float. |

## Security implications
Strict input parsing prevents negative, overlong or malformed amounts (e.g. `1e9`, `-5`, `0.001`). This closes
amount-manipulation and rounding-exploitation bugs such as "salami" rounding abuse.

## Financial implications
This is foundational. Every financial record, ledger entry, fee and payout uses this representation, and
deviation requires a superseding ADR.

## Related
ADR-006, ADR-010, [MONEY.md](../MONEY.md), [LEDGER.md](../LEDGER.md).
