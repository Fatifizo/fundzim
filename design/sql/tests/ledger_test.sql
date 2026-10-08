-- Ledger invariant tests for design/sql/0006_ledger.sql (Stage 2 design draft; in-memory PGlite only).
-- Each case runs in its own BEGIN ... COMMIT, so the deferred journal check fires at COMMIT.
-- Helper functions live in schema t (test-only). Campaigns/providers are fixed UUIDs (no FK: ledger is
-- domain-agnostic). Canonical numbers: settlement-and-custody-model §5 (5000 / 250 / 175).

-- @case setup_helpers_and_periods expect=ok
CREATE SCHEMA t;
CREATE FUNCTION t.p1() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'aaaaaaaa-0000-4000-8000-000000000001'::uuid $$;
CREATE FUNCTION t.p2() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'aaaaaaaa-0000-4000-8000-000000000002'::uuid $$;
CREATE FUNCTION t.c1() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'cccccccc-0000-4000-8000-000000000001'::uuid $$;
CREATE FUNCTION t.c2() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'cccccccc-0000-4000-8000-000000000002'::uuid $$;
CREATE FUNCTION t.c3() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'cccccccc-0000-4000-8000-000000000003'::uuid $$;
CREATE FUNCTION t.c4() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT 'cccccccc-0000-4000-8000-000000000004'::uuid $$;
CREATE FUNCTION t.maker() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '5aff0000-0000-4000-8000-00000000000a'::uuid $$;
CREATE FUNCTION t.checker() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '5aff0000-0000-4000-8000-00000000000b'::uuid $$;

-- Lazily and idempotently create an account (what ledger.Post does on first posting).
CREATE FUNCTION t.acct(p_class text, p_owner uuid, p_cur char(3)) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
  sig    text[] := string_to_array(ledger.account_class_signature(p_class), '|');
  v_code text   := p_class || CASE WHEN p_owner IS NULL THEN '' ELSE ':' || p_owner::text END;
  v_id   uuid;
BEGIN
  INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id)
  VALUES (gen_random_uuid(), v_code, p_class, sig[1], sig[2], sig[3], p_cur, sig[4], p_owner)
  ON CONFLICT ON CONSTRAINT uq_ledger_accounts_code_currency DO NOTHING;
  SELECT id INTO v_id FROM ledger.ledger_accounts WHERE code = v_code AND currency = p_cur;
  RETURN v_id;
END $$;

CREATE FUNCTION t.dr(p_class text, p_owner uuid, p_amount bigint, p_cur text DEFAULT NULL) RETURNS jsonb LANGUAGE sql AS $$
  SELECT jsonb_build_object('c', p_class, 'o', p_owner, 'd', 'DEBIT', 'm', p_amount, 'cur', p_cur) $$;
CREATE FUNCTION t.cr(p_class text, p_owner uuid, p_amount bigint, p_cur text DEFAULT NULL) RETURNS jsonb LANGUAGE sql AS $$
  SELECT jsonb_build_object('c', p_class, 'o', p_owner, 'd', 'CREDIT', 'm', p_amount, 'cur', p_cur) $$;

-- Post a journal: header + entries in one DB transaction (what ledger.Post does).
CREATE FUNCTION t.post(p_rule text, p_key text, p_cur char(3), p_lines jsonb[],
                       p_adj uuid DEFAULT NULL, p_rev uuid DEFAULT NULL, p_occ timestamptz DEFAULT NULL)
RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
  v_tx  uuid := gen_random_uuid();
  v_src text;
  l     jsonb;
  i     int := 0;
  v_cur char(3);
BEGIN
  SELECT allowed_source_types[1] INTO v_src FROM ledger.ledger_posting_rules WHERE code = p_rule;
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id,
                                          occurred_at, reverses_transaction_id, adjustment_id, created_by_type, created_by_id, reason)
  VALUES (v_tx, p_rule, p_key, p_cur, 'test ' || p_rule, coalesce(v_src, 'payment'), gen_random_uuid(),
          coalesce(p_occ, now()), p_rev, p_adj, 'SYSTEM', 'ledger_test', 'test posting with a reason');
  FOREACH l IN ARRAY p_lines LOOP
    i := i + 1;
    v_cur := coalesce(l->>'cur', p_cur);
    INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
    VALUES (gen_random_uuid(), v_tx, i, t.acct(l->>'c', (l->>'o')::uuid, v_cur), l->>'d', (l->>'m')::bigint, v_cur);
  END LOOP;
  RETURN v_tx;
END $$;

CREATE FUNCTION t.tx(p_key text) RETURNS uuid LANGUAGE sql AS $$
  SELECT id FROM ledger.ledger_transactions WHERE idempotency_key = p_key $$;

-- Maker creates an adjustment request (proposed lines in the canonical jsonb shape, hashed).
CREATE FUNCTION t.adj(p_kind text, p_rule text, p_cur char(3), p_target uuid, p_lines jsonb[]) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE
  v_id    uuid := gen_random_uuid();
  v_lines jsonb := '[]'::jsonb;
  l       jsonb;
BEGIN
  IF p_lines IS NOT NULL THEN
    FOREACH l IN ARRAY p_lines LOOP
      v_lines := v_lines || jsonb_build_array(jsonb_build_object(
        'account_code', (l->>'c') || CASE WHEN l->>'o' IS NULL THEN '' ELSE ':' || (l->>'o') END,
        'direction', l->>'d', 'amount_minor', l->>'m'));
    END LOOP;
  END IF;
  INSERT INTO ledger.ledger_adjustments (id, kind, posting_rule, currency, target_transaction_id, proposed_lines,
                                         proposed_lines_sha256, reason_code, reason, status, requested_by, expires_at)
  VALUES (v_id, p_kind, p_rule, p_cur, p_target, v_lines, sha256(convert_to(v_lines::text, 'UTF8')),
          'POSTING_ERROR', 'test adjustment with a reason', 'PENDING_APPROVAL', t.maker(), now() + interval '24 hours');
  RETURN v_id;
