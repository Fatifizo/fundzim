-- Reconciliation tests for design/sql/0017_reconciliation.sql (Stage 2 design draft; in-memory PGlite only).
-- Fixed ids keep cases readable. Provider 'psp_a'; campaign / payment ids are opaque uuids (no FK by design).

-- @case setup_source_import_run expect=ok
CREATE SCHEMA rt;
CREATE FUNCTION rt.h(p text) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$ SELECT sha256(convert_to(p, 'UTF8')) $$;
CREATE FUNCTION rt.maker() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '5aff0000-0000-4000-8000-00000000000a'::uuid $$;
CREATE FUNCTION rt.checker() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '5aff0000-0000-4000-8000-00000000000b'::uuid $$;
CREATE FUNCTION rt.p() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'aaaaaaaa-0000-4000-8000-0000000000a1'::uuid $$;
CREATE FUNCTION rt.c() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'cccccccc-0000-4000-8000-0000000000c1'::uuid $$;

INSERT INTO recon.reconciliation_sources (id, code, source_kind, provider_code, covers, ingestion_method, currency, parser_code, parser_version)
VALUES ('50000000-0000-4000-8000-000000000001', 'psp_a_settlement_usd', 'PROVIDER_SETTLEMENT_REPORT', 'psp_a', 'PROVIDER_POOL',
        'SFTP', 'USD', 'psp_a_csv', '1.0.0');
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, statement_ref, period_start, period_end, currency,
                                          rows_declared, status, evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES ('10000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001', rt.h('file-2026-10-07'), 'STL-2026-10-07',
        '2026-10-07T00:00:00Z', '2026-10-08T00:00:00Z', 'USD', 2, 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());
UPDATE recon.reconciliation_imports SET status = 'PARSING' WHERE id = '10000000-0000-4000-8000-000000000001';
INSERT INTO recon.reconciliation_items (id, import_id, source_id, external_line_id, line_number, item_kind, direction, amount_minor,
                                        fee_minor, net_minor, currency, raw_currency_code, provider_ref, merchant_ref,
                                        provider_batch_ref, value_date, raw_line_sha256, raw_line_locator)
VALUES
  ('17000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
   'TXN-001', 1, 'PAYMENT', 'CREDIT', 5000, 200, 4800, 'USD', 'USD', 'PSPA-TXN-001', 'pay-0001', 'B-77', '2026-10-07',
   rt.h('line1'), 'row:2'),
  ('17000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
   'TXN-002', 2, 'PAYMENT', 'CREDIT', 2000, 70, 1930, 'USD', 'USD', 'PSPA-TXN-002', 'pay-0002', 'B-77', '2026-10-07',
   rt.h('line2'), 'row:3');
UPDATE recon.reconciliation_imports SET status = 'PARSED', rows_parsed = 2 WHERE id = '10000000-0000-4000-8000-000000000001';
INSERT INTO recon.reconciliation_runs (id, provider_code, currency, window_start, window_end, run_kind, status, matcher_version,
                                       triggered_by_type, triggered_by_id)
VALUES ('20000000-0000-4000-8000-000000000001', 'psp_a', 'USD', '2026-10-07T00:00:00Z', '2026-10-08T00:00:00Z', 'SCHEDULED',
        'QUEUED', 'm-1', 'SYSTEM', 'recon.scheduler');
UPDATE recon.reconciliation_runs SET status = 'RUNNING', started_at = now() WHERE id = '20000000-0000-4000-8000-000000000001';
-- Ledger fixtures for three-way matching (period, accounts).
INSERT INTO ledger.ledger_periods (id, code, starts_at, ends_at, status)
VALUES (gen_random_uuid(), '2026+', '2026-01-01T00:00:00Z', '2100-01-01T00:00:00Z', 'OPEN');

-- @case duplicate_import_same_file_rejected expect=error:uq_reconciliation_imports_content_sha256
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, period_start, period_end, currency, status,
                                          evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES (gen_random_uuid(), '50000000-0000-4000-8000-000000000001', rt.h('file-2026-10-07'),
        '2026-10-07T00:00:00Z', '2026-10-08T00:00:00Z', 'USD', 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());

