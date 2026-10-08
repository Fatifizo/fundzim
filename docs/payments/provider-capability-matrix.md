# Payment Provider Capability Matrix

> **Stage 1 deliverable.** This is evidence about providers, not a selection. No provider has been chosen,
> contacted or integrated. It does not certify that any provider is licensed or suitable. All statuses
> reflect only public documentation opened on **2026-10-08**. They must be confirmed in writing by each
> provider during due diligence ([provider-due-diligence-checklist.md](provider-due-diligence-checklist.md)) before
> any decision. Open questions are numbered `PCR-xxx` in [provider-questions.md](provider-questions.md).

Related documents:
- Scoring and recommendation: [provider-comparison.md](provider-comparison.md)
- Provider abstraction and capability model: [../PAYMENTS.md](../PAYMENTS.md) §3–§5
- Operating model, which defines the capabilities that matter most:
  [../compliance/operating-model-decision.md](../compliance/operating-model-decision.md), [ADR-013](../adr/ADR-013-regulatory-operating-model.md)

---

## 1. Status vocabulary

| Status | Meaning in this document |
|---|---|
| **VERIFIED** | An official provider page or developer doc that we opened states the capability. The source code is given. "VERIFIED (marketing)" means an official page states it at marketing level, with no technical documentation. |
| **UNVERIFIED** | Claimed only in marketing, a secondary source, a third-party SDK, or a page that is ambiguous or contradicted elsewhere. |
| **NOT_SUPPORTED** | Official docs say it is not supported, or it is missing from a list the docs present as complete. The cell says which. |
| **REQUIRES_PROVIDER_CONFIRMATION** (shown as **RPC**) | No information was found. |

Rules for using this matrix:
1. **Only VERIFIED counts as evidence for design.** Even VERIFIED means "publicly documented on 2026-10-08", not
   "contractually available to FundZim". Contract and production availability always need confirmation (PCR-001 to PCR-007).
2. A capability FundZim's design depends on (webhook authenticity, payouts, refunds, split/sub-merchant
   settlement) is **never assumed** from UNVERIFIED or RPC cells. The [sandbox provider](../PAYMENTS.md) and the
   capability model must treat it as absent until verified.
3. This matrix is dated. Re-verify before Stage 9 (integration) and again before Stage 20 (pilot).

### 1.1 Identity caveat

"**Payonify**" and "**Linkwa**" did not appear in general web search. They were located through the domains
`payonify.co.zw` (docs at `docs.payonify.com`) and `linkwa.co.zw` (operated by Pandawoga Innovate PBC).
**The project owner must confirm these are the companies intended in the Stage 1 brief** before any outreach.
Other `.com/.io/.africa` variants did not resolve, or were unrelated (`linkwa.com` is a parked page).

---

## 2. Regulatory baseline: RBZ lists of licensed providers

| Item | Finding | Source |
|---|---|---|
| RBZ "Approved Payment Systems Platforms" (PDF, metadata dated 28 Oct 2019) | Lists RTGS, CSDs, Zimswitch, MasterCard, Visa, UnionPay International, MyCash, Paynet, EcoCash, Getcash, OneMoney, Telecash, Cheque and IceCash. **None of Paynow/Softwarehouse, Pesepay/Code Virtus, Payonify, Linkwa/Pandawoga, ContiPay or Smile&Pay appear.** The list is dated and covers platforms (switches and wallets), not gateways. | [RBZ-1] |
| RBZ "Payment Systems Infrastructure in Zimbabwe" (PDF, 2019) | Lists approved providers by channel (card, mobile, internet, EFT). States the Retail Payment Systems and Instruments Guideline came into force 1 Aug 2017. | [RBZ-2] |
| RBZ National Payment System web pages | **Could not be loaded**: the site redirects automated access to a bot-captcha. No current register was retrieved. | [RBZ-3] |

