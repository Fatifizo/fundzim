# FundZim — Ledger Architecture

> Status: Stage 0 specification, amended in Stage 1 by [ADR-014](adr/ADR-014-accounting-separated-from-custody.md)
> (chart of accounts and settlement states; detail in
> [ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md)). Implementation is **Stage 10**, preceded by schema design in Stage 2. Nothing
> in this repository posts ledger entries yet. Normative for all future financial code. Changes require an ADR
> amending [ADR-006](adr/ADR-006-double-entry-ledger.md).
>
> Related: [MONEY.md](MONEY.md) · [PAYMENTS.md](PAYMENTS.md) · [DATABASE.md](DATABASE.md) · [AUDIT.md](AUDIT.md)

---

## 1. Why a ledger

Crowdfunding money passes through several hands, and a balance column cannot explain where it is: a donor's
payment, a PSP's collection account, fees, refunds, chargebacks and payouts. FundZim must be able to answer,
for any campaign and any currency, at any past point in time:

- How much was donated, and through which provider and transaction?
- What fees were taken, and under which schedule?
- What was refunded, disputed or paid out, and to whom?
- What remains payable, held or frozen?

…and to prove that those answers sum correctly. The double-entry ledger is the **single source** for these
answers. Every other balance-like number in the system is derived from it.

## 2. Custody caveat — LEGAL_REVIEW_REQUIRED (LR-001, LR-002)

The ledger records **FundZim's view of funds orchestrated through licensed PSPs**. It does **not** assert that
FundZim holds, owns or has custody of those funds. Account names are chosen not to imply custody, for example
"PSP settlement receivable" rather than "FundZim bank", and "Campaign payable to beneficiary" rather than
"Campaign wallet".

Two questions remain open:

- Whether these balances belong on FundZim's statutory balance sheet.
- Whether FundZim may hold funds at all, and which licences that would require.

Both are `LEGAL_REVIEW_REQUIRED` (LR-001, LR-002) and need accounting review (see [COMPLIANCE.md](COMPLIANCE.md)). The ledger
design works for both "pass-through/agent" and "collecting" models. Only account classification and reporting
differ.

## 3. Model

### 3.1 Concepts

- **Account:** a named bucket in **exactly one currency**, with a type and a normal balance.
- **Transaction (journal):** an atomic, immutable business event made of ≥ 2 entries. It is balanced per
  currency.
- **Entry (posting):** one debit or credit of a positive amount to one account.

| Type | Normal balance | Increases with |
|---|---|---|
| ASSET | DEBIT | debit |
| EXPENSE | DEBIT | debit |
| LIABILITY | CREDIT | credit |
| EQUITY | CREDIT | credit |
| REVENUE | CREDIT | credit |
| SUSPENSE | DEBIT (by convention) | either; the balance may carry either sign and must trend to zero |

### 3.2 Chart of accounts

> **Superseded by [ADR-014](adr/ADR-014-accounting-separated-from-custody.md) (Stage 1).** The authoritative
> chart, what each account represents, the settlement-aware financial states and invariants SC-1 – SC-6 are
> in [ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md) §2–§4. Summary:

Every account exists **per currency** (a USD instance and a ZWG instance). `{p}` = provider, `{c}` = campaign.
Account currency is immutable. Accounts are created lazily and idempotently on first posting.

