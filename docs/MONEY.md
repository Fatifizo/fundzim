# FundZim — Money Value Model Specification

> Status: Stage 0 specification. The Go implementation (`internal/platform/money`) arrives in Stage 3. The
> TypeScript helpers arrive with the first money-displaying UI (Stage 6/7). This spec is **normative**: code
> that disagrees with it is a bug. Changes require an ADR amending
> [ADR-005](adr/ADR-005-exact-money-representation.md) / [ADR-010](adr/ADR-010-multi-currency.md).

## 1. Core rule

**A monetary value is an exact integer count of the currency's minor unit, together with the currency.**

```
USD 100.00  →  { "amount_minor": "10000", "currency": "USD" }
ZiG 12.50   →  { "amount_minor": "1250",  "currency": "ZWG" }
```

The following are always errors, because they are not money:

- a bare number;
- a float;
- an amount without a currency;
- an amount whose currency is "whatever the campaign uses".

## 2. Currency registry

Currencies are **data** (table `currencies`, owned by `platform`), not an enum scattered through code.

| Column | Type | Notes |
|---|---|---|
| `code` | `CHAR(3)` PK | ISO 4217 alphabetic, upper case |
| `numeric_code` | `SMALLINT` | ISO 4217 numeric |
| `minor_units` | `SMALLINT` | `CHECK (minor_units BETWEEN 0 AND 4)` |
| `name` | `TEXT` | |
| `display_symbol` | `TEXT` | e.g. `US$`, `ZiG` |
| `enabled` | `BOOLEAN` | Whether new transactions may use it |

Initial rows:

| code | numeric | minor_units | name | display label |
|---|---|---|---|---|
| `USD` | 840 | 2 | US Dollar | US$ |
| `ZWG` | 924 | 2 | Zimbabwe Gold | ZiG |

- In code and data use **`ZWG`**. In user-facing copy use **"ZiG"**.
- Whether PSPs actually transact ZWG at 2 minor units (or round to whole units for some rails) is an **open
  item** to verify in Stage 9. If a rail cannot handle cents, the adapter rejects non-conforming amounts. It
  never rounds them silently.
- The Go `Currency` type is a validated value looked up from the registry at startup. Unknown codes fail.
  Disabling a currency stops **new** transactions only. Historical records keep their currency.

## 3. Representations

### 3.1 Go (`internal/platform/money`)

```go
type Currency struct { code string; minorUnits uint8 }   // constructed only via registry lookup

type Money struct {
    amountMinor int64    // unexported: no direct arithmetic by callers
    currency    Currency
}

func New(amountMinor int64, c Currency) Money
func (m Money) AmountMinor() int64
func (m Money) Currency() Currency
func (m Money) Add(o Money) (Money, error)        // ErrCurrencyMismatch, ErrOverflow
func (m Money) Sub(o Money) (Money, error)
func (m Money) Neg() (Money, error)               // ErrOverflow on MinInt64
func (m Money) Cmp(o Money) (int, error)          // ErrCurrencyMismatch
func (m Money) IsZero() / IsPositive() / IsNegative() bool
func (m Money) MulBasisPoints(bps int64, r RoundingMode) (Money, error)
func (m Money) Allocate(weights []int64) ([]Money, error)   // largest remainder, §6
func Parse(decimal string, c Currency) (Money, error)        // §7
func (m Money) DecimalString() string                       // "100.00", exact
```

Rules:

- Mixed-currency operations return `ErrCurrencyMismatch`. They **never panic and never convert.**
- All arithmetic is overflow-checked (`math/bits` or explicit bounds). Overflow returns an error. Realistic
  amounts are far below the limit (int64 ≈ 9.2×10¹⁸ minor units), but checking is cheap and mandatory.
- `Money` implements `json.Marshaler`/`Unmarshaler` producing §3.3. It implements `slog.LogValuer`
  (amounts are not secret, but the format stays consistent).
- No `float32`/`float64` appears in the package. A lint rule (from Stage 3) bans float types in `money`,
  `ledger`, `payments`, `payouts`, `fees` and `reconciliation`.

### 3.2 SQL (PostgreSQL)

```sql
amount_minor BIGINT  NOT NULL,
currency     CHAR(3) NOT NULL REFERENCES currencies(code),
```

- Never use `REAL`, `DOUBLE PRECISION` or `MONEY`. `NUMERIC` is not used for stored amounts either: integers
  make the minor-unit invariant structural.
- Sign constraints are explicit per column, for example `CHECK (amount_minor > 0)` on ledger entries and
  payment amounts, where direction is carried by a separate column.
- Every table holding money has both columns. A table never stores amounts "in the campaign's currency"
  without its own `currency` column.