END $$;

CREATE FUNCTION t.approve(p_adj uuid, p_checker uuid) RETURNS void LANGUAGE sql AS $$
  UPDATE ledger.ledger_adjustments
     SET status = 'APPROVED', approved_by = p_checker, approved_at = now(),
         approved_lines_sha256 = proposed_lines_sha256, approval_justification = 'checked against evidence'
   WHERE id = p_adj $$;

CREATE FUNCTION t.mark_posted(p_adj uuid, p_rev uuid, p_res uuid) RETURNS void LANGUAGE sql AS $$
  UPDATE ledger.ledger_adjustments
     SET status = 'POSTED', reversal_transaction_id = p_rev, resulting_transaction_id = p_res, posted_at = now()
   WHERE id = p_adj $$;

-- Assert an account's exact current balance (normal-balance terms) via v_balances_current, and that it equals a
-- full recompute from entries (L10). SYNC accounts must also have projected = current (nothing unfolded).
CREATE FUNCTION t.expect(p_class text, p_owner uuid, p_cur char(3), p_expected bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  v_cur  bigint;
  v_proj bigint;
  v_mode text;
  v_full bigint;
  v_code text := p_class || CASE WHEN p_owner IS NULL THEN '' ELSE ':' || p_owner::text END;
BEGIN
  SELECT c.balance_minor, c.projected_minor, c.projection_mode,
         (SELECT coalesce(sum(CASE WHEN e.direction = a.normal_balance THEN e.amount_minor ELSE -e.amount_minor END), 0)
            FROM ledger.ledger_entries e WHERE e.account_id = a.id)
    INTO v_cur, v_proj, v_mode, v_full
    FROM ledger.ledger_accounts a JOIN ledger.v_balances_current c ON c.account_id = a.id
   WHERE a.code = v_code AND a.currency = p_cur;
  IF coalesce(v_cur, 0) <> p_expected OR coalesce(v_full, 0) <> p_expected
     OR (v_mode = 'SYNC' AND v_proj <> v_cur) THEN
    RAISE EXCEPTION 'balance assertion failed for % % (%): expected %, current %, projected %, recompute %',
      v_code, p_cur, v_mode, p_expected, v_cur, v_proj, v_full;
  END IF;
END $$;

CREATE FUNCTION t.projected(p_class text, p_owner uuid, p_cur char(3)) RETURNS bigint LANGUAGE sql AS $$
  SELECT b.balance_minor FROM ledger.ledger_accounts a JOIN ledger.ledger_balances b ON b.account_id = a.id
   WHERE a.code = p_class || CASE WHEN p_owner IS NULL THEN '' ELSE ':' || p_owner::text END AND a.currency = p_cur $$;

INSERT INTO ledger.ledger_periods (id, code, starts_at, ends_at, status) VALUES
  (gen_random_uuid(), '2025-12', '2025-12-01T00:00:00Z', '2026-01-01T00:00:00Z', 'OPEN'),
  (gen_random_uuid(), '2026+',   '2026-01-01T00:00:00Z', '2100-01-01T00:00:00Z', 'OPEN');

-- @case period_close_self_approval_rejected expect=error:ck_ledger_periods_maker_checker
UPDATE ledger.ledger_periods SET status = 'CLOSING', close_requested_by = t.maker(), close_requested_at = now() WHERE code = '2025-12';
UPDATE ledger.ledger_periods SET status = 'CLOSED', closed_by = t.maker(), closed_at = now() WHERE code = '2025-12';

-- @case period_close_ok expect=ok
UPDATE ledger.ledger_periods SET status = 'CLOSING', close_requested_by = t.maker(), close_requested_at = now() WHERE code = '2025-12';
UPDATE ledger.ledger_periods SET status = 'CLOSED', closed_by = t.checker(), closed_at = now() WHERE code = '2025-12';

-- @case closed_period_cannot_reopen expect=error:illegal ledger_period transition
UPDATE ledger.ledger_periods SET status = 'OPEN' WHERE code = '2025-12';

-- ===== Canonical worked example (custody model §5.1-§5.8), campaign c1, provider p1, USD =====
-- @case canonical_example_t1_t2_t3_t4_remittance_payout expect=ok
SELECT t.post('DONATION_CAPTURED', 'payment:canon1:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p1(), 5000),
  t.cr('liability:campaign_unsettled', t.c1(), 4750),
  t.cr('revenue:platform_fees', NULL, 250)]);
SELECT t.post('PSP_FEE', 'payment:canon1:psp_fee', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c1(), 175),
  t.cr('asset:psp_clearing', t.p1(), 175)]);
DO $$ BEGIN   -- "After T1+T2: clearing 4825, unsettled 4575, revenue 250"
  PERFORM t.expect('asset:psp_clearing', t.p1(), 'USD', 4825);
  PERFORM t.expect('liability:campaign_unsettled', t.c1(), 'USD', 4575);
  PERFORM t.expect('revenue:platform_fees', NULL, 'USD', 250);
END $$;
SELECT t.post('SETTLEMENT_MATCHED', 'settlement:batch1:canon1', 'USD', ARRAY[
  t.dr('asset:psp_settled', t.p1(), 4825),
  t.cr('asset:psp_clearing', t.p1(), 4825)]);
SELECT t.post('RELEASE', 'payment:canon1:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c1(), 4575),
  t.cr('liability:campaign_payable', t.c1(), 4575)]);
SELECT t.post('FEE_REMITTANCE', 'fee_remittance:p1:batch1', 'USD', ARRAY[
  t.dr('asset:fundzim_operating_bank', NULL, 250),
  t.cr('asset:psp_settled', t.p1(), 250)]);