-- @case duplicate_statement_ref_rejected expect=error:uq_reconciliation_imports_statement
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, statement_ref, period_start, period_end, currency, status,
                                          evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES (gen_random_uuid(), '50000000-0000-4000-8000-000000000001', rt.h('file-2026-10-07-different-bytes'), 'STL-2026-10-07',
        '2026-10-07T00:00:00Z', '2026-10-08T00:00:00Z', 'USD', 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());

-- @case parsed_row_counts_must_reconcile expect=error:ck_reconciliation_imports_parsed
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, period_start, period_end, currency, rows_declared, status,
                                          evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES ('10000000-0000-4000-8000-000000000009', '50000000-0000-4000-8000-000000000001', rt.h('file-x'),
        '2026-10-06T00:00:00Z', '2026-10-07T00:00:00Z', 'USD', 5, 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());
UPDATE recon.reconciliation_imports SET status = 'PARSING' WHERE id = '10000000-0000-4000-8000-000000000009';
UPDATE recon.reconciliation_imports SET status = 'PARSED', rows_parsed = 3, rows_rejected = 1 WHERE id = '10000000-0000-4000-8000-000000000009';

-- @case duplicate_statement_line_rejected expect=error:uq_reconciliation_items_source_line
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, period_start, period_end, currency, status,
                                          evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES ('10000000-0000-4000-8000-000000000002', '50000000-0000-4000-8000-000000000001', rt.h('file-overlap'),
        '2026-10-07T00:00:00Z', '2026-10-09T00:00:00Z', 'USD', 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());
UPDATE recon.reconciliation_imports SET status = 'PARSING' WHERE id = '10000000-0000-4000-8000-000000000002';
INSERT INTO recon.reconciliation_items (id, import_id, source_id, external_line_id, line_number, item_kind, direction, amount_minor,
                                        currency, raw_currency_code, raw_line_sha256, raw_line_locator)
VALUES (gen_random_uuid(), '10000000-0000-4000-8000-000000000002', '50000000-0000-4000-8000-000000000001',
        'TXN-001', 1, 'PAYMENT', 'CREDIT', 5000, 'USD', 'USD', rt.h('line1'), 'row:2');

-- @case item_added_to_parsed_import_rejected expect=error:items can only be added while PARSING
INSERT INTO recon.reconciliation_items (id, import_id, source_id, external_line_id, line_number, item_kind, direction, amount_minor,
                                        currency, raw_currency_code, raw_line_sha256, raw_line_locator)
VALUES (gen_random_uuid(), '10000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
        'TXN-099', 9, 'PAYMENT', 'CREDIT', 100, 'USD', 'USD', rt.h('line9'), 'row:10');

-- @case item_amount_immutable expect=error:only columns
UPDATE recon.reconciliation_items SET amount_minor = 4999 WHERE id = '17000000-0000-4000-8000-000000000001';

-- @case item_net_inconsistent_rejected expect=error:ck_reconciliation_items_net
INSERT INTO recon.reconciliation_imports (id, source_id, content_sha256, period_start, period_end, currency, status,
                                          evidence_record_id, imported_by_type, imported_by_id, received_at)
VALUES ('10000000-0000-4000-8000-000000000003', '50000000-0000-4000-8000-000000000001', rt.h('file-net'),
        '2026-10-05T00:00:00Z', '2026-10-06T00:00:00Z', 'USD', 'RECEIVED', gen_random_uuid(), 'SYSTEM', 'recon.ingest', now());
UPDATE recon.reconciliation_imports SET status = 'PARSING' WHERE id = '10000000-0000-4000-8000-000000000003';
INSERT INTO recon.reconciliation_items (id, import_id, source_id, external_line_id, line_number, item_kind, direction, amount_minor,
                                        fee_minor, net_minor, currency, raw_currency_code, raw_line_sha256, raw_line_locator)
VALUES (gen_random_uuid(), '10000000-0000-4000-8000-000000000003', '50000000-0000-4000-8000-000000000001',
        'TXN-NET', 1, 'PAYMENT', 'CREDIT', 5000, 200, 4900, 'USD', 'USD', rt.h('line-net'), 'row:2');