- Aggregations `GROUP BY currency`. A `SUM(amount_minor)` across currencies is a defect. Reporting views
  enforce this structurally.

### 3.3 JSON (API)

```json
{ "amount_minor": "10000", "currency": "USD" }
```

- `amount_minor` is a **string** matching `^-?[0-9]{1,19}$` and within int64. JavaScript `Number` loses
  precision beyond 2⁵³. Strings make that impossible and keep the wire format uniform.
- The server rejects JSON numbers, decimal points, exponents, whitespace, a leading `+` and leading zeros
  (other than `"0"`) with `422 INVALID_MONEY`.
- The API may additionally return a **display-only** `formatted` field (for example `"US$100.00"`) produced
  server-side. Clients never parse it.

### 3.4 TypeScript (`apps/web`)

```ts
type CurrencyCode = "USD" | "ZWG";            // extended from API config, not hand-maintained long-term
interface Money { amount_minor: string; currency: CurrencyCode }
```

- Arithmetic on money in the frontend is **avoided entirely**. Totals, fees and remaining-to-goal come from the
  API. Where unavoidable (for example a live "you will pay" preview), use `BigInt` on `amount_minor`, never
  `Number`. The API recomputes and is authoritative.
- Formatting: convert the minor-unit string to an exact decimal string (insert the decimal point by string
  manipulation using `minor_units`), then pass it to `Intl.NumberFormat`. Intl accepts string input and formats
  it exactly in modern engines. Never compute `Number(amount_minor) / 100`.
- Input fields collect a decimal string, validate it with §7, and send `amount_minor` as a string.

## 4. Arithmetic rules

1. Only same-currency `Add`, `Sub`, `Cmp`. Anything else is an error.
2. Multiplication only by an **integer** (quantity) or by **basis points** (§5). Never by a float factor.
3. Division of money happens only through `Allocate` (§6), which guarantees exact conservation.
4. Negative amounts are allowed in `Money` (for example net movements in reports). Persisted ledger entries are
   always positive with a direction (see [LEDGER.md](LEDGER.md)).
5. Zero is a valid amount. Whether a zero amount is a valid *payment* is a domain rule (it is not).

## 5. Percentages, fees and rounding

- Rates are integer **basis points** (1 bp = 0.01%, 10 000 bp = 100%). A 5% fee is `500`. Fractional bp, if
  ever needed, use hundredths of a bp as a separate documented scale. Never floats.
- `MulBasisPoints(bps, mode)` computes `amount_minor × bps / 10_000` in integer arithmetic with overflow check
  (128-bit intermediate via `math/bits`). It then rounds **once** using the given mode.
- **Default rounding mode: `HalfUp`** (away from zero at .5) for fee calculation. `Floor`/`Ceil` exist for
  specific documented rules (for example a PSP that truncates).
- **Round at a single, documented point.** A fee is computed from the gross amount in one step. You may not
  compute intermediate rounded values and then add them.
- Fixed + percentage fee: `fee = round(gross × bps / 10 000) + fixed_minor` (same currency), capped and
  floored per schedule.
- The fee schedule (fixed amounts per currency, bps, caps, floors, rounding mode, effective dates) is
  versioned in `fees`. Each calculated fee records the **schedule version** used, so historical fees remain
  explainable after schedules change.

Worked example: donation USD 50.00, platform fee 5% (500 bp), HalfUp.

```
gross = 5000
fee   = 5000 × 500 / 10000 = 250.0 → 250 (US$2.50)
net   = 5000 − 250 = 4750
```

Rounding example: donation US$3.33 at 5%.

```
333 × 500 / 10000 = 16.65 → HalfUp → 17 (US$0.17); net = 316
```

## 6. Allocation (largest-remainder method)

Splitting an amount into parts (for example a fee shared between parties, or a batch payout split) must
produce parts that **sum exactly to the original**.

Algorithm `Allocate(total, weights)`:

1. Reject an empty weights list, negative weights, or all-zero weights.
2. For each weight `wᵢ`, `exactᵢ = total × wᵢ / Σw`. Take `floorᵢ = ⌊exactᵢ⌋` and `remᵢ = (total × wᵢ) mod Σw`.
3. `leftover = total − Σ floorᵢ` (0 ≤ leftover < number of parts).
4. Give one extra minor unit to the `leftover` parts with the largest `remᵢ`. **Tie-break: lowest index
   first**, so the result is deterministic.
5. Return the parts. Assert `Σ parts == total` (programming error otherwise).

Worked example: allocate US$100.00 (`10000`) by weights `[1, 1, 1]`.

| part | exact | floor | remainder (×3) |
|---|---|---|---|
| 0 | 3333.33… | 3333 | 1 |
| 1 | 3333.33… | 3333 | 1 |
| 2 | 3333.33… | 3333 | 1 |

