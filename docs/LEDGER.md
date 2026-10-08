# FundZim — Ledger Architecture

> Status: Stage 0 specification. Implementation is **Stage 10**, preceded by schema design in Stage 2. Nothing
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

### 3.2 Chart of accounts

Every account exists **per currency**: there is a USD instance and a ZWG instance. Codes are hierarchical, and
the owner reference identifies per-campaign and per-provider accounts.

| Code pattern | Type | Purpose |
|---|---|---|
| `asset:psp_receivable:{provider}` | ASSET | Funds collected by a PSP and due to settle per FundZim's arrangements. |
| `asset:settlement_bank:{account}` | ASSET | A FundZim-controlled bank/settlement account, **only if** the custody model includes one (LEGAL_REVIEW_REQUIRED, LR-001, G-6). |
| `asset:psp_payout_float:{provider}` | ASSET | Amounts prefunded or held at a provider for payouts, if the model requires it (LEGAL_REVIEW_REQUIRED, LR-001/LR-003). |
| `asset:chargeback_recoverable` | ASSET | Lost-chargeback amounts FundZim seeks to recover. |
| `liability:campaign_payable:{campaign_id}` | LIABILITY | Net donations owed to the campaign beneficiary. **Available to withdraw** = this balance (subject to holds). |
| `liability:campaign_payout_pending:{campaign_id}` | LIABILITY | Reserved for an approved or in-flight payout. Not available to withdraw again. |
| `liability:campaign_held:{campaign_id}` | LIABILITY | Frozen or disputed amounts (campaign FROZEN, dispute open). |
| `liability:refund_payable` | LIABILITY | Approved refunds in flight to donors. |
| `revenue:platform_fees` | REVENUE | FundZim platform fees. |
| `expense:psp_processing_fees` | EXPENSE | Provider fees borne by FundZim (if the fee model makes FundZim bear them). |
| `expense:chargeback_losses` | EXPENSE | Unrecovered chargebacks borne by FundZim (policy-dependent). |
| `equity:platform` | EQUITY | Opening balances and platform capital movements (rare, staff-adjusted). |
| `suspense:unmatched:{provider}` | ASSET or LIABILITY (one per direction) | Reconciliation-discovered funds not yet attributable. Must trend to zero. Monitored. |

Accounts are created lazily and idempotently: the first posting for a campaign in a currency creates its
accounts. Account currency is immutable.

### 3.3 Tables (conceptual)

