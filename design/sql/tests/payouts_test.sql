-- =====================================================================================================
-- Invariant tests for 0016 (payouts). DESIGN DRAFT TESTS — in-memory PGlite only.
-- Each case is one transaction (design/sql/validate/run.mjs). Successful cases commit and are visible to
-- later cases. Helpers live in pg_temp. Money moves through REAL 0006 ledger journals (L1, L8, SC-1).
-- Ids: users a…1x, campaigns c…1x, psp b…1x, destinations d…, payouts e…, recovery 9….
-- =====================================================================================================

-- @case setup_fixtures expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('a0000000-0000-0000-0000-000000000011', 'USER'),    -- campaign owner (payout requester)
  ('a0000000-0000-0000-0000-000000000012', 'STAFF'),   -- FINANCE 1
  ('a0000000-0000-0000-0000-000000000013', 'STAFF'),   -- FINANCE 2
  ('a0000000-0000-0000-0000-000000000014', 'STAFF'),   -- COMPLIANCE
  ('a0000000-0000-0000-0000-000000000015', 'STAFF');   -- staff who verified the destination

CREATE FUNCTION pg_temp.mk_campaign(p_id uuid, p_owner uuid) RETURNS void LANGUAGE plpgsql AS $f$
BEGIN
  INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
  VALUES (p_id, upper(substr(md5(p_id::text), 1, 10)), 'test-' || substr(md5(p_id::text), 1, 8), p_owner, p_owner,
          md5('campaign_category:EDUCATION')::uuid, 'Test campaign', 'USD', '{USD}');
  INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
  VALUES (gen_random_uuid(), p_id, 1, '', 'DRAFT', 'user', p_owner, 'CREATED', now());
END $f$;
SELECT pg_temp.mk_campaign('c0000000-0000-0000-0000-000000000011', 'a0000000-0000-0000-0000-000000000011');
SELECT pg_temp.mk_campaign('c0000000-0000-0000-0000-000000000012', 'a0000000-0000-0000-0000-000000000011');
SELECT pg_temp.mk_campaign('c0000000-0000-0000-0000-000000000013', 'a0000000-0000-0000-0000-000000000011');

INSERT INTO app.payment_providers (id, code, display_name, is_sandbox, status, confirmation_mode)
VALUES ('b0000000-0000-0000-0000-000000000011', 'sandbox', 'Sandbox provider', true, 'ENABLED', 'WEBHOOK_SIGNED');
INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref, status)
VALUES ('b0000000-0000-0000-0000-000000000012', 'b0000000-0000-0000-0000-000000000011', 'SANDBOX', 'default', 'secretref://sandbox/api', 'ENABLED');
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  payout_api, payout_rails, signed_webhooks, status_api, evidence_status, evidence_source, recorded_by)
VALUES
  ('b0000000-0000-0000-0000-000000000013', 'b0000000-0000-0000-0000-000000000011', 1, 'ECOCASH', 'USD', 'USD', 'PSP_POOL',
   true, '{ECOCASH}', true, true, 'VERIFIED', 'sandbox contract (fixture)', 'a0000000-0000-0000-0000-000000000012'),
  ('b0000000-0000-0000-0000-000000000014', 'b0000000-0000-0000-0000-000000000011', 1, 'CARD', 'USD', 'USD', 'PSP_POOL',
   false, '{}', true, true, 'VERIFIED', 'sandbox contract (fixture)', 'a0000000-0000-0000-0000-000000000012');

-- Verified USD EcoCash destination (verified by staff ...15), and a ZWG one.
INSERT INTO app.payout_destinations (id, owner_user_id, payee_type, rail, currency, holder_name, account_number_ciphertext,
  account_number_key_id, account_number_bidx, masked_suffix, destination_hash, status, cooling_off_until, last_changed_by)
VALUES
  ('d0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000011', 'OWNER', 'ECOCASH', 'USD', 'T Owner',
   '\x00ff'::bytea, 'kms:payout-destination:1', sha256('0771234567'::bytea), '4567', sha256('dest1v1'::bytea),
   'PENDING_VERIFICATION', now() + interval '1 day', 'a0000000-0000-0000-0000-000000000011'),
  ('d0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000011', 'OWNER', 'ECOCASH', 'ZWG', 'T Owner',
   '\x00fe'::bytea, 'kms:payout-destination:1', sha256('0771234567'::bytea), '4567', sha256('dest2v1'::bytea),
   'PENDING_VERIFICATION', now() + interval '1 day', 'a0000000-0000-0000-0000-000000000011');
UPDATE app.payout_destinations SET status = 'VERIFIED', verified_at = now(), last_verified_by = 'a0000000-0000-0000-0000-000000000015';

-- Ledger: open period; fund campaign_payable (capture -> settlement -> release) for each campaign.
INSERT INTO ledger.ledger_periods (id, code, starts_at, ends_at, status)
VALUES (gen_random_uuid(), 'TEST', now() - interval '1 day', now() + interval '60 days', 'OPEN');

CREATE FUNCTION pg_temp.acct(p_class text, p_owner uuid, p_cur char(3)) RETURNS uuid LANGUAGE plpgsql AS $f$
DECLARE
  sig    text[] := string_to_array(ledger.account_class_signature(p_class), '|');
  v_code text   := p_class || CASE WHEN p_owner IS NULL THEN '' ELSE ':' || p_owner::text END;
  v      uuid;
BEGIN
  SELECT a.id INTO v FROM ledger.ledger_accounts a WHERE a.code = v_code AND a.currency = p_cur;
  IF v IS NULL THEN
    v := gen_random_uuid();
    INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, owner_id)
    VALUES (v, v_code, p_class, sig[1], sig[2], sig[3], p_cur, sig[4], p_owner);
  END IF;
  RETURN v;