DO $$ BEGIN   -- §5.7 pool check: clearing 0 + settled 4575 = 4575 = payable 4575
  PERFORM t.expect('asset:psp_clearing', t.p1(), 'USD', 0);
  PERFORM t.expect('asset:psp_settled', t.p1(), 'USD', 4575);
  PERFORM t.expect('liability:campaign_unsettled', t.c1(), 'USD', 0);
  PERFORM t.expect('liability:campaign_payable', t.c1(), 'USD', 4575);
  PERFORM t.expect('revenue:platform_fees', NULL, 'USD', 250);
  PERFORM t.expect('asset:fundzim_operating_bank', NULL, 'USD', 250);
END $$;
-- Payout (§5.8 shape; the doc's 400000 assumes a larger balance, so US$40.00 = 4000 is used here).
SELECT t.post('PAYOUT_RESERVED', 'payout:canon1:reserved', 'USD', ARRAY[
  t.dr('liability:campaign_payable', t.c1(), 4000),
  t.cr('liability:campaign_payout_pending', t.c1(), 4000)]);
SELECT t.post('PAYOUT_SUBMITTED', 'payout:canon1:submitted', 'USD', ARRAY[
  t.dr('liability:campaign_payout_pending', t.c1(), 4000),
  t.cr('liability:payout_in_transit', t.p1(), 4000)]);
SELECT t.post('PAYOUT_COMPLETED', 'payout:canon1:completed', 'USD', ARRAY[
  t.dr('liability:payout_in_transit', t.p1(), 4000),
  t.cr('asset:psp_settled', t.p1(), 4000)]);
DO $$ BEGIN
  PERFORM t.expect('liability:campaign_payable', t.c1(), 'USD', 575);
  PERFORM t.expect('liability:campaign_payout_pending', t.c1(), 'USD', 0);
  PERFORM t.expect('liability:payout_in_transit', t.p1(), 'USD', 0);
  PERFORM t.expect('asset:psp_settled', t.p1(), 'USD', 575);
  PERFORM t.expect('asset:psp_clearing', t.p1(), 'USD', 0);
  PERFORM t.expect('revenue:platform_fees', NULL, 'USD', 250);
  PERFORM t.expect('asset:fundzim_operating_bank', NULL, 'USD', 250);
END $$;

-- @case canonical_example_verification_views expect=ok
DO $$
DECLARE o bigint; a bigint;
BEGIN
  IF EXISTS (SELECT 1 FROM ledger.v_trial_balance WHERE debit_total_minor <> credit_total_minor) THEN
    RAISE EXCEPTION 'L9 trial balance does not balance';
  END IF;
  IF EXISTS (SELECT 1 FROM ledger.v_balance_drift) THEN
    RAISE EXCEPTION 'L10 projection drift';
  END IF;
  SELECT obligations_minor, provider_assets_minor INTO o, a FROM ledger.v_pool_integrity WHERE currency = 'USD';
  IF o <> 575 OR a <> 575 THEN
    RAISE EXCEPTION 'SC-2 aggregate: obligations % provider assets % (expected 575/575)', o, a;
  END IF;
  IF (SELECT count(*) FROM ledger.ledger_transactions
       WHERE idempotency_key LIKE '%canon1%' OR idempotency_key = 'fee_remittance:p1:batch1') <> 8 THEN
    RAISE EXCEPTION 'expected 8 canonical journals';
  END IF;
END $$;

-- ===== Further documented examples: refund, failed payout, chargeback after payout, funding, write-off (c3) =====
-- @case documented_examples_refund_payout_chargeback expect=ok
DO $$
DECLARE d text; adj uuid; tx uuid;
BEGIN
  FOREACH d IN ARRAY ARRAY['d3a', 'd3b'] LOOP
    PERFORM t.post('DONATION_CAPTURED', 'payment:' || d || ':capture', 'USD', ARRAY[
      t.dr('asset:psp_clearing', t.p1(), 5000), t.cr('liability:campaign_unsettled', t.c3(), 4750), t.cr('revenue:platform_fees', NULL, 250)]);
    PERFORM t.post('PSP_FEE', 'payment:' || d || ':psp_fee', 'USD', ARRAY[
      t.dr('liability:campaign_unsettled', t.c3(), 175), t.cr('asset:psp_clearing', t.p1(), 175)]);
    PERFORM t.post('SETTLEMENT_MATCHED', 'settlement:batch3:' || d, 'USD', ARRAY[
      t.dr('asset:psp_settled', t.p1(), 4825), t.cr('asset:psp_clearing', t.p1(), 4825)]);
    PERFORM t.post('RELEASE', 'payment:' || d || ':release', 'USD', ARRAY[
      t.dr('liability:campaign_unsettled', t.c3(), 4575), t.cr('liability:campaign_payable', t.c3(), 4575)]);
  END LOOP;
  PERFORM t.post('FEE_REMITTANCE', 'fee_remittance:p1:batch3', 'USD', ARRAY[
    t.dr('asset:fundzim_operating_bank', NULL, 500), t.cr('asset:psp_settled', t.p1(), 500)]);
  PERFORM t.expect('liability:campaign_payable', t.c3(), 'USD', 9150);

  -- Example 8: refund after settlement, before payout (refund flows §3.3 B), fee already remitted.
  PERFORM t.post('REFUND_RESERVED', 'refund:r3a:reserved', 'USD', ARRAY[
    t.dr('liability:campaign_payable', t.c3(), 4750), t.dr('revenue:platform_fees', NULL, 250), t.cr('liability:refund_payable', NULL, 5000)]);
  PERFORM t.post('REFUND_FEE_TRUE_UP', 'refund:r3a:fee_true_up', 'USD', ARRAY[
    t.dr('asset:psp_settled', t.p1(), 250), t.cr('asset:fundzim_operating_bank', NULL, 250)]);
  PERFORM t.post('REFUND_SETTLED', 'refund:r3a:settled', 'USD', ARRAY[
    t.dr('liability:refund_payable', NULL, 5000), t.cr('asset:psp_settled', t.p1(), 5000)]);
  PERFORM t.expect('liability:campaign_payable', t.c3(), 'USD', 4400);
  PERFORM t.expect('liability:refund_payable', NULL, 'USD', 0);

  -- Completed payout of 3400, then a payout of 500 that fails after submission (example 7).
  PERFORM t.post('PAYOUT_RESERVED',  'payout:y3a:reserved',  'USD', ARRAY[t.dr('liability:campaign_payable', t.c3(), 3400), t.cr('liability:campaign_payout_pending', t.c3(), 3400)]);
  PERFORM t.post('PAYOUT_SUBMITTED', 'payout:y3a:submitted', 'USD', ARRAY[t.dr('liability:campaign_payout_pending', t.c3(), 3400), t.cr('liability:payout_in_transit', t.p1(), 3400)]);
  PERFORM t.post('PAYOUT_COMPLETED', 'payout:y3a:completed', 'USD', ARRAY[t.dr('liability:payout_in_transit', t.p1(), 3400), t.cr('asset:psp_settled', t.p1(), 3400)]);
  PERFORM t.post('PAYOUT_RESERVED',  'payout:y3b:reserved',  'USD', ARRAY[t.dr('liability:campaign_payable', t.c3(), 500), t.cr('liability:campaign_payout_pending', t.c3(), 500)]);
  PERFORM t.post('PAYOUT_SUBMITTED', 'payout:y3b:submitted', 'USD', ARRAY[t.dr('liability:campaign_payout_pending', t.c3(), 500), t.cr('liability:payout_in_transit', t.p1(), 500)]);
  PERFORM t.post('PAYOUT_FAILED',    'payout:y3b:failed',    'USD', ARRAY[t.dr('liability:payout_in_transit', t.p1(), 500), t.cr('liability:campaign_payable', t.c3(), 500)]);
  PERFORM t.expect('liability:campaign_payable', t.c3(), 'USD', 1000);
  PERFORM t.expect('liability:payout_in_transit', t.p1(), 'USD', 0);

  -- Example 9: chargeback of d3b after payout (refund flows §6 B shape; no reserve here): payable covers 1000,
  -- the rest (3750) is a recoverable; the platform fee is reversed.
  PERFORM t.post('DISPUTE_OPENED', 'dispute:k3b:opened', 'USD', ARRAY[
    t.dr('liability:campaign_payable', t.c3(), 1000), t.dr('asset:chargeback_recoverable', NULL, 3750),
    t.dr('revenue:platform_fees', NULL, 250), t.cr('asset:psp_settled', t.p1(), 5000)]);
  -- Pool-integrity funding (3750 shortfall + 250 remitted fee) needs an approved adjustment (maker-checker).
  adj := t.adj('MANUAL', 'DISPUTE_FUNDING', 'USD', NULL, ARRAY[
    t.dr('asset:psp_settled', t.p1(), 4000), t.cr('asset:fundzim_operating_bank', NULL, 4000)]);
  PERFORM t.approve(adj, t.checker());
  tx := t.post('DISPUTE_FUNDING', 'dispute:k3b:funding', 'USD', ARRAY[
    t.dr('asset:psp_settled', t.p1(), 4000), t.cr('asset:fundzim_operating_bank', NULL, 4000)], adj);
  PERFORM t.mark_posted(adj, NULL, tx);
  -- Lost and unrecovered: write-off (maker-checker).
  adj := t.adj('MANUAL', 'WRITE_OFF', 'USD', NULL, ARRAY[
    t.dr('expense:chargeback_losses', NULL, 3750), t.cr('asset:chargeback_recoverable', NULL, 3750)]);
  PERFORM t.approve(adj, t.checker());
  tx := t.post('WRITE_OFF', 'dispute:k3b:written_off', 'USD', ARRAY[
    t.dr('expense:chargeback_losses', NULL, 3750), t.cr('asset:chargeback_recoverable', NULL, 3750)], adj);
  PERFORM t.mark_posted(adj, NULL, tx);

  PERFORM t.expect('liability:campaign_payable', t.c3(), 'USD', 0);
  PERFORM t.expect('asset:chargeback_recoverable', NULL, 'USD', 0);
  PERFORM t.expect('expense:chargeback_losses', NULL, 'USD', 3750);
  PERFORM t.expect('asset:psp_settled', t.p1(), 'USD', 575);           -- c3 nets to zero in the pool
  PERFORM t.expect('revenue:platform_fees', NULL, 'USD', 250);         -- 250 canonical + 500 - 250 - 250
  PERFORM t.expect('asset:fundzim_operating_bank', NULL, 'USD', -3500); -- 250 + 500 - 250 - 4000 (FundZim funded)
END $$;

-- ===== Example 10: correction = reversal + replacement (maker-checker), campaign c4 =====
-- @case correction_reversal_and_replacement expect=ok
SELECT t.post('DONATION_CAPTURED', 'payment:d4:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 5000), t.cr('liability:campaign_unsettled', t.c4(), 4750), t.cr('revenue:platform_fees', NULL, 250)]);
SELECT t.post('PSP_FEE', 'payment:d4:psp_fee', 'USD', ARRAY[   -- wrong: provider charged 150, not 175
  t.dr('liability:campaign_unsettled', t.c4(), 175), t.cr('asset:psp_clearing', t.p2(), 175)]);
DO $$
DECLARE adj uuid; rev uuid; rep uuid;
BEGIN
  adj := t.adj('CORRECTION', 'PSP_FEE', 'USD', t.tx('payment:d4:psp_fee'), ARRAY[
    t.dr('liability:campaign_unsettled', t.c4(), 150), t.cr('asset:psp_clearing', t.p2(), 150)]);
  PERFORM t.approve(adj, t.checker());
  rev := t.post('REVERSAL', 'reversal:' || t.tx('payment:d4:psp_fee'), 'USD', ARRAY[
    t.dr('asset:psp_clearing', t.p2(), 175), t.cr('liability:campaign_unsettled', t.c4(), 175)], adj, t.tx('payment:d4:psp_fee'));
  rep := t.post('PSP_FEE', 'payment:d4:psp_fee:corrected:' || adj, 'USD', ARRAY[
    t.dr('liability:campaign_unsettled', t.c4(), 150), t.cr('asset:psp_clearing', t.p2(), 150)], adj);
  PERFORM t.mark_posted(adj, rev, rep);
  PERFORM t.expect('liability:campaign_unsettled', t.c4(), 'USD', 4600);
  PERFORM t.expect('asset:psp_clearing', t.p2(), 'USD', 4850);
END $$;

-- ===== L1 / L2 / L3 / L4 / SC-6 =====
-- @case balanced_journal_ok expect=ok
SELECT t.post('DONATION_CAPTURED', 'payment:c2-1:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 1000), t.cr('liability:campaign_unsettled', t.c2(), 950), t.cr('revenue:platform_fees', NULL, 50)]);
SELECT t.post('DONATION_CAPTURED', 'payment:c2-2:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 2000), t.cr('liability:campaign_unsettled', t.c2(), 1900), t.cr('revenue:platform_fees', NULL, 100)]);
SELECT t.post('DONATION_CAPTURED', 'payment:c2-3:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 3000), t.cr('liability:campaign_unsettled', t.c2(), 2850), t.cr('revenue:platform_fees', NULL, 150)]);
SELECT t.post('DONATION_CAPTURED', 'payment:z2-1:capture', 'ZWG', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 7000), t.cr('liability:campaign_unsettled', t.c2(), 7000)]);

-- @case unbalanced_journal_rejected_at_commit expect=error:unbalanced
SELECT t.post('DONATION_CAPTURED', 'payment:c2-bad1:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 1000), t.cr('liability:campaign_unsettled', t.c2(), 900)]);

-- @case single_entry_journal_rejected expect=error:at least 2 required
SELECT t.post('PSP_FEE', 'payment:c2-bad2:psp_fee', 'USD', ARRAY[t.cr('asset:psp_clearing', t.p2(), 10)]);

-- @case header_without_entries_rejected expect=error:at least 2 required
SELECT t.post('PSP_FEE', 'payment:c2-bad3:psp_fee', 'USD', ARRAY[]::jsonb[]);

-- @case mixed_currency_journal_rejected expect=error:fk_ledger_entries_transaction_currency
SELECT t.post('DONATION_CAPTURED', 'payment:c2-bad4:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 1000), t.cr('liability:campaign_unsettled', t.c2(), 1000, 'ZWG')]);

-- @case entry_currency_differs_from_account_currency_rejected expect=error:fk_ledger_entries_account_currency
INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id,
                                        occurred_at, created_by_type, created_by_id)
VALUES ('0b000000-0000-4000-8000-000000000001', 'PSP_FEE', 'payment:c2-bad5:psp_fee', 'USD', 'x', 'payment', gen_random_uuid(),
        now(), 'SYSTEM', 'ledger_test');
INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
  (gen_random_uuid(), '0b000000-0000-4000-8000-000000000001', 1, t.acct('liability:campaign_unsettled', t.c2(), 'ZWG'), 'DEBIT', 10, 'USD');

-- @case zero_amount_rejected expect=error:ck_ledger_entries_amount_positive
SELECT t.post('PSP_FEE', 'payment:c2-bad6:psp_fee', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 0), t.cr('asset:psp_clearing', t.p2(), 0)]);

-- @case negative_amount_rejected expect=error:ck_ledger_entries_amount_positive
SELECT t.post('PSP_FEE', 'payment:c2-bad7:psp_fee', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), -10), t.cr('asset:psp_clearing', t.p2(), -10)]);

