# Stage 10 — Reversals, corrections and manual journals

Nothing is edited or deleted. All staff-initiated journals go through `ledger_adjustments` (maker-checker, ADR-017):

| Kind | Posts on approval |
|---|---|
| REVERSAL | the exact mirror of the target journal (`posting_rule = REVERSAL`, `reverses_transaction_id`) |
| CORRECTION | the mirror plus a replacement journal under the proposed rule and lines |
| MANUAL | one journal under an approval-requiring rule (`ADJUSTMENT`, `WRITE_OFF`, …) with the proposed lines |

## Controls

- Maker needs `ledger.adjustment.create`, checker `ledger.adjustment.approve`; both require step-up MFA; the checker
  must not be the maker (staff user id comparison, `LEDGER_SELF_APPROVAL_FORBIDDEN`; the same rule applies to period
  close); a reason code
  (POSTING_ERROR, RECON_DISCREPANCY, DUPLICATE_POSTING, PROVIDER_CORRECTION, WRITE_OFF, TEST_FIXTURE_CORRECTION,
  OTHER) and a written reason are required; requests expire after 24 h (maintenance job).
- The posting happens **in the approval transaction**: either the adjustment becomes APPROVED and its journals exist,
  or neither.
- A journal already reversed cannot be targeted again (`LEDGER_ALREADY_REVERSED`); a reversal cannot be reversed
  (`LEDGER_REVERSAL_OF_REVERSAL`); the database enforces both (unique index, L7 in the deferred trigger).
- Reversals of non-negative accounts are subject to the same sufficiency check: reversing a capture whose money was
  already moved on fails (`LEDGER_INSUFFICIENT_FUNDS`) rather than driving an obligation negative.
- A reversal posts into the current OPEN period with `occurred_at` of the approval; the original is untouched.
- Approved staff journals carry `evidence_class = MANUALLY_REVIEWED` and `source_system = staff`.
- **Production manual posting is disabled**: Stage 10 ledgers are TEST, and the production gate is closed.

## Partial refunds

A partial refund is **not** a reversal. It is a new journal for the refunded amount under the refund rules
(fixture `partial_refund`: USD 100.00 captured, USD 30.00 reserved and settled, USD 70.00 remains owed to the
campaign). A full reversal is reserved for journals posted in error.

Tested: `TestLedgerReversalsMakerCheckerAndNoDoubleReversal`, property test
`TestPropertyReversalCancelsAndAggregationMatches`.
