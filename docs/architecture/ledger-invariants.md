# Ledger Invariants

**Stage 2 — design only.** For every ledger invariant: the statement, where it is enforced, what happens when
it fails, the alert severity and the test that proves the database half. Sources: [LEDGER.md §4](../LEDGER.md)
(L1–L10), [settlement-and-custody-model.md §4](../ledger/settlement-and-custody-model.md) (SC-1 – SC-6),
[ADR-006](../adr/ADR-006-double-entry-ledger.md), [ADR-023](../adr/ADR-023-ledger-posting-architecture.md).
L11–L14 are **new in Stage 2** (proposed additions to LEDGER §4 via ADR-023). Schema:
[ledger-schema.md](../database/ledger-schema.md); draft SQL [`0006_ledger.sql`](../../design/sql/0006_ledger.sql);
tests [`tests/ledger_test.sql`](../../design/sql/tests/ledger_test.sql) (case names below).
Jobs: [background-processing.md §6.6](background-processing.md).

Enforcement layers: **Go** (`internal/ledger` validation before insert, clear errors) · **DB constraint**
(CHECK / UNIQUE / FK) · **DB trigger** (immediate or deferred) · **Grant** (0018) · **Job** (verification run
stored in `ledger_invariant_runs`). The database layer is the backstop: a Go bug must not be able to break an
invariant. Any DB-layer rejection that Go should have prevented raises at least a SEV2 alert
(`ledger.db_guard_rejected`) because it means Go and SQL disagree.

Severity: SEV1 = page immediately, stop affected postings/payouts; SEV2 = page in hours, FINANCE queue; SEV3 =
next business day.

---

## 1. Summary

| # | Invariant | Go | DB constraint | DB trigger | Grant | Job | Test |
|---|---|---|---|---|---|---|---|
| L1 | Balanced per journal and currency | ✔ | | deferred `check_transaction` | | L9 nightly | `unbalanced_journal_rejected_at_commit` |
| L2 | ≥ 2 entries | ✔ | | deferred | | | `single_entry_journal_rejected`, `header_without_entries_rejected` |
| L3 | amount > 0, direction carries sign | ✔ | `ck_ledger_entries_amount_positive` | | | | `zero_amount_rejected`, `negative_amount_rejected` |
| L4 | Entry currency = account currency; account currency immutable | ✔ | `fk_ledger_entries_account_currency` | forbid_mutation on accounts | | | `entry_currency_differs_from_account_currency_rejected`, `update_account_currency_rejected` |
| L5 | Append-only | | | `app.forbid_mutation()` | INSERT/SELECT only | | `update_entry_rejected`, `delete_entry_rejected`, `truncate_entries_rejected`, `update_transaction_rejected` |
| L6 | Event posts at most once | ✔ (equivalence check) | `uq_ledger_transactions_idempotency_key` | | | | `duplicate_idempotency_key_rejected` |
| L7 | Reversal mirrors the original; at most once | ✔ | `uq_ledger_transactions_reverses`, composite FK, `ck_…_reversal_rule` | deferred mirror check | | | `reversal_ok`, `second_reversal_of_same_transaction_rejected`, `reversal_not_mirroring_original_rejected` |
| L8 | `campaign_payable` ≥ 0 | ✔ (lock + check) | `ck_ledger_balances_non_negative` (SYNC row) | entry trigger updates projection | | L10 | `campaign_payable_negative_rejected`, `payout_exceeding_available_rejected`, `payout_of_exact_available_ok` |
| L9 | Global Σ Dr = Σ Cr per currency | | | | | nightly + on demand (`v_trial_balance`) | `final_trial_balance_and_projection_recompute` |
| L10 | Projection = recompute | | `ck_ledger_balances_consistent` | guard against direct UPDATE | (UPDATE inert) | nightly (`v_balance_drift`) | `direct_projection_update_rejected`, `fold_deferred_balances_exact_and_idempotent` |
| L11 | Journal sealed at commit | | | `before_insert_entry` (xid8) | | | `append_entry_to_committed_journal_rejected` |
| L12 | Lines conform to the posting rule | ✔ | rule-line CHECKs | deferred rule-line check | rules SELECT-only | | `sc1_…`, `sc5_…`, `required_rule_line_missing_rejected`, `wrong_source_type_rejected`, `two_campaigns_in_one_release_rejected` |
| L13 | Every OBLIGATION/RECOVERABLE account ≥ 0; all are SYNC | ✔ | `ck_ledger_balances_non_negative`, `ck_ledger_accounts_guarded_is_sync` | | | | `projection_modes_by_kind` |
| L14 | Manual journals equal the approved lines; maker ≠ checker | ✔ | `ck_ledger_adjustments_maker_checker`, `…_approval`, `…_lines_hash` | approval check + deferred line comparison | | | `adjustment_self_approval_rejected`, `adjustment_approval_of_different_hash_rejected`, `adjustment_posted_lines_differ_from_approved_rejected`, `reversal_without_approved_adjustment_rejected`, `write_off_without_approval_rejected` |
| SC-1 | Payout reservation debits only `campaign_payable` | ✔ | `ck_ledger_posting_rule_lines_sc1` | deferred rule-line check | | | `sc1_payout_reservation_from_unsettled_rejected`, `sc1_rule_line_widening_rejected` |
| SC-2 | Pool integrity | ✔ (after refund/reversal/payout journals) | | | | per event + daily (`v_pool_integrity` + attribution) | `canonical_example_verification_views` |
| SC-3 | `psp_settled` = provider statement balance (± open suspense) | | | | | reconciliation run | `reconciliation_test.sql` (Stage 17 full) |
| SC-4 | Old suspense items escalated | | | | | daily ageing | — (job, Stage 17) |
| SC-5 | Operating bank touched only by allow-listed rules | ✔ | `ck_ledger_posting_rule_lines_sc5` | deferred rule-line check | | | `sc5_operating_bank_touched_by_release_rejected`, `sc5_rule_line_on_operating_bank_rejected`, `manual_adjustment_cannot_touch_operating_bank` |
| SC-6 | One currency per journal | ✔ | `fk_ledger_entries_transaction_currency` | | | | `mixed_currency_journal_rejected` |
| P-1 | Periods: no posting into a CLOSED period unless the rule allows prior-period | ✔ | `ex_ledger_periods_no_overlap`, `ck_ledger_periods_maker_checker` | header trigger | | | `posting_into_closed_period_rejected`, `late_provider_fact_posts_into_open_period`, `closed_period_cannot_reopen`, `period_close_self_approval_rejected` |

