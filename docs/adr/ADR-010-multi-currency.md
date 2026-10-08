# ADR-010: Multi-currency architecture

- **Status:** Accepted — **amended by [ADR-018](ADR-018-currency-isolation-and-fx.md)** (currency isolation rules, `minor_units_verified`; Stage 1)
- **Date:** 2026-10-08
- **Stage:** 0 (design), applies from Stage 2 data model onward

## Context

Zimbabwe uses both USD and ZiG (Zimbabwe Gold, ISO 4217 code **ZWG**). International donors will mostly pay in
USD, often by card. Local donors may pay in either currency via mobile money or bank. Exchange rates are
volatile, and exchange control rules may restrict conversion and settlement (LEGAL_REVIEW_REQUIRED, LR-005, LR-006). Implicitly
combining or converting currencies would misstate what a campaign has raised and corrupt accounting.

## Decision

- Every amount carries its currency (ADR-005). A `currencies` table (code, numeric, minor_units, minor_units_verified, name, display_symbol, enabled)
  starts with **USD (840, 2)** and **ZWG (924, 2)**. Code and data use `ZWG`, and the UI displays "ZiG". The
  minor-unit usage that PSPs apply to ZWG must be verified (open item). *Amended by
  [ADR-018](ADR-018-currency-isolation-and-fx.md):* `minor_units_verified` is `false` for ZWG until LR-043
  and PCR-018 are resolved, and while it is false ZWG runs only in sandbox/test.
- **USD and ZWG are never combined, summed or compared.**
  - The `Money` type rejects cross-currency arithmetic.
  - Ledger accounts are single-currency.
  - Balances, reports and payouts are always per currency.
- **There is no automatic FX.** No conversion is performed anywhere in the platform.
  - Any future "combined" display must be explicitly labelled *indicative*, show the rate source and
    timestamp, and never feed accounting.
  - Adding FX requires a new ADR and legal review.
- **Campaigns** have exactly one **goal currency**.
  - A campaign may accept donations in one or more enabled currencies. The MVP default is the goal currency
    only. Multi-currency acceptance is an open product decision.
  - Raised amounts are shown per currency. The progress bar uses only the goal-currency amount.
- Adding a currency is a data change plus configuration and tests, not a schema change.

## Consequences

### Positive
- Accounting is correct by construction, with no hidden FX exposure.
- Expansion to other African markets and currencies requires no redesign.

### Negative / costs
- The UX for a campaign receiving both USD and ZiG is more complex, because there are two totals.
- Payouts may be needed in each currency separately, depending on provider settlement capabilities.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Single platform currency (USD only) | Excludes ZiG-paying local donors. Not Zimbabwe-first. |
| Convert everything to USD at donation time | Introduces FX risk, rate-source disputes and likely exchange control questions. Misstates donor intent. |
| Store a "base currency" equivalent alongside every amount | Tempts combined accounting. It may exist later only as clearly labelled indicative reporting data. |

## Security implications
Currency is validated on input against enabled currencies. A currency-confusion attack, such as paying in ZWG
while a USD amount is recorded, is prevented because the provider-confirmed currency must match the payment
intent's currency. A mismatch is rejected and raises a reconciliation alert.

## Financial implications
Ledger balancing is checked per currency. Payouts, fees and refunds are computed per currency, and
reconciliation is per currency per provider.

## Related
ADR-005, ADR-006, ADR-007, [MONEY.md](../MONEY.md), [LEDGER.md](../LEDGER.md), [COMPLIANCE.md](../COMPLIANCE.md).