-- ===== Runs =====
-- @case illegal_run_transition_rejected expect=error:illegal reconciliation_run transition
INSERT INTO recon.reconciliation_runs (id, provider_code, currency, window_start, window_end, run_kind, status, matcher_version,
                                       triggered_by_type, triggered_by_id)
VALUES ('20000000-0000-4000-8000-000000000002', 'psp_b', 'USD', '2026-10-07T00:00:00Z', '2026-10-08T00:00:00Z', 'SCHEDULED',
        'QUEUED', 'm-1', 'SYSTEM', 'recon.scheduler');
UPDATE recon.reconciliation_runs SET status = 'COMPLETED', finished_at = now(), report_evidence_record_id = gen_random_uuid()
 WHERE id = '20000000-0000-4000-8000-000000000002';

-- @case second_active_run_same_scope_rejected expect=error:uq_reconciliation_runs_one_active
INSERT INTO recon.reconciliation_runs (id, provider_code, currency, window_start, window_end, run_kind, status, matcher_version,
                                       triggered_by_type, triggered_by_id)
VALUES (gen_random_uuid(), 'psp_a', 'USD', '2026-10-06T00:00:00Z', '2026-10-07T00:00:00Z', 'ON_DEMAND',
        'QUEUED', 'm-1', 'STAFF', rt.maker()::text);

-- ===== Settlement batches =====
-- @case settlement_batch_setup expect=ok
INSERT INTO recon.settlement_batches (id, provider_code, currency, provider_batch_ref, import_id, settlement_date,
                                      gross_credits_minor, gross_debits_minor, fees_minor, net_minor, declared_item_count, status)
VALUES ('30000000-0000-4000-8000-000000000001', 'psp_a', 'USD', 'B-77', '10000000-0000-4000-8000-000000000001', '2026-10-07',
        7000, 0, 270, 6730, 2, 'RECEIVED');