**Consequence:** for every gateway candidate, licensing status is at best self-described (row 21). This is the
single largest evidence gap, and it is why [provider-comparison.md](provider-comparison.md) makes no selection.
It is tracked as PCR-001 and in the regulatory register
([../compliance/regulatory-requirements-register.md](../compliance/regulatory-requirements-register.md)).

---

## 3. Full matrix: 23 criteria × candidates

Each cell gives the status, a short piece of evidence, and the source key(s) from §6. "Mktg" = official marketing page only.

### 3.1 Collection rails and currencies (criteria 1–10)

| # | Criterion | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay (ZB) | ContiPay |
|---|---|---|---|---|---|---|---|
| 1 | Collection methods | **VERIFIED.** Express checkout: Visa/MC, Zimswitch, EcoCash, OneMoney, InnBucks, O'mari. The FAQ also lists Telecash, Vpayments and POS2U. [P2][P11][P14] | **VERIFIED.** USD: EcoCash, InnBucks, Omari, Zimswitch, Visa, Mastercard. ZWG: EcoCash, PayGo, Zimswitch, Omari. [S2] | **VERIFIED.** Checkout: EcoCash, OneMoney, Visa/MC; Zimswitch "Coming Soon". The Charges API is mobile money only. [Y9][Y7] | **VERIFIED.** EcoCash, InnBucks, OneMoney, Omari, SmileCash, Visa, Mastercard, via hosted links or QR. [L4][L1] | **VERIFIED (mktg).** Visa, Mastercard, EcoCash, InnBucks, Zimswitch "and more". [Z1][Z2] | **UNVERIFIED.** "Mobile money, card, and supported bank-linked payments"; wallets not named. [C1] |
| 2 | EcoCash | **VERIFIED.** `method=ecocash` [P2][P3] | **VERIFIED.** USD $1–$500, ZiG 2–8,000; larger amounts are split into parts. [S3] | **VERIFIED** [Y7][Y9] | **VERIFIED** [L4] | **VERIFIED (mktg)** [Z1] | **UNVERIFIED** [C1] |
| 3 | OneMoney | **VERIFIED.** `method=onemoney`, USD and ZiG. [P2][P16] | **NOT_SUPPORTED** (missing from the list of methods "live on Pesepay today") [S2] | **VERIFIED** [Y7][Y9] | **VERIFIED** [L4] | **UNVERIFIED** (SmileLink page and third-party SDK only) [Z3][Z4] | **UNVERIFIED** [C1] |
| 4 | InnBucks | **VERIFIED**, USD only. [P2][P15] | **VERIFIED**, USD only, $1–$1,000. [S5] | **RPC** (not mentioned) | **VERIFIED** [L4] | **VERIFIED (mktg)** [Z1][Z2] | **UNVERIFIED** [C1] |
| 5 | O'Mari | **VERIFIED.** `method=omari`, OTP step. [P2] | **VERIFIED**, USD and ZWG, two-step OTP. [S6] | **RPC** (not mentioned) | **VERIFIED** [L4] | **VERIFIED (mktg)** [Z2] | **UNVERIFIED** [C1] |
| 6 | ZimSwitch / bank | **VERIFIED.** `method=zimswitch`; Vpayments for several banks. [P2][P11] | **VERIFIED**, Zimswitch USD and ZWG (redirect only); no bank-transfer method listed. [S2][S4] | **NOT_SUPPORTED (currently)** per docs ("Coming Soon"). **Contradicted** by a homepage claim, so that claim is UNVERIFIED. [Y9][Y1] | **RPC** (not listed) [L4][L1] | **VERIFIED (mktg).** Zimswitch; SmileLink accepts ZB accounts. [Z1][Z3] | **UNVERIFIED** [C1] |
| 7 | Visa / Mastercard | **VERIFIED.** `method=vmc`; available to "Paynow Verified Merchants". [P2][P11] | **VERIFIED**, USD only, hosted page with 3-D Secure. [S4] | **VERIFIED**, Checkout only. [Y9][Y11] | **VERIFIED** [L4] | **VERIFIED (mktg)** [Z1] | **UNVERIFIED** [C1] |
| 8 | USD | **VERIFIED.** Settles to the merchant's USD account; Visa settles to a NOSTRO account. [P15] | **VERIFIED.** `currencyCode: "USD"` [S2][S12] | **VERIFIED.** The `currency` enum is usd and zwg. [Y7] | **VERIFIED.** "All payments today are in USD." [L4] | **RPC** | **UNVERIFIED.** "Paid in ZiG or USD" [C1] |
| 9 | ZiG (ZWG) | **VERIFIED** (blog) for EcoCash, OneMoney, Zimswitch and Omari. **How an integration selects the currency is RPC**: the API has no currency field. [P1][P15][P16][P17] | **VERIFIED, with a quirk.** EcoCash and PayGo use code `ZiG`; Zimswitch and Omari use `ZWG`. [S2][S16] | **VERIFIED** for mobile money (`zwg`). [Y7][Y1] | **NOT_SUPPORTED (today).** "When ZWL/ZiG is introduced, users will be notified." [L4] | **RPC** | **UNVERIFIED** [C1] |
| 10 | International donors (foreign-issued cards) | **VERIFIED.** "Both locally and internationally issued Visa / Mastercard cards will work"; foreign cards settle T+3. [P11] | **UNVERIFIED.** Not stated in the card docs; homepage marketing only. [S4][S21] | **RPC** | **VERIFIED.** "…isn't limited to cards issued in Zimbabwe." [L4] | **VERIFIED for SmileLink only**; RPC for Smile&Pay. [Z3] | **UNVERIFIED** [C1] |