END $f$;

CREATE FUNCTION pg_temp.post(p_rule text, p_key text, p_src text, p_src_id uuid, p_cur char(3), p_lines jsonb)
RETURNS uuid LANGUAGE plpgsql AS $f$
DECLARE t uuid := gen_random_uuid(); l jsonb; n integer := 0;
BEGIN
  INSERT INTO ledger.ledger_transactions (id, posting_rule, idempotency_key, currency, description, source_type, source_id,
    occurred_at, created_by_type, created_by_id)
  VALUES (t, p_rule, p_key, p_cur, 'test fixture', p_src, p_src_id, now(), 'SYSTEM', 'tests');
  FOR l IN SELECT * FROM jsonb_array_elements(p_lines) LOOP
    n := n + 1;
    INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
    VALUES (gen_random_uuid(), t, n, pg_temp.acct(l->>'class', NULLIF(l->>'owner', '')::uuid, p_cur), l->>'dir', (l->>'amt')::bigint, p_cur);
  END LOOP;
  RETURN t;
END $f$;

CREATE FUNCTION pg_temp.two(p_dr_class text, p_dr_owner uuid, p_cr_class text, p_cr_owner uuid, p_amt bigint) RETURNS jsonb
LANGUAGE sql AS $f$
  SELECT jsonb_build_array(
    jsonb_build_object('class', p_dr_class, 'owner', COALESCE(p_dr_owner::text, ''), 'dir', 'DEBIT',  'amt', p_amt),
    jsonb_build_object('class', p_cr_class, 'owner', COALESCE(p_cr_owner::text, ''), 'dir', 'CREDIT', 'amt', p_amt))
$f$;

CREATE FUNCTION pg_temp.fund(p_campaign uuid, p_amt bigint) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE pay uuid := gen_random_uuid(); prov uuid := 'b0000000-0000-0000-0000-000000000011';
BEGIN
  PERFORM pg_temp.post('DONATION_CAPTURED', 'payment:' || pay || ':capture', 'payment', pay, 'USD',
    pg_temp.two('asset:psp_clearing', prov, 'liability:campaign_unsettled', p_campaign, p_amt));
  PERFORM pg_temp.post('SETTLEMENT_MATCHED', 'settlement:' || pay || ':' || pay, 'settlement_item', pay, 'USD',
    pg_temp.two('asset:psp_settled', prov, 'asset:psp_clearing', prov, p_amt));
  PERFORM pg_temp.post('RELEASE', 'payment:' || pay || ':release', 'payment', pay, 'USD',
    pg_temp.two('liability:campaign_unsettled', p_campaign, 'liability:campaign_payable', p_campaign, p_amt));
END $f$;
SELECT pg_temp.fund('c0000000-0000-0000-0000-000000000011', 1000000);
SELECT pg_temp.fund('c0000000-0000-0000-0000-000000000012', 1000000);
SELECT pg_temp.fund('c0000000-0000-0000-0000-000000000013', 1000000);

-- 22 check results: all PASS (EC-22 NOT_APPLICABLE at REQUEST) unless overridden, e.g. '{"EC-12":"FAIL"}'.
CREATE FUNCTION pg_temp.results(p_phase text, p_over jsonb DEFAULT '{}') RETURNS jsonb LANGUAGE sql AS $f$
  SELECT jsonb_agg(jsonb_build_object('check_id', cid, 'result',
           COALESCE(p_over->>cid, CASE WHEN cid = 'EC-22' AND p_phase = 'REQUEST' THEN 'NOT_APPLICABLE' ELSE 'PASS' END),
           'detail_code', 'OK') ORDER BY cid)
    FROM (SELECT 'EC-' || lpad(n::text, 2, '0') AS cid FROM generate_series(1, 22) n) c
$f$;

CREATE FUNCTION pg_temp.decide(p_payout uuid, p_campaign uuid, p_amount bigint, p_phase text, p_outcome text DEFAULT 'ELIGIBLE',
    p_over jsonb DEFAULT '{}') RETURNS uuid LANGUAGE plpgsql AS $f$
DECLARE v uuid := gen_random_uuid();
BEGIN
  INSERT INTO app.payout_eligibility_decisions (id, payout_request_id, campaign_id, currency, amount_minor, requested_by, phase,
    evaluated_at, engine_version, policy_version, inputs, results, outcome, approval_tier, record_hash)
  VALUES (v, p_payout, p_campaign, 'USD', p_amount, 'a0000000-0000-0000-0000-000000000011', p_phase, now(), 'engine-test',
    'policy-test-1', '{"available_minor":"1000000"}', pg_temp.results(p_phase, p_over), p_outcome, 'SINGLE', sha256(v::text::bytea));
  RETURN v;
END $f$;

CREATE FUNCTION pg_temp.po_move(p_id uuid, p_to text, p_source text DEFAULT 'SYSTEM', p_actor uuid DEFAULT NULL,
    p_bump boolean DEFAULT false) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE cur text;
BEGIN
  SELECT status INTO cur FROM app.payout_requests WHERE id = p_id;
  INSERT INTO app.payout_events (id, payout_request_id, from_status, to_status, applied, source, actor_type, actor_id, reason)
  VALUES (gen_random_uuid(), p_id, cur, p_to, true, p_source,
          CASE p_source WHEN 'STAFF' THEN 'STAFF' WHEN 'OWNER' THEN 'OWNER' WHEN 'SYSTEM' THEN 'SYSTEM' ELSE 'PROVIDER' END,
          p_actor, 'test transition');
  UPDATE app.payout_requests
     SET status = p_to, status_rank = app.payout_status_rank(p_to), version = version + 1,
         approval_round = approval_round + CASE WHEN p_bump THEN 1 ELSE 0 END,
         submitted_at   = CASE WHEN p_to = 'SUBMITTED' THEN now() ELSE submitted_at END,
         completed_at   = CASE WHEN p_to = 'COMPLETED' THEN now() ELSE completed_at END,
         failure_code   = CASE WHEN p_to = 'FAILED' THEN 'PROVIDER_DECLINED' ELSE failure_code END,
         unknown_since  = CASE WHEN p_to = 'UNKNOWN' THEN now() ELSE unknown_since END,
         unknown_reason = CASE WHEN p_to = 'UNKNOWN' THEN 'TIMEOUT' ELSE unknown_reason END
   WHERE id = p_id;
