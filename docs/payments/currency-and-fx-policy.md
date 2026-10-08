# Currency and FX Policy

**Stage 1 — design policy.** Extends [MONEY.md](../MONEY.md) and [ADR-010](../adr/ADR-010-multi-currency.md);
decision record [ADR-018](../adr/ADR-018-currency-isolation-and-fx.md). Regulatory statements cite research
findings (R1-xx, R3), accessed 2026-10-08. Their interpretation is **LEGAL_REVIEW_REQUIRED**.

## 1. Rules (normative)

1. **Money is integer minor units plus an ISO 4217 code**, everywhere ([MONEY.md](../MONEY.md)). There are no
   floats, including for FX rates (§6).
2. **Currencies are isolated.** USD and ZWG (ZiG) amounts are never summed, netted, compared or converted
   implicitly. Every ledger transaction is single-currency.
3. **FundZim never converts currency.** In Zimbabwe, foreign-currency dealing is "only limited to Authorised
   Dealers and Authorised Dealers with Limited Authority" (RBZ FX Guideline FXD 2/2025 para 1.1.2.2 — R1-13;
   HIGH). Any conversion is performed, and recorded, by a bank, ADLA or the provider, never by FundZim.
4. **No FX in the MVP.** No product feature converts amounts or shows a converted figure as a fact.
5. **A donation is accepted on a rail only if that rail settles in the donation's currency.** If a provider
   cannot settle USD-for-USD and ZWG-for-ZWG, the method is not offered in that currency.

## 2. Currency of each financial element

| Element | Rule | Source of truth |
|---|---|---|
| **Donation currency** | The currency the donor pays in, chosen at checkout from the campaign's accepted currencies | Payment record (`currency`) |
| **Campaign goal currency** | Exactly one per campaign, set at creation and changed only via re-review (Stage 0, PRODUCT §9) | Campaign |
| **Accepted currencies** | MVP default: goal currency only (PD-02). Multi-currency acceptance is optional, per campaign. | Campaign config |
| **Settlement currency** | Per provider × method × currency (capability). **Must equal the donation currency.** | Provider capability matrix |
| **Payout currency** | Equals the currency of the ledger account it is paid from. A USD payout comes only from USD `campaign_payable`. | Payout record |
| **Processing-fee currency** | The donation's currency | Ledger T2 |
| **Platform-fee currency** | The donation's currency (fee computed in basis points on that amount — [MONEY.md](../MONEY.md) §5) | Ledger T1 |
| **Refund currency** | The original payment's currency | Refund record |
| **Reserve, held, recoverable balances** | Per currency, per campaign | Ledger |

**Goal progress:** the progress bar uses only donations in the goal currency. Donations in other accepted
currencies are shown as separate "also raised" lines ([MONEY.md](../MONEY.md) §9). There is no combined total.

## 3. Regulatory context (LEGAL_REVIEW_REQUIRED)