| Code pattern | Type | Represents |
|---|---|---|
| `asset:psp_clearing:{p}` | ASSET | Captured, not yet settled by the PSP (claim on the provider). Renamed from Stage 0 `psp_clearing`. |
| `asset:psp_settled:{p}` | ASSET | Settled per PSP statement and held **by the PSP** for disbursement (Model A). Memorandum/control account, not FundZim cash. |
| `asset:fundzim_operating_bank` | ASSET | FundZim's own bank account. Only platform-fee remittances and FundZim's own funding movements touch it. |
| `asset:chargeback_recoverable`, `asset:refund_recoverable:{c}` | ASSET | Shortfalls FundZim funded and seeks to recover. |
| `asset:psp_payout_float:{p}` | ASSET | Prefunded payout float, only if a provider requires one. Not used in Model A (LR-001, LR-003). |
| `liability:campaign_unsettled:{c}` | LIABILITY | Net donations captured, not yet released. |
| `liability:campaign_payable:{c}` | LIABILITY | Released funds: **available** to withdraw, subject to holds. |
| `liability:campaign_reserve:{c}` | LIABILITY | Holdback under a reserve policy (PD-33); not available. |
| `liability:campaign_payout_pending:{c}` | LIABILITY | Reserved for a requested payout, not yet submitted. |
| `liability:payout_in_transit:{p}` | LIABILITY | Submitted to the provider; outcome not final (includes payouts in `UNKNOWN`). |
| `liability:campaign_held:{c}` | LIABILITY | Frozen or dispute-held. |
| `liability:refund_payable` | LIABILITY | Approved refunds in flight to donors. |
| `revenue:platform_fees` | REVENUE | FundZim platform fees. |
| `expense:psp_processing_fees`, `expense:chargeback_losses`, `expense:refund_losses` | EXPENSE | Costs and write-offs borne by FundZim. |
| `equity:platform` | EQUITY | Opening balances, capital (rare). |
| `suspense:settlement_discrepancy:{p}` | SUSPENSE | Reconciliation differences; may carry either sign; must trend to zero. Renamed from Stage 0 `suspense:settlement_discrepancy`. |

The Stage 0 conditional `asset:settlement_bank:{account}` is not part of Model A. It reappears only if a
platform-controlled settlement model (Model B) were ever approved by ADR and legal sign-off
([ADR-013](adr/ADR-013-regulatory-operating-model.md)).

### 3.3 Tables (conceptual)

```sql
CREATE TABLE ledger_accounts (
  id              UUID PRIMARY KEY,                    -- UUIDv7
  code            TEXT        NOT NULL,                -- e.g. liability:campaign_payable:{uuid}
  type            TEXT        NOT NULL CHECK (type IN ('ASSET','LIABILITY','EQUITY','REVENUE','EXPENSE','SUSPENSE')),
  normal_balance  TEXT        NOT NULL CHECK (normal_balance IN ('DEBIT','CREDIT')),
  currency        CHAR(3)     NOT NULL REFERENCES currencies(code),
  owner_type      TEXT,                                -- 'campaign' | 'provider' | NULL (platform)
  owner_id        TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (code, currency),
  UNIQUE (id, currency)                                -- target for composite FK from entries
);

CREATE TABLE ledger_transactions (
  id                      UUID PRIMARY KEY,
  type                    TEXT        NOT NULL,        -- DONATION_CAPTURED, REFUND_SETTLED, PAYOUT_RESERVED ...
  idempotency_key         TEXT        NOT NULL UNIQUE, -- e.g. 'payment:{id}:capture'
  occurred_at             TIMESTAMPTZ NOT NULL,        -- business time (provider-reported where applicable)
  posted_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  description             TEXT        NOT NULL,
  source_type             TEXT        NOT NULL,        -- payment | refund | dispute | payout | adjustment | recon
  source_id               UUID        NOT NULL,
  provider                TEXT,
  provider_reference      TEXT,
  fee_schedule_version    TEXT,
  created_by_type         TEXT        NOT NULL,        -- system | staff
  created_by_id           TEXT        NOT NULL,
  reason                  TEXT,                        -- required for adjustments/reversals
  reverses_transaction_id UUID REFERENCES ledger_transactions(id),
  request_id              TEXT,
  correlation_id          TEXT
);

CREATE TABLE ledger_entries (
  id             UUID PRIMARY KEY,
  transaction_id UUID    NOT NULL REFERENCES ledger_transactions(id),
  account_id     UUID    NOT NULL,
  direction      TEXT    NOT NULL CHECK (direction IN ('DEBIT','CREDIT')),
  amount_minor   BIGINT  NOT NULL CHECK (amount_minor > 0),
  currency       CHAR(3) NOT NULL,
  FOREIGN KEY (account_id, currency) REFERENCES ledger_accounts (id, currency)  -- entry currency = account currency
);
CREATE INDEX ON ledger_entries (account_id, transaction_id);
```

## 4. Invariants