`leftover = 10000 − 9999 = 1`. The tie-break gives it to index 0, so the result is `[3334, 3333, 3333]`, which
sums to `10000`. ✔

Worked example: allocate US$0.05 (`5`) by weights `[70, 30]` → exact `[3.5, 1.5]`, floors `[3, 1]`,
leftover 1, remainders `350 mod 100 = 50` and `150 mod 100 = 50` are equal, tie → index 0 →
`[4, 1]`. ✔

Negative totals (reversals) allocate on the absolute value and negate each part.

## 7. Parsing user input

`Parse(s, currency)` accepts a human decimal string and returns exact minor units, or an error. It never rounds.

- Accepted: `^[0-9]{1,15}(\.[0-9]{1,N})?$`, where `N = currency.minor_units`. Leading/trailing whitespace is
  trimmed by the caller (UI) before sending.
- Rejected with a specific error: too many decimal places (`"10.005"` for USD), separators (`"1,000"`), sign
  characters, exponent (`"1e3"`), empty input, non-ASCII digits, and values that overflow.
- Thousands separators and locale decimal commas are normalised **in the UI layer** for display convenience.
  The API accepts only the canonical form.
- Domain limits (minimum donation, maximum per transaction per method) are separate validations. They are
  configured per currency and per payment method/provider capability.

## 8. Formatting

- Server and client format from the exact decimal string. The default locale is `en-ZW`, falling back to
  `en`. Examples: `US$1,250.00`, `ZiG 1,250.00`.
- The currency is **always visible** next to an amount. Never show a bare "1,250.00".
- Mixed-currency lists show one line per currency. There are no totals across currencies.
- Any future indicative conversion display, for example "≈ US$40 at RBZ rate of 2026-10-08 09:00 UTC", must:
  - be labelled indicative;
  - show the rate source and timestamp;
  - come from a dedicated FX-rates read model;
  - never feed goal progress, fees, ledger or payouts.

  This is not in scope until a product decision and an ADR exist.

## 9. Campaign goal and multi-currency display

- A campaign has exactly one **goal currency** and a goal `Money`.
- MVP default: donations are accepted **only in the goal currency**. Accepting additional currencies is an
  open product decision. If enabled, the raised amount is reported per currency, and **the progress bar uses
  only the goal-currency amount**.
- Raised amounts come from ledger-derived figures (see [LEDGER.md §8](LEDGER.md#8-balances-and-projections)),
  never from a counter column incremented on donation.

## 10. Forbidden patterns

| Pattern | Why it's banned |
|---|---|
| `float64(amount)/100`, `amount * 0.05`, `parseFloat(...)`, `Number(amount_minor)` | Binary floating point cannot represent most decimal fractions. Errors accumulate and compound. |
| `SUM(amount_minor)` without `GROUP BY currency` | Combines currencies. |
| `DOUBLE PRECISION`, `REAL`, `MONEY` SQL types | Inexact, or locale-dependent. |
| An amount column without a sibling currency column | Currency becomes implicit. |
| `balance = balance + x` on any table | Bypasses the ledger ([LEDGER.md](LEDGER.md)). |
| Rounding intermediate results, then summing | The total drifts from a single-rounding result. |
| Converting currency implicitly ("treat ZWG as USD at rate X") | Accounting corruption, and an open regulatory question. |
| JSON numbers for amounts | Precision loss in JavaScript clients. |

## 11. Required test cases (Stage 3 implementation)

- Construction: unknown currency rejected. Both USD and ZWG round-trip through JSON and SQL.
- Add/Sub/Cmp: same currency OK. A USD+ZWG mismatch returns an error for every operation.
- Overflow: `MaxInt64 + 1`, `MinInt64 − 1` and `Neg(MinInt64)` all return an error.
- `MulBasisPoints`: 0 bp, 10 000 bp, the HalfUp boundary (`.5` exactly), very large amounts (128-bit path),
  and negative amounts.
- `Allocate`: equal weights with remainder, a zero weight, a single part, a total of zero, a negative total,
  property test asserting `Σ parts == total` and `|partᵢ − exactᵢ| < 1` for random inputs.
- `Parse`:
  - accepted: `"0"`, `"0.01"`, `"10"`, `"10.5"` (→1050), `"10.50"`;
  - rejected: `"10.505"`, `"1,000"`, `"-1"`, `"1e2"`, `""`, `" 1"`, `"１"` (full-width digit);
  - overflow values rejected.
- JSON:
  - rejected as invalid: number input, `"01"`, `"1.0"`;
  - must stay exact: values above 2⁵³ round-trip unchanged.
- Formatting: the TS formatter output matches Go `DecimalString` for a shared fixture table (golden file used
  by both test suites).
- Static check: no float types in the money/financial packages (lint test).