-- @case account_class_spec_mismatch_rejected expect=error:ck_ledger_accounts_class_spec
INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id)
VALUES (gen_random_uuid(), 'asset:psp_clearing:' || t.p2(), 'asset:psp_clearing', 'LIABILITY', 'CREDIT', 'CLAIM_ON_PROVIDER', 'ZWG', 'PROVIDER', t.p2());

-- @case unknown_account_class_rejected expect=error:ck_ledger_accounts_class_spec
INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id)
VALUES (gen_random_uuid(), 'liability:user_wallet:' || t.c2(), 'liability:user_wallet', 'LIABILITY', 'CREDIT', 'OBLIGATION', 'USD', 'CAMPAIGN', t.c2());

-- ===== L5 append-only =====
-- @case update_entry_rejected expect=error:append-only
UPDATE ledger.ledger_entries SET amount_minor = amount_minor + 1
 WHERE transaction_id = t.tx('payment:c2-1:capture') AND line_no = 1;

-- @case delete_entry_rejected expect=error:append-only
DELETE FROM ledger.ledger_entries WHERE transaction_id = t.tx('payment:c2-1:capture');

-- @case truncate_entries_rejected expect=error:append-only
TRUNCATE ledger.ledger_entries CASCADE;

-- @case update_transaction_rejected expect=error:append-only
UPDATE ledger.ledger_transactions SET occurred_at = occurred_at - interval '1 day' WHERE idempotency_key = 'payment:c2-1:capture';