END $f$;

CREATE FUNCTION pg_temp.new_payout(p_id uuid, p_campaign uuid, p_amount bigint, p_policy text DEFAULT 'SINGLE',
    p_cap uuid DEFAULT 'b0000000-0000-0000-0000-000000000013', p_dest uuid DEFAULT 'd0000000-0000-0000-0000-000000000001',
    p_review boolean DEFAULT true) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE res uuid; d app.payout_destinations%ROWTYPE;
BEGIN
  SELECT * INTO d FROM app.payout_destinations WHERE id = p_dest;
  res := pg_temp.post('PAYOUT_RESERVED', 'payout:' || p_id || ':reserved', 'payout', p_id, 'USD',
    pg_temp.two('liability:campaign_payable', p_campaign, 'liability:campaign_payout_pending', p_campaign, p_amount));
  PERFORM pg_temp.decide(p_id, p_campaign, p_amount, 'REQUEST');
  INSERT INTO app.payout_events (id, payout_request_id, to_status, applied, transition_code, source, actor_type, actor_id, ledger_transaction_ids)
  VALUES (gen_random_uuid(), p_id, 'PAYOUT_REQUESTED', true, 'Y1', 'OWNER', 'OWNER', 'a0000000-0000-0000-0000-000000000011', ARRAY[res]);
  INSERT INTO app.payout_requests (id, campaign_id, currency, amount_minor, requested_by, status, status_rank, approval_policy,
    policy_version, destination_id, destination_version, destination_hash, destination_rail, destination_masked,
    provider_id, provider_account_id, capability_id, capability_routable, capability_payout_api, idempotency_scope, idempotency_key)
  VALUES (p_id, p_campaign, 'USD', p_amount, 'a0000000-0000-0000-0000-000000000011', 'PAYOUT_REQUESTED', 0, p_policy,
    'policy-test-1', d.id, d.version, d.destination_hash, d.rail, '****' || d.masked_suffix,
    'b0000000-0000-0000-0000-000000000011', 'b0000000-0000-0000-0000-000000000012', p_cap, true, true, 'test', p_id);
  INSERT INTO app.payout_reservations (id, payout_request_id, campaign_id, currency, amount_minor, status, ledger_reserve_txn_id)
  VALUES (gen_random_uuid(), p_id, p_campaign, 'USD', p_amount, 'RESERVED', res);
  IF p_review THEN
    PERFORM pg_temp.po_move(p_id, 'PENDING_REVIEW');
  END IF;
END $f$;

CREATE FUNCTION pg_temp.approve(p_id uuid, p_approver uuid, p_role text DEFAULT 'FINANCE', p_decision text DEFAULT 'APPROVE')
RETURNS void LANGUAGE plpgsql AS $f$
BEGIN
  INSERT INTO app.payout_approvals (id, payout_request_id, approval_round, approver_id, approver_role, decision, reason, step_up_at,
    conflict_attested, expires_at)
  SELECT gen_random_uuid(), p_id, approval_round, p_approver, p_role, p_decision, 'reviewed evidence', now(), true, now() + interval '72 hours'
    FROM app.payout_requests WHERE id = p_id;
END $f$;

CREATE FUNCTION pg_temp.submit(p_id uuid) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE p app.payout_requests%ROWTYPE; t uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = p_id;
  PERFORM pg_temp.decide(p_id, p.campaign_id, p.amount_minor, 'PRE_SUBMIT');
  t := pg_temp.post('PAYOUT_SUBMITTED', 'payout:' || p_id || ':submitted', 'payout', p_id, 'USD',
    pg_temp.two('liability:campaign_payout_pending', p.campaign_id, 'liability:payout_in_transit', p.provider_id, p.amount_minor));
  UPDATE app.payout_reservations SET ledger_submit_txn_id = t WHERE payout_request_id = p_id;
  PERFORM pg_temp.po_move(p_id, 'SUBMITTED');
END $f$;

CREATE FUNCTION pg_temp.complete(p_id uuid) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE p app.payout_requests%ROWTYPE; t uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = p_id;
  t := pg_temp.post('PAYOUT_COMPLETED', 'payout:' || p_id || ':completed', 'payout', p_id, 'USD',
    pg_temp.two('liability:payout_in_transit', p.provider_id, 'asset:psp_settled', p.provider_id, p.amount_minor));
  UPDATE app.payout_reservations SET status = 'CONSUMED', ledger_consume_txn_id = t WHERE payout_request_id = p_id;
  PERFORM pg_temp.po_move(p_id, 'COMPLETED', 'WEBHOOK');
END $f$;

