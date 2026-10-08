# Payment Provider Comparison and Recommendation

> **Stage 1 deliverable. Provider selection status: PENDING.** No provider is selected. The leading
> candidates below are an **order for due diligence**, not an endorsement and not a decision. Scores rest on
> public documentation opened on 2026-10-08 ([provider-capability-matrix.md](provider-capability-matrix.md)).
> They will change once providers answer [provider-questions.md](provider-questions.md) and pass
> [provider-due-diligence-checklist.md](provider-due-diligence-checklist.md).
> The final selection is made in Stage 9 under an ADR, after legal review of the operating model
> ([ADR-013](../adr/ADR-013-regulatory-operating-model.md)).

---

## 1. What FundZim needs from a provider

These requirements come from the operating model ([../compliance/operating-model-decision.md](../compliance/operating-model-decision.md)),
the funds flows ([funds-flow-architecture.md](funds-flow-architecture.md)) and the Stage 0 provider abstraction
([../PAYMENTS.md](../PAYMENTS.md) §3–§5).

| Need | Why | Design that depends on it |
|---|---|---|
| **Licensed handling of funds** | Under Model A, a licensed party collects, holds and disburses. FundZim does not. | [ADR-013](../adr/ADR-013-regulatory-operating-model.md), [settlement-and-custody-model](../ledger/settlement-and-custody-model.md) |
| **Contract that permits donation crowdfunding for third-party beneficiaries** | Many merchant terms assume the merchant sells its own goods. | Campaign model; beneficiary model ([beneficiary-verification](../compliance/beneficiary-verification.md)) |
| **A path to pay beneficiaries without FundZim custody** | One of: provider-held funds disbursed on instruction, sub-merchant/split settlement, or a payout API. | [payout-lifecycle](payout-lifecycle.md), [payout-eligibility-and-controls](payout-eligibility-and-controls.md) |
| **Authentic status signals** | A browser redirect is never confirmation. FundZim needs a signed webhook **or** an authenticated status API. | [transaction-lifecycle](transaction-lifecycle.md), webhook pipeline ([../PAYMENTS.md](../PAYMENTS.md)) |
| **Local rail coverage** | EcoCash, OneMoney, InnBucks, O'Mari, ZimSwitch/bank. | Routing and capabilities |
| **International cards** | Diaspora donors. | Flow 2 |
| **USD and ZiG, never implicitly converted** | Currency isolation. | [currency-and-fx-policy](currency-and-fx-policy.md) |
| **Refunds, reversals, disputes** | Donor protection. | [refund-and-reversal-flows](refund-and-reversal-flows.md) |
| **Settlement and reconciliation data** | The ledger's *settled* state needs an authoritative source. | Settlement model; Stage 17 |
| **Sandbox and documentation** | Stage 8/9 testing with no real money. | Test strategy ([../TESTING.md](../TESTING.md)) |

---

## 2. Method

### 2.1 Gates (must pass before any selection; not scored)

A provider can be **selected** only when all four gates are passed with **written evidence**:

| Gate | Pass condition | Evidence source |
|---|---|---|
| G-LIC | Licence or authorisation evidence, reviewed by counsel, covering the services FundZim would use | PCR-001; LR register |
| G-CON | The contract permits donation crowdfunding on behalf of third-party beneficiaries | PCR-002; contract review |
| G-CUS | The funds-holding and settlement arrangement is compatible with Model A (or Model C), so funds do not settle into a FundZim-controlled account | PCR-003; counsel |
| G-AUTH | Authentic status: a signed webhook with replay protection **or** an authenticated server-to-server status API, plus a defined finality rule | PCR-010, PCR-017 |

**On current public evidence, no candidate passes any of the four gates in full.** G-AUTH comes closest:
Payonify has strong signing [Y3], and every candidate with docs has a status API. But G-AUTH also needs the
finality semantics of PCR-010, which nobody documents. This alone means no selection can be made in Stage 1.

