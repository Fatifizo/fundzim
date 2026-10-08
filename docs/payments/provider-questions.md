# Provider Questions: PROVIDER_CONFIRMATION_REQUIRED Register

> **Stage 1 deliverable.** This is the authoritative register of `PCR-xxx` items: facts only a payment provider
> can confirm. **No provider has been contacted yet.** Answers must come in writing (contract, official
> documentation, or a signed letter on letterhead). A sales call is not enough. Record every answer in the
> evidence store ([../compliance/audit-evidence-model.md](../compliance/audit-evidence-model.md)) and update
> [provider-capability-matrix.md](provider-capability-matrix.md).

**How to use this register**
- Cross-cutting questions (PCR-001 to PCR-024) go to **every** shortlisted provider. Provider-specific questions
  (PCR-030 onward) close gaps found in that provider's public documentation.
- "Unblocks" names the FundZim design decision or document that waits on the answer. A blocked decision stays
  on its safe default (the capability is treated as absent) until the PCR is closed.
- Statuses: `OPEN` (not asked) → `ASKED` (date, contact) → `ANSWERED` (evidence ref) → `CLOSED` (design
  updated) or `ESCALATED` (contradiction or unsatisfactory answer).
- Answers that touch regulation (licensing, custody, AML duties, tax) are **inputs to legal review**, not
  conclusions. Cross-reference the `LR-xxx` items in [../compliance/open-legal-questions.md](../compliance/open-legal-questions.md).
- Other Stage 1 documents use topic placeholders such as "PCR: payout cancel". §4 maps each placeholder to an ID.