-- @case update_account_currency_rejected expect=error:append-only
UPDATE ledger.ledger_accounts SET currency = 'ZWG' WHERE code = 'liability:campaign_unsettled:' || t.c2() AND currency = 'USD';

-- @case direct_projection_update_rejected expect=error:projection maintained only by ledger postings
UPDATE ledger.ledger_balances SET balance_minor = balance_minor + 100, credit_total_minor = credit_total_minor + 100
 WHERE account_id = t.acct('liability:campaign_payable', t.c1(), 'USD');

-- @case append_entry_to_committed_journal_rejected expect=error:sealed
INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency) VALUES
  (gen_random_uuid(), t.tx('payment:c2-1:capture'), 9, t.acct('asset:psp_clearing', t.p2(), 'USD'), 'DEBIT', 5, 'USD'),
  (gen_random_uuid(), t.tx('payment:c2-1:capture'), 10, t.acct('liability:campaign_unsettled', t.c2(), 'USD'), 'CREDIT', 5, 'USD');

-- ===== L6 idempotency =====
-- @case duplicate_idempotency_key_rejected expect=error:uq_ledger_transactions_idempotency_key
SELECT t.post('DONATION_CAPTURED', 'payment:c2-1:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 1000), t.cr('liability:campaign_unsettled', t.c2(), 950), t.cr('revenue:platform_fees', NULL, 50)]);

-- ===== L12 posting-rule lines: SC-1, SC-5, source type, single owner =====
-- @case sc1_payout_reservation_from_unsettled_rejected expect=error:posting rule PAYOUT_RESERVED does not allow DEBIT on liability:campaign_unsettled
SELECT t.post('PAYOUT_RESERVED', 'payout:c2-bad1:reserved', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('liability:campaign_payout_pending', t.c2(), 100)]);