## 2. Detail

### L1 — balanced per journal and currency
- **Statement.** For every journal, Σ debit amounts = Σ credit amounts. With SC-6 the journal has one currency,
  so this is the per-currency rule of CLAUDE.md §5.
- **Enforcement.** Go builder rejects before insert. DB: deferred constraint trigger
  `trg_ledger_transactions_check` (AFTER INSERT on the **header**, `DEFERRABLE INITIALLY DEFERRED`) runs once per
  journal at COMMIT and sums its entries. Complete because of L11.
- **Failure.** COMMIT fails; the whole business transaction (status change, audit, outbox) rolls back. The
  operation is retried only after the code is fixed.
- **Alert.** DB-level rejection: SEV2 (`ledger.db_guard_rejected`). Global imbalance found by L9: SEV1.

### L2 — at least two entries
Same trigger; a header with zero or one entry fails at COMMIT. Running `SET CONSTRAINTS ALL IMMEDIATE` makes it
fire right after the header insert, which fails closed.

### L3 — positive amounts
`CHECK (amount_minor > 0)`; the direction carries the sign. Zero-amount lines (e.g. a 0 platform fee) are
omitted by the builder.

### L4 — currency of entry = account; account currency immutable
Composite FK `(account_id, currency) → ledger_accounts(id, currency)`. Accounts are append-only
(`app.forbid_mutation()`), so currency, type and class never change. `ck_ledger_accounts_class_spec` ties type,
normal balance, kind and owner type to the chart.

### L5 — append-only
`app.forbid_mutation()` BEFORE UPDATE/DELETE (row) and BEFORE TRUNCATE (statement) on accounts, transactions,
entries, posting batches, rules, rule lines and invariant runs; DELETE/TRUNCATE blocked on balances, adjustments
and periods. Grants (0018): INSERT/SELECT only on journal tables. **Failure:** the statement raises
`restrict_violation`; any attempt in production is a SEV1 security event (it means code or a human tried to edit
history).

