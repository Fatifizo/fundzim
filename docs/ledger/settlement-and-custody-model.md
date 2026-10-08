# Settlement and Custody Model (Ledger Boundaries)

**Stage 1 — design only.** Nothing here is implemented; the ledger is built in Stage 10
([ROADMAP](../ROADMAP.md)). This document **supersedes [LEDGER.md](../LEDGER.md) §3.2 (chart of accounts)**
and extends LEDGER §6 (worked examples). Everything else in LEDGER.md (invariants L1–L10, posting API,
append-only enforcement, concurrency) still applies unchanged. Decision record:
[ADR-014](../adr/ADR-014-accounting-separated-from-custody.md).

> **Custody caveat — LEGAL_REVIEW_REQUIRED (LR-001, LR-002, LR-003).** FundZim's ledger is a *record* of
> money that, under the provisional operating model
> ([operating-model-decision.md](../compliance/operating-model-decision.md), Model A), is collected, held and
> disbursed by a licensed payment service provider (PSP). The ledger does **not** assert that FundZim owns,
> holds or controls those funds. Whether any of these balances belong on FundZim's statutory balance sheet is
> an accounting and legal question (LR-002). The account design keeps both presentations possible.

Related: [transaction-lifecycle.md](../payments/transaction-lifecycle.md),
[payout-lifecycle.md](../payments/payout-lifecycle.md),
[refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md),
[funds-flow-architecture.md](../payments/funds-flow-architecture.md),
[currency-and-fx-policy.md](../payments/currency-and-fx-policy.md), [MONEY.md](../MONEY.md).

---

## 1. Why this document exists

Stage 0 posted a captured donation straight into `campaign_payable` ("available to withdraw"). That conflated
three financially different facts:

1. the provider has **confirmed** the donor paid (capture);
2. the provider has **settled** that money into the pool it holds for FundZim's campaigns; and
3. FundZim's policy has **released** it as eligible for payout.

Between (1) and (2) a capture can still be reversed or netted, and the provider can settle a different amount
than expected. Paying out against captured-but-unsettled money would let one campaign's withdrawal be funded
from money the PSP has not yet received, or from other campaigns' settled funds. Stage 1 therefore separates
every financial state into its own account.

## 2. Financial states

These are **different states of money**. They are never added together into one "campaign balance"; the UI
shows them separately (§8).

| # | State | Meaning | Ledger effect | Account(s) holding it | Withdrawable? |
|---|---|---|---|---|---|
| S1 | Donation initiated | Payment intent created; donor has not paid | **None** (payment record only) | — | No |
| S2 | Payment authorised | Card authorised but not captured (only where the provider separates auth and capture — PCR-025 — auth/capture) | **None** (payment status `AUTHORISED`) | — | No |
| S3 | Captured / confirmed | Provider authoritatively confirms payment (`SUCCEEDED`) | T1 capture + T2 PSP fee | `asset:psp_clearing:{p}` / `liability:campaign_unsettled:{c}` | No |
| S4 | Settled | Provider settlement report matches the capture | T3 settlement | `asset:psp_settled:{p}` (campaign side still `campaign_unsettled`) | No |
| S5 | Eligible (available) | Release rules satisfied (§5.5) | T4 release | `liability:campaign_payable:{c}` | **Yes** (subject to holds) |
| S5r | Reserved (holdback) | Portion withheld under a reserve policy (PD-33) | T4 split | `liability:campaign_reserve:{c}` | No |
| S6 | Reserved for withdrawal | Payout requested | Payout reserve | `liability:campaign_payout_pending:{c}` | No (already claimed) |
| S6t | Payout in transit | Submitted to provider; outcome not final (incl. `UNKNOWN`) | Payout submit | `liability:payout_in_transit:{p}` | No |
| S7 | Paid out | Provider confirms disbursement | Payout complete | leaves the pool (`psp_settled` decreases) | — |
| S8 | Refunded | Refund confirmed | Refund reserve + settle | `liability:refund_payable` → leaves pool | — |
| S9 | Reversed | Chargeback / provider reversal | Dispute journals | `campaign_*` debited; shortfall `asset:chargeback_recoverable` | — |
| SH | Held | Campaign FROZEN or dispute hold | Freeze / hold journal | `liability:campaign_held:{c}` | No |

## 3. Chart of accounts (supersedes LEDGER §3.2)

Every account exists **per currency** (one USD instance, one ZWG instance). `{p}` = provider, `{c}` = campaign.
Account currency is immutable (LEDGER invariant).