-- @case sc5_operating_bank_touched_by_release_rejected expect=error:does not allow CREDIT on asset:fundzim_operating_bank
SELECT t.post('RELEASE', 'payment:c2-bad8:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('asset:fundzim_operating_bank', NULL, 100)]);

-- @case sc5_rule_line_on_operating_bank_rejected expect=error:ck_ledger_posting_rule_lines_sc5
INSERT INTO ledger.ledger_posting_rule_lines (id, rule_code, direction, account_class)
VALUES (gen_random_uuid(), 'RELEASE', 'DEBIT', 'asset:fundzim_operating_bank');

-- @case sc1_rule_line_widening_rejected expect=error:ck_ledger_posting_rule_lines_sc1
INSERT INTO ledger.ledger_posting_rule_lines (id, rule_code, direction, account_class)
VALUES (gen_random_uuid(), 'PAYOUT_RESERVED', 'DEBIT', 'liability:campaign_reserve');

-- @case required_rule_line_missing_rejected expect=error:requires a CREDIT line on liability:campaign_reserve
SELECT t.post('RELEASE_WITH_RESERVE', 'payment:c2-bad12:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('liability:campaign_payable', t.c2(), 100)]);

-- @case wrong_source_type_rejected expect=error:does not accept source_type
INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id,
                                        occurred_at, created_by_type, created_by_id)
VALUES (gen_random_uuid(), 'PAYOUT_RESERVED', 'payout:c2-bad2:reserved', 'USD', 'x', 'payment', gen_random_uuid(),
        now(), 'SYSTEM', 'ledger_test');

-- @case two_campaigns_in_one_release_rejected expect=error:more than one CAMPAIGN owner
SELECT t.post('RELEASE', 'payment:c2-bad9:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('liability:campaign_payable', t.c4(), 100)]);

-- ===== L8 / L13 non-negative obligations (projection CHECK backstop) =====
-- @case campaign_payable_negative_rejected expect=error:ck_ledger_balances_non_negative
SELECT t.post('PAYOUT_RESERVED', 'payout:c2-bad3:reserved', 'USD', ARRAY[
  t.dr('liability:campaign_payable', t.c2(), 100), t.cr('liability:campaign_payout_pending', t.c2(), 100)]);

-- @case payout_exceeding_available_rejected expect=error:ck_ledger_balances_non_negative
SELECT t.post('PAYOUT_RESERVED', 'payout:c1-bad1:reserved', 'USD', ARRAY[
  t.dr('liability:campaign_payable', t.c1(), 576), t.cr('liability:campaign_payout_pending', t.c1(), 576)]);

-- @case payout_of_exact_available_ok expect=ok
SELECT t.post('PAYOUT_RESERVED', 'payout:c1-2:reserved', 'USD', ARRAY[
  t.dr('liability:campaign_payable', t.c1(), 575), t.cr('liability:campaign_payout_pending', t.c1(), 575)]);
SELECT t.post('PAYOUT_RELEASED', 'payout:c1-2:released', 'USD', ARRAY[
  t.dr('liability:campaign_payout_pending', t.c1(), 575), t.cr('liability:campaign_payable', t.c1(), 575)]);
SELECT t.expect('liability:campaign_payable', t.c1(), 'USD', 575);

-- ===== Periods =====
-- @case posting_into_closed_period_rejected expect=error:is CLOSED
SELECT t.post('RELEASE', 'payment:c2-bad10:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('liability:campaign_payable', t.c2(), 100)],
  NULL, NULL, '2025-12-15T10:00:00Z');

-- @case late_provider_fact_posts_into_open_period expect=ok
SELECT t.post('DONATION_CAPTURED', 'payment:c2-late:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 400), t.cr('liability:campaign_unsettled', t.c2(), 400)],
  NULL, NULL, '2025-12-31T23:59:00Z');
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM ledger.ledger_transactions t1 JOIN ledger.ledger_periods p ON p.id = t1.accounting_period_id
                  WHERE t1.idempotency_key = 'payment:c2-late:capture' AND t1.is_prior_period AND p.code = '2026+') THEN
    RAISE EXCEPTION 'late capture not flagged prior-period in the open period';
  END IF;
END $$;

-- @case occurred_at_outside_any_period_rejected expect=error:no ledger period covers
SELECT t.post('RELEASE', 'payment:c2-bad11:release', 'USD', ARRAY[
  t.dr('liability:campaign_unsettled', t.c2(), 100), t.cr('liability:campaign_payable', t.c2(), 100)],
  NULL, NULL, '2020-01-01T00:00:00Z');

-- ===== Maker-checker adjustments, L7 reversals, L14 approved lines =====
-- @case adjustment_self_approval_rejected expect=error:ck_ledger_adjustments_maker_checker
SELECT t.approve(t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL), t.maker());