### 2.2 Weighted criteria

Each criterion is scored 0–4: 0 = no verified evidence, 1 = weak or partial, 2 = partial, 3 = good, 4 = strong.
Weighted points are weight × score / 4. The maximum total is 100.

| Code | Criterion (master-prompt criteria #) | Weight | Rationale |
|---|---|---|---|
| W1 | Regulatory fit and licensing evidence (#21) | 15 | Model A only works if the party holding funds is authorised. |
| W2 | Contract fit for crowdfunding (#22) | 12 | An unsuitable contract can terminate the business overnight. |
| W3 | Model A/C mechanics: funds held by provider, split or sub-merchant (#16, #17) | 15 | This decides whether FundZim can avoid custody. |
| W4 | Webhook and status authenticity (#13) | 10 | Payment confirmation correctness. |
| W5 | Payout API (#15) | 10 | Beneficiary disbursement without FundZim custody. |
| W6 | Refund API (#14) | 6 | Donor protection; a manual fallback is possible. |
| W7 | Local rail coverage (#1–#6) | 10 | Zimbabwe-first. |
| W8 | International card acceptance (#7, #10) | 5 | Diaspora donors. |
| W9 | Currency: USD and ZiG (#8, #9) | 5 | Both currencies are in scope. Can launch USD-only (PD-11). |
| W10 | Documentation quality and sandbox (#11, #12) | 5 | Delivery risk. |
| W11 | Reconciliation and settlement reporting (#18, #20) | 5 | Settled state and Stage 17. |
| W12 | Fee and settlement transparency (#19, #20) | 2 | Cost matters, but correctness and legality matter more. |
| | **Total** | **100** | |

**Scoring rules**
- Only **VERIFIED** evidence scores (statuses as in the [capability matrix](provider-capability-matrix.md)).
  "VERIFIED (marketing)" scores at most 2.
- UNVERIFIED and RPC score **0** in the *verified-only* column.
- The *optimistic* column rescores every criterion that is entirely UNVERIFIED or RPC at 2/4. That is what a
  moderately favourable provider answer would earn. The gap between the two columns shows how much each
  ranking depends on unknowns.
- Criterion #23 (support and reliability) is unverified for every candidate, so it is excluded from scoring.
  It is assessed in due diligence (PCR-008).

---

## 3. Scores

### 3.1 Per-criterion scores (verified-only; optimistic rescoring in brackets where it applies)

| Code | Paynow | Pesepay | Payonify | Linkwa | Smile&Pay (ZB) | ContiPay |
|---|---|---|---|---|---|---|
| W1 Licensing (15) | 0 [2]: self-described PSP only [P13] | 0 [2]: "software development company"; a trust account is mentioned but no licence [S18][S17] | 0 [2]: "technical facilitator only", which contradicts its own balances [Y11][Y4] | 0 [2]: unnamed "licensed partners" [L3] | **2**: regulated bank group; product authorisation unconfirmed [Z1] | 0 [2]: "RBZ compliant" marketing only [C1] |
| W2 Contract fit (12) | 0 [2]: third-party-beneficiary warranty; escrow designed for goods [P12][P11] | **3**: "donation campaigns"; donations not prohibited; agreement not public [S19][S18] | 0 [2]: "goods and services" [Y11] | **3**: "Donations", "Group Contributions" [L4] | 0 [2]: SmileLink mentions churches and families (unverified for the gateway) [Z3] | 0 [2] |
| W3 Model A/C mechanics (15) | 0 [2]: settles to the merchant's bank; no split or sub-merchant documented [P15][P10] | **2**: native split, but one beneficiary per transaction, the beneficiary must be an approved merchant, set up manually [S7][S20] | **2**: Relay holds balances and transfers with an application fee, EcoCash only [Y4] | **2**: platform balance → wallet payouts; beneficiary KYC by API [L1] | 0 [2] | 0 [2] |
| W4 Status authenticity (10) | **3**: SHA512 hash with key, 10 resends, poll URL; no timestamp [P4][P6] | **1**: unsigned, no retry; polling required [S8][S10] | **4**: HMAC-SHA256 + timestamp, 10 retries, event IDs [Y3] | **3**: HMAC-SHA256 (summarised page) [L2] | 0 [1]: reportedly unsigned (third-party SDK) [Z4] | 0 [2] |
| W5 Payout API (10) | 0 [2] | 0 [2] | **2**: EcoCash B2C, needs approval [Y5] | **1**: SmileCash wallets only [L1] | 0 [2] | 0 [2] |
| W6 Refund API (6) | 0 [2] | 0 [2]: dashboard only [S16] | **3**: full refunds only [Y8] | 0 [2] | 0 [2] | 0 [2] |
| W7 Local rails (10) | **4**: all six [P2] | **3**: no OneMoney [S2] | **1**: EcoCash and OneMoney only verified [Y7][Y9] | **3**: no ZimSwitch documented [L4] | **2**: marketing level [Z1][Z2] | 0 [2] |
| W8 International cards (5) | **4**: foreign cards stated [P11] | 0 [2]: not stated [S4] | 0 [2] | **4**: stated [L4] | **1**: SmileLink only [Z3] | 0 [2] |
| W9 USD + ZiG (5) | **3**: ZiG confirmed; selection mechanism unknown [P15][P1] | **3**: both, with a code quirk [S2] | **4**: both [Y7] | **2**: USD only [L4] | 0 [2] | 0 [2] |
| W10 Docs + sandbox (5) | **3**: moderate docs; sandbox [P8] | **3**: high docs; limited sandbox [S1][S15] | **4** [Y2][Y10] | **2**: basic docs; sandbox [L1][L3] | 0 [2] | 0 [2] |
| W11 Reconciliation (5) | **1**: poll/trace only [P7] | **1**: check-payment; dashboard reports [S13][S19] | **2**: lists and balance; no settlement report [Y4] | **3**: balance and statement API [L1] | 0 [2] | 0 [2] |
| W12 Fees/settlement transparency (2) | **4** [P10][P11] | **4** [S17][S16] | **2**: fees published; settlement unknown [Y11][Y4] | **2**: settlement published; fees in-app [L3] | 0 [2] | 0 [2] |

### 3.2 Totals (out of 100)

| Provider | Verified-only | Optimistic | Swing | Gates passed (G-LIC / G-CON / G-CUS / G-AUTH) |
|---|---|---|---|---|
| Linkwa | **48.75** | 59.25 | +10.5 | ✗ / partial / ✗ / partial |
| Payonify | **43.00** | 59.00 | +16.0 | ✗ / ✗ / ✗ (contradiction) / partial |
| Pesepay | **37.25** | 55.25 | +18.0 | ✗ / partial / partial (stated trust account) / ✗ (unsigned) |
| Paynow | **33.25** | 62.25 | +29.0 | ✗ / ✗ (risk flag) / ✗ / partial |
| Smile&Pay (ZB) | **13.75** | 46.25 | +32.5 | partial (bank group) / ✗ / ✗ / ✗ |
| ContiPay | **0.00** | 50.00 | +50.0 | ✗ / ✗ / ✗ / ✗ |

Calculation, for reproducibility: Paynow verified-only = W4 7.5 + W7 10 + W8 5 + W9 3.75 + W10 3.75 + W11 1.25 +
W12 2 = 33.25. The other rows are computed the same way from the per-criterion table.

### 3.3 Sensitivity: what the numbers mean

1. **The ranking is not robust.** With moderately favourable answers, the order changes completely: Paynow goes
   from 4th to 1st, and ContiPay goes from 6th to 5th. Swings of 10.5 to 50 points are larger than the gaps
   between candidates. A selection made now would rest on unknowns, not evidence.
2. **The heaviest weights (W1, W2, W3: 42 points) are almost entirely unverified** for every candidate. The
   verified-only totals mostly reflect *documentation quality* (W4–W12), not suitability.
3. **Disqualifying answers would override scores.** Examples:
   - a provider that will not permit crowdfunding (fails G-CON);
   - a provider that can only settle into FundZim's own account (fails G-CUS, which turns the arrangement into Model B);
   - a provider with no authorisation evidence (fails G-LIC).
4. **Marketing claims were deliberately not scored.** Examples: "RBZ compliant" [C1], "get paid by anyone,
   anywhere" [S21], Payonify's homepage ZimSwitch claim [Y1].

---

## 4. Integration design notes per provider

These map each provider's documented behaviour onto the FundZim provider interface and capability model
([../PAYMENTS.md](../PAYMENTS.md) §3–§4). They are inputs to Stage 8/9 adapter design, not commitments.

### 4.1 Paynow
- **Capabilities:**
  - Collection methods: `ecocash`, `onemoney`, `innbucks`, `omari`, `zimswitch`, `vmc` (express) or a hosted redirect [P2].
  - `supports_refund=false`, `supports_payout=false` until PCR-031.
  - `supports_split=false` until PCR-034.
- **Currency:** the API has no currency field [P1]. Assume **one integration (ID + key) per currency** until
  PCR-030 says otherwise, and route by currency to the matching integration. Never infer the currency from the amount.
- **Webhook verification:** recompute SHA512 over the field values in message order plus the integration key,
  then compare in constant time [P4][P5]. There is no timestamp, so **replay protection must be FundZim-side**:
  - dedupe on (`paynowreference`, `status`);
  - apply precedence rules;
  - **confirm every financially significant status by polling `pollurl`**, as Paynow itself recommends [P6].
- **Idempotency:**
  - `reference` = FundZim `payment_id`.
  - Use `merchanttrace` (≤32 characters) set from `payment_id` for traceable recovery via `/interface/trace` [P7].
  - Treat a trace `Error` as **UNKNOWN, not "not found"** [P7].
- **Status mapping:** Paid → SUCCEEDED; Cancelled → CANCELLED/FAILED (per PCR-010); Disputed → DISPUTED;
  Refunded → REFUNDED; Created/Sent → PENDING; Awaiting Delivery/Delivered = escrow states. Escrow must be
  disabled or understood before launch (PCR-033).
- **O'Mari:** two-step OTP via `remoteotpurl`, cancelled after 5 wrong attempts → REQUIRES_ACTION [P2].
- **Secrets:** a key per integration, regenerated at go-live [P9]. Store in the secret manager, never in config files.

### 4.2 Pesepay
- **Capabilities:**
  - Methods per currency code [S2], with an explicit mapping table: `ZiG` for EcoCash and PayGo, `ZWG` for
    ZimSwitch and Omari. The internal currency is always `ZWG`.
  - `supports_refund=false` and `supports_payout=false` until PCR-041.
  - `supports_split=true` with `max_beneficiaries_per_txn=1`.
- **Webhook verification: callbacks are not authentic** ("does not currently sign callback payloads" [S8]).
  The adapter must:
  - treat a callback only as a *hint* to poll;
  - confirm via `check-payment` (encrypted request and response) before any state change [S13];
  - because there is "no retry" [S10], run scheduled polling for every non-terminal payment;
  - never assume that missing callbacks mean failure.
- **Encryption:** AES-256-CBC with the IV derived from the key [S11] is the provider's scheme. Implement it
  exactly, but do not treat it as authentication of callbacks (callbacks are plain JSON [S9]).
- **Idempotency:** none is documented (PCR-046). Set `merchantReference` = `payment_id`. **Never re-initiate**
  after an ambiguous response; resolve by polling. Retrying creation is allowed only if PCR-046 confirms
  duplicate rejection.
- **Split:**
  - Every transaction carries `beneficiaryMerchantEmail` [S7].
  - The split is invisible on the callback, so reconcile split outcomes from reports (PCR-007, PCR-016).
  - **Never configure a FIXED share ≥ the minimum payment amount**: the split "fails silently after the customer
    is charged" [S7]. Enforce this as a validation rule (PCR-048).
- **EcoCash amounts above the ceiling** are collected in parts and reversed if any leg fails [S3]. Model the
  payment as one intent; treat partial states as PENDING until terminal.

### 4.3 Payonify
- **Capabilities:**
  - Charges (EcoCash, OneMoney), Checkout (cards).
  - `supports_refund=full_only` [Y8].
  - `supports_payout=ecocash_b2c` (after approval) [Y5].
  - Relay transfers with `application_fee_amount` (EcoCash only) [Y4].
- **Webhook verification:**
  - HMAC-SHA256 over `{t}.{raw_body}`, 5-minute tolerance, dedupe on event `id` [Y3]. This matches FundZim's
    pipeline directly.
  - Still poll-confirm before posting ledger journals for payouts (UNKNOWN resolution).
- **Idempotency:** `Idempotency-Key` header scoped per API key [Y6]. Set it to `payment_id` / `refund_id` /
  `payout_id` on every retry.
- **Amounts** are already integer minor units (`100` = 1.00) [Y7]. Map 1:1 to `amount_minor`.
- **Custody contradiction** (PCR-050): Relay balances must not be modelled as anything until it is resolved.
  This blocks Model A use.

### 4.4 Linkwa
- **Capabilities:**
  - Hosted payment links and QR [L1].
  - `supports_payout=smilecash_wallet` [L1].
  - Beneficiary user and wallet creation through an API that collects ID data [L1]. **That is KYC data flowing
    to a third party**, so it falls under the data-protection review (PCR-019, LR-011) and
    [identity-data-protection](../security/identity-data-protection.md).
- **Webhook verification:** HMAC-SHA256 of the raw body with a constant-time compare [L2]. No timestamp is
  documented, so dedupe on `reference` and confirm by status polling [L1].
- **Idempotency:** none documented (PCR-064). One payment link per FundZim `payment_id`.
- **Currency:** USD only today [L4]. ZWG must stay disabled on this adapter.

### 4.5 Smile&Pay, ContiPay, EcoCash Open API
No usable public technical documentation was found (PCR-070, PCR-080, PCR-090), so adapter design is not
possible. Smile&Pay callbacks are *reported* as unsigned by a third-party SDK [Z4]. If confirmed, the
Pesepay-style poll-confirm pattern applies.

---

## 5. Recommendation

### 5.1 Selection status

| Slot | Status | Reason |
|---|---|---|
| **PRIMARY CANDIDATE** | **PENDING: not selected** | No candidate passes any gate (§2.1). Licensing (G-LIC), contract fit (G-CON) and custody arrangement (G-CUS) are unverified for all, and those are the criteria that decide whether Model A is lawful and viable. |
| **BACKUP CANDIDATE** | **PENDING: not selected** | The same reasons. A backup is meaningless before a primary exists. |
| **PILOT TEST CANDIDATE** | **PENDING: not selected** | A *pilot* moves real money (Gate C, [../ROADMAP.md](../ROADMAP.md)), so it needs everything a primary needs. *Sandbox* experiments need no selection and may run in Stage 8/9 against any provider's public test environment, with no real credentials committed. |

Why no selection is made, plainly:
1. **No current, official evidence of RBZ authorisation** was found for any gateway. The only RBZ list we could
   open dates from 2019 and names none of them [RBZ-1]. The RBZ site blocked retrieval of current lists [RBZ-3].
2. **No candidate documents a custody arrangement that clearly keeps funds out of FundZim's control while still
   letting FundZim direct disbursement to beneficiaries.** This is the defining requirement of Model A
   ([ADR-013](../adr/ADR-013-regulatory-operating-model.md)). Paynow settles to the merchant's own bank account
   [P15]. Pesepay's split pays approved merchants [S7]. Payonify's terms contradict its own docs [Y11][Y4].
   Linkwa relies on unnamed partners [L3].
3. **Contract suitability is unconfirmed.** For Paynow there is a clause that may conflict with collecting for
   third-party beneficiaries [P12].
4. The weighted scores swing by **10.5 to 50 points** depending on unanswered questions (§3.3).

Selecting a provider on this evidence would select on marketing and documentation quality, which the Stage 1
brief explicitly forbids.

### 5.2 Leading candidates for due diligence

This is an outreach and assessment order. **It is not a ranking of suitability.**

| Order | Provider | Why it leads | What would disqualify it | First PCRs to send |
|---|---|---|---|---|
| 1 | **Pesepay** | The only native split mechanism found. A stated bank-held trust account (pricing page). Explicit "donation campaigns" fit. The best docs. | Split restricted to registered businesses (no individuals); no authorisation evidence; refusal of crowdfunding. | PCR-001, 002, 003, 041, 042, 043, 046, 047 |
| 2 | **Paynow** | The broadest verified rails, including all four target wallets, ZimSwitch and foreign cards. Long-established. Signed callbacks. | The third-party-beneficiary warranty applies to FundZim; settlement only to the merchant's own account with no split or payout (which forces Model B). | PCR-001, 002, 003, 030, 031, 032, 033, 034 |
| 3 | **Payonify** | The strongest developer primitives: signed timestamped webhooks, idempotency keys, refund and payout APIs, platform fee on transfers. | The custody contradiction resolves badly; no authorisation evidence; rails stay EcoCash-only. | PCR-050, 001, 002, 052, 054, 056 |
| 4 | **Linkwa** | The closest product fit (donations, group contributions, foreign cards, beneficiary onboarding by API). | Licensed partners cannot be named; USD-only with SmileCash-only payouts persists. | PCR-061, 001, 060, 062, 063 |
| — | **Smile&Pay (ZB)** | A regulated bank group. Worth approaching as a possible **custody or settlement partner** as well as a gateway. | No technical docs; unsigned callbacks confirmed. | PCR-070, 073, 003 |
| — | **ContiPay**, **EcoCash Open API** | Documentation access first. EcoCash direct is a possible single-rail complement. | — | PCR-080, PCR-090 |

Before any outreach, the project owner must confirm that Payonify and Linkwa are the intended companies
([capability matrix §1.1](provider-capability-matrix.md#11-identity-caveat)).

### 5.3 A likely shape, to test in due diligence (a hypothesis, not a decision)

If due diligence is favourable, a plausible arrangement is:
- one gateway for **collections** with broad rail coverage, plus authentic status;
- a licensed party (the same provider or a bank partner) for **holding and disbursement**, with payouts by API
  or split settlement.

The provider abstraction already supports per-capability routing across providers ([ADR-007](../adr/ADR-007-payment-provider-abstraction.md)).
Any arrangement where donations settle into a FundZim-controlled account is **Model B in substance** and is out
of scope without an ADR and legal sign-off ([ADR-013](../adr/ADR-013-regulatory-operating-model.md)).

### 5.4 Decision path to selection (Stage 1 → Stage 9)

1. The owner confirms the provider identities (§5.2) and authorises outreach.
2. Send the cross-cutting PCRs plus each provider's specific PCRs. Record answers as evidence.
3. Run the due-diligence checklist for the providers that pass G-CON and G-CUS on their answers.
4. Counsel reviews licensing (G-LIC) and the custody structure (LR-001, LR-003, LR-004).
5. Rescore with verified evidence. Hold sandbox spikes in Stage 9 (no real money).
6. Select PRIMARY and BACKUP by ADR, with the contract reviewed. Choose a PILOT candidate only at the Stage 20 gate.