-- Release back to available: before submission (PAYOUT_RELEASED) or after an authoritative failure (PAYOUT_FAILED).
CREATE FUNCTION pg_temp.release(p_id uuid, p_to text, p_source text DEFAULT 'SYSTEM', p_actor uuid DEFAULT NULL)
RETURNS void LANGUAGE plpgsql AS $f$
DECLARE p app.payout_requests%ROWTYPE; t uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = p_id;
  IF p_to = 'FAILED' THEN
    t := pg_temp.post('PAYOUT_FAILED', 'payout:' || p_id || ':failed', 'payout', p_id, 'USD',
      pg_temp.two('liability:payout_in_transit', p.provider_id, 'liability:campaign_payable', p.campaign_id, p.amount_minor));
  ELSE
    t := pg_temp.post('PAYOUT_RELEASED', 'payout:' || p_id || ':released', 'payout', p_id, 'USD',
      pg_temp.two('liability:campaign_payout_pending', p.campaign_id, 'liability:campaign_payable', p.campaign_id, p.amount_minor));
  END IF;
  UPDATE app.payout_reservations SET status = 'RELEASED', ledger_release_txn_id = t WHERE payout_request_id = p_id;
  PERFORM pg_temp.po_move(p_id, p_to, p_source, p_actor);
END $f$;

CREATE FUNCTION pg_temp.attempt(p_id uuid, p_no integer, p_op text DEFAULT 'CREATE_PAYOUT') RETURNS void LANGUAGE sql AS $f$
  INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference, correlation_id, started_at)
  VALUES (gen_random_uuid(), p_id, p_no, p_op, 'b0000000-0000-0000-0000-000000000011', p_id, 'corr-' || p_no, now())
$f$;
CREATE FUNCTION pg_temp.finish(p_id uuid, p_no integer, p_class text) RETURNS void LANGUAGE sql AS $f$
  UPDATE app.payout_attempts SET classification = p_class, completed_at = now(),
         unknown_reason = CASE WHEN p_class = 'OUTCOME_UNKNOWN' THEN 'TIMEOUT' END
   WHERE payout_request_id = p_id AND attempt_no = p_no
$f$;

-- ============================================================================ request, reservation ====

-- @case payout_requested_reserved_and_in_review expect=ok
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 400000);
DO $$ BEGIN
  IF (SELECT b.balance_minor FROM ledger.ledger_balances b JOIN ledger.ledger_accounts a ON a.id = b.account_id
       WHERE a.code = 'liability:campaign_payable:c0000000-0000-0000-0000-000000000011' AND a.currency = 'USD') <> 600000 THEN
    RAISE EXCEPTION 'available balance not reduced by the reservation';
  END IF;
END $$;

-- @case second_inflight_payout_same_campaign_currency_rejected expect=error:uq_payout_requests_one_inflight
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-0000000000f1', 'c0000000-0000-0000-0000-000000000011', 1000);

-- @case payout_above_available_balance_rejected_by_ledger expect=error:ck_ledger_balances_non_negative
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-0000000000f2', 'c0000000-0000-0000-0000-000000000012', 1000001);

-- @case payout_without_reservation_rejected expect=error:has no reservation
SELECT pg_temp.decide('e0000000-0000-0000-0000-0000000000f3', 'c0000000-0000-0000-0000-000000000012', 1000, 'REQUEST');
INSERT INTO app.payout_events (id, payout_request_id, to_status, applied, source, actor_type, actor_id)
VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-0000000000f3', 'PAYOUT_REQUESTED', true, 'OWNER', 'OWNER', 'a0000000-0000-0000-0000-000000000011');
INSERT INTO app.payout_requests (id, campaign_id, currency, amount_minor, requested_by, status, status_rank, approval_policy,
  policy_version, destination_id, destination_version, destination_hash, destination_rail, destination_masked,
  provider_id, provider_account_id, capability_id, capability_routable, capability_payout_api, idempotency_scope, idempotency_key)
SELECT 'e0000000-0000-0000-0000-0000000000f3', 'c0000000-0000-0000-0000-000000000012', 'USD', 1000, 'a0000000-0000-0000-0000-000000000011',
  'PAYOUT_REQUESTED', 0, 'SINGLE', 'policy-test-1', id, version, destination_hash, rail, '****4567',
  'b0000000-0000-0000-0000-000000000011', 'b0000000-0000-0000-0000-000000000012', 'b0000000-0000-0000-0000-000000000013', true, true, 'test', gen_random_uuid()
  FROM app.payout_destinations WHERE id = 'd0000000-0000-0000-0000-000000000001';

-- @case payout_without_request_decision_rejected expect=error:no non-rejecting REQUEST eligibility decision
DO $$
DECLARE res uuid;
BEGIN
  res := pg_temp.post('PAYOUT_RESERVED', 'payout:e0000000-0000-0000-0000-0000000000f4:reserved', 'payout', 'e0000000-0000-0000-0000-0000000000f4', 'USD',
    pg_temp.two('liability:campaign_payable', 'c0000000-0000-0000-0000-000000000012', 'liability:campaign_payout_pending', 'c0000000-0000-0000-0000-000000000012', 1000));
  INSERT INTO app.payout_events (id, payout_request_id, to_status, applied, source, actor_type, actor_id)
  VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-0000000000f4', 'PAYOUT_REQUESTED', true, 'OWNER', 'OWNER', 'a0000000-0000-0000-0000-000000000011');
  INSERT INTO app.payout_requests (id, campaign_id, currency, amount_minor, requested_by, status, status_rank, approval_policy,
    policy_version, destination_id, destination_version, destination_hash, destination_rail, destination_masked,
    provider_id, provider_account_id, capability_id, capability_routable, capability_payout_api, idempotency_scope, idempotency_key)
  SELECT 'e0000000-0000-0000-0000-0000000000f4', 'c0000000-0000-0000-0000-000000000012', 'USD', 1000, 'a0000000-0000-0000-0000-000000000011',
    'PAYOUT_REQUESTED', 0, 'SINGLE', 'policy-test-1', id, version, destination_hash, rail, '****4567',
    'b0000000-0000-0000-0000-000000000011', 'b0000000-0000-0000-0000-000000000012', 'b0000000-0000-0000-0000-000000000013', true, true, 'test', gen_random_uuid()
    FROM app.payout_destinations WHERE id = 'd0000000-0000-0000-0000-000000000001';
  INSERT INTO app.payout_reservations (id, payout_request_id, campaign_id, currency, amount_minor, status, ledger_reserve_txn_id)
  VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-0000000000f4', 'c0000000-0000-0000-0000-000000000012', 'USD', 1000, 'RESERVED', res);