-- @case adjustment_approval_of_different_hash_rejected expect=error:ck_ledger_adjustments_approval
DO $$
DECLARE adj uuid := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL);
BEGIN
  UPDATE ledger.ledger_adjustments SET status = 'APPROVED', approved_by = t.checker(), approved_at = now(),
         approved_lines_sha256 = sha256('something else'::bytea), approval_justification = 'x'
   WHERE id = adj;
END $$;

-- @case adjustment_request_is_immutable expect=error:request fields are immutable
DO $$
DECLARE adj uuid := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL);
BEGIN
  UPDATE ledger.ledger_adjustments SET reason = 'changed after the checker looked at it' WHERE id = adj;
END $$;

-- @case adjustment_illegal_transition_rejected expect=error:illegal ledger_adjustment transition
DO $$
DECLARE adj uuid := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL);
BEGIN
  UPDATE ledger.ledger_adjustments SET status = 'POSTED', posted_at = now() WHERE id = adj;
END $$;

-- @case reversal_without_approved_adjustment_rejected expect=error:requires an APPROVED adjustment
SELECT t.post('REVERSAL', 'reversal:' || t.tx('payment:c2-2:capture'), 'USD', ARRAY[
  t.cr('asset:psp_clearing', t.p2(), 2000), t.dr('liability:campaign_unsettled', t.c2(), 1900), t.dr('revenue:platform_fees', NULL, 100)],
  NULL, t.tx('payment:c2-2:capture'));

-- @case write_off_without_approval_rejected expect=error:requires an APPROVED adjustment
SELECT t.post('WRITE_OFF', 'recovery:rc9:written_off', 'USD', ARRAY[
  t.dr('expense:refund_losses', NULL, 10), t.cr('asset:refund_recoverable', t.c2(), 10)]);

-- @case reversal_ok expect=ok
DO $$
DECLARE adj uuid; rev uuid;
BEGIN
  adj := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL);
  PERFORM t.approve(adj, t.checker());
  rev := t.post('REVERSAL', 'reversal:' || t.tx('payment:c2-2:capture'), 'USD', ARRAY[
    t.cr('asset:psp_clearing', t.p2(), 2000), t.dr('liability:campaign_unsettled', t.c2(), 1900), t.dr('revenue:platform_fees', NULL, 100)],
    adj, t.tx('payment:c2-2:capture'));
  PERFORM t.mark_posted(adj, rev, NULL);
END $$;

-- @case second_reversal_of_same_transaction_rejected expect=error:uq_ledger_transactions_reverses
DO $$
DECLARE adj uuid;
BEGIN
  adj := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-2:capture'), NULL);
  PERFORM t.approve(adj, t.checker());
  PERFORM t.post('REVERSAL', 'reversal:again:' || t.tx('payment:c2-2:capture'), 'USD', ARRAY[
    t.cr('asset:psp_clearing', t.p2(), 2000), t.dr('liability:campaign_unsettled', t.c2(), 1900), t.dr('revenue:platform_fees', NULL, 100)],
    adj, t.tx('payment:c2-2:capture'));
END $$;

-- @case posted_adjustment_cannot_post_again expect=error:requires an APPROVED adjustment
SELECT t.post('REVERSAL', 'reversal:third:' || t.tx('payment:c2-2:capture'), 'USD', ARRAY[
  t.cr('asset:psp_clearing', t.p2(), 2000), t.dr('liability:campaign_unsettled', t.c2(), 1900), t.dr('revenue:platform_fees', NULL, 100)],
  (SELECT id FROM ledger.ledger_adjustments WHERE status = 'POSTED' AND kind = 'REVERSAL' LIMIT 1), t.tx('payment:c2-2:capture'));

-- @case reversal_not_mirroring_original_rejected expect=error:does not mirror
DO $$
DECLARE adj uuid;
BEGIN
  adj := t.adj('REVERSAL', 'REVERSAL', 'USD', t.tx('payment:c2-3:capture'), NULL);
  PERFORM t.approve(adj, t.checker());
  PERFORM t.post('REVERSAL', 'reversal:' || t.tx('payment:c2-3:capture'), 'USD', ARRAY[
    t.cr('asset:psp_clearing', t.p2(), 2900), t.dr('liability:campaign_unsettled', t.c2(), 2850), t.dr('revenue:platform_fees', NULL, 50)],
    adj, t.tx('payment:c2-3:capture'));
END $$;

-- @case adjustment_posted_lines_differ_from_approved_rejected expect=error:differ from the approved lines
DO $$
DECLARE adj uuid;
BEGIN
  adj := t.adj('MANUAL', 'ADJUSTMENT', 'USD', NULL, ARRAY[
    t.dr('suspense:settlement_discrepancy', t.p2(), 30), t.cr('asset:psp_clearing', t.p2(), 30)]);
  PERFORM t.approve(adj, t.checker());
  PERFORM t.post('ADJUSTMENT', 'adjustment:' || adj, 'USD', ARRAY[
    t.dr('suspense:settlement_discrepancy', t.p2(), 31), t.cr('asset:psp_clearing', t.p2(), 31)], adj);
END $$;

-- @case manual_adjustment_cannot_touch_operating_bank expect=error:does not allow DEBIT on asset:fundzim_operating_bank
DO $$
DECLARE adj uuid;
BEGIN
  adj := t.adj('MANUAL', 'ADJUSTMENT', 'USD', NULL, ARRAY[
    t.dr('asset:fundzim_operating_bank', NULL, 30), t.cr('asset:psp_settled', t.p2(), 30)]);
  PERFORM t.approve(adj, t.checker());
  PERFORM t.post('ADJUSTMENT', 'adjustment:' || adj, 'USD', ARRAY[
    t.dr('asset:fundzim_operating_bank', NULL, 30), t.cr('asset:psp_settled', t.p2(), 30)], adj);