INSERT INTO recon.settlement_items (id, settlement_batch_id, currency, line_ref, item_kind, direction, gross_minor, fee_minor, merchant_ref)
VALUES ('31000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'USD', 'B-77/1', 'PAYMENT', 'CREDIT', 5000, 200, 'pay-0001');

-- @case settlement_item_currency_mismatch_rejected expect=error:fk_settlement_items_batch_currency
INSERT INTO recon.settlement_items (id, settlement_batch_id, currency, line_ref, item_kind, direction, gross_minor, fee_minor)
VALUES (gen_random_uuid(), '30000000-0000-4000-8000-000000000001', 'ZWG', 'B-77/2', 'PAYMENT', 'CREDIT', 2000, 70);

-- @case settlement_batch_net_inconsistent_rejected expect=error:ck_settlement_batches_net
INSERT INTO recon.settlement_batches (id, provider_code, currency, provider_batch_ref, settlement_date,
                                      gross_credits_minor, gross_debits_minor, fees_minor, net_minor, status)
VALUES (gen_random_uuid(), 'psp_a', 'USD', 'B-78', '2026-10-08', 7000, 0, 270, 6800, 'RECEIVED');

-- @case settlement_batch_matched_with_missing_items_rejected expect=error:totals do not match its items
UPDATE recon.settlement_batches SET status = 'MATCHING' WHERE id = '30000000-0000-4000-8000-000000000001';
UPDATE recon.settlement_items SET match_status = 'MATCHED' WHERE settlement_batch_id = '30000000-0000-4000-8000-000000000001';
UPDATE recon.settlement_batches SET status = 'MATCHED' WHERE id = '30000000-0000-4000-8000-000000000001';

-- @case settlement_batch_amounts_immutable expect=error:only columns
UPDATE recon.settlement_batches SET gross_credits_minor = 5000, fees_minor = 200, net_minor = 4800
 WHERE id = '30000000-0000-4000-8000-000000000001';

-- ===== Three-way match with ledger journals (SETTLEMENT_MATCHED), then batch MATCHED and CLOSED =====
-- @case three_way_match_and_batch_close_ok expect=ok
DO $$
DECLARE
  clr uuid := gen_random_uuid(); stl uuid := gen_random_uuid(); uns uuid := gen_random_uuid(); fee uuid := gen_random_uuid();
  pb uuid := gen_random_uuid(); tx1 uuid := gen_random_uuid(); tx2 uuid := gen_random_uuid();
  cap1 uuid := gen_random_uuid(); cap2 uuid := gen_random_uuid();
BEGIN
  INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id) VALUES
    (clr, 'asset:psp_clearing:' || rt.p(), 'asset:psp_clearing', 'ASSET', 'DEBIT', 'CLAIM_ON_PROVIDER', 'USD', 'PROVIDER', rt.p()),
    (stl, 'asset:psp_settled:' || rt.p(), 'asset:psp_settled', 'ASSET', 'DEBIT', 'PROVIDER_POOL', 'USD', 'PROVIDER', rt.p()),
    (uns, 'liability:campaign_unsettled:' || rt.c(), 'liability:campaign_unsettled', 'LIABILITY', 'CREDIT', 'OBLIGATION', 'USD', 'CAMPAIGN', rt.c()),
    (fee, 'expense:psp_processing_fees', 'expense:psp_processing_fees', 'EXPENSE', 'DEBIT', 'RESULT', 'USD', 'PLATFORM', NULL);
  -- Captures (FundZim bears the fee here, to keep the fixture small): clearing = 4800 and 1930 after fees.
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at, created_by_type, created_by_id)
  VALUES (cap1, 'DONATION_CAPTURED', 'payment:pay-0001:capture', 'USD', 'capture', 'payment', gen_random_uuid(), now(), 'SYSTEM', 't'),
         (cap2, 'DONATION_CAPTURED', 'payment:pay-0002:capture', 'USD', 'capture', 'payment', gen_random_uuid(), now(), 'SYSTEM', 't');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
    (gen_random_uuid(), cap1, 1, clr, 'DEBIT', 5000, 'USD'), (gen_random_uuid(), cap1, 2, uns, 'CREDIT', 5000, 'USD'),
    (gen_random_uuid(), cap2, 1, clr, 'DEBIT', 2000, 'USD'), (gen_random_uuid(), cap2, 2, uns, 'CREDIT', 2000, 'USD');
  INSERT INTO ledger.ledger_posting_batches (id, batch_kind, currency, idempotency_key, source_type, source_id, description, created_by_type, created_by_id)
  VALUES (pb, 'SETTLEMENT', 'USD', 'settlement_batch:30000000-0000-4000-8000-000000000001', 'settlement_batch',
          '30000000-0000-4000-8000-000000000001', 'B-77', 'SYSTEM', 'recon.matcher');
  -- PSP fee + settlement per payment (one journal each would be typical; combined lines here are not allowed:
  -- PSP_FEE and SETTLEMENT_MATCHED are separate named rules).
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at, batch_id, created_by_type, created_by_id)
  VALUES (gen_random_uuid(), 'PSP_FEE', 'payment:pay-0001:psp_fee', 'USD', 'fee', 'payment', gen_random_uuid(), now(), NULL, 'SYSTEM', 't');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
  SELECT gen_random_uuid(), id, 1, fee, 'DEBIT', 200, 'USD' FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0001:psp_fee'
  UNION ALL
  SELECT gen_random_uuid(), id, 2, clr, 'CREDIT', 200, 'USD' FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0001:psp_fee';
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at, created_by_type, created_by_id)
  VALUES (gen_random_uuid(), 'PSP_FEE', 'payment:pay-0002:psp_fee', 'USD', 'fee', 'payment', gen_random_uuid(), now(), 'SYSTEM', 't');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
  SELECT gen_random_uuid(), id, 1, fee, 'DEBIT', 70, 'USD' FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0002:psp_fee'
  UNION ALL
  SELECT gen_random_uuid(), id, 2, clr, 'CREDIT', 70, 'USD' FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0002:psp_fee';
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at, batch_id, created_by_type, created_by_id)
  VALUES (tx1, 'SETTLEMENT_MATCHED', 'settlement:30000000-0000-4000-8000-000000000001:pay-0001', 'USD', 'settled', 'settlement_item',
          '31000000-0000-4000-8000-000000000001', now(), pb, 'SYSTEM', 'recon.matcher'),
         (tx2, 'SETTLEMENT_MATCHED', 'settlement:30000000-0000-4000-8000-000000000001:pay-0002', 'USD', 'settled', 'settlement_item',
          '31000000-0000-4000-8000-000000000002', now(), pb, 'SYSTEM', 'recon.matcher');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
    (gen_random_uuid(), tx1, 1, stl, 'DEBIT', 4800, 'USD'), (gen_random_uuid(), tx1, 2, clr, 'CREDIT', 4800, 'USD'),
    (gen_random_uuid(), tx2, 1, stl, 'DEBIT', 1930, 'USD'), (gen_random_uuid(), tx2, 2, clr, 'CREDIT', 1930, 'USD');

  UPDATE recon.settlement_batches SET status = 'MATCHING' WHERE id = '30000000-0000-4000-8000-000000000001';
  INSERT INTO recon.settlement_items (id, settlement_batch_id, currency, line_ref, item_kind, direction, gross_minor, fee_minor, merchant_ref)
  VALUES ('31000000-0000-4000-8000-000000000002', '30000000-0000-4000-8000-000000000001', 'USD', 'B-77/2', 'PAYMENT', 'CREDIT', 2000, 70, 'pay-0002');
  UPDATE recon.settlement_items SET match_status = 'MATCHED', subject_type = 'PAYMENT_INTENT', subject_id = gen_random_uuid(),
         ledger_transaction_id = CASE line_ref WHEN 'B-77/1' THEN tx1 ELSE tx2 END,
         reconciliation_item_id = CASE line_ref WHEN 'B-77/1' THEN '17000000-0000-4000-8000-000000000001'::uuid
                                                ELSE '17000000-0000-4000-8000-000000000002'::uuid END
   WHERE settlement_batch_id = '30000000-0000-4000-8000-000000000001';
  INSERT INTO recon.reconciliation_matches (id, run_id, item_id, currency, subject_type, subject_id, ledger_transaction_id,
                                            match_type, matched_amount_minor, created_by_type)
  VALUES (gen_random_uuid(), '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001', 'USD',
          'PAYMENT_INTENT', gen_random_uuid(), tx1, 'EXACT', 4800, 'SYSTEM'),
         (gen_random_uuid(), '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000002', 'USD',
          'PAYMENT_INTENT', gen_random_uuid(), tx2, 'EXACT', 1930, 'SYSTEM');
  UPDATE recon.reconciliation_items SET match_status = 'MATCHED' WHERE import_id = '10000000-0000-4000-8000-000000000001';
  UPDATE recon.settlement_batches SET status = 'MATCHED', ledger_posting_batch_id = pb WHERE id = '30000000-0000-4000-8000-000000000001';
  UPDATE recon.settlement_batches SET status = 'CLOSED' WHERE id = '30000000-0000-4000-8000-000000000001';