> Stage 2 ([ADR-023](adr/ADR-023-ledger-posting-architecture.md)) adds L11–L14 and P-1 and defines the
> database enforcement of every invariant: [architecture/ledger-invariants.md](architecture/ledger-invariants.md).

| # | Invariant | Enforced in |
|---|---|---|
| L1 | For every transaction **and every currency within it**, Σ debits = Σ credits | Go (`ledger.Post` builder) **and** DB deferred constraint trigger |
| L2 | Each transaction has ≥ 2 entries | Go and DB trigger |
| L3 | `amount_minor > 0`; direction carries sign | DB CHECK |
| L4 | Entry currency = account currency | DB composite FK |
| L5 | Entries and transactions are never updated or deleted | DB grants (app role has INSERT/SELECT only on `ledger_transactions` and `ledger_entries`; UPDATE is granted only on the `ledger_balances` projection, used only by the `ledger` module) **and** trigger raising on UPDATE/DELETE/TRUNCATE |
| L6 | A business event posts at most once | `UNIQUE (idempotency_key)` |
| L7 | A reversal references the original. An original is reversed at most once. | FK + partial unique index on `reverses_transaction_id` |
| L8 | Withdrawable (campaign_payable) balance never goes below zero | Checked inside the posting transaction under a row lock (§9). Verified by the invariant job. |
| L9 | Global: per currency, Σ all debits = Σ all credits | Nightly + on-demand verification job. A failure pages immediately. |
| L10 | Projections equal a full recompute from entries | Verification job |

### 4.1 Database enforcement (illustrative)

```sql
-- L1/L2: checked at COMMIT so a transaction's entries can be inserted in any order.
CREATE FUNCTION ledger_check_balanced() RETURNS trigger AS $$
DECLARE bad RECORD; n INT;
BEGIN
  SELECT count(*) INTO n FROM ledger_entries WHERE transaction_id = NEW.transaction_id;
  IF n < 2 THEN
    RAISE EXCEPTION 'ledger transaction % has % entries', NEW.transaction_id, n;
  END IF;
  SELECT currency,
         sum(CASE WHEN direction='DEBIT'  THEN amount_minor ELSE 0 END) AS dr,
         sum(CASE WHEN direction='CREDIT' THEN amount_minor ELSE 0 END) AS cr
    INTO bad
    FROM ledger_entries WHERE transaction_id = NEW.transaction_id
    GROUP BY currency
    HAVING sum(CASE WHEN direction='DEBIT' THEN amount_minor ELSE -amount_minor END) <> 0
    LIMIT 1;
  IF FOUND THEN
    RAISE EXCEPTION 'ledger transaction % unbalanced in %: dr=% cr=%',
      NEW.transaction_id, bad.currency, bad.dr, bad.cr;
  END IF;
  RETURN NULL;
END $$ LANGUAGE plpgsql;

CREATE CONSTRAINT TRIGGER ledger_entries_balanced
  AFTER INSERT ON ledger_entries
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION ledger_check_balanced();

-- L5: immutability, in addition to REVOKE UPDATE, DELETE, TRUNCATE FROM fundzim_app.
CREATE FUNCTION ledger_immutable() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'ledger tables are append-only (% on %)', TG_OP, TG_TABLE_NAME; END $$ LANGUAGE plpgsql;
CREATE TRIGGER ledger_entries_no_mutation BEFORE UPDATE OR DELETE ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION ledger_immutable();
CREATE TRIGGER ledger_entries_no_truncate BEFORE TRUNCATE ON ledger_entries
  FOR EACH STATEMENT EXECUTE FUNCTION ledger_immutable();
-- (same for ledger_transactions)
```

The per-row deferred trigger re-checks the whole transaction once per entry. That is acceptable at expected
volumes. Stage 10 may optimise it, for example with a statement-level transition table or by checking once per
`transaction_id`, provided the guarantee is preserved and tested.

## 5. Posting API (Go, sketch)

```go
type Posting struct {
    Type           TxType
    IdempotencyKey string          // deterministic from the business event
    OccurredAt     time.Time       // UTC
    Source         SourceRef       // type, id, provider, provider_reference
    Reason         string
    Lines          []Line          // {AccountRef, Direction, money.Money}
}
func (s *Service) Post(ctx context.Context, tx pgx.Tx, p Posting) (TxID, error)
```