### 3.2 Integration quality and security (criteria 11–13)

| # | Criterion | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay (ZB) | ContiPay |
|---|---|---|---|---|---|---|---|
| 11 | API docs quality | **VERIFIED (moderate).** Clear field tables and hash examples, but a legacy form-POST API with no JSON/REST or OpenAPI spec. Note: the docs index returned HTTP 403 while individual pages loaded. [P1–P8] | **VERIFIED (high).** OpenAPI/Postman, SDKs in four ecosystems, failure modes, go-live checklist. [S1] | **VERIFIED (high).** Versioned REST/JSON `/v1/`, OpenAPI reference, pagination, error codes. [Y2][Y7] | **UNVERIFIED (basic).** Small REST API, read through a summarised rendering. [L1] | **RPC.** No public docs. [Z4] | **RPC.** Docs are behind a login. [C1] |
| 12 | Sandbox / test mode | **VERIFIED.** Test mode with documented test numbers and tokens; "no actual money is moved". [P8][P3] | **VERIFIED (limited).** USD only; EcoCash, Visa and MC only. [S15][S2] | **VERIFIED.** `sk_test_` keys and test numbers for success, delay, failure and timeout. [Y2][Y10][Y5] | **VERIFIED.** Free sandbox; a production-replica staging environment. [L3][L4] | **UNVERIFIED** (third-party SDK claim) [Z4] | **UNVERIFIED.** "Separate test environment" [C1] |
| 13 | Webhook security | **VERIFIED.** `hash` = SHA512 over the field values with the Integration Key appended. **No timestamp or nonce**, so no built-in replay protection. Resent up to 10 times; polling `pollurl` is recommended for important updates. [P4][P5][P6] | **VERIFIED (weak).** "Pesepay does not currently sign callback payloads"; **"There is no retry"** (sent once, terminal statuses only). Polling is required. [S8][S9][S10] | **VERIFIED (strong).** `Payonify-Signature: t=…,v1=…`, HMAC-SHA256 over `{t}.{raw_body}`, recommended 5-minute tolerance, 10 retries with backoff, event `id` for deduplication. [Y3] | **VERIFIED** (summarised page). `X-Linkwa-Signature`, HMAC-SHA256 of the raw body; retries at 10s, 30s, 60s then every 5 minutes, no maximum stated; webhooks are a Growth-plan feature. [L2] | **UNVERIFIED (negative).** A third-party SDK says callbacks are unsigned. [Z4] | **UNVERIFIED.** "Secure notifications" [C1] |