END $$;

-- @case item_added_to_closed_batch_rejected expect=error:items cannot be added
INSERT INTO recon.settlement_items (id, settlement_batch_id, currency, line_ref, item_kind, direction, gross_minor)
VALUES (gen_random_uuid(), '30000000-0000-4000-8000-000000000001', 'USD', 'B-77/3', 'PAYMENT', 'CREDIT', 10);

-- ===== Matches =====
-- @case match_to_nonexistent_ledger_journal_rejected expect=error:fk_reconciliation_matches_ledger_transaction
INSERT INTO recon.reconciliation_matches (id, run_id, item_id, currency, subject_type, subject_id, ledger_transaction_id,
                                          match_type, matched_amount_minor, created_by_type)
VALUES (gen_random_uuid(), '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001', 'USD',
        'PAYMENT_INTENT', gen_random_uuid(), gen_random_uuid(), 'EXACT', 4800, 'SYSTEM');

-- @case match_currency_differs_from_item_rejected expect=error:fk_reconciliation_matches_item_currency
INSERT INTO recon.reconciliation_matches (id, run_id, item_id, currency, subject_type, subject_id, ledger_transaction_id,
                                          match_type, matched_amount_minor, created_by_type)
SELECT gen_random_uuid(), '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001', 'ZWG',
       'PAYMENT_INTENT', gen_random_uuid(), id, 'EXACT', 4800, 'SYSTEM'
  FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0001:capture';

