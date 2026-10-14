# Stage 11 — Ledger integration

Payouts never write ledger tables. They call `ledger.Service.PostOnce` on the ledger's own pool (`fundzim_ledger`,
ADR-042) with the Stage 10 posting rules, read balances through `CampaignBalances` and look journals up by key with
`JournalByKey`.

| Payout event | Rule (version 1) | Key | Lines |
|---|---|---|---|
| reservation (Y2) | PAYOUT_RESERVED | `payout:{id}:reserved` | Dr campaign_payable:{c} · Cr campaign_payout_pending:{c} |
| submission (Y8) | PAYOUT_SUBMITTED | `payout:{id}:submitted` | Dr campaign_payout_pending:{c} · Cr payout_in_transit:{provider} |
| completion (Y10) | PAYOUT_COMPLETED | `payout:{id}:completed` | Dr payout_in_transit:{provider} · Cr psp_settled:{provider} |
| failure after submission (Y11) | PAYOUT_FAILED | `payout:{id}:failed` | Dr payout_in_transit:{provider} · Cr campaign_payable:{c} |
| rejection/cancellation before submission (Y4/Y5) | PAYOUT_RELEASED | `payout:{id}:released` | Dr campaign_payout_pending:{c} · Cr campaign_payable:{c} |
| return after completion (Y14) | PAYOUT_REVERSED | `payout:{id}:reversed` | Dr psp_settled:{provider} · Cr campaign_payable:{c} (reported amount) |

Every payout journal has `source_system = payouts`, `source_type = payout`, `source_id = payout id`,
`evidence_class = SIMULATED` (required in a TEST ledger by CHECK `ck_ledger_transactions_payouts_simulated_in_test` and by
Go validation), `environment = TEST`, metadata `test_payout=true` and a "TEST_ONLY payout …" description. Postings are
idempotent by key; the state is the source of truth and `syncLedger` derives the required journals (ADR-043 §4).

**Boundaries kept:** provider acceptance (`SUBMITTED`/`PROCESSING`) moves money only to `payout_in_transit` (still
reserved, not paid); only an authoritative completion posts `PAYOUT_COMPLETED` and consumes the reservation; a timeout
never posts `PAYOUT_FAILED`. Provider reversal ≠ accounting reversal: a returned payout posts the compensating
`PAYOUT_REVERSED` journal (and a PAYOUT_RETURNED exception); posted journals are never edited. An accounting correction is
a Stage 10 maker-checker adjustment. The `PAYOUT_FEE` rule is unused (no payout fee is known; PCR-013).

**Integrity:** the ledger integrity checker passed after every payout integration test (`p.integrity(t)`); payout
reconciliation compares each payout's required journals by key and amount (LEDGER_MISMATCH).