### 3.3 Money movement (criteria 14–18)

| # | Criterion | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay (ZB) | ContiPay |
|---|---|---|---|---|---|---|---|
| 14 | Refunds via API | **RPC.** A `Refunded` status exists; no refund endpoint is documented. [P6] | **RPC.** No refund endpoint; the dashboard mentions refunds "in the panel". [S16][S1] | **VERIFIED.** `POST /v1/refunds`, full refunds; no amount field, so partial refunds look unsupported (PCR). [Y8][Y1] | **RPC.** Terms say buyers contact the seller, and Linkwa may mediate. [L3][L1] | **UNVERIFIED** (SDK description) [Z4] | **RPC** |
| 15 | Payouts / disbursements API | **RPC.** No payout API in the collections docs. [developer hub] | **RPC.** No payout endpoint; settlement to the merchant's payout bank account is T+2. [S16] | **VERIFIED (EcoCash only, approval needed).** `POST /v1/payouts/validate` and `POST /v1/payouts` (EcoCash B2C) "requires prior approval"; OneMoney "Coming Soon"; no bank payouts; a reversals API exists. [Y5] | **VERIFIED (SmileCash only).** `POST /payouts` to a verified SmileCash wallet; other wallets are "next". [L1][L4] | **RPC** | **RPC** |
| 16 | Beneficiary / sub-merchant onboarding | **RPC, with a contract risk.** No sub-merchant API. Terms: an individual account holder warrants "that you are not acting on behalf of an undisclosed principal or a third party beneficiary". [P12] | **VERIFIED (manual).** A split beneficiary must be "an approved Pesepay merchant account" with an approved payout account in that currency, invited through the dashboard; merchant KYC includes ID, proof of address, CR14 and bank proof. No onboarding API. [S7][S20] | **NOT_SUPPORTED** as a KYC'd sub-merchant concept. Relay pays mobile numbers directly; Relay "only shows for approved businesses; sole traders aren't eligible". [Y4] | **VERIFIED.** `POST /users`, and `POST /wallets` registers a SmileCash wallet with ID fields and an ID picture for unregistered numbers. [L1] | **RPC** | **RPC.** "Multi-Merchant Hub" (branches/brands, not clearly third parties) [C1] |
| 17 | Split settlement / platform fee | **RPC.** Only splitting fees with the *customer* is documented; there is no platform/beneficiary split. [P10][P11] | **VERIFIED.** Native split: master share PERCENTAGE or FIXED_AMOUNT, in PRINCIPAL or ADD_ON mode. Limits: **one beneficiary per transaction**; terms fixed per arrangement; "Nothing about the split appears on the callback"; a fixed share ≥ the amount fails silently *after* the customer is charged. [S7] | **VERIFIED (EcoCash only).** Relay: `POST /v1/transfers` with `application_fee_amount`; funds held per currency and per provider, with available/pending/reserved balances. [Y4] | **RPC.** No native split; a fee would mean paying out less (not documented). [L1] | **RPC** | **UNVERIFIED.** A customer-fee split only. [C1] |
| 18 | Reconciliation / statements API | **RPC.** Only per-transaction `pollurl` and `/interface/trace`; transaction history in the UI. [P6][P7] | **UNVERIFIED (dashboard only).** A Reports tab; there is no reports API, only `check-payment`. [S19][S13] | **VERIFIED (partial).** List and filter endpoints and `GET /v1/transfers/balance`; no settlement-report API. [Y7][Y4][Y1] | **VERIFIED.** `GET /balance` and `GET /statement` (paginated). [L1] | **RPC.** "Generate reports" [Z1] | **UNVERIFIED.** "Reconciliation-ready reports" [C1] |

### 3.4 Commercial, regulatory and operational (criteria 19–23)

