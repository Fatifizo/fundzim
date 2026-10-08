# Refund and Reversal Flows (Flow 4 and Flow 5)

> **Stage 1 — design specification.** Nothing here is implemented. Ledger journals use the Stage 1 account
> model in [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md) and **supersede the
> journal shapes** of [LEDGER.md](../LEDGER.md) §6.3–6.4 where they differ (the principles — reserve on
> approval, settle on confirmation, exact reversal on failure, recoverable for shortfalls — are unchanged).
> Entity and state design: [refund-and-dispute-architecture.md](refund-and-dispute-architecture.md).
> Payment states: [transaction-lifecycle.md](transaction-lifecycle.md). End-to-end context:
> [funds-flow-architecture.md](funds-flow-architecture.md). Currency rules:
> [currency-and-fx-policy.md](currency-and-fx-policy.md).

## 1. Principles

1. **A refund is a new financial event**, never an edit of the donation. The original capture journal stays;
   refund journals are added and reference it.
2. **Reserve on approval, settle on authoritative confirmation, reverse exactly on failure** (Stage 0 rule).
   The reservation removes the amount from what the campaign can withdraw, so a refund and a payout can never
   both spend the same money.
3. **Refunds and reversals must never be funded by other campaigns' money.** Under Model A the PSP holds a
   pooled balance for all campaigns. If a refund or chargeback is not covered by *this* campaign's own
   balances (plus any platform-fee true-up), the shortfall must be funded by FundZim **before** a refund is
   submitted (or, for chargebacks the provider debits unilaterally, immediately after). This is the
   **pool-integrity rule** (§7).
4. **Refund confirmation comes only from authoritative provider state** (webhook, status API,
   reconciliation). A refund call that times out is `UNKNOWN`, never `FAILED`, and is never resubmitted under a
   new reference. The provider reference for a refund is `refund_id`.
5. **Chargebacks are not refunds.** A lost dispute or provider-initiated reversal ends in `CHARGED_BACK`, never
   `REFUNDED`.
6. **Same currency throughout.** A refund is made in the currency of the original payment; no FX.
7. **Every journal balances per currency.** All examples below are checked.
8. **Policy vs law vs provider.** Who bears fees, refund windows and recovery order are **platform policy**
   (PD items). Donor statutory rights are **legal questions** (LR-018). Refund capability, windows and
   dispute deadlines are **provider rules** (PCR). See [refund-and-dispute-architecture.md](refund-and-dispute-architecture.md) §8.

## 2. Canonical numbers

All examples use one US$50.00 donation (`5000`) with: platform fee 5% = `250`; PSP processing fee `175`;
campaign bears the PSP fee (PD-01 / LR-028, illustrative only). After capture (T1, T2) the donation
contributes:

| Account | Balance from this donation |
|---|---|
| `asset:psp_clearing:{p}` (until settled) / `asset:psp_settled:{p}` (after) | 4825 |
| `liability:campaign_unsettled:{c}` (until release) / `liability:campaign_payable:{c}` (after) | 4575 |
| `revenue:platform_fees` | 250 |

Illustrative refund fee policy (PD-20, open): **the donor receives the full gross amount; the platform fee is
reversed; the unrecovered PSP fee is borne by the campaign.** The ledger supports other policies (platform
absorbs the PSP fee; platform fee not reversed); only the debited accounts change.

Under that policy, refunding the full 5000 needs funding of:

| Source | Amount | Why |
|---|---|---|
| Campaign | 4750 | its net from this donation (4575) + the PSP fee it bears (175) |
| Platform fee reversal | 250 | fee returned to the donor |
| **Total** | **5000** | = refund to donor |

## 3. Flow 4 — refunds

### 3.1 Initiation channels

| Channel | Who | Creates | Reason codes |
|---|---|---|---|
| Donor request | Donor (authenticated, or via verified receipt link) | `refund_request` in `REQUESTED` | `DONOR_REQUEST` |
| Support | SUPPORT (on donor contact) | `REQUESTED` (SUPPORT can only request) | `DONOR_REQUEST`, `PAYMENT_ERROR` |
| Compliance | COMPLIANCE (fraud outcome, investigation) | `REQUESTED` | `CAMPAIGN_FRAUD`, `COMPLIANCE_ORDER` |
| System | Duplicate detector | `REQUESTED` (auto) | `DUPLICATE_PAYMENT` |
| System | Campaign cancellation with refund disposition (LR-019) | `REQUESTED` (bulk, one per payment) | `CAMPAIGN_CANCELLED` |

