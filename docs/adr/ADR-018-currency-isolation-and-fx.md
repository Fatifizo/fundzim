# ADR-018: Currency isolation and FX policy (no FX in the MVP)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; counsel for exchange-control questions (LR-005, LR-006)
- **Stage:** 1 (policy). Extends [ADR-010](ADR-010-multi-currency.md). Applied from Stage 2 onward.

## Context

[ADR-010](ADR-010-multi-currency.md) made the data model multi-currency (USD and ZWG/ZiG) and forbade
implicit combination. Stage 1 must define which currency applies at each step — donation, goal, settlement,
payout and fees — and what any future conversion must record.

Stage 1 research ([regulatory-landscape.md](../compliance/regulatory-landscape.md)):

- ZiG was introduced by S.I. 60 of 2024 (5 April 2024) as legal tender alongside other currencies; the
  instrument does not define its minor unit (unverified; PCR and LR-006).
- The RBZ foreign-exchange guideline (April 2025) states that only authorised dealers may buy and sell foreign
  currency; residents' and NGOs' foreign currency accounts may receive donations and diaspora remittances.
- Providers differ: one uses different ZiG currency codes per rail (`ZiG` vs `ZWG`); another's API exposes no
  currency field ([provider-capability-matrix.md](../payments/provider-capability-matrix.md)).

## Decision

1. **Donation currency** = the currency the donor pays in on that rail, as reported by the provider.
2. **Goal currency** = exactly one per campaign (Stage 0). Progress uses goal-currency amounts only.
3. **Settlement currency** must equal the donation currency. If a rail or provider would settle a donation in
   a different currency, that rail is not offered for that campaign currency. No implicit FX.
4. **Payout currency** = the currency of the ledger account being paid from. A USD payout draws only on the
   USD `campaign_payable`; a ZWG payout only on the ZWG one.
5. **Processing-fee and platform-fee currency** = the donation currency.
6. **FundZim never converts currency** and never quotes or applies an exchange rate in the MVP. Converting
   would mean dealing in foreign currency, which the RBZ guideline reserves to authorised dealers.
7. **International cards:** if the issuer, scheme or provider converts the donor's home currency, that
   conversion is outside FundZim's ledger. FundZim records only the transaction currency and amount the
   provider reports as charged and settled; any cardholder-currency figure is informational metadata.
8. **Future FX** (only via a new ADR and an authorised conversion provider) must record, per conversion:
   explicit rate (as an exact decimal string or integer-scaled value, never float), rate source, rate
   timestamp, quoted amounts, final amounts, fees, conversion provider and its reference, audit event id, and a
   reconciliation link. Each side posts to its own currency's accounts.
9. The currency registry keeps ZWG `minor_units = 2` provisionally with a `minor_units_verified = false` flag
   until a primary source or provider confirms; ZWG cannot be enabled for live money while the flag is false.
10. Every money type, table and API keeps currency attached ([ADR-005](ADR-005-exact-money-representation.md)).

## Consequences

### Positive
- Removes exchange-control exposure from FundZim's own operations.
- Keeps every journal, balance and payout single-currency and auditable.

### Negative / costs
- Donors cannot pay in one currency to a campaign whose goal is another unless multi-currency acceptance is
  enabled (PD-02); totals per currency may confuse users.
- ZiG launch depends on provider confirmation of codes, minor units and settlement currency.

### Follow-up work
- PCR-018, PCR-004 — currency selection per provider, ZiG codes and minor units, settlement currency.
- LR-005 (inbound international donations), LR-006 (ZiG handling), LR-016 (IMTT on each leg).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Convert ZiG donations to USD for a single campaign total | FundZim would be dealing in FX; needs an authorised dealer; misstates the ledger. |
| Display a combined indicative total by default | Misleading without labelled rates; allowed later only as labelled, non-accounting display. |

## Security implications
Currency fields are validated against the registry at API boundaries; mismatched provider-reported currency
is a reconciliation anomaly and a potential manipulation signal, never silently accepted.

## Financial implications
No cross-currency postings exist in the MVP; any mismatch between expected and provider-reported currency goes
to `suspense:settlement_discrepancy:{p}` in the reported currency and opens a case.

## Related
[currency-and-fx-policy.md](../payments/currency-and-fx-policy.md), [MONEY.md](../MONEY.md), ADR-005,
ADR-010, ADR-014. LEGAL_REVIEW_REQUIRED: LR-005, LR-006, LR-016.