- `Post` runs **inside the caller's DB transaction**, so a payment state change and its posting commit or
  roll back together.
- It validates L1–L4 in Go before insert, giving clear errors. The DB is the backstop.
- On `idempotency_key` conflict it returns the existing transaction ID, **after verifying that the stored
  transaction is identical** (same lines). A mismatch means the same key was used for a different posting:
  `ErrIdempotencyConflict`, alert, stop.
- Posting rules (§6) are encoded as **named functions** (`PostDonationCaptured`, `PostPayoutPaid` …) so that
  callers never assemble arbitrary lines. Free-form postings exist only as staff **adjustments**, which require:
  - the `ledger.adjustment.create` permission;
  - a reason;
  - maker-checker approval;
  - an audit event.

## 6. Posting rules and worked examples

All examples use USD. ZWG is identical with ZWG accounts, and **one transaction never mixes currencies unless
a future FX ADR defines it**. Amounts are in minor units. Values are illustrative only. The fee model (fees
deducted from the donation vs added on top) is an open business decision, and both shapes are shown where
they differ.

### 6.1 Donation captured (fees deducted from donation)

> Stage 1 ([ADR-014](adr/ADR-014-accounting-separated-from-custody.md)): capture now credits
> `campaign_unsettled`, not `campaign_payable`. Funds become available only after a settlement match (§6.2)
> and a release journal. Authoritative journals:
> [ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md) §5.

Donor gives US$50.00 (`5000`). The platform fee is 5% (`250`). The PSP deducts a processing fee of US$1.75
(`175`). Posted only when the payment becomes SUCCEEDED from an authoritative source.

**Rule: the gross donation and the PSP fee are always posted explicitly**, so that donated amounts and every
fee reconcile from the ledger alone (§1, §10). Two ledger transactions are posted **in the same DB
transaction** as the payment's move to SUCCEEDED:

1. Capture at gross. Key: `payment:{payment_id}:capture`.

| Account | Dr | Cr |
|---|---|---|
| asset:psp_clearing:{p} | 5000 | |
| liability:campaign_unsettled:{c} | | 4750 |
| revenue:platform_fees | | 250 |

Σ Dr = 5000 = Σ Cr ✔.

2. PSP processing fee. Key: `payment:{payment_id}:psp_fee`. Posted at capture if the provider reports the fee
then, otherwise at settlement with the same key. Who bears it is an open business decision (LR-028); the
ledger supports both shapes, and only the debited account differs.

Campaign bears the PSP fee:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 175 | |
| asset:psp_clearing:{p} | | 175 |

FundZim bears the PSP fee:

| Account | Dr | Cr |
|---|---|---|
| expense:psp_processing_fees | 175 | |
| asset:psp_clearing:{p} | | 175 |

Result (campaign bears): PSP clearing 4825, campaign unsettled 4575 (= 5000 − 250 − 175), platform fee revenue
250. Result (FundZim bears): PSP clearing 4825, campaign unsettled 4750, revenue 250, expense 175. The
examples below assume **the campaign bears the PSP fee**.

Variant: **donor covers fees** (donor pays 5250, campaign receives the full 5000). This is the same shape with
different numbers; the fee calculation lives in `fees`.

### 6.2 Settlement matched and release to available (reconciliation-confirmed)

When a PSP settlement line matches the capture, reconciliation posts the settlement. Key:
`settlement:{batch_id}:{payment_id}`. Under Model A the settled funds stay **with the PSP** (`psp_settled`);
no FundZim bank account is involved.

| Account | Dr | Cr |
|---|---|---|
| asset:psp_settled:{p} | 4825 | |
| asset:psp_clearing:{p} | | 4825 |

Σ 4825 = 4825 ✔. A short or over settlement posts the difference to `suspense:settlement_discrepancy:{p}`
(see the custody model §5.4); unexplained surplus is never released to a campaign.

Once the release rules hold (settlement matched, no open dispute, any hold period elapsed, campaign not
FROZEN; PD-14), the release journal makes the funds available. Key: `payment:{payment_id}:release`.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_unsettled:{c} | 4575 | |
| liability:campaign_payable:{c} | | 4575 |