Source keys in brackets (e.g. [P12]) refer to the source register in
[provider-capability-matrix.md §6](provider-capability-matrix.md#6-source-register). All sources were accessed on 2026-10-08.

---

## 1. Cross-cutting questions (ask every shortlisted provider)

| ID | Question | Why it matters / evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-001 | Provide your RBZ licence or authorisation letter: category, number, date, conditions. Name your sponsoring or settlement bank. | No candidate gateway appears on the only RBZ list we could open (2019) [RBZ-1]; the RBZ site blocked retrieval of current lists [RBZ-3]. Licensing is self-described at best. | Licensing assessment; provider shortlist; Model A viability ([ADR-013](../adr/ADR-013-regulatory-operating-model.md)) | OPEN |
| PCR-002 | Do your terms permit a **donation-crowdfunding platform collecting on behalf of third-party beneficiaries** (individuals, families, NGOs, schools, hospitals)? What KYC/KYB do you require for (a) FundZim, (b) each beneficiary, (c) donors? | Several terms are written for "goods and services" [Y11]. Paynow has a third-party-beneficiary warranty [P12]. Only Pesepay and Linkwa mention donations [S19][L4]. | Operating model; [kyc-architecture](../compliance/kyc-architecture.md); [beneficiary-verification](../compliance/beneficiary-verification.md) | OPEN |
| PCR-003 | **Who holds collected funds before disbursement, in which account (trust/escrow, which bank), under which authorisation?** Can funds be held on FundZim's instruction and disbursed to verified beneficiaries without passing through a FundZim-controlled account? | This is the core requirement of Model A. If funds settle to FundZim's own bank account, the arrangement is Model B in substance ([operating-model-decision](../compliance/operating-model-decision.md)). | Operating model decision; [settlement-and-custody-model](../ledger/settlement-and-custody-model.md) | OPEN |
| PCR-004 | List every **currency × method** combination live in **production** today (USD/ZWG × EcoCash, OneMoney, InnBucks, O'Mari, ZimSwitch, Visa/MC, bank), with per-transaction minimums and maximums, and daily or monthly limits. | Public lists differ between docs, homepages and sandboxes (e.g. [S15] vs [S2]; [Y9] vs [Y1]). | Provider capability config; routing; `PROVIDER` limits ([payout-eligibility-and-controls](payout-eligibility-and-controls.md) limits model) | OPEN |
| PCR-005 | Are **foreign-issued** Visa/Mastercard cards accepted? Is 3-D Secure enforced? What fraud screening applies? Describe the **chargeback process, liability allocation and fees**. | International donors are a core segment. Only Paynow and Linkwa document foreign-card acceptance [P11][L4]. | Flow 2 ([funds-flow-architecture](funds-flow-architecture.md)); dispute handling | OPEN |
| PCR-006 | **Refunds:** is there an API? Are partial refunds supported? Which rails support refunds, and within what refund window per method? Are original processing fees refunded? Is refund creation idempotent? | Only Payonify documents a refund API (full refunds only) [Y8]. | [refund-and-reversal-flows](refund-and-reversal-flows.md); [refund-and-dispute-architecture](refund-and-dispute-architecture.md) | OPEN |
| PCR-007 | **Settlement:** schedule per rail, minimum thresholds, cut-offs, settlement fees, holds or reserves. **Reports:** do you provide settlement or reconciliation reports by API or SFTP? What format, fields, timing, and are fees itemised per transaction? | The "settled" ledger state needs an authoritative settlement source ([settlement-and-custody-model](../ledger/settlement-and-custody-model.md)). Most candidates expose only per-transaction polling or dashboards [P7][S13]. | Settled vs available states; reconciliation (Stage 17) | OPEN |
| PCR-008 | **Operations:** uptime/SLA commitments, maintenance windows, incident and status communication, escalation contacts, support hours. | No candidate publishes an SLA; no public status page was found for most [P13][S22][Y12]. | [incident-response-workflows](../compliance/incident-response-workflows.md) (provider outage); routing failover | OPEN |
| PCR-009 | **Idempotent payment creation:** is a duplicate merchant reference rejected? What happens if the same request is retried with the same reference after a timeout? Is there an idempotency-key header? | FundZim reuses `payment_id` as the provider reference on every retry ([transaction-lifecycle](transaction-lifecycle.md)). Only Payonify documents an idempotency key [Y6]; Paynow documents `merchanttrace` for token card payments [P7]. | Retry policy for `CreatePayment`; the UNKNOWN resolution path | OPEN |
| PCR-010 | **Status-query semantics:** is "not found" final, or can a transaction appear later? What do error codes mean? When does a pending payment expire? What is the expected completion window per rail? | FundZim never marks FAILED on a timeout; it resolves UNKNOWN only from authoritative status ([transaction-lifecycle](transaction-lifecycle.md)). Paynow warns "A trace error does not necessarily mean that the transaction was not found" [P7]. | UNKNOWN → FAILED/EXPIRED rules; polling horizon | OPEN |
| PCR-011 | **Collection reversals:** can a successful mobile-money or bank payment be reversed after success (by the operator, the provider or a donor complaint)? How are reversals notified (event type, timing)? | Pesepay lists a `REVERSED` status [S14]; Payonify has `reversal.*` events [Y3]. FundZim models these as `CHARGED_BACK` with `reversal_kind`. | Flow 5 ([refund-and-reversal-flows](refund-and-reversal-flows.md)) | OPEN |
| PCR-012 | **Disputes:** timelines, evidence requirements and format, dispute fees, and whether FundZim or the beneficiary bears losses. | Dispute deadlines are provider- and scheme-defined. | [refund-and-dispute-architecture](refund-and-dispute-architecture.md) | OPEN |
| PCR-013 | **Payouts:** is there a payout/disbursement API? To which destinations (mobile wallets, bank accounts)? Is there account-name validation before payout? Is payout creation idempotent? Is there a status API? What are the payout fees? | Payouts are documented only by Payonify (EcoCash B2C, approval needed) [Y5] and Linkwa (SmileCash only) [L1]. | [payout-lifecycle](payout-lifecycle.md); [payout-eligibility-and-controls](payout-eligibility-and-controls.md); Model A | OPEN |
| PCR-014 | **Payout cancellation or recall:** can a submitted payout be cancelled or recalled, and until when? | FundZim lets holds stop a payout only up to `APPROVED`. After `SUBMITTED`, only a provider-supported cancel works ([payout-lifecycle](payout-lifecycle.md)). | Payout states after `SUBMITTED` | OPEN |
| PCR-015 | **Payout returns:** how are bounced or returned payouts (wrong account, closed wallet) notified? Can a return be partial? How long after completion can a return occur? | This drives the `COMPLETED → REVERSED` transition and its ledger journal. | Payout lifecycle; reconciliation | OPEN |
| PCR-016 | **Fees and remittance:** is the per-transaction processing fee reported on the callback, the status response or only in settlement reports, and when? If a platform-fee share or split is used, how and when is FundZim's share remitted? Is it netted against anything? | Pesepay's callback carries `transactionServiceFee` and `merchantAmount` [S9], but "Nothing about the split appears on the callback" [S7]. | Fee journals (capture vs settlement); platform fee recognition ([settlement-and-custody-model](../ledger/settlement-and-custody-model.md)) | OPEN |
| PCR-017 | **Webhook security:** signing algorithm, timestamp or nonce for replay protection, secret rotation, source IP ranges, retry schedule and maximum, and which events are sent (including failed and non-terminal ones). | Evidence ranges from strong (Payonify [Y3]) to none (Pesepay: unsigned, no retry [S8][S10]). | Webhook verification per adapter ([../PAYMENTS.md](../PAYMENTS.md) webhook pipeline) | OPEN |
| PCR-018 | **ZiG handling:** which currency code(s) do you use (`ZWG`, `ZiG`, other), what is the minor-unit precision, and how is the currency selected per transaction or integration? | Pesepay uses both `ZiG` and `ZWG` depending on method [S2]. Paynow's API has no currency field [P1]. The minor unit is unverified in Stage 1 research. | [currency-and-fx-policy](currency-and-fx-policy.md); currency registry ([../MONEY.md](../MONEY.md)) | OPEN |
| PCR-019 | **Data protection:** where is transaction and personal data stored and processed? Who are your sub-processors? Will you sign a data processing agreement? What are your breach-notification commitments? | Needed for the cross-border and controller/processor analysis. | [data-protection-assessment](../compliance/data-protection-assessment.md); LR-011, LR-033 | OPEN |
| PCR-020 | **AML and fraud:** what CDD and sanctions screening do you perform on the merchant, sub-merchants/beneficiaries and payers? What fraud-reporting duties does FundZim owe you? What information can you share for FundZim's investigations? | The provider does not automatically take on FundZim's own obligations ([financial-responsibility-matrix](../compliance/financial-responsibility-matrix.md)). | [aml-risk-framework](../compliance/aml-risk-framework.md); [sanctions-screening](../compliance/sanctions-screening.md) | OPEN |
| PCR-021 | **Portal controls:** what user roles and permissions exist in your merchant portal, especially for refunds and payouts? Is there dual approval? Is an API or portal audit log available? | A portal user who can refund or pay out outside FundZim bypasses maker-checker ([operational-controls](../compliance/operational-controls.md)). | Operational controls; provider-side access reviews | OPEN |
| PCR-022 | **Exit:** what are the termination terms, what happens to funds held at termination, how can transaction history be exported, and what notice periods apply? | Portability and continuity of donor funds. | Provider contract review; multi-provider strategy | OPEN |
| PCR-023 | **Card data scope:** confirm that card data is captured only on your hosted page or fields and never reaches FundZim. Provide your PCI DSS attestation evidence and your 3-D Secure configuration. | FundZim's stance is never to touch raw card data ([../PAYMENTS.md](../PAYMENTS.md) §16). We request evidence and make no claim on the provider's behalf. | Security review; card flow design | OPEN |
| PCR-024 | **Taxes collected by you:** do you deduct the intermediated money transfer tax or other levies on collection and/or disbursement legs? Do your fee invoices carry VAT? How are these reported? | These affect amounts received and ledger lines. Applicability is a legal or tax question (see the regulatory register). | Fee and tax journal lines; [currency-and-fx-policy](currency-and-fx-policy.md) | OPEN |
| PCR-025 | **Authorisation vs capture:** for card payments, do you separate authorisation from capture (delayed capture), or is every successful card payment captured immediately? Is the state exposed via callback and status API? | Determines whether FundZim needs the `AUTHORISED` payment state for a provider. | [transaction-lifecycle](transaction-lifecycle.md); [settlement-and-custody-model](../ledger/settlement-and-custody-model.md) S2 | OPEN |
| PCR-026 | **Payout status semantics:** expected completion window per payout rail; whether a payout status of "not found" is final; which payout statuses are terminal; how long after submission a payout can still change state. | FundZim never resubmits a payout in `UNKNOWN`; it needs a documented horizon for status polling. | [payout-lifecycle](payout-lifecycle.md); [payout-eligibility-and-controls](payout-eligibility-and-controls.md) | OPEN |
| PCR-027 | **Freeze / block on instruction:** can you block or hold collected funds attributed to a specific campaign or payee, or stop collection into it, on FundZim's instruction, and how quickly (e.g. for a sanctions match or fraud freeze)? | Under Model A FundZim cannot freeze funds itself (LR-066). | [sanctions-screening](../compliance/sanctions-screening.md); [compliance-case-management](../compliance/compliance-case-management.md) | OPEN |
| PCR-028 | **Risk signals available to FundZim:** for each payment, do you report card issuer country, BIN, 3-D Secure result, device/IP data, or mobile-money SIM-swap / number-porting signals? Via callback, status API or reports? | Transaction-monitoring rules depend on which signals exist. | [transaction-monitoring](../compliance/transaction-monitoring.md) | OPEN |

---

## 2. Provider-specific questions

### 2.1 Paynow (Softwarehouse (Private) Limited)

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-030 | How is USD vs ZWG chosen for an integration? Is one integration ID needed per currency? | The API has no currency field [P1]; a 2020 forum post said the plugin "does not currently support multi-currency" [P17]. | Currency routing; adapter design | OPEN |
| PCR-031 | Is there any refund API, or any payout/B2C API to EcoCash or bank accounts? | A `Refunded` status exists but no endpoint is documented [P6]; the developer hub lists no payout API. | Refunds; payouts; Model A | OPEN |
| PCR-032 | Does the warranty "not acting on behalf of an undisclosed principal or a third party beneficiary" prohibit crowdfunding? Is a marketplace or aggregator agreement available? | Contract risk [P12]. | Contract fit; shortlisting | OPEN |
| PCR-033 | Can Buysafe escrow be disabled for a donation platform? Is Verified Merchant status needed for daily settlement and Visa/MC? | Escrow is built around "delivery of goods"; non-verified merchants settle weekly [P11]. | Settlement timing; card acceptance | OPEN |
| PCR-034 | Is any sub-merchant, split-settlement or "settle to multiple bank accounts" capability available? | Only customer fee-splitting is documented [P10][P11]. | Model A / Model C viability | OPEN |
| PCR-035 | Does test mode support InnBucks and O'Mari express checkout? | Test numbers are documented for mobile; coverage per method is unclear [P8]. | Stage 8/9 test plan | OPEN |
| PCR-036 | Is replay protection (timestamp/nonce) available for status-update callbacks? What are the callback source IPs? | The SHA512 hash has no timestamp [P4][P6]. | Webhook verification (adapter must dedupe and poll-confirm) | OPEN |

### 2.2 Pesepay (operated by Code Virtus)

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-040 | Is OneMoney planned? When will the sandbox support ZWG, InnBucks, O'Mari and ZimSwitch? | OneMoney is missing from the live list [S2]; the sandbox is USD-only with limited methods [S15]. | Rail coverage; test plan | OPEN |
| PCR-041 | Is a refund API available or planned? Is there a payout/disbursement API? | The API reference lists no refund or payout endpoints [S1][S16]. | Refunds; payouts | OPEN |
| PCR-042 | Split payments: can beneficiary arrangements be created by API? Can a beneficiary be an **individual** rather than a registered business? Is more than one beneficiary per transaction possible? Can terms change without revoke and re-invite? | Beneficiaries must be approved Pesepay merchants invited through the dashboard; one per transaction; terms fixed [S7][S20]. | Model A/C via split; beneficiary onboarding scale | OPEN |
| PCR-043 | Do you plan signed callbacks and retries? What are the callback source IPs? | "Pesepay does not currently sign callback payloads"; "There is no retry" [S8][S10]. | Webhook trust (until then, poll-confirm only) | OPEN |
| PCR-044 | Are foreign-issued cards accepted? | Not stated in the card docs [S4]. | International donor flow | OPEN |
| PCR-045 | What is the minimum settlement threshold in the Merchant Agreement? Please share the Merchant Agreement. | Settlement is T+2 "once the minimum amount… is reached" [S16]; the agreement is not public. | Settlement timing; contract review | OPEN |
| PCR-046 | Is there an idempotency mechanism for initiate and make-payment, for example rejecting a duplicate `merchantReference`? | No idempotency key is documented [S12][S23]. | Retry safety | OPEN |
| PCR-047 | State Code Virtus's RBZ authorisation status and name the bank holding the "Pesepay trust account". | Terms describe "a Software Development Company" [S18]; pricing mentions a trust account held by the bank [S17]. | Licensing; custody (Model A) | OPEN |
| PCR-048 | Confirm the `ZiG` vs `ZWG` code rules per method. What happens to a split where the fixed share is ≥ the amount (it "fails silently after the customer is charged")? How is that failure detected and resolved? | Documented quirks [S2][S7]. | Currency mapping; split failure handling | OPEN |

### 2.3 Payonify (payonify.co.zw)

> **Identity caveat:** the project owner must confirm this is the intended company
> ([capability matrix §1.1](provider-capability-matrix.md#11-identity-caveat)) before any outreach.

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-050 | Your Terms say "We do not hold, manage, or deposit funds", yet Relay and Payouts describe balances held by Payonify. Who holds collected funds, under which authorisation, and in which bank trust account? | Contradiction [Y11][Y4]. | Custody; licensing; Model A | OPEN |
| PCR-051 | What is the settlement timeline and the duration of the "short hold"? | Not documented [Y4]. | Settled/available states | OPEN |
| PCR-052 | Are InnBucks, O'Mari and ZimSwitch supported? The homepage claims ZimSwitch; the docs say "Coming Soon". | Contradiction [Y1][Y9]. | Rail coverage | OPEN |
| PCR-053 | Are foreign-issued cards accepted, and in which currencies? | Not documented. | International flow | OPEN |
| PCR-054 | Relay and payouts are EcoCash-only. When will OneMoney and bank payouts arrive? What is the B2C approval process? | [Y4][Y5]. | Payout rails | OPEN |
| PCR-055 | Are partial refunds supported? | The refund request has no amount field [Y8]. | Partial refunds | OPEN |
| PCR-056 | Is a donation-crowdfunding use case acceptable? Can sole traders, individuals or NGOs be Relay beneficiaries? | Terms cover "goods and services" [Y11]; Relay excludes sole traders [Y4]. | Contract fit; beneficiary types | OPEN |

### 2.4 Linkwa (operated by Pandawoga Innovate PBC)

> **Identity caveat:** confirm this is the intended company before outreach.

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-060 | When will ZiG/ZWG be available? Provide current plan prices and transaction fees in writing. | USD only today; fees are "in-app" [L4][L3]. | Currency; cost model | OPEN |
| PCR-061 | Who are the "licensed payment and banking partners" that perform processing and settlement? | Partners are unnamed [L3][L4]. | Licensing; custody | OPEN |
| PCR-062 | Can FundZim or beneficiaries settle to a bank account rather than a SmileCash wallet? When will payouts to EcoCash, OneMoney, InnBucks, O'Mari and banks arrive? | Payouts go to SmileCash only [L1][L4]. | Payout rails; Model A | OPEN |
| PCR-063 | Is there a refund API? | None documented; refunds are seller-led [L3]. | Refunds | OPEN |
| PCR-064 | Is there an idempotency mechanism? What is the maximum webhook retry count? Do webhooks cover failed payments? | No idempotency documented; no stated retry maximum [L1][L2]. | Retry safety; webhook pipeline | OPEN |
| PCR-065 | Is ZimSwitch accepted? | Not listed [L4]. | Rail coverage | OPEN |

### 2.5 Smile&Pay (ZB Financial Holdings)

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-070 | Provide the official API documentation and sandbox access. | No public developer docs found [Z1][Z4]. | Any evaluation | OPEN |
| PCR-071 | Are callbacks signed? | A third-party SDK reports they are unsigned [Z4]. | Webhook trust | OPEN |
| PCR-072 | Which USD and ZWG methods are supported? Are refunds, payouts and split settlement available? | Marketing-level only [Z1][Z2]. | Capability config | OPEN |
| PCR-073 | What are the fees and settlement timelines? Is a crowdfunding platform or aggregator arrangement acceptable? Which ZB authorisation covers the gateway product? | Not published [Z1]. | Cost; contract fit; licensing | OPEN |

### 2.6 ContiPay

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-080 | Provide access to the developer documentation (login-gated) or a PDF. | Docs are behind a login [C1]. | Any evaluation | OPEN |
| PCR-081 | Which methods and currencies are supported? What is the webhook signature scheme? Do you offer refund, payout or split APIs? | Marketing-level only [C1]. | Capability config | OPEN |
| PCR-082 | Provide licence details behind "RBZ compliant", plus fee and settlement terms. | Marketing claim with no licence cited [C1][C2]. | Licensing; cost | OPEN |

### 2.7 EcoCash Open API (EcoCash Holdings)

| ID | Question | Evidence gap | Unblocks | Status |
|---|---|---|---|---|
| PCR-090 | Provide the Open API documentation for C2B collection, refund, B2C/payout and transaction lookup, including how callbacks are authenticated. | The portal is client-rendered; the docs could not be read [E1][E2]. | Direct EcoCash rail option | OPEN |
| PCR-091 | What are the merchant onboarding requirements and fees? Is ZWG supported? | Not found. | Direct rail cost and currency | OPEN |

---

## 3. Questions for the project owner (not providers)

| Item | Question |
|---|---|
| Identity of "Payonify" and "Linkwa" | Are `payonify.co.zw` and `linkwa.co.zw` (Pandawoga Innovate PBC) the companies intended? They did not appear in general web search. |
| Introductions | Does FundZim have existing relationships or introductions with any candidate that should set the outreach order? |

---

## 4. Placeholder → PCR mapping

Other Stage 1 documents refer to provider questions by topic. Use this table to resolve them.

| Topic placeholder used in other docs | PCR |
|---|---|
| licensing / RBZ authorisation | PCR-001 (+ PCR-047, PCR-050, PCR-061, PCR-073, PCR-082) |
| crowdfunding permitted / third-party beneficiaries | PCR-002 (+ PCR-032, PCR-056) |
| fund holding / trust account / custody | PCR-003 (+ PCR-047, PCR-050) |
| currency × method availability; limits | PCR-004 |
| foreign cards; chargeback process and liability | PCR-005 (+ PCR-044, PCR-053) |
| refund capability per rail; refund support per method; refund window per method; partial refunds; idempotent refunds | PCR-006 (+ PCR-031, PCR-041, PCR-055, PCR-063) |
| settlement timelines; settlement report formats | PCR-007 (+ PCR-045, PCR-051) |
| SLA and incident communication | PCR-008 |
| idempotent payment creation; error code semantics and idempotent creation | PCR-009 (+ PCR-046, PCR-064) |
| finality of "not found"; payment expiry semantics; expected completion and expiry | PCR-010 |
| collection reversal events | PCR-011 |
| dispute timelines; dispute fees | PCR-012 |
| payout API; idempotent payouts; payout fees | PCR-013 (+ PCR-031, PCR-041, PCR-054, PCR-062) |
| payout cancel / cancellation / recall | PCR-014 |
| payout return events; partial payout returns | PCR-015 |
| fee reporting timing; fee remittance netting | PCR-016 |
| webhook signing / replay / retries | PCR-017 (+ PCR-036, PCR-043, PCR-064, PCR-071) |
| ZiG minor units / currency code / currency selection | PCR-018 (+ PCR-030, PCR-048) |
| data location; sub-processors; DPA | PCR-019 |
| fraud reporting duties to provider; provider AML screening | PCR-020 |
| provider portal user roles and payout permissions; API audit log availability | PCR-021 |
| exit / termination | PCR-022 |
| PCI scope | PCR-023 |
| taxes deducted by provider | PCR-024 |
| auth/capture separation | PCR-025 |
| payout completion window; payout "not found" finality | PCR-026 |
| freeze or block capability; provider freeze capability | PCR-027 |
| card issuer country / BIN / device / SIM-swap signals | PCR-028 |
| reserve split | PCR-007 (+ split questions PCR-034, PCR-042) |
| name enquiry / account-name validation | PCR-013 |
| dispute evidence submission method and format | PCR-012 |
| statement format | PCR-007 |