### 3.1 What each account *represents*

The most important property of an account here is what real-world thing it stands for. There are four kinds:

| Kind | Represents | Accounts |
|---|---|---|
| **Claim on provider** | Money the PSP has collected and owes into the campaign pool but has not yet settled | `asset:psp_clearing:{p}` |
| **Provider-held pool (memorandum / control)** | Money the PSP holds in its own trust or settlement arrangement for FundZim's campaigns, per the PSP's statements. **Not FundZim's bank cash.** | `asset:psp_settled:{p}` |
| **FundZim's own cash** | FundZim's own operating bank account. Only FundZim's revenue (platform fees) and FundZim's own funding movements touch it. | `asset:fundzim_operating_bank` |
| **Obligations to campaigns / donors** | What is owed onward to beneficiaries or donors, split by financial state | all `liability:campaign_*`, `liability:payout_in_transit:{p}`, `liability:refund_payable` |
| FundZim's own result | FundZim's income and costs | `revenue:*`, `expense:*` |
| Recoverables | Amounts FundZim has funded and seeks back | `asset:chargeback_recoverable`, `asset:refund_recoverable:{c}` |
| Unexplained | Differences found by reconciliation | `suspense:settlement_discrepancy:{p}` |

### 3.2 Accounts

| Code pattern | Type | Normal balance | Purpose | Change vs Stage 0 |
|---|---|---|---|---|
| `asset:psp_clearing:{p}` | ASSET | Dr | Captured, not yet settled by the PSP | **Renamed** from `asset:psp_receivable:{p}` |
| `asset:psp_settled:{p}` | ASSET | Dr | Settled per PSP statement; held by the PSP for disbursement (Model A) | **New** |
| `asset:fundzim_operating_bank` | ASSET | Dr | FundZim's own bank account | **New** (replaces the conditional `asset:settlement_bank:{account}` for Model A; see §10 for Model B) |
| `asset:chargeback_recoverable` | ASSET | Dr | Lost-chargeback / reversal shortfalls FundZim funded and seeks to recover | Unchanged |
| `asset:refund_recoverable:{c}` | ASSET | Dr | Refund shortfalls after payout FundZim funded and seeks to recover from the campaign | **New** (W2b) |
| `asset:psp_payout_float:{p}` | ASSET | Dr | Prefunded payout float, only if a provider requires it | Unchanged, **not used in Model A** (LR-001/LR-003) |
| `liability:campaign_unsettled:{c}` | LIABILITY | Cr | Net donations captured, not yet released | **New** |
| `liability:campaign_payable:{c}` | LIABILITY | Cr | Released; **available** for payout, subject to holds | Meaning narrowed: now only released funds |
| `liability:campaign_reserve:{c}` | LIABILITY | Cr | Holdback under a reserve policy (PD-33) | **New** |
| `liability:campaign_payout_pending:{c}` | LIABILITY | Cr | Reserved for a requested payout, not yet submitted | Unchanged |
| `liability:payout_in_transit:{p}` | LIABILITY | Cr | Submitted to the provider; outcome not final | **New** |
| `liability:campaign_held:{c}` | LIABILITY | Cr | Frozen or dispute-held | Unchanged |
| `liability:refund_payable` | LIABILITY | Cr | Approved refunds in flight | Unchanged |
| `revenue:platform_fees` | REVENUE | Cr | FundZim platform fees | Unchanged |
| `expense:psp_processing_fees` | EXPENSE | Dr | Provider fees borne by FundZim | Unchanged |
| `expense:chargeback_losses` | EXPENSE | Dr | Written-off chargeback recoverables | Unchanged |
| `expense:refund_losses` | EXPENSE | Dr | Written-off refund recoverables | **New** (W2b) |
| `equity:platform` | EQUITY | Cr | Opening balances, capital (rare) | Unchanged |
| `suspense:settlement_discrepancy:{p}` | SUSPENSE | either | Reconciliation differences. May carry a debit or credit balance; must trend to zero; monitored | **Renamed** from `suspense:unmatched:{p}` |

Accounts are created lazily and idempotently on first posting (LEDGER §3.2 rule kept).

### 3.3 Accounts deliberately absent

- **No per-user "wallet" or "balance" account.** A donor or owner never has a spendable, transferable balance
  at FundZim. That would look like stored value / e-money (R1-06; LR-003).