| # | Criterion | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay (ZB) | ContiPay |
|---|---|---|---|---|---|---|---|
| 19 | Published fees | **VERIFIED.** Visa 3.5% + 50c; Mastercard 3.5% + 50c; Vpayments 1% + 50c; EcoCash 2.5%; OneMoney 2.5%; Telecash 2.5%. "From as little as 1.5% + 50c"; no setup or standing fees. [P10][P14][P11] | **VERIFIED.** Mobile Money 2%, ZimSwitch 2%, VISA 3%, Mastercard 3% (indirect settlement); "Zero integration fee", "Zero maintenance fee"; volume discounts. [S17] | **VERIFIED.** Mobile money 2.45%; Visa/MC 2.9% + 59c; EcoCash disbursements 1.5%; settlements USD 1.5% + tax (max $20.00), ZWG 1.5% + tax (max USD 20 equivalent). Fees are "deducted on the customer side". [Y11] | **Not published** ("shown in-app"). Each transaction carries a platform fee and an Online Payment Fee paid by the buyer. [L3] | **Not published** | **Not published.** Disclosed in merchant agreements. [C2] |
| 20 | Settlement timelines | **VERIFIED.** Local switched payments next day (after escrow clears); local Visa/MC T+2; foreign Visa/MC T+3; 8pm cut-off; non-verified merchants weekly on Tuesday, verified merchants daily Mon–Sat. Escrow (Buysafe) holds funds until delivery is confirmed plus 24h, unless disabled for Verified Merchants. [P11][P6] | **VERIFIED.** T+2 once the minimum amount in the Merchant Agreement is reached; funds go "to Pesepay trust account held by the bank" first. [S16][S17] | **RPC.** Only "pending, then available once a short hold has passed". [Y4] | **VERIFIED.** Mobile-wallet payments settle "instantly" to the linked wallet; cards in 3–4 working days; no weekend settlement. [L3][L4] | **RPC** | **Not published** [C2] |
| 21 | Licensing / authorisation evidence | **UNVERIFIED.** Self-described "Payment Service Provider"; no RBZ licence found; not on the 2019 RBZ list. [P13][RBZ-1] | **RPC.** Operated by Code Virtus, "a Software Development Company"; no RBZ statement; not on the 2019 list. [S18][RBZ-1] | **UNVERIFIED, contradictory.** Terms: "technical facilitator only… We do not hold, manage, or deposit funds", which **conflicts** with the Payonify-held balances its Relay and Payouts docs describe. [Y11][Y4] | **UNVERIFIED.** "Payment processing and settlement are performed by licensed payment and banking partners" (unnamed). [L3][L4] | **VERIFIED (bank group).** Offered by ZB Financial Holdings, "Member of the Deposit Protection Corporation". The product-level authorisation is RPC. [Z1] | **UNVERIFIED.** "RBZ compliant" (marketing, no licence cited) [C1] |
| 22 | Contractual suitability for crowdfunding | **RPC, with risk flags.** Donations are not named in the prohibited list reviewed, but there is the third-party-beneficiary warranty (row 16), escrow designed around "delivery of goods", and a local bank account is required. [P12][P11] | **VERIFIED (favourable), contract pending.** FAQ: "From donation campaigns…"; the prohibited list excludes donations and crowdfunding; split docs cite collecting "on behalf of a school". The Merchant Agreement is not public. [S19][S18][S7] | **RPC.** Terms cover "goods and services"; donations are neither named nor prohibited. [Y11] | **VERIFIED (favourable).** Supported purposes include "Donations" and "Group Contributions". [L3][L4] | **UNVERIFIED (favourable).** SmileLink is "ideal for schools, churches, clubs… family contributions". [Z3] | **RPC** |
| 23 | Support / reliability signals | **UNVERIFIED.** Webdev Group, "established in 2001"; community forums; callback resend up to 10 times; no public status page found. [P13][P16][P6] | **UNVERIFIED.** Phone and email contacts; forums failed to load; callbacks not retried; no status page found. [S22][S19][S10] | **UNVERIFIED.** Email, community forum (not opened), JS-only status page; "strive for 99.9% uptime" with no guarantee. [Y2][Y11][Y12] | **UNVERIFIED.** Young product; failed-payment visibility is "Not yet" available. [L5][L4] | **UNVERIFIED.** "Dedicated merchant support" [Z1][Z3] | **UNVERIFIED.** Harare contact; claims multi-country operation. [C1] |