-- @case manual_match_self_approved_rejected expect=error:ck_reconciliation_matches_manual
INSERT INTO recon.reconciliation_matches (id, run_id, item_id, currency, subject_type, subject_id, ledger_transaction_id,
                                          match_type, matched_amount_minor, created_by_type, created_by_staff_id, approved_by, evidence_record_ids)
SELECT gen_random_uuid(), '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000002', 'USD',
       'REFUND_TRANSACTION', gen_random_uuid(), id, 'MANUAL', 1930, 'STAFF', rt.maker(), rt.maker(), ARRAY[gen_random_uuid()]
  FROM ledger.ledger_transactions WHERE idempotency_key = 'payment:pay-0002:capture';

-- ===== Discrepancies and resolutions =====
-- @case discrepancy_missing_in_fundzim_requires_item expect=error:ck_reconciliation_discrepancies_shape
INSERT INTO recon.reconciliation_discrepancies (id, dedupe_key, discrepancy_type, severity, status, provider_code, currency,
                                                first_run_id, last_seen_run_id, detected_at, sla_due_at)
VALUES (gen_random_uuid(), 'MISSING_IN_FUNDZIM:x', 'MISSING_IN_FUNDZIM', 'SEV2', 'OPEN', 'psp_a', 'USD',
        '20000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', now(), now() + interval '1 day');

-- @case discrepancy_settlement_short_must_be_short expect=error:ck_reconciliation_discrepancies_shape
INSERT INTO recon.reconciliation_discrepancies (id, dedupe_key, discrepancy_type, severity, status, provider_code, currency,
                                                first_run_id, last_seen_run_id, item_id, expected_amount_minor, actual_amount_minor,
                                                detected_at, sla_due_at)
VALUES (gen_random_uuid(), 'SETTLEMENT_SHORT:y', 'SETTLEMENT_SHORT', 'SEV2', 'OPEN', 'psp_a', 'USD',
        '20000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001',
        4825, 4850, now(), now() + interval '1 day');

-- @case discrepancy_settlement_short_ok expect=ok
INSERT INTO recon.reconciliation_discrepancies (id, dedupe_key, discrepancy_type, severity, status, provider_code, currency,
                                                first_run_id, last_seen_run_id, item_id, expected_amount_minor, actual_amount_minor,
                                                detected_at, sla_due_at)
VALUES ('40000000-0000-4000-8000-000000000001', 'SETTLEMENT_SHORT:B-77:pay-0001', 'SETTLEMENT_SHORT', 'SEV3', 'OPEN', 'psp_a', 'USD',
        '20000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001',
        4825, 4800, now(), now() + interval '3 days');
DO $$ BEGIN
  IF (SELECT difference_minor FROM recon.reconciliation_discrepancies WHERE id = '40000000-0000-4000-8000-000000000001') <> -25 THEN
    RAISE EXCEPTION 'difference_minor should be -25';
  END IF;
END $$;

-- @case duplicate_discrepancy_dedupe_key_rejected expect=error:uq_reconciliation_discrepancies_dedupe
INSERT INTO recon.reconciliation_discrepancies (id, dedupe_key, discrepancy_type, severity, status, provider_code, currency,
                                                first_run_id, last_seen_run_id, item_id, expected_amount_minor, actual_amount_minor,
                                                detected_at, sla_due_at)
VALUES (gen_random_uuid(), 'SETTLEMENT_SHORT:B-77:pay-0001', 'SETTLEMENT_SHORT', 'SEV3', 'OPEN', 'psp_a', 'USD',
        '20000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '17000000-0000-4000-8000-000000000001',
        4825, 4800, now(), now() + interval '3 days');

-- @case resolution_self_approval_rejected expect=error:ck_reconciliation_resolutions_maker_checker
INSERT INTO recon.reconciliation_resolutions (id, discrepancy_id, resolution_type, status, justification, evidence_record_ids,
                                              proposed_by, expires_at)
VALUES ('41000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001', 'EXPLAINED_NO_ACTION', 'PROPOSED',
        'Provider confirmed rounding difference in writing', ARRAY[gen_random_uuid()], rt.maker(), now() + interval '24 hours');
UPDATE recon.reconciliation_resolutions SET status = 'APPROVED', approved_by = rt.maker(), approved_at = now(), approval_justification = 'ok'
 WHERE id = '41000000-0000-4000-8000-000000000001';

