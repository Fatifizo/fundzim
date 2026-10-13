# Stage 10 — Chart of accounts

The chart is the Stage 1/2 design ([settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md)
§3.2, ADR-014), implemented twice and kept identical by a test (`TestChartMatchesSQL`, internal/ledger/ledger_test.go): the SQL function
`ledger.account_class_signature()` and the Go map `ledger.Chart`. Adding a class is a migration plus an ADR-014
amendment. **No accountant has reviewed it** (KI-S10-02).

## 1. Classes

| Class | Type | Normal | Kind | Owner | Meaning (not a custody claim) |
|---|---|---|---|---|---|
| `asset:psp_clearing` | ASSET | DEBIT | CLAIM_ON_PROVIDER | provider | Captured, not yet settled by the provider |
| `asset:psp_settled` | ASSET | DEBIT | PROVIDER_POOL | provider | Settled into the provider-held pool (needs settlement evidence) |
| `asset:fundzim_operating_bank` | ASSET | DEBIT | FUNDZIM_CASH | platform | FundZim's own fee income only (Model A) |
| `asset:chargeback_recoverable` | ASSET | DEBIT | RECOVERABLE | platform | Chargeback amounts to recover |
| `asset:refund_recoverable` | ASSET | DEBIT | RECOVERABLE | campaign | Refund shortfall to recover from a campaign |
| `asset:psp_payout_float` | ASSET | DEBIT | PROVIDER_POOL | provider | Not used in Model A |
| `liability:campaign_unsettled` | LIABILITY | CREDIT | OBLIGATION | campaign | Owed to a campaign once settled |
| `liability:campaign_payable` | LIABILITY | CREDIT | OBLIGATION | campaign | Released, available for payout (Stage 11) |
| `liability:campaign_reserve` | LIABILITY | CREDIT | OBLIGATION | campaign | Reserve holdback |
| `liability:campaign_payout_pending` | LIABILITY | CREDIT | OBLIGATION | campaign | Reserved for a requested payout |
| `liability:payout_in_transit` | LIABILITY | CREDIT | OBLIGATION | provider | Submitted to a provider |
| `liability:campaign_held` | LIABILITY | CREDIT | OBLIGATION | campaign | Frozen campaign funds |
| `liability:refund_payable` | LIABILITY | CREDIT | OBLIGATION | platform | Approved refunds not yet settled |
| `revenue:platform_fees` | REVENUE | CREDIT | RESULT | platform | Platform fee income (fees not configured: LR items) |
| `expense:psp_processing_fees` | EXPENSE | DEBIT | RESULT | platform | Provider fees FundZim bears |
| `expense:chargeback_losses` | EXPENSE | DEBIT | RESULT | platform | |
| `expense:refund_losses` | EXPENSE | DEBIT | RESULT | platform | |
| `equity:platform` | EQUITY | CREDIT | RESULT | platform | |
| `suspense:settlement_discrepancy` | SUSPENSE | DEBIT | SUSPENSE | provider | Unexplained settlement differences — investigated, never used to force balance |

Obligation and recoverable accounts are **non-negative** (L13) and use the SYNC projection; the others are
DEFERRED (folded by the maintenance job; exact balances via `v_balances_current`).

## 2. Account identity, numbering and scope

- The account **code** is deterministic: `<class>` for platform accounts, `<class>:<owner uuid>` for campaign- and
  provider-owned classes (CHECK `ck_ledger_accounts_code`; `ledger.ParseAccountCode` refuses an owned class without
  an owner). There is no separate numeric account number: the code is the stable identifier, unique per
  (code, currency) (`uq_ledger_accounts_code_currency`); the environment is the database's, so codes never collide
  across TEST and PRODUCTION ledgers.
- Accounts are created lazily by the posting path the first time a journal names them (audited
  `ledger.account.created`), in the journal's currency and the database's environment.
- **Scope:** one account per (class, owner, currency). A currency's accounts never net against another's.

## 3. Status

| Status | Postings | Transitions |
|---|---|---|
| ACTIVE | all rule-conforming journals | → FROZEN, → CLOSED |
| FROZEN | only journals backed by an APPROVED adjustment (maker-checker) | → ACTIVE, → CLOSED |
| CLOSED | none | terminal; requires a zero balance |

Transitions use `app.guard_transition` (machine `ledger_account`), need `ledger.account.manage` (step-up) and a
reason, and are audited and published (`ledger.account_frozen`, `ledger.account_status_changed`). The entry trigger
reads the account row `FOR SHARE`, so a freeze and a concurrent posting serialise (tested:
`TestLedgerAccountFreezeVersusConcurrentPostings`).

## 4. Restricted funds and agency

Whether campaign money is a FundZim liability, an agency balance or off-balance-sheet is **LEGAL_REVIEW_REQUIRED**
(LR-002). The chart records obligations in FundZim's books as the Stage 1 design chose; this is an accounting
representation for orchestration and reconciliation, not a determination of legal ownership, trust or restricted
fund status. No restricted-fund treatment is implemented.