### 3.5 EcoCash Open API (EcoCash Holdings): single rail

| Criterion | Status | Evidence |
|---|---|---|
| EcoCash C2B collection | **UNVERIFIED** | A developer portal exists. Its config references a sandbox payment base URL, and the portal JavaScript references transaction, amount and refund routes, which suggests sandbox, payment and refund operations exist. **The documentation content itself could not be read** (client-rendered). [E1][E2] |
| Other wallets, cards, ZimSwitch | Not applicable | EcoCash-only rail |
| Fees, B2C/payouts, webhook security, settlement, licensing | **RPC** | EcoCash appears on the 2019 RBZ approved-platforms list as a platform [RBZ-1]. That concerns the wallet platform, not necessarily a merchant API arrangement. |

---

## 4. Not viable (on current evidence)

| Provider | Evidence | Status | Source |
|---|---|---|---|
| **Paystack** | "Our services are available only to businesses registered in Nigeria, Ghana, South Africa and Kenya" (plus private betas elsewhere). `paystack.com` itself returned HTTP 403; the support article loaded. | **NOT_SUPPORTED** for a Zimbabwe-registered FundZim | [X1] |
| **Flutterwave** | The country list on the opened page excludes Zimbabwe; the supported-countries developer page returned 404. | **UNVERIFIED (likely not supported)**; RPC if reconsidered | [X2] |
| **DPO Pay** (Network International) | News report (21 Oct 2024): DPO decided "to exit the Zimbabwean market and terminate its merchant contracts" (secondary source). | **UNVERIFIED (secondary)**: treat as not viable unless DPO confirms | [X3] |
| **ZimSwitch (direct)** | States it provides developer APIs (pages behind the links not opened). As the national switch it is normally reached through a gateway. | **UNVERIFIED**: not a near-term candidate | [X4] |

---

## 5. Summary matrix

Key: **V** = VERIFIED · **U** = UNVERIFIED · **X** = NOT_SUPPORTED · **?** = RPC · **NP** = not published.

| Need | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay | ContiPay |
|---|---|---|---|---|---|---|
| EcoCash | V | V | V | V | V (mktg) | U |
| OneMoney | V | X | V | V | U | U |
| InnBucks | V (USD) | V (USD) | ? | V | V (mktg) | U |
| O'Mari | V | V | ? | V | V (mktg) | U |
| ZimSwitch / bank cards | V | V (redirect) | X (docs) / U (homepage) | ? | V (mktg) | U |
| Visa/MC | V (Verified Merchant) | V (USD) | V (checkout) | V | V (mktg) | U |
| Foreign-issued cards | V | U | ? | V | V (SmileLink) | U |
| USD | V | V | V | V | ? | U |
| ZiG / ZWG | V (selection mechanism ?) | V (code quirk) | V (mobile money) | X | ? | U |
| Sandbox | V | V (USD subset) | V | V | U | U |
| Signed webhooks | V (SHA512, no timestamp) | **Unsigned**, no retry | V (HMAC-SHA256 + timestamp) | V (HMAC-SHA256) | U (reportedly unsigned) | U |
| Refund API | ? | ? (dashboard) | V (full only) | ? | U | ? |
| Payout API | ? | ? | V (EcoCash B2C, approval needed) | V (SmileCash only) | ? | ? |
| Beneficiary onboarding | ? (contract risk) | V (manual, merchant-grade) | X | V (API, wallet KYC) | ? | ? |
| Split / platform fee | ? | V (1 beneficiary/txn) | V (EcoCash only) | ? | ? | U |
| Reconciliation API | poll/trace only | check-payment only | V (partial) | V | ? | U |
| Published fees | V | V | V | NP | NP | NP |
| Settlement timeline | V | V | ? | V | ? | NP |
| RBZ licence evidence | U | ? | U (contradictory) | U | V (bank group) | U |
| Donations/crowdfunding explicitly OK | ? (risk flags) | V | ? | V | U | ? |