END $$;

-- @case payout_on_capability_without_payout_api_rejected expect=error:fk_payout_requests_capability
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-0000000000f5', 'c0000000-0000-0000-0000-000000000012', 1000, 'SINGLE',
  'b0000000-0000-0000-0000-000000000014');

-- @case payout_currency_must_equal_destination_currency expect=error:fk_payout_requests_destination
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-0000000000f6', 'c0000000-0000-0000-0000-000000000012', 1000, 'SINGLE',
  'b0000000-0000-0000-0000-000000000013', 'd0000000-0000-0000-0000-000000000002');

-- @case payout_amount_immutable expect=error:immutable
UPDATE app.payout_requests SET amount_minor = 1 WHERE id = 'e0000000-0000-0000-0000-000000000001';

-- @case payout_destination_snapshot_immutable expect=error:immutable
UPDATE app.payout_requests SET destination_version = 2 WHERE id = 'e0000000-0000-0000-0000-000000000001';

-- ============================================================================= approvals (maker-checker) ====

-- @case payout_self_approval_rejected expect=error:approver cannot be the requester
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000011');

-- @case destination_verifier_cannot_approve expect=error:segregation of duties
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000015');

-- @case approve_without_approval_rows_rejected expect=error:insufficient approvals
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case single_approval_then_approved expect=ok
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case approval_only_in_pending_review expect=error:approvals are recorded only in PENDING_REVIEW
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000013');

-- @case approvals_append_only expect=error:append-only
UPDATE app.payout_approvals SET decision = 'REJECT' WHERE payout_request_id = 'e0000000-0000-0000-0000-000000000001';

-- @case dual_policy_needs_two_distinct_approvers expect=error:insufficient approvals
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000012', 300000, 'DUAL');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000002', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case dual_policy_same_approver_twice_rejected expect=error:uq_payout_approvals_approver
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000012', 300000, 'DUAL');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000012');

-- @case compliance_never_approves_payouts expect=error:ck_payout_approvals_role
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000012', 300000, 'DUAL');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000014', 'COMPLIANCE');

-- @case dual_policy_two_distinct_finance_approved expect=ok
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000012', 300000, 'DUAL');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000013');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000002', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case non_auto_policy_cannot_skip_review expect=error:only the AUTO tier may skip review
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000004', 'c0000000-0000-0000-0000-000000000013', 1000, 'SINGLE',
  p_review => false);
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000004', 'APPROVED');

-- @case approval_policy_cannot_be_lowered expect=error:may only be raised
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000004', 'c0000000-0000-0000-0000-000000000013', 1000, 'DUAL');
UPDATE app.payout_requests SET approval_policy = 'SINGLE' WHERE id = 'e0000000-0000-0000-0000-000000000004';

-- ===================================================================================== eligibility ====

-- @case submit_without_pre_submit_recheck_rejected expect=error:without an ELIGIBLE PRE_SUBMIT decision
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'SUBMITTED');

-- @case eligible_outcome_with_failed_check_rejected expect=error:ck_payout_eligibility_decisions_results
SELECT pg_temp.decide('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 400000, 'PRE_SUBMIT', 'ELIGIBLE', '{"EC-12":"FAIL"}');

-- @case eligible_outcome_with_review_check_rejected expect=error:ck_payout_eligibility_decisions_results
SELECT pg_temp.decide('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 400000, 'PRE_SUBMIT', 'ELIGIBLE', '{"EC-21":"REVIEW"}');

-- @case decision_missing_a_check_rejected expect=error:ck_payout_eligibility_decisions_results
INSERT INTO app.payout_eligibility_decisions (id, payout_request_id, campaign_id, currency, amount_minor, requested_by, phase,
  evaluated_at, engine_version, policy_version, inputs, results, outcome, record_hash)
VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 'USD', 400000,
  'a0000000-0000-0000-0000-000000000011', 'PRE_SUBMIT', now(), 'engine-test', 'policy-test-1', '{}',
  (SELECT jsonb_agg(e) FROM jsonb_array_elements(pg_temp.results('PRE_SUBMIT')) e WHERE e->>'check_id' <> 'EC-15'),
  'ELIGIBLE', sha256('x'::bytea));

-- @case pre_submit_must_check_approvals expect=error:ck_payout_eligibility_decisions_results
SELECT pg_temp.decide('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 400000, 'PRE_SUBMIT', 'ELIGIBLE', '{"EC-22":"NOT_APPLICABLE"}');

-- @case decision_facts_must_match_payout expect=error:fk_payout_eligibility_decisions_payout
SELECT pg_temp.decide('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 999, 'PRE_SUBMIT');

-- @case refused_request_decision_without_payout_ok expect=ok
SELECT pg_temp.decide(NULL, 'c0000000-0000-0000-0000-000000000013', 5000000, 'REQUEST', 'REJECTED', '{"EC-09":"FAIL"}');

-- @case refused_request_cannot_be_eligible expect=error:ck_payout_eligibility_decisions_refused
SELECT pg_temp.decide(NULL, 'c0000000-0000-0000-0000-000000000013', 1000, 'REQUEST', 'ELIGIBLE');

-- @case eligibility_decisions_append_only expect=error:append-only
UPDATE app.payout_eligibility_decisions SET outcome = 'ELIGIBLE' WHERE outcome = 'REJECTED';

-- ================================================================ submission, UNKNOWN, no resubmission ====

-- @case submitted_after_recheck_with_in_transit_journal expect=ok
SELECT pg_temp.submit('e0000000-0000-0000-0000-000000000001');
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000001', 1);

-- @case attempt_reference_is_payout_id expect=error:ck_payout_attempts_reference
INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference, correlation_id, started_at)
VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-000000000001', 9, 'GET_STATUS', 'b0000000-0000-0000-0000-000000000011',
  gen_random_uuid(), 'corr-x', now());

-- @case second_create_while_first_in_flight_rejected expect=error:payout resubmission forbidden
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000001', 2);

-- @case timeout_moves_submitted_to_unknown expect=ok
SELECT pg_temp.finish('e0000000-0000-0000-0000-000000000001', 1, 'OUTCOME_UNKNOWN');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'UNKNOWN', 'SYSTEM');