- **No FX or conversion account.** There is no FX in the MVP ([currency-and-fx-policy.md](../payments/currency-and-fx-policy.md)).
- **No "platform fee payable to FundZim" liability.** The platform fee is revenue at capture. Until the PSP
  remits it, the money physically sits in the provider pool, which is why `psp_settled` includes it (§5.7).

## 4. Invariants specific to this model

In addition to LEDGER L1–L10:

| ID | Invariant | Enforcement |
|---|---|---|
| SC-1 | Payout reservation may only debit `campaign_payable`. Never `campaign_unsettled`, `campaign_reserve` or `campaign_held`. | Posting rule + DB check on rule id (Stage 10) |
| SC-2 | **Pool integrity** per provider and currency: Σ campaign-side obligations (`campaign_unsettled + campaign_payable + campaign_reserve + campaign_payout_pending + payout_in_transit + campaign_held + refund_payable`) ≤ `psp_clearing + psp_settled`. | After every refund, reversal and payout journal, and daily. Breach = SEV1, stops automated payouts for that provider/currency ([refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md) §7). |
| SC-3 | `psp_settled:{p}` equals the PSP's own statement balance for the pool after each reconciliation run (± open items in `suspense`). | Reconciliation (Stage 17) |
| SC-4 | `suspense:settlement_discrepancy:{p}` items older than the configured age are escalated. | Daily job, FINANCE queue |
| SC-5 | `asset:fundzim_operating_bank` is touched only by fee remittance, pool funding, fee true-up and recovery journals. | Allow-list of posting rules |
| SC-6 | All lines of a transaction share one currency. Cross-currency transactions do not exist. | LEDGER L2/L3 |

## 5. Worked journals

Canonical numbers (shared with all Stage 1 payment docs): donation **US$50.00 = `5000`**, platform fee 5% =
`250`, PSP processing fee `175`, **campaign bears the PSP fee** (illustrative; PD-01 / LR-028). Every journal
shows Σ Dr = Σ Cr. All amounts in USD minor units.

### 5.1 T1 — capture (`payment:{id}:capture`, on authoritative `SUCCEEDED`)

| Account | Dr | Cr |
|---|---|---|
| asset:psp_clearing:{p} | 5000 | |
| liability:campaign_unsettled:{c} | | 4750 |
| revenue:platform_fees | | 250 |
| **Σ** | **5000** | **5000** ✔ |

### 5.2 T2 — PSP fee (`payment:{id}:psp_fee`)

Posted at capture if the provider reports the fee then (e.g. Pesepay's callback carries
`transactionServiceFee` and `merchantAmount` — R3 §2), otherwise at settlement with the same key.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 175 | |
| asset:psp_clearing:{p} | | 175 |
| **Σ** | **175** | **175** ✔ |

FundZim-bears variant: Dr `expense:psp_processing_fees` 175 · Cr `asset:psp_clearing:{p}` 175 ✔.

After T1+T2: clearing 4825, unsettled 4575, revenue 250.

### 5.3 T3 — settlement matched (`settlement:{batch_id}:{payment_id}`)

Posted by reconciliation when a PSP settlement line matches the capture.

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 4825 | |
| asset:psp_clearing:{p} | | 4825 |
| **Σ** | **4825** | **4825** ✔ |

### 5.4 T3′ — settlement discrepancy (`settlement:{batch_id}:{payment_id}`)

The PSP settles 4800 against an expected 4825 (an undeclared extra fee of 25, a short settlement, or an error).

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 4800 | |
| suspense:settlement_discrepancy:{p} | 25 | |
| asset:psp_clearing:{p} | | 4825 |
| **Σ** | **4825** | **4825** ✔ |

The release (§5.5) still uses only what is confirmed. Resolution is a separate, approved journal
(`discrepancy:{case_id}:resolved`), for example:

| Resolution | Lines |
|---|---|
| PSP pays the shortfall later | Dr `psp_settled:{p}` 25 · Cr `suspense:settlement_discrepancy:{p}` 25 ✔ |
| Confirmed extra PSP fee, campaign bears it (fee policy) | Dr `campaign_unsettled:{c}` 25 · Cr `suspense:settlement_discrepancy:{p}` 25 ✔ (then release 4550 instead of 4575) |
| Confirmed extra PSP fee, FundZim bears it | Dr `expense:psp_processing_fees` 25 · Cr `suspense:settlement_discrepancy:{p}` 25 ✔ |