**Reading the matrix against Model A** ([operating-model-decision](../compliance/operating-model-decision.md)).
Model A needs: (a) funds held and disbursed by a licensed party, (b) a path to pay beneficiaries (split,
sub-merchant or payout API), and (c) authentic status signals. No single candidate has VERIFIED evidence for all
three, plus licensing evidence, plus contract fit:
- **Pesepay** has the split mechanism and stated trust-account settlement, but unsigned callbacks and no documented licensing evidence.
- **Payonify** has strong primitives but contradictory custody statements.
- **Linkwa** fits the use case but settles to SmileCash wallets through unnamed partners.
- **Paynow** has the broadest rails, but a third-party-beneficiary clause and no documented payout or split capability.

---

## 6. Source register

All sources were opened on **2026-10-08**. Keys match R3 research notes, and raw page captures were kept in the
Stage 1 working notes. "Summarised" means the page rendered client-side and was read through a summarising fetch,
so wording is a close paraphrase.

| Key | URL | Note |
|---|---|---|
| RBZ-1 | https://www.rbz.co.zw/documents/nps/Approved-Payment-System-Platforms.pdf | PDF metadata 28 Oct 2019 |
| RBZ-2 | https://www.rbz.co.zw/documents/nps/Payment-Systems-Infrastructure.pdf | 2019 |
| RBZ-3 | https://www.rbz.co.zw/index.php/financial-markets/national-payment-system | **Failed to load** (bot-captcha) |
| P1 | https://developers.paynow.co.zw/docs/paynow/initiate_transaction/ | |
| P2 | https://developers.paynow.co.zw/docs/paynow/express_checkout_transactions/ | |
| P3 | https://developers.paynow.co.zw/docs/paynow/initiate_mobile_transaction/ | |
| P4 | https://developers.paynow.co.zw/docs/paynow/generating_hash/ | |
| P5 | https://developers.paynow.co.zw/docs/paynow/validating_hash/ | |
| P6 | https://developers.paynow.co.zw/docs/paynow/status_update/ | |
| P7 | https://developers.paynow.co.zw/docs/paynow/polling_status/ | |
| P8 | https://developers.paynow.co.zw/docs/paynow/test_mode/ | |
| P9 | https://developers.paynow.co.zw/docs/paynow/integration_generation/ | |
| P10 | https://www.paynow.co.zw/Home/Fees | |
| P11 | https://www.paynow.co.zw/Home/MerchantTutorial | Merchant FAQ |
| P12 | https://www.paynow.co.zw/Home/Terms | |
| P13 | https://www.paynow.co.zw/Home/AboutUs | |
| P14 | https://www.paynow.co.zw/home/businesshome | |
| P15 | https://paynow.co.zw/blog/paynow-merchant-account/ | |
| P16 | https://paynow.co.zw/blog/onemoney-is-back-on-paynow-now-supporting-both-usd-and-zig-payments/ | |
| P17 | https://forums.paynow.co.zw/t/paynow-currency/1712 | 2020 forum post, apparent Paynow staff (secondary) |
| P18 | https://developers.paynow.co.zw/docs/paynow/notification_success_cancel_urls/ | |
| — | https://developers.paynow.co.zw/ | Developer hub (BillPay, TXT, CloudESD) |
| S1 | https://developers.pesepay.com/ | Docs show "Last updated" Sep 2026 |
| S2 | https://developers.pesepay.com/payment-methods/overview/ | |
| S3 | https://developers.pesepay.com/payment-methods/ecocash/ | |
| S4 | https://developers.pesepay.com/payment-methods/card-payments/ | |
| S5 | https://developers.pesepay.com/payment-methods/innbucks/ | |
| S6 | https://developers.pesepay.com/payment-methods/omari/ | |
| S7 | https://developers.pesepay.com/payments/split-payments/ | |
| S8 | https://developers.pesepay.com/webhooks/verifying-callbacks/ | |
| S9 | https://developers.pesepay.com/webhooks/result-callback/ | |
| S10 | https://developers.pesepay.com/webhooks/retries/ | |
| S11 | https://developers.pesepay.com/security/encryption/ | |
| S12 | https://developers.pesepay.com/api/initiate-transaction/ | |
| S13 | https://developers.pesepay.com/api/check-payment-status/ | |
| S14 | https://developers.pesepay.com/resources/transaction-statuses/ | |
| S15 | https://developers.pesepay.com/testing/sandbox-environment/ | |
| S16 | https://developers.pesepay.com/getting-started/onboarding/ | |
| S17 | https://pesepay.com/pricing | |
| S18 | https://pesepay.com/terms-and-conditions | |
| S19 | https://pesepay.com/faq | |
| S20 | https://pesepay.com/merchant-approvals | |
| S21 | https://pesepay.com/ | |
| S22 | https://pesepay.com/contact-us | |
| S23 | https://developers.pesepay.com/api/make-payment/ | |
| S24 | https://developers.pesepay.com/payments/how-payments-work/ | |
| Y1 | https://payonify.co.zw/ | |
| Y2 | https://docs.payonify.com/overview | Docs "Last modified on August 12, 2026" |
| Y3 | https://docs.payonify.com/webhooks | |
| Y4 | https://docs.payonify.com/relay | |
| Y5 | https://docs.payonify.com/payouts | |
| Y6 | https://docs.payonify.com/idempotence | |
| Y7 | https://docs.payonify.com/api/charges | |
| Y8 | https://docs.payonify.com/api/refunds | |
| Y9 | https://docs.payonify.com/checkouts | |
| Y10 | https://docs.payonify.com/get-started | |
| Y11 | https://payonify.co.zw/terms | |
| Y12 | https://status.payonify.com | Loaded; JS-rendered, no readable text |
| L1 | https://linkwa.co.zw/developer-apps/docs | Summarised |
| L2 | https://linkwa.co.zw/webhooks/docs | Summarised |
| L3 | https://linkwa.co.zw/terms | Verbatim |
| L4 | https://linkwa.co.zw/faqs | Verbatim |
| L5 | https://linkwa.co.zw/about | Verbatim |
| L6 | https://linkwa.co.zw/integrations/docs | Fetched; content not extracted |
| Z1 | https://www.zb.co.zw/banking/smilepay | |
| Z2 | https://www.zb.co.zw/holding/media/insights/reimagining-future-payments | |
| Z3 | https://www.zb.co.zw/banking/smilelink | |
| Z4 | https://packagist.org/packages/aaronkatema/laravel-smilepay | Third-party SDK (not official) |
| C1 | https://www.contipay.co.zw/ | |
| C2 | https://www.contipay.co.zw/termsandconditions.html | Developer docs (docs.contipay.co.zw) login-gated, not read |
| E1 | https://developers.ecocash.co.zw/ (and /docs) | Client-rendered; no readable doc text |
| E2 | https://developers.ecocash.co.zw/env-config.js | Portal configuration inspected |
| X1 | https://support.paystack.com/en/articles/2130562 | paystack.com returned 403 |
| X2 | https://flutterwave.com/us/ | developer supported-countries page returned 404 |
| X3 | https://www.theanchor.co.zw/c-trade-says-dpo-payment-option-no-longer-available-after-zim-exit/ | News report, 21 Oct 2024 (secondary) |
| X4 | https://zimswitch.co.zw/?p=252 | |