-- @case resolution_approval_without_evidence_rejected expect=error:ck_reconciliation_resolutions_approval
INSERT INTO recon.reconciliation_resolutions (id, discrepancy_id, resolution_type, status, justification, proposed_by, expires_at)
VALUES ('41000000-0000-4000-8000-000000000002', '40000000-0000-4000-8000-000000000001', 'EXPLAINED_NO_ACTION', 'PROPOSED',
        'Provider confirmed rounding difference by phone', rt.maker(), now() + interval '24 hours');
UPDATE recon.reconciliation_resolutions SET status = 'APPROVED', approved_by = rt.checker(), approved_at = now(), approval_justification = 'ok'
 WHERE id = '41000000-0000-4000-8000-000000000002';

-- @case ledger_resolution_requires_ledger_adjustment expect=error:ck_reconciliation_resolutions_ledger
INSERT INTO recon.reconciliation_resolutions (id, discrepancy_id, resolution_type, status, justification, evidence_record_ids,
                                              proposed_by, expires_at)
VALUES (gen_random_uuid(), '40000000-0000-4000-8000-000000000001', 'SUSPENSE_CLEARED', 'PROPOSED',
        'Provider paid the 25 shortfall in batch B-80', ARRAY[gen_random_uuid()], rt.maker(), now() + interval '24 hours');

-- @case resolution_with_ledger_adjustment_applied_ok expect=ok
-- Custody model §5.4: suspense parked by SETTLEMENT_DISCREPANCY, cleared when the PSP pays the shortfall later.
DO $$
DECLARE
  sus uuid := gen_random_uuid(); stl uuid; tx_s uuid := gen_random_uuid(); tx_r uuid := gen_random_uuid();
  adj uuid := gen_random_uuid(); res uuid := gen_random_uuid(); lines jsonb;