Over-settlement (PSP settles 4850): Dr `psp_settled` 4850 · Cr `psp_clearing` 4825 · Cr `suspense` 25 ✔. The
surplus is never released to a campaign until explained.

### 5.5 T4 — release to available (`payment:{id}:release`)

Release rules (configurable; PD-14): the payment is `SUCCEEDED` and settlement-matched (T3 posted), it has no
open dispute, any post-settlement hold period has elapsed, and the campaign is not FROZEN.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 4575 | |
| liability:campaign_payable:{c} | | 4575 |
| **Σ** | **4575** | **4575** ✔ |

If the campaign is FROZEN at release time, the credit goes to `liability:campaign_held:{c}` instead.

### 5.6 T4r — release with reserve (illustrative 5% holdback, PD-33)

5% of 4575 = 228.75. The split uses the MONEY.md largest-remainder rule, with the remainder to `campaign_payable`.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 4575 | |
| liability:campaign_payable:{c} | | 4347 |
| liability:campaign_reserve:{c} | | 228 |
| **Σ** | **4575** | **4575** ✔ |

Reserve release after the reserve period (`reserve:{c}:{n}:released`): Dr `campaign_reserve:{c}` 228 · Cr
`campaign_payable:{c}` 228 ✔.

### 5.7 Platform fee remittance (`fee_remittance:{p}:{batch_id}`)

The PSP pays FundZim its platform-fee share from the pool (split at source, or a periodic remittance — PD-19).

| Account | Dr | Cr |
|---|---|---|
| asset:fundzim_operating_bank | 250 | |
| asset:psp_settled:{p} | | 250 |
| **Σ** | **250** | **250** ✔ |

Pool check after T1–T4 and remittance: assets clearing 0 + settled 4575 = 4575; obligations payable 4575 ✔ (SC-2 holds with equality).

### 5.8 Payout of US$4,000.00 (`400000`)

Posted at the payout transitions in [payout-lifecycle.md](../payments/payout-lifecycle.md) §3.

| Step | Key | Dr | Cr | Σ |
|---|---|---|---|---|
| Reserve (Y1, `PAYOUT_REQUESTED`) | `payout:{id}:reserved` | `campaign_payable:{c}` 400000 | `campaign_payout_pending:{c}` 400000 | ✔ |
| Submit (Y8, `SUBMITTED`) | `payout:{id}:submitted` | `campaign_payout_pending:{c}` 400000 | `payout_in_transit:{p}` 400000 | ✔ |
| Unknown (Y12, `UNKNOWN`) | — | **no journal**: money stays in transit | — | — |
| Complete (Y10, `COMPLETED`) | `payout:{id}:completed` | `payout_in_transit:{p}` 400000 | `asset:psp_settled:{p}` 400000 | ✔ |
| Fail after submit (Y11, `FAILED`) | `payout:{id}:failed` | `payout_in_transit:{p}` 400000 | `campaign_payable:{c}` 400000 (or `campaign_held:{c}` if frozen) | ✔ |
| Reject / cancel before submit (Y4, Y5) | `payout:{id}:released` | `campaign_payout_pending:{c}` 400000 | `campaign_payable:{c}` 400000 (or `campaign_held:{c}`) | ✔ |
| Reversed after completion (Y14, `REVERSED`) | `payout:{id}:reversed` | `asset:psp_settled:{p}` 400000 | `campaign_payable:{c}` 400000 (or `campaign_held:{c}`) | ✔ |

`payout_in_transit` is per provider rather than per campaign because, once submitted, the obligation is the
provider's execution. The payout record carries the campaign id, so reporting per campaign remains possible.

### 5.9 Refund before settlement (`refund:{id}:reserved` → `refund:{id}:settled`)

From [refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md) §3.3 A (full refund of 5000; the
platform fee is reversed; the campaign bears the unrecovered PSP fee — PD-20).

| Journal | Account | Dr | Cr |
|---|---|---|---|
| Reserve | liability:campaign_unsettled:{c} | 4750 | |
| | revenue:platform_fees | 250 | |
| | liability:refund_payable | | 5000 |
| | **Σ** | **5000** | **5000** ✔ |
| Settle | liability:refund_payable | 5000 | |
| | asset:psp_clearing:{p} | | 5000 |
| | **Σ** | **5000** | **5000** ✔ |

### 5.10 Refund after settlement, before payout