-- @case unknown_to_submitted_rejected_even_with_fresh_recheck expect=error:illegal payout transition
SELECT pg_temp.decide('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000011', 400000, 'PRE_SUBMIT');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'SUBMITTED');

-- @case create_payout_while_unknown_rejected expect=error:payout resubmission forbidden
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000001', 2);

-- @case status_query_while_unknown_allowed expect=ok
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000001', 2, 'GET_STATUS');

-- @case staff_cannot_fail_unknown_without_provider_evidence expect=error:ck_payout_events_staff_failed_evidence
SELECT pg_temp.release('e0000000-0000-0000-0000-000000000001', 'FAILED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case system_cannot_fail_unknown_payout expect=error:ck_payout_events_failed_source
SELECT pg_temp.release('e0000000-0000-0000-0000-000000000001', 'FAILED', 'SYSTEM');

-- @case unknown_to_failed_needs_funds_returned expect=error:but its reservation is RESERVED
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'FAILED', 'POLL');

-- @case unknown_to_processing_by_webhook expect=ok
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'PROCESSING', 'WEBHOOK');

-- @case external_event_cannot_push_into_unknown expect=error:ck_payout_events_unknown_system
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'UNKNOWN', 'WEBHOOK');

-- @case completion_never_from_owner_or_system expect=error:ck_payout_events_completed_source
SELECT pg_temp.complete('e0000000-0000-0000-0000-000000000001');
INSERT INTO app.payout_events (id, payout_request_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-000000000001', 'PROCESSING', 'COMPLETED', true, 'SYSTEM', 'SYSTEM');

-- @case completion_without_consumed_reservation_rejected expect=error:but its reservation is RESERVED
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'COMPLETED', 'WEBHOOK');

-- @case processing_to_completed_with_journal expect=ok
SELECT pg_temp.complete('e0000000-0000-0000-0000-000000000001');

-- @case completed_to_failed_illegal expect=error:illegal payout transition
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000001', 'FAILED', 'WEBHOOK');

-- @case reservation_journal_links_set_once expect=error:set once
UPDATE app.payout_reservations SET ledger_consume_txn_id = ledger_submit_txn_id
 WHERE payout_request_id = 'e0000000-0000-0000-0000-000000000001';

-- @case payout_events_update_rejected expect=error:append-only
UPDATE app.payout_events SET reason = 'edited' WHERE payout_request_id = 'e0000000-0000-0000-0000-000000000001';

-- @case payout_events_delete_rejected expect=error:append-only
DELETE FROM app.payout_events WHERE payout_request_id = 'e0000000-0000-0000-0000-000000000001';

-- @case next_payout_allowed_after_completion expect=ok
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000003', 'c0000000-0000-0000-0000-000000000011', 1000);

-- @case same_reference_retry_only_after_definitely_not_sent expect=ok
SELECT pg_temp.submit('e0000000-0000-0000-0000-000000000002');
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000002', 1);
SELECT pg_temp.finish('e0000000-0000-0000-0000-000000000002', 1, 'DEFINITELY_NOT_SENT');
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000002', 2);
SELECT pg_temp.finish('e0000000-0000-0000-0000-000000000002', 2, 'OUTCOME_UNKNOWN');

-- @case retry_after_possible_send_rejected expect=error:payout resubmission forbidden
SELECT pg_temp.attempt('e0000000-0000-0000-0000-000000000002', 3);

-- @case unknown_to_failed_on_authoritative_failure_with_funds_returned expect=ok
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000002', 'UNKNOWN', 'SYSTEM');
SELECT pg_temp.release('e0000000-0000-0000-0000-000000000002', 'FAILED', 'POLL');

-- @case failed_is_terminal expect=error:illegal payout transition
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000002', 'PROCESSING', 'WEBHOOK');

-- ============================================================================== review loop, cancel ====

-- @case back_to_review_must_invalidate_approvals expect=error:must start a new approval round
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'PENDING_REVIEW');

-- @case old_round_approvals_do_not_count expect=error:insufficient approvals
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'PENDING_REVIEW', p_bump => true);
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'APPROVED', 'STAFF', 'a0000000-0000-0000-0000-000000000012');

-- @case owner_cannot_drive_approval expect=error:ck_payout_events_owner_scope
INSERT INTO app.payout_events (id, payout_request_id, from_status, to_status, applied, source, actor_type, actor_id)
VALUES (gen_random_uuid(), 'e0000000-0000-0000-0000-000000000003', 'PENDING_REVIEW', 'APPROVED', true, 'OWNER', 'OWNER',
  'a0000000-0000-0000-0000-000000000011');

-- @case cancel_requires_funds_released expect=error:but its reservation is RESERVED
SELECT pg_temp.po_move('e0000000-0000-0000-0000-000000000003', 'CANCELLED', 'OWNER', 'a0000000-0000-0000-0000-000000000011');