| Topic | Finding | Policy consequence |
|---|---|---|
| ZiG legal basis | ZiG was introduced by S.I. 60 of 2024 (5 Apr 2024), inserting s44D in the RBZ Act. ZiG notes and coins are legal tender "alongside any other currency acceptable as legal tender as prescribed under section 44A" (R1-10; Veritas; HIGH). The lapse dispute and the parliamentary basis remain open (R1-10). | Support ZWG as a first-class currency. Track legal basis under LR-079. |
| Multi-currency regime | The RBZ guideline states that S.I. 218 of 2023 keeps USD, other denominated currencies and local currency as legal tender "till 31 December 2030" (FXD 2/2025 para 1.1.1.6; R1-12; HIGH that the guideline says so, MEDIUM for the underlying legal text). A Feb 2026 MPS report says 2030 is no longer the policy deadline (R1-12; MEDIUM). | USD campaigns are permitted under the current regime. The design must allow a campaign's accepted currencies to be changed by policy (LR-079). |
| Exchange control: who converts | FX dealing is limited to ADs and ADLAs (R1-13; HIGH) | FundZim never converts (rule 3) |
| Inbound foreign-currency donations | Individual FCAs may be funded from "donations… diaspora remittances" as free funds; NGO FCAs are permitted (FXD 2/2025 paras 2.1.2.10, 2.1.2.18 — R1-14; HIGH) | USD payouts to beneficiaries' USD accounts and wallets are the expected path. Whether campaign proceeds count as "donations"/free funds is LR-005. |
| Outbound | "All corporate monetary donations… require prior Reserve Bank approval" (para 3.1.13.1 — R1-14) | No payouts to beneficiaries outside Zimbabwe in the MVP. Refunds to foreign card donors run via the provider (LR-078 (→ LR-044)). |
| Cross-border card donations | Remittance vs merchant payment characterisation unclear (R1-15 open question) | LR-078 (→ LR-044) |
| IMTT | 1.5% per transaction on local currency from 1 Jan 2026 (Finance Act 2025 s8 — R1-16; HIGH). 2% on USD (secondary; MEDIUM). Collected by financial institutions; no donation exemption found. | It may apply on collection and on payout legs. Disclosure and who bears it are LR-077 (→ LR-059) / PD-16. Never compute or deduct IMTT in FundZim's ledger unless the provider reports it as a deduction (then record it as reported, as a separate line). |

## 4. ZWG specifics

- **Code.** The internal code is always `ZWG` (ISO 4217 numeric 924 per [MONEY.md](../MONEY.md) §2); the display
  label is "ZiG".
- **Minor unit — UNVERIFIED.** S.I. 60 of 2024 does not define a subdivision (R1-11; LOW). MONEY.md registers
  ZWG with `minor_units = 2`, which is a common convention and remains **unverified**. Required actions:
  - add `minor_units_verified BOOLEAN` to the currency registry, `false` for ZWG until verified against the ISO
    4217 maintenance list and the provider's API behaviour (Payonify documents amounts in minor units with
    "100 = $1.00 or ZWG 1.00" — R3 §3 technical notes);
  - while `false`, ZWG may run only in sandbox/test, and Stage 20 launch with ZWG is blocked (LR-006; PCR-018 — ZWG minor units).
- **Provider code variance.** Pesepay charges EcoCash and PayGo with currency code `ZiG` but Zimswitch and Omari
  with `ZWG` (R3 §2 row 9; VERIFIED). Adapters own this mapping; `ZiG` never enters the domain model.

  | Domain | Pesepay EcoCash / PayGo | Pesepay Zimswitch / Omari |
  |---|---|---|
  | `ZWG` | `ZiG` | `ZWG` |

- **Currency selection where the API has no currency field.** Paynow's initiate API takes an `amount` with no
  currency field. Its blog says ZiG and USD settle to the merchant's respective accounts, but how an
  integration selects the currency is unconfirmed (R3 §1 row 9; PCR-030, PCR-018 — Paynow currency selection). The adapter
  must not guess. The capability is `REQUIRES_PROVIDER_CONFIRMATION` until documented, possibly as one
  integration ID per currency.
- **Rail/currency combinations** differ by provider (e.g. InnBucks is USD-only on Paynow and Pesepay; Linkwa is
  USD-only today — R3). The capability model lists supported pairs explicitly. There is no assumption that a
  method supports both currencies.

## 5. International donations

- The donor's card may be in another currency (GBP, ZAR…). If the card scheme or issuer converts to USD, that
  conversion is **between the donor and their issuer or the scheme**. It is outside FundZim's ledger. FundZim
  records only the **transaction currency and amount reported by the provider** (e.g. USD 50.00).
- FundZim shows the donor the amount in the transaction currency, plus a notice that their bank may convert
  and charge fees. It shows no estimated home-currency amount in the MVP.
- The ledger never contains GBP, ZAR or any other foreign currency unless that currency is added to the
  registry by ADR. No such ADR exists.

## 6. Future FX (not in MVP) — required record

If conversion is ever offered (for example, a beneficiary wants a USD campaign paid out in ZWG, executed by an
authorised dealer), it requires an ADR and legal review. The conversion must be represented by an explicit
`fx_conversions` record. It is never an implicit cross-currency journal.