| Journal | Account | Dr | Cr |
|---|---|---|---|
| Reserve | liability:campaign_payable:{c} | 4750 | |
| | revenue:platform_fees | 250 | |
| | liability:refund_payable | | 5000 |
| | **Σ** | **5000** | **5000** ✔ |
| Fee true-up (only if the 250 was already remitted) | asset:psp_settled:{p} | 250 | |
| | asset:fundzim_operating_bank | | 250 |
| | **Σ** | **250** | **250** ✔ |
| Settle | liability:refund_payable | 5000 | |
| | asset:psp_settled:{p} | | 5000 |
| | **Σ** | **5000** | **5000** ✔ |

### 5.11 Chargeback after payout

From [refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md) §6 B. The campaign has `payable`
1000 and `reserve` 500 left; the provider debits 5000 from the pool when the dispute opens.

| Journal | Account | Dr | Cr |
|---|---|---|---|
| `dispute:{id}:opened` | liability:campaign_payable:{c} | 1000 | |
| | liability:campaign_reserve:{c} | 500 | |
| | asset:chargeback_recoverable | 3250 | |
| | revenue:platform_fees | 250 | |
| | asset:psp_settled:{p} | | 5000 |
| | **Σ** | **5000** | **5000** ✔ |
| `dispute:{id}:funding` (pool integrity; fee had been remitted) | asset:psp_settled:{p} | 3500 | |
| | asset:fundzim_operating_bank | | 3500 |
| | **Σ** | **3500** | **3500** ✔ |
| `dispute:{id}:written_off` (if lost and unrecovered; maker-checker) | expense:chargeback_losses | 3250 | |
| | asset:chargeback_recoverable | | 3250 |
| | **Σ** | **3250** | **3250** ✔ |

FundZim moving its own money into the provider-held pool is a custody-relevant act: **LR-076**.

### 5.12 Freeze and unfreeze (`campaign:{id}:freeze:{n}` / `:unfreeze:{n}`)

The campaign has `payable` 4575 at freeze.

| Journal | Account | Dr | Cr |
|---|---|---|---|
| Freeze | liability:campaign_payable:{c} | 4575 | |
| | liability:campaign_held:{c} | | 4575 |
| | **Σ** | **4575** | **4575** ✔ |
| Unfreeze | liability:campaign_held:{c} | 4575 | |
| | liability:campaign_payable:{c} | | 4575 |
| | **Σ** | **4575** | **4575** ✔ |

While FROZEN: releases (§5.5) and payout failure/reversal returns (§5.8) credit `campaign_held`, not
`campaign_payable`, so nothing becomes withdrawable. `campaign_unsettled` and `campaign_reserve` stay where they
are, since neither is withdrawable anyway. A payout already `SUBMITTED` stays in `payout_in_transit` until the
provider resolves it ([funds-flow-architecture.md](../payments/funds-flow-architecture.md) §7).

## 6. Balance projections

Per campaign and **per currency**, never summed across currencies (LEDGER §8 rules: projections are updated in
the same DB transaction as postings and are verifiable by full recompute).

| Projection | Formula (from ledger) | Shown to |
|---|---|---|
| Raised (gross) | Σ T1 gross (Dr `psp_clearing` on capture rules) − Σ refunded gross − Σ charged back gross | Public, owner |
| Pending settlement | `campaign_unsettled` | Owner, staff |
| Available to withdraw | `campaign_payable` | Owner, staff |
| Reserved (holdback) | `campaign_reserve` | Owner, staff |
| Withdrawal in progress | `campaign_payout_pending` + this campaign's in-transit payouts (from payout records) | Owner, staff |
| Held | `campaign_held` | Owner (as a notice), staff |
| Paid out | Σ `payout:*:completed` − Σ `payout:*:reversed` | Owner, staff |

## 7. Auditability when the PSP holds the money

FundZim never sees the bank account where the money sits. The ledger stays auditable by **evidence matching**,
not by trusting its own entries:

1. **Event capture.** Every provider callback and poll result is stored in `webhook_inbox` / `payment_events`
   (raw, redacted payload plus `provider_reference`) before it changes state ([PAYMENTS.md](../PAYMENTS.md) §9).
2. **Statement ingestion (Stage 17).** Settlement reports and statements are imported per provider and currency
   (API where available; otherwise dashboard export or SFTP, with the file hash recorded as an evidence record —
   [audit-evidence-model.md](../compliance/audit-evidence-model.md)). R3 found statement or balance APIs only
   for Payonify (`GET /v1/transfers/balance`) and Linkwa (`GET /statement`). Paynow offers per-transaction
   poll/trace only and Pesepay dashboard reports only, so file-based ingestion must be designed for (PCR-007 — settlement reports).