### L6 — at most once
`UNIQUE (idempotency_key)`. Go returns the existing journal for an equivalent replay and raises
`ErrIdempotencyConflict` (SEV1, page) for a different journal under the same key.

### L7 — reversals
`reverses_transaction_id` has a composite FK with the currency (same currency as the original) and a partial
unique index (reversed at most once). `ck_ledger_transactions_reversal_rule` ties it to rule `REVERSAL`, which
requires an approved adjustment. The deferred check compares the multisets `(account, opposite direction,
amount)`; any difference is rejected. Business inverses that are not corrections (`REFUND_FAILED`, `DISPUTE_WON`,
`CAMPAIGN_UNFROZEN`) are their own named rules, not `REVERSAL`.

### L8 — available never below zero
- **Statement.** `campaign_payable:{c}` ≥ 0 at every commit.
- **Enforcement.** Go: lock the SYNC projection row `FOR UPDATE` (ascending account id), check, then post
  (LEDGER §9). DB: the entry trigger updates the SYNC row in the same transaction;
  `ck_ledger_balances_non_negative` rejects a negative result. Because the UPDATE re-reads the latest committed
  version under READ COMMITTED, two concurrent reservations cannot both succeed even if Go's check were skipped.
- **Depends only on SYNC accounts.** `campaign_payable` is `SYNC` by construction
  (`ck_ledger_accounts_guarded_is_sync`); DEFERRED accounts never participate in L8.
- **Failure.** Reservation fails with `LEDGER_INSUFFICIENT_FUNDS`; refunds/chargebacks that cannot be covered go
  to FINANCE (recoverables), never to a negative balance.
- **Alert.** A DB-level rejection (Go missed it): SEV2. A negative SYNC balance found by a job (impossible unless
  constraints were disabled): SEV1.

### L9 — global trial balance
Job `ledger.invariant_verify{scope=L9}` nightly 01:00 UTC + on demand, `REPEATABLE READ`, per currency, using
`ledger.v_trial_balance`; writes a `ledger_invariant_runs` row. **Failure:** SEV1 (ALR-L01), payout gate fails
closed, `ledger.invariant_failed` event.

### L10 — projection = recompute
- SYNC rows: `balance_minor` = recompute over all entries. DEFERRED rows: `balance_minor` = recompute over entries
  with `created_txid < folded_through_txid`. `ledger.v_balance_drift` lists violations (must be empty).
- `ck_ledger_balances_consistent` keeps totals and balance consistent; direct UPDATE is rejected by
  `trg_ledger_balances_guard_update`.
- The fold (`ledger.fold_deferred_balances()`) is exact and idempotent (snapshot-xmin watermark).
- **Failure.** SEV1 (ALR-L02). The projection is **rebuilt from entries** (`ledger.rebuild_balance`, break-glass
  with maker-checker), never the other way round.

### L11 — sealed journals (new)
- **Statement.** Entries can only be added in the DB transaction that created their header.
- **Enforcement.** Header and entries carry `created_txid xid8` forced to `pg_current_xact_id()`; the entry
  trigger rejects a mismatch (`… is sealed`).
- **Why.** It makes the once-per-journal deferred check complete and stops anyone appending lines to a historic
  journal. **Alert:** SEV1 (attempt to alter history).

### L12 — posting-rule conformance (new; carries SC-1 and SC-5)
- Every entry's `(direction, account_class)` must be listed for the journal's rule; every `is_required` line must
  be present; `source_type` must be allowed for the rule; at most one campaign and one provider per journal
  (except `ADJUSTMENT`/`REVERSAL`).
- Deferred check + rule-line table CHECKs (SC-1, SC-5, no payout float). Rules are migration-only reference data.
- **Failure:** COMMIT fails, SEV2.

### L13 — obligations and recoverables are non-negative and SYNC (new; generalises L8)
- All OBLIGATION (`campaign_*`, `payout_in_transit`, `refund_payable`) and RECOVERABLE accounts ≥ 0, and all are
  `SYNC`. A negative obligation would mean FundZim records owing a campaign "less than nothing", which only a
  posting bug produces. Open question LQ-2 (`campaign_unsettled`).