```sql
CREATE TABLE ledger_accounts (
  id              UUID PRIMARY KEY,                    -- UUIDv7
  code            TEXT        NOT NULL,                -- e.g. liability:campaign_payable:{uuid}
  type            TEXT        NOT NULL CHECK (type IN ('ASSET','LIABILITY','EQUITY','REVENUE','EXPENSE')),
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

Donor gives US$50.00 (`5000`). The platform fee is 5% (`250`). The PSP deducts a processing fee of US$1.75
(`175`) at settlement. Posted only when the payment becomes SUCCEEDED from an authoritative source.

**Rule: the gross donation and the PSP fee are always posted explicitly**, so that donated amounts and every
fee reconcile from the ledger alone (§1, §10). Two ledger transactions are posted **in the same DB
transaction** as the payment's move to SUCCEEDED:

1. Capture at gross. Key: `payment:{payment_id}:capture`.

| Account | Dr | Cr |
|---|---|---|
| asset:psp_receivable:{psp} | 5000 | |
| liability:campaign_payable:{c} | | 4750 |
| revenue:platform_fees | | 250 |

Σ Dr = 5000 = Σ Cr ✔.

2. PSP processing fee. Key: `payment:{payment_id}:psp_fee`. Who bears it is an open business decision
(LR-028); the ledger supports both shapes, and only the debited account differs.

Campaign bears the PSP fee:

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 175 | |
| asset:psp_receivable:{psp} | | 175 |

FundZim bears the PSP fee:

| Account | Dr | Cr |
|---|---|---|
| expense:psp_processing_fees | 175 | |
| asset:psp_receivable:{psp} | | 175 |

Result (campaign bears): PSP receivable 4825, campaign payable 4575 (= 5000 − 250 − 175), platform fee revenue
250. Result (FundZim bears): PSP receivable 4825, campaign payable 4750, revenue 250, expense 175. The
examples below assume **the campaign bears the PSP fee**.

Variant: **donor covers fees** (donor pays 5250, campaign receives the full 5000). This is the same shape with
different numbers; the fee calculation lives in `fees`.

### 6.2 Settlement received from PSP (reconciliation-confirmed)

The PSP settles US$4,825.00 for a batch (`482500`). Key: `settlement:{provider}:{settlement_ref}`.
Whether a FundZim-controlled bank account appears here at all depends on the custody model
(LEGAL_REVIEW_REQUIRED, LR-001). If settlement goes directly to beneficiaries, this rule is replaced by payout-side
rules.

| Account | Dr | Cr |
|---|---|---|
| asset:settlement_bank:{account} | 482500 | |
| asset:psp_receivable:{psp} | | 482500 |

### 6.3 Refund (full, after capture, before any payout)

The refund of US$50.00 is approved and the PSP confirms it. Key: `refund:{refund_id}:settled`. The fee policy
on refund (is the platform fee returned?) is open. Shown: platform fee reversed, PSP fee not recovered and
borne by the campaign.

On approval (reservation, key `refund:{refund_id}:reserved`):

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4575 | |
| revenue:platform_fees | 250 | |
| liability:refund_payable | | 4825 |

On PSP confirmation of the refund (the donor receives 5000, and the PSP debits FundZim's receivable for 5000):

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 4825 | |
| liability:campaign_payable:{c} | 175 | |
| asset:psp_receivable:{psp} | | 5000 |

The campaign absorbs the unrecovered PSP fee (175) in this policy. If campaign payable would go negative (the
funds were already paid out), the refund must not be approved automatically. It goes to FINANCE for a policy
decision. **L8 must never be violated.**

If the PSP refund FAILS after reservation (key `refund:{refund_id}:failed`), reverse the reservation exactly:

| Account | Dr | Cr |
|---|---|---|
| liability:refund_payable | 4825 | |
| liability:campaign_payable:{c} | | 4575 |
| revenue:platform_fees | | 250 |

### 6.4 Chargeback (card)

Assume the common card pattern: the PSP debits the disputed amount **when the dispute opens**. The policy
shown, which is open (see P-3 and P-6 in [PAYMENTS.md](PAYMENTS.md)), is the same as for refunds: the platform
fee is reversed and the campaign bears the unrecovered PSP fee. Any separate dispute fee charged by the PSP is
posted as additional lines per policy.

Dispute opened, PSP debits US$50.00. Key: `dispute:{dispute_id}:opened`.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 4750 | |
| revenue:platform_fees | 250 | |
| asset:psp_receivable:{psp} | | 5000 |

If `campaign_payable` cannot cover 4750 (the funds were already paid out), the shortfall is debited to
`asset:chargeback_recoverable` instead, which keeps L8 intact. A recovery case is opened, and a **payout hold**
is placed on the campaign. If recovery later fails, a write-off moves it from `asset:chargeback_recoverable` to
`expense:chargeback_losses` (key `dispute:{id}:written_off`, approval required).

Dispute **won** (the PSP re-credits): exact inverse of the opening transaction (key `dispute:{id}:won`).
Dispute **lost**: no further movement for the disputed amount, because it was already debited. The dispute is
closed in the payments module, and recovery or write-off follows the rule above.

If a provider instead debits only when the dispute is lost, the opening transaction moves the amount from
`campaign_payable` into `campaign_held` (no PSP leg), so it cannot be withdrawn. The won/lost transactions
release it back or settle it against `psp_receivable`.

### 6.5 Payout requested and reserved

The owner requests US$4,000.00 (`400000`) from available balance. Key: `payout:{payout_id}:reserved`. This is
posted in the same DB transaction that creates the payout request, under a lock on the campaign's payable
account (§9).

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | 400000 | |
| liability:campaign_payout_pending:{c} | | 400000 |

### 6.6 Payout paid (provider confirms)

Key: `payout:{payout_id}:paid`. Any payout fee is posted as separate lines per fee policy.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payout_pending:{c} | 400000 | |
| asset:psp_receivable:{psp} *(or asset:psp_payout_float / settlement_bank, per custody model)* | | 400000 |

### 6.7 Payout failed or cancelled after reservation

Key: `payout:{payout_id}:failed`. Funds return to available.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payout_pending:{c} | 400000 | |
| liability:campaign_payable:{c} | | 400000 |

`RETURNED` after PAID (the rail bounces funds back) posts the inverse of 6.6 into `campaign_payable`, with
key `payout:{payout_id}:returned`, and raises an ops case.

### 6.8 Campaign frozen

Key: `campaign:{id}:freeze:{n}`. The available balance moves to held, so payouts are structurally impossible
even if a code-path check were missed.

| Account | Dr | Cr |
|---|---|---|
| liability:campaign_payable:{c} | X | |
| liability:campaign_held:{c} | | X |

Unfreezing posts the inverse (`campaign:{id}:unfreeze:{n}`). Donations that arrive while the campaign is
FROZEN (in flight before the freeze) post to `campaign_held`, not `campaign_payable`.

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
  | **Raised (gross)** per campaign per currency | Σ gross amounts of `DONATION_CAPTURED` ledger transactions for the campaign (the `asset:psp_receivable` debit leg, §6.1), less refunds and lost disputes |
  | **Available to withdraw** | `campaign_payable` balance, subject to payout holds/policy checks |
  | **Pending payout** | `campaign_payout_pending` |
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
- Posting a payout as PAID before authoritative provider confirmation.
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
| G-6 | Whether `asset:settlement_bank` exists at all (depends on G-1) | Stage 1 (LR-001) |