3. **Three-way match.** For each payment: **payment record** (FundZim's state machine) ↔ **provider report**
   (callback/poll + settlement line) ↔ **ledger** (T1–T4 journals). Each pairing is checked:

| Match | Detects |
|---|---|
| Payment `SUCCEEDED` ↔ capture journal exists | Missed posting, double posting (idempotency key) |
| Capture ↔ provider settlement line | Unsettled captures past SLA, settlements for unknown payments (→ suspense) |
| Provider pool balance ↔ `psp_settled:{p}` | Pool drift, unrecorded provider debits (fees, reversals) |
| Payout `COMPLETED` ↔ provider disbursement line | Unrecorded or duplicate disbursements |

4. **Pool integrity (SC-2)** runs after every relevant journal and daily.
5. **Immutable trail.** Ledger rows are append-only (LEDGER §4.1). Corrections are reversing journals with a
   reason, an actor and maker-checker.

## 8. Operating model deltas

### 8.1 Model A (provisional) — as above

Pool = PSP-held. FundZim instructs; the PSP executes. FundZim's own cash touches only fees and funding.

### 8.2 Model C — direct beneficiary settlement (variant for verified organisations)

The PSP settles each payment directly to the beneficiary's sub-merchant account and FundZim's fee to FundZim (for
example Pesepay split settlement, where the beneficiary must be an approved Pesepay merchant — R3 §2 row 16–17).
There is no FundZim-instructed payout. Capture (T1, T2) is unchanged. Settlement becomes:

| Journal `settlement:{batch}:{payment_id}:direct` | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 4575 | |
| asset:fundzim_operating_bank | 250 | |
| asset:psp_clearing:{p} | | 4825 |
| **Σ** | **4825** | **4825** ✔ |

Consequences: no `psp_settled`, `campaign_payable` or payout accounts are used for that campaign. Refunds and
chargebacks after direct settlement have nothing in the pool to draw from, so they go straight to recoverables
(`refund_recoverable` / `chargeback_recoverable`) with FundZim funding. Reserves are impossible unless the
provider supports a split to a reserve account (PCR-007, PCR-034, PCR-042 — reserve split).

### 8.3 Model B — platform-controlled settlement account (not selected)

`asset:psp_settled:{p}` would be replaced by `asset:settlement_bank:{account}`: a FundZim-controlled trust or
settlement bank account. All journals keep their shape. The ledger is unchanged in form, but the account would then
be real FundZim-controlled client money, which brings in the safeguarding, trust-account and licensing
obligations described in [operating-model-decision.md](../compliance/operating-model-decision.md) §4.2 (R1-06
paras 16–17). It is not selected; any move to Model B requires a superseding ADR and legal sign-off.

## 9. What must never happen

- Paying out from `campaign_unsettled`, `campaign_reserve` or `campaign_held` (SC-1).
- Releasing an amount larger than the settlement actually matched.
- Releasing an unexplained settlement surplus to a campaign.
- Treating `psp_settled` as FundZim's cash, or paying FundZim expenses from it.
- Netting one campaign's refund or chargeback against another campaign's funds without a pool-funding journal.
- Any journal mixing USD and ZWG lines.

## New legal questions

| ID | Area | Question | Why it matters / what is blocked |
|---|---|---|---|
| LR-076 | Pool funding by FundZim | May FundZim move its own funds into a PSP-held pool (fee true-ups, chargeback shortfall funding, `*:funding` journals) without being treated as holding, commingling or safeguarding client funds, or as issuing e-money? | The pool-integrity control (SC-2) depends on it. If not permitted, shortfalls must be funded by the PSP under contract, or covered by reserves, and the Model A contract terms change. Blocks Stage 11 refund/dispute funding design. |

(See also LR-001, LR-002, LR-003 and LR-075 (→ LR-036) in [operating-model-decision.md](../compliance/operating-model-decision.md).)

## New business decisions

| ID | Decision | Default proposed |
|---|---|---|
| PD-14 | Release timing: funds become available at settlement match, or settlement match + an N-day hold (per risk tier) | Settlement match + configurable hold per risk tier; pilot hold set by FINANCE |
| PD-19 | Platform fee collection mechanism: split at source by the PSP vs periodic remittance from the pool, and its cadence | Split at source where supported; otherwise remittance per settlement batch |