BEGIN
  SELECT id INTO stl FROM ledger.ledger_accounts WHERE code = 'asset:psp_settled:' || rt.p() AND currency = 'USD';
  INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id)
  VALUES (sus, 'suspense:settlement_discrepancy:' || rt.p(), 'suspense:settlement_discrepancy', 'SUSPENSE', 'DEBIT', 'SUSPENSE', 'USD', 'PROVIDER', rt.p());
  -- The 25 short was parked in suspense (illustrative standalone journal against the pool claim).
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at, created_by_type, created_by_id)
  VALUES (tx_s, 'SETTLEMENT_DISCREPANCY', 'settlement:B-77:short:pay-0001', 'USD', 'short settlement', 'settlement_item',
          '31000000-0000-4000-8000-000000000001', now(), 'SYSTEM', 'recon.matcher');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
    (gen_random_uuid(), tx_s, 1, sus, 'DEBIT', 25, 'USD'),
    (gen_random_uuid(), tx_s, 2, (SELECT id FROM ledger.ledger_accounts WHERE code = 'asset:psp_clearing:' || rt.p() AND currency = 'USD'), 'CREDIT', 25, 'USD');
  UPDATE recon.reconciliation_discrepancies SET suspense_transaction_id = tx_s, status = 'INVESTIGATING'
   WHERE id = '40000000-0000-4000-8000-000000000001';
  -- Maker proposes: resolution + ledger adjustment (same maker); checker approves both.
  lines := jsonb_build_array(
    jsonb_build_object('account_code', 'asset:psp_settled:' || rt.p(), 'direction', 'DEBIT', 'amount_minor', '25'),
    jsonb_build_object('account_code', 'suspense:settlement_discrepancy:' || rt.p(), 'direction', 'CREDIT', 'amount_minor', '25'));
  INSERT INTO ledger.ledger_adjustments (id, kind, posting_rule, currency, proposed_lines, proposed_lines_sha256, reason_code, reason,
                                         linked_subject_type, linked_subject_id, status, requested_by, expires_at)
  VALUES (adj, 'MANUAL', 'SETTLEMENT_DISCREPANCY_RESOLVED', 'USD', lines, sha256(convert_to(lines::text, 'UTF8')), 'RECON_DISCREPANCY',
          'PSP paid the 25 shortfall in batch B-80', 'reconciliation_discrepancy', '40000000-0000-4000-8000-000000000001',
          'PENDING_APPROVAL', rt.maker(), now() + interval '24 hours');
  INSERT INTO recon.reconciliation_resolutions (id, discrepancy_id, resolution_type, status, justification, evidence_record_ids,
                                                proposed_by, expires_at, ledger_adjustment_id)
  VALUES (res, '40000000-0000-4000-8000-000000000001', 'SUSPENSE_CLEARED', 'PROPOSED',
          'Provider paid the 25 shortfall in batch B-80', ARRAY[gen_random_uuid()], rt.maker(), now() + interval '24 hours', adj);
  UPDATE recon.reconciliation_discrepancies SET status = 'RESOLUTION_PROPOSED' WHERE id = '40000000-0000-4000-8000-000000000001';
  UPDATE ledger.ledger_adjustments SET status = 'APPROVED', approved_by = rt.checker(), approved_at = now(),
         approved_lines_sha256 = proposed_lines_sha256, approval_justification = 'matches B-80 statement line' WHERE id = adj;
  UPDATE recon.reconciliation_resolutions SET status = 'APPROVED', approved_by = rt.checker(), approved_at = now(),
         approval_justification = 'matches B-80 statement line' WHERE id = res;
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id, occurred_at,
                                          adjustment_id, created_by_type, created_by_id, reason)
  VALUES (tx_r, 'SETTLEMENT_DISCREPANCY_RESOLVED', 'discrepancy:40000000-0000-4000-8000-000000000001:resolved', 'USD', 'resolved',
          'discrepancy', '40000000-0000-4000-8000-000000000001', now(), adj, 'STAFF', rt.checker()::text,
          'PSP paid the 25 shortfall in batch B-80');
  INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
    (gen_random_uuid(), tx_r, 1, stl, 'DEBIT', 25, 'USD'), (gen_random_uuid(), tx_r, 2, sus, 'CREDIT', 25, 'USD');
  UPDATE ledger.ledger_adjustments SET status = 'POSTED', resulting_transaction_id = tx_r, posted_at = now() WHERE id = adj;
  UPDATE recon.reconciliation_resolutions SET status = 'APPLIED', resulting_transaction_id = tx_r, applied_at = now() WHERE id = res;
  UPDATE recon.reconciliation_discrepancies SET status = 'RESOLVED', resolved_at = now() WHERE id = '40000000-0000-4000-8000-000000000001';
  IF (SELECT balance_minor FROM ledger.v_balances_current WHERE account_id = sus) <> 0 THEN
    RAISE EXCEPTION 'suspense should be back to zero';
  END IF;
END $$;

-- @case resolved_discrepancy_is_frozen expect=error:is final
UPDATE recon.reconciliation_discrepancies SET severity = 'SEV1' WHERE id = '40000000-0000-4000-8000-000000000001';

-- @case second_live_resolution_rejected expect=error:uq_reconciliation_resolutions_live
INSERT INTO recon.reconciliation_resolutions (id, discrepancy_id, resolution_type, status, justification, evidence_record_ids,
                                              proposed_by, expires_at)
VALUES (gen_random_uuid(), '40000000-0000-4000-8000-000000000001', 'EXPLAINED_NO_ACTION', 'PROPOSED',
        'Second attempt to resolve the same discrepancy', ARRAY[gen_random_uuid()], rt.maker(), now() + interval '24 hours');

-- @case complete_run_requires_report_evidence expect=error:ck_reconciliation_runs_completed
UPDATE recon.reconciliation_runs SET status = 'COMPLETED', finished_at = now(), items_considered = 2, items_matched = 2
 WHERE id = '20000000-0000-4000-8000-000000000001';

-- @case complete_run_ok_then_frozen expect=error:is final
UPDATE recon.reconciliation_runs SET status = 'COMPLETED', finished_at = now(), items_considered = 2, items_matched = 2,
       report_evidence_record_id = gen_random_uuid()
 WHERE id = '20000000-0000-4000-8000-000000000001';
UPDATE recon.reconciliation_runs SET items_matched = 1 WHERE id = '20000000-0000-4000-8000-000000000001';