Σ 4575 = 4575 ✔. If the campaign is FROZEN at release time, the credit goes to `campaign_held` instead. A
reserve policy (PD-33) splits the credit between `campaign_payable` and `campaign_reserve`.

When the PSP remits FundZim's platform fee (split at source or periodic remittance, PD-19), key
`fee_remittance:{p}:{batch_id}`: Dr `asset:fundzim_operating_bank` 250 · Cr `asset:psp_settled:{p}` 250 ✔.

### 6.3 Refund (full, after settlement, before any payout)

> Stage 1 detail for every timing (before settlement, after settlement, after payout, partial, failure):
> [payments/refund-and-reversal-flows.md](payments/refund-and-reversal-flows.md) §3.3. Shown here: refund after
> settlement and release, platform fee reversed, PSP fee not recovered and borne by the campaign (PD-20).

On approval (reservation, key `refund:{refund_id}:reserved`). The campaign's share is drawn from where the money
sits: `campaign_unsettled` → `campaign_payable` → `campaign_reserve` (the last only with FINANCE approval).

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| liability:refund_payable | | 5000 |

Σ 5000 = 5000 ✔. The campaign's debit of 4750 exceeds the 4575 released from this donation by the 175 PSP fee
it bears, so the campaign must hold at least 175 of other available funds; otherwise the refund is not
auto-approved (L8).

If the 250 platform fee was already remitted to FundZim, a fee true-up returns it to the provider-held pool
before submission (key `refund:{refund_id}:fee_true_up`): Dr `asset:psp_settled:{p}` 250 · Cr
`asset:fundzim_operating_bank` 250 ✔.

On PSP confirmation (key `refund:{refund_id}:settled`):

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 5000 | |
| asset:psp_settled:{p} | | 5000 |

Σ 5000 = 5000 ✔. A refund before settlement credits `psp_clearing` instead.

If the PSP refund FAILS after reservation (key `refund:{refund_id}:failed`), the reservation is reversed exactly:

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 5000 | |
| liability:campaign_payable:{c} | | 4750 |
| revenue:platform_fees | | 250 |

If the campaign cannot cover the refund (the funds were already paid out), the refund is never approved
automatically. It goes to FINANCE for a policy decision (PD-22, LR-080, LR-082). **L8 must never be violated.**

### 6.4 Chargeback (card)

Assume the common card pattern: the PSP debits the disputed amount **when the dispute opens**, from the
provider-held pool after settlement. The policy
shown, which is open (see P-3 and P-6 in [PAYMENTS.md](PAYMENTS.md)), is the same as for refunds: the platform
fee is reversed and the campaign bears the unrecovered PSP fee. Any separate dispute fee charged by the PSP is
posted as additional lines per policy.

Dispute opened, PSP debits US$50.00. Key: `dispute:{dispute_id}:opened`.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| asset:psp_settled:{p} | | 5000 |

If `campaign_payable` cannot cover 4750 (the funds were already paid out), the shortfall is debited to
`asset:chargeback_recoverable` instead, which keeps L8 intact. A recovery case is opened, and a **payout hold**
is placed on the campaign. If recovery later fails, a write-off moves it from `asset:chargeback_recoverable` to
`expense:chargeback_losses` (key `dispute:{id}:written_off`, approval required).

Dispute **won** (the PSP re-credits): exact inverse of the opening transaction (key `dispute:{id}:won`).
Dispute **lost**: no further movement for the disputed amount, because it was already debited. The dispute is
closed in the payments module, and recovery or write-off follows the rule above.

If a provider instead debits only when the dispute is lost, the opening transaction moves the amount from
`campaign_payable` into `campaign_held` (no PSP leg), so it cannot be withdrawn. The won/lost transactions
release it back or settle it against `psp_settled` (or `psp_clearing` if the payment had not settled).
The full set of chargeback journals, including after payout and pool funding (LR-076), is in
[ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md) §5.11.

### 6.5 Payout requested and reserved

> Stage 1: payout states follow [ADR-020](adr/ADR-020-payment-payout-state-model-revision.md)
> (`PAYOUT_REQUESTED` … `COMPLETED`, `REVERSED`); journals per
> [ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md) §5.8.