**Duplicate payments.** Two SUCCEEDED payments from the same payer identity (same wallet/MSISDN or card
fingerprint token from the provider) to the same campaign, same amount and currency, within a configured
window, where the second is flagged `possible_duplicate_of` (see
[transaction-lifecycle.md](transaction-lifecycle.md) §8), create an automatic refund request for the later
payment. The request still needs approval unless the auto-approval policy for `DUPLICATE_PAYMENT` below the
configured limit is enabled (PD-21). The donor is told both payments went through and one is being returned.

### 3.2 Approval

- **FINANCE approves**; the approver can never be the requester (maker-checker, enforced by a DB check
  `approved_by <> requested_by`).
- Approval preconditions (all evaluated in one DB transaction under row locks on the payment and the
  campaign's balance projections, account-id order):
  - payment is `SUCCEEDED` or `PARTIALLY_REFUNDED`;
  - requested amount ≤ refundable remaining = captured − Σ(refunds in APPROVED…SUCCEEDED) − Σ(chargebacks),
    computed from refund records and the ledger, never a mutable counter;
  - the provider supports refunds for this method (capability `refunds: full|partial`) **or** the manual
    refund path (§3.6) is chosen;
  - the provider's refund window has not passed (PCR-006 — refund window per method);
  - no open dispute on the payment (a disputed payment is not refunded; the dispute flow governs);
  - funding is available: the campaign accounts named in §3.3 cover the campaign's share; if not, §3.5.
- On approval the **reservation journal** posts (§3.3). The request moves to `APPROVED`.

### 3.3 Ledger — refund journals by timing

The reservation draws the campaign's share **from the account where the money currently sits**, in this order:
`campaign_unsettled` (this donation not yet released) → `campaign_payable` → `campaign_reserve` (only with
FINANCE approval, since reserves exist to cover exactly these events). The provider-side credit is
`psp_clearing` if the payment is not yet settled, else `psp_settled`.

**A. Refund before settlement** (this donation still in `campaign_unsettled`, the campaign has ≥175 of other
unsettled funds to cover the PSP fee it bears):

Reservation `refund:{refund_id}:reserved` (on approval):

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| liability:refund_payable | | 5000 |

Σ Dr 5000 = Σ Cr 5000 ✔

Settlement `refund:{refund_id}:settled` (on authoritative confirmation; the PSP nets the refund against
collections it has not yet settled):

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 5000 | |
| asset:psp_clearing:{p} | | 5000 |

Σ 5000 = 5000 ✔. Net effect for this payment: clearing 4825 − 5000 = −175, i.e. the provider will settle 175
less from the campaign's other collections — exactly the PSP fee the campaign bears. Reconciliation must expect
this netting (§3.8).

**B. Refund after settlement, before payout** (net sits in `campaign_payable`):

Reservation `refund:{refund_id}:reserved`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| liability:refund_payable | | 5000 |

✔ 5000 = 5000

**Platform-fee true-up** (only if the 250 platform fee was already remitted from the PSP to FundZim's operating
account): FundZim must return it to the provider-held pool before submission, otherwise the refund would use
other campaigns' money. Key `refund:{refund_id}:fee_true_up`:

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 250 | |
| asset:fundzim_operating_bank | | 250 |

✔ 250 = 250. (In practice this may be a deduction from the next fee remittance rather than a transfer; the
journal is the same — PCR-016 — fee remittance netting.)

Settlement `refund:{refund_id}:settled`:

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 5000 | |
| asset:psp_settled:{p} | | 5000 |

✔ 5000 = 5000

**C. Refund after payout** (campaign balances insufficient): never auto-approved. FINANCE chooses an option
per policy (PD-22, LR-080, LR-082); see §3.5.

**D. Failure after reservation** `refund:{refund_id}:failed` — exact inverse of the reservation, e.g. for B:

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 5000 | |
| liability:campaign_payable:{c} | | 4750 |
| revenue:platform_fees | | 250 |

✔ 5000 = 5000. If a fee true-up was posted, its inverse is posted too (`refund:{refund_id}:fee_true_up_reversed`)
or the amount stays in the pool against the next remittance — whichever reflects the actual cash movement.

### 3.4 Partial refunds

A partial refund of `r` reverses a proportional share of the platform fee, computed in basis points with the
single documented rounding point of [MONEY.md](../MONEY.md); any remainder unit stays with the campaign share so
the parts sum exactly to `r`. Example: refund 2000 of the 5000 donation, platform fee 5% of 2000 = 100, PSP fee
not recovered:

Reservation `refund:{refund_id}:reserved`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 1900 | |
| revenue:platform_fees | 100 | |
| liability:refund_payable | | 2000 |

✔ 2000 = 2000. Settlement: Dr `refund_payable` 2000 · Cr `psp_settled:{p}` 2000 ✔. Payment moves to
`PARTIALLY_REFUNDED`; a later refund of the remaining 3000 moves it to `REFUNDED`. Whether the provider allows
partial and multiple refunds is a capability flag (PCR-006 — partial refunds).

### 3.5 Refunds the campaign cannot cover (after payout)

Never auto-approved. Options, chosen per policy by FINANCE with COMPLIANCE input where fraud is involved:

| Option | When | Ledger |
|---|---|---|
| **C1 Recover first** | Default for non-fraud donor requests after payout (PD-21): the request goes `ON_HOLD` until the campaign has sufficient funds (later donations) or is declined with reasons. | No journal until approval; then as in B. |
| **C2 Platform-funded with recovery** | Fraud, duplicate or error cases where the donor must be made whole now. FundZim funds the shortfall and opens a recovery case against the campaign owner/beneficiary (enforceability: LR-080). | See below. |
| **C3 Decline** | Policy permits and no legal obligation (LR-018). | None. Donor informed with escalation route. |

C2 example — campaign `payable` has only 1000 left; fee already remitted:

Reservation `refund:{refund_id}:reserved`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 1000 | |
| asset:refund_recoverable:{c} | 3750 | |
| revenue:platform_fees | 250 | |
| liability:refund_payable | | 5000 |

✔ 5000 = 5000

Pool funding by FundZim `refund:{refund_id}:funding` (FundZim moves its own money into the provider-held pool;
whether and how this is permitted under the operating model is LR-001/LR-004):

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 4000 | |
| asset:fundzim_operating_bank | | 4000 |

✔ 4000 = 4000 (3750 shortfall + 250 fee true-up).

Settlement: Dr `refund_payable` 5000 · Cr `psp_settled:{p}` 5000 ✔.

Later recovery from the beneficiary (paid to FundZim) `recovery:{case_id}:{n}`: Dr `fundzim_operating_bank` ·
Cr `refund_recoverable:{c}`. Recovery by set-off against later donations to the same campaign (LR-082, PD-22):
Dr `campaign_payable:{c}` · Cr `refund_recoverable:{c}`. Write-off after recovery fails (maker-checker):
Dr `expense:refund_losses` · Cr `refund_recoverable:{c}`.

> `asset:refund_recoverable:{c}` and `expense:refund_losses` are **proposed additions** to the chart of
> accounts, mirroring `chargeback_recoverable` / `chargeback_losses` so refund and chargeback losses report
> separately. To be confirmed in [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md).

### 3.6 Manual refunds (provider has no refund API)

Some rails may not support API refunds (PCR-006 — refund support per method). The refund is then executed as a
**payout to the original payer** using the payout rail:

- destination must be the **same** payer instrument that paid (same MSISDN/wallet as reported by the provider
  on the original payment); sending to any other instrument is not allowed without COMPLIANCE approval and is
  a legal question (LR-081);
- the refund request carries `execution_method = MANUAL_PAYOUT` and is processed with the payout state
  machine's submission/UNKNOWN rules ([payout-lifecycle.md](payout-lifecycle.md) §5), using `refund_id` as the
  provider reference;
- ledger: identical reservation/settlement journals; the settlement credit is `psp_settled:{p}` (or the
  disbursing provider's account if different).

### 3.7 Provider processing and unknown outcomes

```mermaid
sequenceDiagram
    autonumber
    actor Dn as Donor
    actor S as SUPPORT
    actor F as FINANCE
    participant API as FundZim API
    participant DB as PostgreSQL
    participant P as PSP
    participant J as Poll job

    Dn->>S: "Please refund my donation"
    S->>API: POST /api/v1/admin/refund-requests (payment_id, amount, reason)
    API->>DB: refund_request REQUESTED → PENDING_APPROVAL; audit
    F->>API: POST /api/v1/admin/refund-requests/{id}/approve
    API->>DB: BEGIN; lock payment + campaign projections; check refundable & funding;<br/>post refund:{id}:reserved; status APPROVED; COMMIT
    API->>P: RefundPayment(payment ref, refund ref=refund_id, amount)
    alt provider acknowledges
        API->>DB: APPROVED → SUBMITTED → PROCESSING
    else timeout / 5xx / reset after send
        API->>DB: APPROVED → UNKNOWN (never FAILED); schedule poll
        J->>P: GetRefund(ref=refund_id)
    end
    P->>API: webhook refund.succeeded (verified, inbox, dedupe)
    API->>DB: BEGIN; refund → SUCCEEDED; post refund:{id}:settled;<br/>payment → PARTIALLY_REFUNDED/REFUNDED; COMMIT
    API-->>Dn: Refund confirmation (amount, currency, expected arrival per provider)
```

Rules:

- `RefundPayment` is called **after** the reservation commits, never inside the DB transaction.
- `UNKNOWN` refunds follow the same resolution procedure as payments
  ([transaction-lifecycle.md](transaction-lifecycle.md) §7.3): poll by `refund_id`, reconciliation, ops review
  with provider confirmation. While UNKNOWN the reservation stays in place — the money is neither available to
  the campaign nor released.
- Retry is allowed only with the same `refund_id` and only if the provider documents idempotent refund
  creation (PCR-006 — idempotent refunds). Otherwise poll only.
- A refund failure (`FAILED`, authoritative) posts the exact reversal (§3.3 D). Permanently undeliverable
  refunds (closed wallet, invalid account) are handled per LR-083.

### 3.8 Reconciliation of refunds

- Provider statements are expected to show refunds as negative lines referencing our `refund_id` (PCR-007 — statement format). The matcher pairs each statement line with a `refund:{id}:settled` journal.
- Refunds may be **netted** against settlement batches. The settlement matcher must compute expected
  settlement = Σ captures − Σ PSP fees − Σ refunds − Σ chargebacks ± adjustments for the batch window, per
  currency, and post any difference to `suspense:settlement_discrepancy:{p}` with a case.
- A refund confirmed by webhook but absent from the statement after the provider's settlement window, or
  present in the statement without a confirmed refund record, is a discrepancy case (SEV2 if amount above the
  configured threshold).

## 4. Payment status effects

| Event | Payment status |
|---|---|
| Refund reserved (APPROVED) | unchanged |
| Refund SUCCEEDED, cumulative < captured | `PARTIALLY_REFUNDED` |
| Refund SUCCEEDED, cumulative = captured | `REFUNDED` (terminal) |
| Refund FAILED / CANCELLED | unchanged |

## 5. Flow 5 — chargebacks and reversals

### 5.1 Event types

| Type | Rail | Provider debits | Payment path |
|---|---|---|---|
| Card dispute, provider debits **on open** | Cards | When dispute opens | SUCCEEDED → DISPUTED → SUCCEEDED (won) / CHARGED_BACK (lost) |
| Card dispute, provider debits **on loss** | Cards | Only if lost | same |
| Provider-initiated reversal of a completed collection | Mobile money / bank, **if the provider supports and notifies it** (PCR-011 — collection reversal events) | Immediately | SUCCEEDED → CHARGED_BACK (`reversal_kind = PROVIDER_REVERSAL`) |
| Pre-dispute inquiry / retrieval request | Cards | No | No status change; dispute case opened in `INQUIRY` |

### 5.2 Recovery order

When a reversal takes money from the pool, the campaign's share is recovered from the campaign's own accounts
in this default order (PD-22): `campaign_held` (amounts already held for this dispute) → `campaign_payable` →
`campaign_reserve` → `campaign_unsettled` (only the portion that will settle, offset at release) → any
shortfall to `asset:chargeback_recoverable`. The platform fee portion is reversed from `revenue:platform_fees`
(policy, PD-20). Every reversal also places a **payout hold** on the campaign (DISPUTE_HOLD, see
[payout-eligibility-and-controls.md](payout-eligibility-and-controls.md) §5) until the case is closed or
COMPLIANCE releases it.

## 6. Ledger — chargeback journals

**A. Dispute opened, provider debits on open, funds still in `campaign_payable`** `dispute:{id}:opened`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| asset:psp_settled:{p} | | 5000 |

✔ 5000 = 5000 (+ fee true-up journal as in §3.3 B if the platform fee was already remitted).

**B. Dispute opened after payout** — campaign has `payable` 1000 and `reserve` 500 left:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 1000 | |
| liability:campaign_reserve:{c} | 500 | |
| asset:chargeback_recoverable | 3250 | |
| revenue:platform_fees | 250 | |
| asset:psp_settled:{p} | | 5000 |

✔ 5000 = 5000. The provider has taken 5000 from the pool, but only 1500 + fee belonged to this campaign. The
pool is now short by 3250 (plus 250 if the fee was remitted). **Pool-integrity funding** follows immediately
`dispute:{id}:funding`:

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 3500 | |
| asset:fundzim_operating_bank | | 3500 |

✔ 3500 = 3500 (3250 shortfall + 250 fee true-up, assuming remitted).

A recovery case is opened against the campaign owner/beneficiary (LR-080, LR-020).

**C. Dispute won** `dispute:{id}:won` — exact inverse of the opening journal (provider re-credits the pool):

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 5000 | |
| liability:campaign_payable:{c} | | 1000 |
| liability:campaign_reserve:{c} | | 500 |
| asset:chargeback_recoverable | | 3250 |
| revenue:platform_fees | | 250 |

✔ 5000 = 5000. FundZim's funding top-up is now surplus in the pool and is returned to FundZim on the next
remittance: Dr `fundzim_operating_bank` 3500 · Cr `psp_settled:{p}` 3500 ✔ (`dispute:{id}:funding_returned`).
The recovery case closes; the payout hold is released by the case outcome.

**D. Dispute lost** — no further movement for the disputed amount (already debited). Payment → `CHARGED_BACK`.
Recovery then proceeds:

| Recovery route | Journal | Lines |
|---|---|---|
| Set-off against later donations to the same campaign (LR-082, PD-22) | `recovery:{case}:{n}` | Dr `campaign_payable:{c}` · Cr `asset:chargeback_recoverable` |
| Beneficiary repays FundZim | `recovery:{case}:{n}` | Dr `asset:fundzim_operating_bank` · Cr `asset:chargeback_recoverable` |
| Write-off (maker-checker: FINANCE + second approver) | `dispute:{id}:written_off` | Dr `expense:chargeback_losses` · Cr `asset:chargeback_recoverable` |

**E. Provider debits only on loss** — on open, hold the campaign's share so it cannot be withdrawn
`dispute:{id}:held`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| liability:campaign_held:{c} | | 4750 |

✔. Won: inverse (`dispute:{id}:released`). Lost `dispute:{id}:lost`:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_held:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| asset:psp_settled:{p} | | 5000 |

✔ 5000 = 5000. If `campaign_payable` held less than 4750 at open, only the available amount is moved to
`campaign_held`, and the remainder at loss goes to `chargeback_recoverable` with pool funding as in B.

**F. Dispute / chargeback fees** charged by the provider (PCR-012 — dispute fees) are separate lines, e.g.
Dr `expense:psp_processing_fees` (FundZim bears) or Dr `campaign_payable:{c}` (campaign bears, PD-20) ·
Cr `psp_settled:{p}`.

**G. Provider-initiated reversal (mobile money/bank)** — same shape as A or B, key
`reversal:{payment_id}:{provider_event_id}`, payment directly to `CHARGED_BACK`, plus a COMPLIANCE-visible
risk signal (a reversal can indicate a fraudulent or mistaken payer).

### 6.1 Sequence — chargeback after payout

```mermaid
sequenceDiagram
    autonumber
    participant P as PSP
    participant API as FundZim API
    participant DB as PostgreSQL
    actor F as FINANCE
    actor C as COMPLIANCE
    actor O as Campaign owner

    P->>API: webhook dispute.opened (payment ref, amount, deadline) — verified
    API->>DB: BEGIN; payment SUCCEEDED → DISPUTED; dispute_case OPENED;<br/>post dispute:{id}:opened (payable, reserve, recoverable, platform fee / Cr psp_settled);<br/>payout hold DISPUTE_HOLD; COMMIT
    API->>F: pool-integrity alert: shortfall 3250 (+250 fee)
    F->>API: approve pool funding (maker-checker)
    API->>DB: post dispute:{id}:funding
    API->>C: case: evidence due by provider deadline
    C->>O: request evidence (receipts, beneficiary proof)
    O-->>C: evidence
    C->>API: submit evidence pack (evidence_records)
    API->>P: SubmitDisputeEvidence (if supported; else provider portal, logged)
    alt won
        P->>API: dispute.won
        API->>DB: post dispute:{id}:won; DISPUTED → SUCCEEDED; release hold (per case)
    else lost
        P->>API: dispute.lost
        API->>DB: DISPUTED → CHARGED_BACK; recovery case stays OPEN
        API->>O: recovery notice (per terms, LR-080)
    end
```

## 7. Pool-integrity rule (control)

For each provider and currency, at all times:

```
Σ campaign-side liabilities owed from that provider's pool
   (campaign_unsettled + campaign_payable + campaign_reserve + campaign_payout_pending
    + payout_in_transit + campaign_held + refund_payable)
≤  psp_clearing + psp_settled   (for that provider and currency)
```

- Checked after every refund/reversal journal and in the daily reconciliation run.
- A breach means other campaigns' money is being used to cover this campaign's reversal. It is a **SEV1**
  alert, stops automated payouts for that provider/currency, and opens a FINANCE incident until funded.
- Funding journals (`*:funding`) and fee true-ups are the only sanctioned way to restore it.

## 8. New decisions introduced here

Business decisions (to be numbered into [PRODUCT.md](../PRODUCT.md) §13 by the lead):

| ID | Decision | Default proposed |
|---|---|---|
| PD-20 | Fee treatment on refunds and chargebacks: is the platform fee reversed; who bears the unrecovered PSP fee and dispute fees | Platform fee reversed; campaign bears PSP and dispute fees |
| PD-21 | Donor refund window and eligibility (donor-initiated refunds allowed until when — before payout only? within N days?); auto-approval of `DUPLICATE_PAYMENT` refunds below a limit | Donor-initiated refunds before the donation is paid out, subject to review; duplicates auto-approved below a configured internal limit |
| PD-22 | Recovery order across campaign accounts and whether shortfalls may be set off against later donations to the same campaign | Order in §5.2; set-off only after legal confirmation (LR-082) |

## New legal questions

| ID | Area | Question | Why it matters / what is blocked |
|---|---|---|---|
| LR-080 | Beneficiary recovery (clawback) | Can FundZim contractually require campaign owners/beneficiaries to repay amounts refunded or charged back after payout, and how enforceable is that against individuals and organisations? | Determines whether C2 refunds and post-payout chargebacks are recoverable or are platform losses; drives reserve policy and terms of service (LR-022). Blocks the recovery workflow design in Stage 11/14. |
| LR-081 | Refunds to another instrument | May a refund be paid to an instrument other than the one that paid (e.g. manual refund payout to a different wallet or bank account), and what verification is required? | Third-party payment and AML risk; affects manual refund path (§3.6). Until resolved: same-instrument only. |
| LR-082 | Set-off against later donations | May shortfalls from refunds or chargebacks be recovered from later donations to the same campaign, given donors' intent and consumer-protection rules? | Affects recovery route and disclosures to donors. Until resolved: no automatic set-off. |
| LR-083 | Unclaimed / undeliverable refunds | What must happen to refunds that cannot be delivered (closed wallet, deceased donor, invalid account) — holding period, escheat/unclaimed-funds rules, disposal? | Needs a holding account treatment and retention; blocks closure of such cases. |