### L14 — approved manual journals (new)
- Rules with `requires_approval` (ADJUSTMENT, REVERSAL, WRITE_OFF, SETTLEMENT_DISCREPANCY_RESOLVED,
  REFUND_FUND_SHORTFALL, DISPUTE_FUNDING) need an `APPROVED`, unexpired adjustment of matching kind, rule,
  currency and target; maker ≠ checker (CHECK); the checker approves the **hash** of the proposed lines; the
  posted entries must equal the proposed lines (deferred check); request fields are immutable; an adjustment
  posts at most one reversal and one result journal.
- **Failure:** rejected; denied self-approval attempts are recorded as `outcome='denied'` audit events (SEV3,
  repeated → SEV2).

### SC-1 — payout reservation only from `campaign_payable`
`PAYOUT_RESERVED` lines: Dr `campaign_payable` (required, the only debit allowed), Cr `campaign_payout_pending`.
Enforced by the rule-line CHECK (cannot be widened without editing the constraint) and the deferred check.
Failure: COMMIT fails, SEV2.

### SC-2 — pool integrity
- **Statement.** Per provider and currency: Σ obligations owed from that provider's pool ≤ `psp_clearing +
  psp_settled`.
- **Enforcement.** Go checks after every refund, reversal and payout journal; job `invariant_verify{scope=SC2}`
  per event (outbox consumer) and daily 02:00 UTC. `ledger.v_pool_integrity` gives the aggregate per-currency
  form; the per-provider form attributes campaign obligations to providers through payment/payout records
  (campaign accounts are per campaign, not per provider — LQ-4). Reads use `v_balances_current` because provider
  accounts are DEFERRED.
- **Failure.** SEV1 (ALR-L03): `risk` places a `PROVIDER_HOLD` for the provider and currency (automated payouts
  stop); FINANCE incident until funded (`*_FUNDING` journals, LR-076).

### SC-3 — provider-held pool equals the statement
Reconciliation compares `psp_settled:{p}` (exact current balance) with the imported closing balance
(`reconciliation_imports.closing_balance_minor`) ± open suspense. A difference opens a
`SETTLEMENT_SHORT`/`SETTLEMENT_OVER` discrepancy; SEV per amount threshold; never auto-corrected.

### SC-4 — suspense ageing
Daily `reconciliation.discrepancy_ageing`: discrepancies past `sla_due_at` are escalated (FINANCE queue; SEV2,
SEV1 above a configured amount). The suspense balance itself is monitored and must trend to zero.

### SC-5 — operating bank allow-list
Only `FEE_REMITTANCE`, `REFUND_FEE_TRUE_UP(_REVERSED)`, `REFUND_FUND_SHORTFALL`, `DISPUTE_FUNDING(_RETURNED)`,
`RECOVERY_RECEIVED`, `SETTLEMENT_DIRECT` (and `REVERSAL` of these) may touch `asset:fundzim_operating_bank`.
Rule-line CHECK + deferred check; `ADJUSTMENT` excludes it. Failure: COMMIT fails; an attempt from code is SEV2,
since it may indicate an attempt to route donor money to FundZim (Model B in substance).

### SC-6 — single currency
`ledger_transactions.currency` + composite FK `(transaction_id, currency)` from entries; reversals, batches and
adjustments carry the currency in their composite FKs too. There is no FX account (ADR-018).

### P-1 — periods
See [ledger-schema.md §5](../database/ledger-schema.md). Close requires maker ≠ checker, and CLOSED is terminal.

## 3. Things the database deliberately does not check

| Rule | Where it lives | Why not in SQL |
|---|---|---|
| Release rules (settlement matched, no open dispute, hold elapsed, not FROZEN — PD-14) | `payments`/`ledger` Go + reconciliation state | Needs other modules' data; the ledger is domain-agnostic |
| Which campaign/provider owns a payment | Go (named rule builders) | No FK into other modules by design |
| Refund recovery order (held → payable → reserve → unsettled) | Go | Policy (PD-22), not an invariant |
| Payout eligibility, holds, approvals | `payouts`, `risk` (DB-guarded there) | Owned by those modules |