The owner requests US$4,000.00 (`400000`) from available balance. Key: `payout:{payout_id}:reserved`. This is
posted in the same DB transaction that creates the payout request (`PAYOUT_REQUESTED`), under a lock on the
campaign's payable account (§9). Only `campaign_payable` may be debited (SC-1).

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 400000 | |
| liability:campaign_payout_pending:{c} | | 400000 |

### 6.6 Payout submitted and completed (provider confirms)

On `SUBMITTED` (key `payout:{payout_id}:submitted`), the obligation moves to the provider's execution:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payout_pending:{c} | 400000 | |
| liability:payout_in_transit:{p} | | 400000 |

A payout in `UNKNOWN` posts **nothing**; the amount stays in `payout_in_transit` until resolved.

On `COMPLETED` from authoritative provider state (key `payout:{payout_id}:completed`). Any payout fee is posted
as separate lines per fee policy (PD-23).

| Account | Dr | Cr |
|---|---|---|
| liability:payout_in_transit:{p} | 400000 | |
| asset:psp_settled:{p} | | 400000 |

### 6.7 Payout failed, rejected, cancelled or reversed

Funds return to available (or to `campaign_held` if the campaign is FROZEN).

| Event | Key | Dr | Cr |
|---|---|---|---|
| Rejected / cancelled before submission | `payout:{payout_id}:released` | `campaign_payout_pending:{c}` 400000 | `campaign_payable:{c}` 400000 |
| Failed after submission (authoritative) | `payout:{payout_id}:failed` | `payout_in_transit:{p}` 400000 | `campaign_payable:{c}` 400000 |
| Reversed after completion (rail returns funds) | `payout:{payout_id}:reversed` | `asset:psp_settled:{p}` 400000 | `campaign_payable:{c}` 400000 |

Each row balances (400000 = 400000 ✔). A reversal also raises an ops case.

### 6.8 Campaign frozen

Key: `campaign:{id}:freeze:{n}`. The available balance moves to held, so payouts are structurally impossible
even if a code-path check were missed.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | X | |
| liability:campaign_held:{c} | | X |

Unfreezing posts the inverse (`campaign:{id}:unfreeze:{n}`). While FROZEN, captures still credit
`campaign_unsettled` (nothing there is withdrawable), and releases and payout returns credit `campaign_held`
instead of `campaign_payable`. A payout already `SUBMITTED` stays in `payout_in_transit` until the provider
resolves it.

### 6.9 Reversal / correction

Mistakes are corrected by a new transaction with `reverses_transaction_id = <original>`, exactly mirroring the
original's lines with directions swapped. A correct replacement transaction follows if needed. Both require a
reason, an authorised actor (system rule or staff with maker-checker) and an audit event. The original is
never edited.

## 7. Business time vs posting time

- `occurred_at` is when the business event happened (provider-reported time where trustworthy, otherwise
  receipt time).
- `posted_at` is when FundZim recorded it.
- Both are UTC. Reports state which one they use.
- Period-closing rules (for example "no postings with `occurred_at` in a closed period") are Stage 17 decisions.

## 8. Balances and projections

- **Authoritative balance** of an account = Σ(entries in its normal direction) − Σ(entries in the opposite
  direction), per account (hence per currency).
- **Projection** `ledger_balances(account_id, currency, balance_minor, last_entry_id, updated_at)` is allowed
  for performance. Rules:
  - it is updated **in the same DB transaction** as the postings that change it (`UPDATE ... SET balance_minor =
    balance_minor + $delta` on this projection table is the *only* place such arithmetic is permitted, and only
    inside `ledger`);
  - it is never written by any other module;
  - it is verified against a full recompute nightly and on demand. A mismatch is an L10 incident, and the
    projection is **rebuilt from entries**, never the other way round.
- Product-facing figures, all derived:

  | Figure | Derivation |
  |---|---|
  | **Raised (gross)** per campaign per currency | Σ gross amounts of `DONATION_CAPTURED` ledger transactions for the campaign (the `asset:psp_clearing` debit leg, §6.1), less refunds and lost disputes |
  | **Awaiting settlement** | `campaign_unsettled` (captured, not yet settled and released) |
  | **Available to withdraw** | `campaign_payable` balance, subject to payout holds/policy checks |
  | **Reserved** | `campaign_reserve` (holdback per PD-33) |
  | **Pending payout** | `campaign_payout_pending` plus this campaign's payouts in `payout_in_transit` (from payout records) |
  | **Held** | `campaign_held` |

  Point-in-time balances use `occurred_at`/`posted_at` filters on entries.