| Field | Rule |
|---|---|
| `id`, `idempotency_key` | UUIDv7; unique |
| `source_amount_minor`, `source_currency` | Integer minor units |
| `quoted_target_amount_minor`, `final_target_amount_minor`, `target_currency` | Quote vs executed amounts, both stored |
| `rate` | **Decimal string** with an explicit scale (e.g. `"26.8312"`), or an integer numerator/denominator pair. **Never float.** |
| `rate_source` | E.g. the executing authorised dealer and its rate reference |
| `rate_timestamp` | UTC |
| `fees_minor`, `fees_currency` | Conversion fees |
| `conversion_provider` | The AD/ADLA/provider that executed it (must be authorised — R1-13) |
| `provider_reference` | External reference |
| `audit_event_id`, `evidence_record_id` | Links |
| `reconciliation_status` | UNMATCHED / MATCHED / DISCREPANCY |

Ledger representation: **two single-currency transactions linked by `fx_conversion_id`.** The source currency
leaves the pool (Dr campaign side / Cr pool) and the target currency enters (Dr pool / Cr campaign side), each
balanced in its own currency. FX gains and losses would need an ADR-defined account. Rate and amount
computation uses integer arithmetic with a documented single rounding point ([MONEY.md](../MONEY.md) §5).

## 7. Enforcement

| Layer | Control |
|---|---|
| Domain | `money.Money` operations reject mixed currencies (MONEY.md). A `CurrencyPair` type is not permitted without an ADR. |
| Routing | Provider × method × currency must be a declared capability; otherwise `PAYMENT_METHOD_UNAVAILABLE` |
| Ledger | LEDGER L2/L3: entry currency = account currency; one currency per transaction |
| DB | `CHECK` that `payouts.currency = (account currency)`, and a payment's refund currency equals the payment's |
| UI | No combined totals. Each amount carries its currency label. |
| Tests | Mixed-currency attempts fail at every layer ([TESTING.md](../TESTING.md)) |

## New legal questions

| ID | Area | Question | Why it matters / what is blocked |
|---|---|---|---|
| LR-077 (→ LR-059) | IMTT on donation flows | Is IMTT charged on each leg of a Model A flow (donor → PSP collection, PSP → beneficiary payout, PSP → FundZim fee remittance)? Is any exemption available for donations or for registered PVO/charity beneficiaries? What must be disclosed to donors and beneficiaries? | It affects the net amount beneficiaries receive and donor disclosures. It may make some rails uneconomic. Blocks fee disclosure copy (Stage 7) and the fee model (PD-01). |
| LR-078 (→ LR-044) | Cross-border card donations & refunds | Is a foreign donor's card donation to a Zimbabwean campaign a merchant card payment, or a cross-border remittance requiring a registered remittance operator (2017 Guidelines para 13.1 — R1-06; R1-15)? How are refunds to foreign cards treated under FXD 2/2025 para 3.1.13 (outbound payments)? | Determines whether international donations are allowed at launch (PD-18) and which provider products qualify. |
| LR-079 | Currency regime stability | The ZiG legal-basis dispute (R1-10), the statutory 31 Dec 2030 multi-currency sunset vs the 2026 policy change (R1-12): what happens to USD-denominated campaign obligations and USD payouts if the regime changes? Can FundZim be required to pay out in ZWG? | Long-lived USD campaign balances and reserves. FundZim needs a policy for regime changes (forced conversion would be executed by authorised dealers, never FundZim). Affects terms of service (LR-022). |

## New business decisions

| ID | Decision | Default proposed |
|---|---|---|
| PD-16 | IMTT and rail-cost disclosure: show IMTT and fees as estimates at checkout? Who bears them (donor-covers option)? | Disclose that bank/wallet taxes and fees may apply; no FundZim computation of IMTT; revisit after LR-077 (→ LR-059) |
| PD-18 | Accept international (foreign-issued) card donations at pilot? | Not at pilot unless LR-078 (→ LR-044) is resolved favourably and the provider verifies foreign-card acceptance (Paynow VERIFIED; others unverified — R3) |