-- @case owner_cancels_with_release_journal expect=ok
SELECT pg_temp.release('e0000000-0000-0000-0000-000000000003', 'CANCELLED', 'OWNER', 'a0000000-0000-0000-0000-000000000011');

-- =================================================================================== destinations ====

-- @case destination_account_change_requires_new_version expect=error:destination change requires
UPDATE app.payout_destinations SET account_number_bidx = sha256('0779999999'::bytea), account_number_ciphertext = '\x0100'::bytea
 WHERE id = 'd0000000-0000-0000-0000-000000000001';

-- @case destination_account_change_as_new_version_ok expect=ok
UPDATE app.payout_destinations SET account_number_bidx = sha256('0779999999'::bytea), account_number_ciphertext = '\x0100'::bytea,
  masked_suffix = '9999', version = version + 1, status = 'PENDING_VERIFICATION', cooling_off_until = now() + interval '2 days',
  destination_hash = sha256('dest1v2'::bytea)
 WHERE id = 'd0000000-0000-0000-0000-000000000001';

-- @case destination_currency_immutable expect=error:immutable
UPDATE app.payout_destinations SET currency = 'ZWG' WHERE id = 'd0000000-0000-0000-0000-000000000001';

-- @case destination_needs_exactly_one_payee expect=error:ck_payout_destinations_payee
INSERT INTO app.payout_destinations (id, owner_user_id, payee_type, rail, currency, holder_name, account_number_ciphertext,
  account_number_key_id, account_number_bidx, masked_suffix, destination_hash, status, cooling_off_until, last_changed_by)
VALUES (gen_random_uuid(), 'a0000000-0000-0000-0000-000000000011', 'BENEFICIARY', 'ECOCASH', 'USD', 'X', '\x00'::bytea, 'k',
  sha256('x1'::bytea), '1111', sha256('x2'::bytea), 'PENDING_VERIFICATION', now() + interval '1 day', 'a0000000-0000-0000-0000-000000000011');

-- @case destination_verifications_append_only expect=error:append-only
INSERT INTO app.payout_destination_verifications (id, payout_destination_id, destination_version, check_kind, method, result, performed_at)
VALUES ('d0000000-0000-0000-0000-0000000000a1', 'd0000000-0000-0000-0000-000000000001', 2, 'NAME_MATCH', 'PROVIDER_NAME_LOOKUP', 'PASS', now());
UPDATE app.payout_destination_verifications SET result = 'FAIL' WHERE id = 'd0000000-0000-0000-0000-0000000000a1';

-- ====================================================================== staff destination override ====

-- @case override_self_approval_rejected expect=error:ck_pdor_maker_checker
INSERT INTO app.payout_destination_override_requests (id, payout_destination_id, base_destination_version, proposed_holder_name,
  proposed_account_ciphertext, proposed_account_key_id, proposed_account_bidx, proposed_masked_suffix, proposed_destination_hash,
  reason, evidence_record_ids, requested_by, expires_at, status)
VALUES ('d0000000-0000-0000-0000-0000000000b1', 'd0000000-0000-0000-0000-000000000002', 1, 'T Owner', '\x0200'::bytea, 'k',
  sha256('0778888888'::bytea), '8888', sha256('dest2v2'::bytea), 'owner lost SIM, verified call', ARRAY[gen_random_uuid()],
  'a0000000-0000-0000-0000-000000000014', now() + interval '24 hours', 'PENDING');
UPDATE app.payout_destination_override_requests SET status = 'APPROVED', approved_by = 'a0000000-0000-0000-0000-000000000014',
  approved_at = now(), approved_step_up_at = now(), decision_reason = 'ok' WHERE id = 'd0000000-0000-0000-0000-0000000000b1';

-- @case override_approved_and_applied_with_cooling_off expect=ok
INSERT INTO app.payout_destination_override_requests (id, payout_destination_id, base_destination_version, proposed_holder_name,
  proposed_account_ciphertext, proposed_account_key_id, proposed_account_bidx, proposed_masked_suffix, proposed_destination_hash,
  reason, evidence_record_ids, requested_by, expires_at, status)
VALUES ('d0000000-0000-0000-0000-0000000000b2', 'd0000000-0000-0000-0000-000000000002', 1, 'T Owner', '\x0200'::bytea, 'k',
  sha256('0778888888'::bytea), '8888', sha256('dest2v2'::bytea), 'owner lost SIM, verified call', ARRAY[gen_random_uuid()],
  'a0000000-0000-0000-0000-000000000014', now() + interval '24 hours', 'PENDING');
UPDATE app.payout_destination_override_requests SET status = 'APPROVED', approved_by = 'a0000000-0000-0000-0000-000000000012',
  approved_at = now(), approved_step_up_at = now(), decision_reason = 'evidence checked' WHERE id = 'd0000000-0000-0000-0000-0000000000b2';
UPDATE app.payout_destinations SET account_number_bidx = sha256('0778888888'::bytea), account_number_ciphertext = '\x0200'::bytea,
  masked_suffix = '8888', version = 2, status = 'PENDING_VERIFICATION', cooling_off_until = now() + interval '3 days',
  destination_hash = sha256('dest2v2'::bytea), last_changed_by = 'a0000000-0000-0000-0000-000000000014'
 WHERE id = 'd0000000-0000-0000-0000-000000000002';
UPDATE app.payout_destination_override_requests r SET status = 'APPLIED', applied_at = now(), applied_destination_version = 2,
  cooling_off_until = d.cooling_off_until
  FROM app.payout_destinations d WHERE d.id = r.payout_destination_id AND r.id = 'd0000000-0000-0000-0000-0000000000b2';