## 9. Concurrency

- Postings that **decrease** a constrained balance (payout reservation, refund reservation, freeze) must:
  1. lock the relevant projection row(s) (`SELECT ... FOR UPDATE`) in a **consistent global order** (sorted by
     account id) to avoid deadlocks;
  2. check sufficiency;
  3. insert entries and update the projection;
  4. commit.

  Two concurrent payout requests on the same campaign therefore serialise, and the second sees the reduced
  balance.
- Postings that only **increase** a liability (donations) also update the projection row under a row lock.
  For a viral campaign, this row becomes a hot spot. Mitigations, in order:
  1. keep transactions short;
  2. batch projection updates from a per-account queue while still committing entries synchronously;
  3. split into N sub-accounts summed on read.

  None of these relaxes L1–L10.
- Isolation: `READ COMMITTED` plus explicit row locks for posting. Verification jobs use `REPEATABLE READ`
  snapshots so totals are consistent.
- Required tests (Stage 10/19):
  - concurrent donations on one campaign: the final balance equals the sum;
  - concurrent payout requests exceeding the balance: exactly the right number succeed;
  - the same idempotency key posted concurrently from two workers: one transaction is stored and both callers
    receive its ID;
  - a crash between the provider callback and the commit: no posting and the inbox is reprocessed, so the
    result is a single posting.

## 10. Reconciliation interface (Stage 17)

`reconciliation` reads ledger, payments and payouts. It writes only its own tables, plus ledger postings
through named rules (for example settlement received, unmatched funds to suspense). Matching is three-way:

1. **Provider report line** ↔ **payment/payout/refund record** by provider reference and our reference.
2. **Record** ↔ **ledger transaction** by `source_id` and `idempotency_key`.
3. **Amounts and currency** equal at each hop. Fees match the recorded schedule version.

Mismatch classes:

- provider has it, we don't (unknown funds → suspense + case);
- we have SUCCEEDED, provider doesn't (critical → case, payout hold on campaign);
- amount or currency differs;
- status differs (for example provider refunded, we didn't record);
- duplicate.

Mismatches create cases and alerts. **They are never auto-corrected by editing records.** Corrections are
postings with reasons and approval.

## 11. What must never happen

- Any code outside `internal/ledger` inserting into ledger tables, or any code anywhere updating or deleting
  them.
- A balance stored as a mutable column outside the controlled projection (`campaign.raised += x`,
  `user.balance -= y`).
- A posting triggered by a browser redirect, client claim or unverified webhook.
- A transaction mixing currencies, or any sum across currencies.
- Floats anywhere in the posting path.
- "Fixing" an imbalance or projection drift by editing rows, disabling the trigger or relaxing a constraint.
  Imbalance means **stop, alert, investigate** (see CLAUDE.md).
- Posting a payout as COMPLETED before authoritative provider confirmation.
- Reusing an idempotency key for a different posting.
- Reading KYC or payout-account details into ledger metadata. The ledger stores references, not personal data.

## 12. Open questions

| # | Question | Notes |
|---|---|---|
| G-1 | Custody model, and whether ledger balances are FundZim balance-sheet items | LEGAL_REVIEW_REQUIRED (LR-001, LR-002) + accountant |
| G-2 | Fee model (deducted / on top / tip) and who bears PSP fees | Stage 12 (LR-028) |
| G-3 | Fee treatment on refunds and chargebacks | Stage 12 |
| G-4 | Treatment of funds on CANCELLED campaigns (refund vs payout) | Product + LEGAL_REVIEW_REQUIRED (LR-019) |
| G-5 | Period close and accounting export format for external accountants | Stage 17 |
| G-6 | Whether `asset:settlement_bank` exists at all (depends on G-1) | Stage 1: not in Model A (ADR-013, ADR-014); revisit only if LR-001 changes the model |