END $$;

-- @case manual_adjustment_ok expect=ok
DO $$
DECLARE adj uuid; tx uuid;
BEGIN
  adj := t.adj('MANUAL', 'ADJUSTMENT', 'USD', NULL, ARRAY[
    t.dr('suspense:settlement_discrepancy', t.p2(), 30), t.cr('asset:psp_clearing', t.p2(), 30)]);
  PERFORM t.approve(adj, t.checker());
  tx := t.post('ADJUSTMENT', 'adjustment:' || adj, 'USD', ARRAY[
    t.dr('suspense:settlement_discrepancy', t.p2(), 30), t.cr('asset:psp_clearing', t.p2(), 30)], adj);
  PERFORM t.mark_posted(adj, NULL, tx);
END $$;

-- ===== Projection modes: SYNC (decision-guarding) vs DEFERRED (platform-wide hot rows) =====
-- @case projection_modes_by_kind expect=ok
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM ledger.ledger_accounts WHERE non_negative AND projection_mode <> 'SYNC') THEN
    RAISE EXCEPTION 'a non-negative (L8/L13) account is not SYNC';
  END IF;
  IF (SELECT projection_mode FROM ledger.ledger_accounts WHERE code = 'liability:campaign_payable:' || t.c1() AND currency = 'USD') <> 'SYNC'
     OR (SELECT projection_mode FROM ledger.ledger_accounts WHERE code = 'revenue:platform_fees' AND currency = 'USD') <> 'DEFERRED'
     OR (SELECT projection_mode FROM ledger.ledger_accounts WHERE code = 'asset:psp_clearing:' || t.p1() AND currency = 'USD') <> 'DEFERRED' THEN
    RAISE EXCEPTION 'unexpected projection modes';
  END IF;
END $$;

-- @case deferred_posting_does_not_touch_projection_row expect=ok
SELECT t.post('DONATION_CAPTURED', 'payment:c2-def:capture', 'USD', ARRAY[
  t.dr('asset:psp_clearing', t.p2(), 100), t.cr('liability:campaign_unsettled', t.c2(), 95), t.cr('revenue:platform_fees', NULL, 5)]);
DO $$ BEGIN
  IF (SELECT c.unfolded_minor FROM ledger.v_balances_current c JOIN ledger.ledger_accounts a ON a.id = c.account_id
       WHERE a.code = 'revenue:platform_fees' AND a.currency = 'USD') < 5 THEN
    RAISE EXCEPTION 'revenue entry should be in the unfolded tail, not in the projection';
  END IF;
END $$;

-- @case fold_deferred_balances_exact_and_idempotent expect=ok
DO $$
DECLARE before_cur bigint; n int;
BEGIN
  SELECT balance_minor INTO before_cur FROM ledger.v_balances_current c JOIN ledger.ledger_accounts a ON a.id = c.account_id
   WHERE a.code = 'revenue:platform_fees' AND a.currency = 'USD';
  n := ledger.fold_deferred_balances();
  IF n = 0 THEN RAISE EXCEPTION 'fold advanced no rows'; END IF;
  IF t.projected('revenue:platform_fees', NULL, 'USD') <> before_cur THEN
    RAISE EXCEPTION 'fold: projected % <> current %', t.projected('revenue:platform_fees', NULL, 'USD'), before_cur;
  END IF;
  IF EXISTS (SELECT 1 FROM ledger.v_balances_current WHERE unfolded_minor <> 0) THEN
    RAISE EXCEPTION 'unfolded tail remains after fold';
  END IF;
  PERFORM ledger.fold_deferred_balances();   -- rerun: no change
  IF t.projected('revenue:platform_fees', NULL, 'USD') <> before_cur OR EXISTS (SELECT 1 FROM ledger.v_balance_drift) THEN
    RAISE EXCEPTION 'fold not idempotent';
  END IF;
END $$;

-- @case direct_projection_maintenance_flag_is_transaction_local expect=error:projection maintained only by ledger postings
SELECT set_config('ledger.projection_maintenance', 'on', true) WHERE false;  -- flag never set outside the functions
UPDATE ledger.ledger_balances SET version = version + 1 WHERE projection_mode = 'DEFERRED';

-- ===== Global checks after all cases =====
-- @case final_trial_balance_and_projection_recompute expect=ok
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM ledger.v_trial_balance WHERE debit_total_minor <> credit_total_minor) THEN
    RAISE EXCEPTION 'L9 trial balance does not balance';
  END IF;
  IF EXISTS (SELECT 1 FROM ledger.v_balance_drift) THEN
    RAISE EXCEPTION 'L10 projection drift';
  END IF;
  IF (SELECT count(*) FROM ledger.v_trial_balance) <> 2 THEN
    RAISE EXCEPTION 'expected USD and ZWG in the trial balance';
  END IF;
END $$;

-- @case invariant_run_recorded_and_append_only expect=error:append-only
INSERT INTO ledger.ledger_invariant_runs (id, invariant_code, currency, status, started_at, finished_at, checked_count,
                                          triggered_by_type, triggered_by_id)
VALUES ('0c000000-0000-4000-8000-000000000001', 'L9', 'USD', 'PASSED', now() - interval '1 minute', now(), 42, 'SYSTEM', 'ledger.verify');
UPDATE ledger.ledger_invariant_runs SET checked_count = 43 WHERE id = '0c000000-0000-4000-8000-000000000001';

-- @case failed_invariant_run_requires_evidence expect=error:ck_ledger_invariant_runs_failed
INSERT INTO ledger.ledger_invariant_runs (id, invariant_code, currency, status, started_at, finished_at, checked_count, violation_count,
                                          triggered_by_type, triggered_by_id)
VALUES (gen_random_uuid(), 'L10', 'USD', 'FAILED', now() - interval '1 minute', now(), 42, 1, 'SYSTEM', 'ledger.verify');