-- @case override_applied_without_destination_change_rejected expect=error:applied without the matching new destination version
INSERT INTO app.payout_destination_override_requests (id, payout_destination_id, base_destination_version, proposed_holder_name,
  proposed_account_ciphertext, proposed_account_key_id, proposed_account_bidx, proposed_masked_suffix, proposed_destination_hash,
  reason, evidence_record_ids, requested_by, expires_at, status)
VALUES ('d0000000-0000-0000-0000-0000000000b3', 'd0000000-0000-0000-0000-000000000002', 2, 'T Owner', '\x0300'::bytea, 'k',
  sha256('0777777777'::bytea), '7777', sha256('dest2v3'::bytea), 'second change request', ARRAY[gen_random_uuid()],
  'a0000000-0000-0000-0000-000000000014', now() + interval '24 hours', 'PENDING');
UPDATE app.payout_destination_override_requests SET status = 'APPROVED', approved_by = 'a0000000-0000-0000-0000-000000000012',
  approved_at = now(), approved_step_up_at = now(), decision_reason = 'ok' WHERE id = 'd0000000-0000-0000-0000-0000000000b3';
UPDATE app.payout_destination_override_requests SET status = 'APPLIED', applied_at = now(), applied_destination_version = 3,
  cooling_off_until = now() + interval '3 days' WHERE id = 'd0000000-0000-0000-0000-0000000000b3';

-- @case override_maker_cannot_approve_payouts_to_destination expect=error:segregation of duties
SELECT pg_temp.new_payout('e0000000-0000-0000-0000-000000000005', 'c0000000-0000-0000-0000-000000000013', 1000);
-- Destination last changed by staff ...13 (a staff override maker): ...13 may not approve payouts to it.
UPDATE app.payout_destinations SET status = 'VERIFIED', verified_at = now(), last_verified_by = 'a0000000-0000-0000-0000-000000000015',
  last_changed_by = 'a0000000-0000-0000-0000-000000000013'
 WHERE id = 'd0000000-0000-0000-0000-000000000001';
SELECT pg_temp.approve('e0000000-0000-0000-0000-000000000005', 'a0000000-0000-0000-0000-000000000013');

-- ================================================================================= recovery cases ====

-- @case recovery_case_opened expect=ok
INSERT INTO app.recovery_case_events (id, recovery_case_id, to_status, event_kind, currency, actor_type)
VALUES (gen_random_uuid(), '90000000-0000-0000-0000-000000000001', 'OPEN', 'OPENED', 'USD', 'SYSTEM');
INSERT INTO app.recovery_cases (id, source_type, source_id, campaign_id, subject_type, subject_id, currency, amount_minor, status)
VALUES ('90000000-0000-0000-0000-000000000001', 'PAYMENT_DISPUTE', gen_random_uuid(), 'c0000000-0000-0000-0000-000000000011',
  'USER', 'a0000000-0000-0000-0000-000000000011', 'USD', 3250, 'OPEN');

-- @case recovery_write_off_self_approval_rejected expect=error:ck_recovery_cases_write_off_sod
INSERT INTO app.recovery_case_events (id, recovery_case_id, from_status, to_status, event_kind, currency, actor_type, actor_id, reason)
VALUES (gen_random_uuid(), '90000000-0000-0000-0000-000000000001', 'OPEN', 'WRITE_OFF_PENDING', 'WRITE_OFF_REQUESTED', 'USD', 'STAFF',
  'a0000000-0000-0000-0000-000000000012', 'unrecoverable');
UPDATE app.recovery_cases SET status = 'WRITE_OFF_PENDING', write_off_requested_by = 'a0000000-0000-0000-0000-000000000012',
  write_off_amount_minor = 3250, write_off_threshold_limit_ref = 'limit:recovery.write_off_business_min@1',
  write_off_approved_by = 'a0000000-0000-0000-0000-000000000012' WHERE id = '90000000-0000-0000-0000-000000000001';

-- @case recovery_case_status_change_needs_event expect=error:missing recovery_case_events row
UPDATE app.recovery_cases SET status = 'WRITE_OFF_PENDING', write_off_requested_by = 'a0000000-0000-0000-0000-000000000012',
  write_off_amount_minor = 3250, write_off_threshold_limit_ref = 'limit:recovery.write_off_business_min@1'
 WHERE id = '90000000-0000-0000-0000-000000000001';

-- @case write_off_pending_with_maker expect=ok
INSERT INTO app.recovery_case_events (id, recovery_case_id, from_status, to_status, event_kind, currency, amount_minor, actor_type, actor_id, reason)
VALUES (gen_random_uuid(), '90000000-0000-0000-0000-000000000001', 'OPEN', 'WRITE_OFF_PENDING', 'WRITE_OFF_REQUESTED', 'USD', 3250, 'STAFF',
  'a0000000-0000-0000-0000-000000000012', 'unrecoverable after 3 attempts');
UPDATE app.recovery_cases SET status = 'WRITE_OFF_PENDING', write_off_requested_by = 'a0000000-0000-0000-0000-000000000012',
  write_off_amount_minor = 3250, write_off_threshold_limit_ref = 'limit:recovery.write_off_business_min@1'
 WHERE id = '90000000-0000-0000-0000-000000000001';

-- @case written_off_needs_checker_and_role expect=error:ck_recovery_cases_written_off
INSERT INTO app.recovery_case_events (id, recovery_case_id, from_status, to_status, event_kind, currency, actor_type, actor_id, reason)
VALUES (gen_random_uuid(), '90000000-0000-0000-0000-000000000001', 'WRITE_OFF_PENDING', 'WRITTEN_OFF', 'NOTE', 'USD', 'STAFF',
  'a0000000-0000-0000-0000-000000000013', 'approve write-off');
UPDATE app.recovery_cases SET status = 'WRITTEN_OFF', written_off_minor = 3250, closed_at = now()
 WHERE id = '90000000-0000-0000-0000-000000000001';
