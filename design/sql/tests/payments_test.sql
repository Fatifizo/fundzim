-- =====================================================================================================
-- Invariant tests for 0007 (fees, psp) and 0015 (payments). DESIGN DRAFT TESTS — in-memory PGlite only.
-- Each case is one transaction (design/sql/validate/run.mjs). Successful cases commit and are visible to
-- later cases. Helper functions live in pg_temp (session-scoped; PGlite has one session).
-- Ids: users a…, campaigns c…, psp b…, fees f…, intents 1…, refunds 2…, disputes 3…, inbox 4….
-- =====================================================================================================

-- @case setup_fixtures expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('a0000000-0000-0000-0000-000000000001', 'USER'),    -- donor
  ('a0000000-0000-0000-0000-000000000002', 'STAFF'),   -- SUPPORT (refund maker)
  ('a0000000-0000-0000-0000-000000000003', 'STAFF'),   -- FINANCE (checker)
  ('a0000000-0000-0000-0000-000000000004', 'STAFF');   -- second FINANCE / COMPLIANCE

-- Campaign fixture in one place (columns follow 0014_campaigns.sql; DRAFT is enough for payments
-- invariants — "campaign ACTIVE" is an application pre-submit check, P4).
CREATE FUNCTION pg_temp.mk_campaign(p_id uuid, p_owner uuid) RETURNS void LANGUAGE plpgsql AS $f$
BEGIN
  INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
  VALUES (p_id, upper(substr(md5(p_id::text), 1, 10)), 'test-' || substr(md5(p_id::text), 1, 8), p_owner, p_owner,
          md5('campaign_category:EDUCATION')::uuid, 'Test campaign', 'USD', '{USD}');
  INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
  VALUES (gen_random_uuid(), p_id, 1, '', 'DRAFT', 'user', p_owner, 'CREATED', now());
END $f$;
SELECT pg_temp.mk_campaign('c0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001');
SELECT pg_temp.mk_campaign('c0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000001');

INSERT INTO app.payment_providers (id, code, display_name, is_sandbox, status, confirmation_mode)
VALUES ('b0000000-0000-0000-0000-000000000001', 'sandbox', 'Sandbox provider', true, 'ENABLED', 'WEBHOOK_SIGNED');
INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref, webhook_secret_ref, status)
VALUES ('b0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000001', 'SANDBOX', 'default',
        'secretref://sandbox/api', 'secretref://sandbox/webhook', 'ENABLED');
-- Routable: VERIFIED, PSP_POOL, signed webhooks + status API.
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  refund_api, partial_refund, payout_api, payout_rails, signed_webhooks, status_api, evidence_status, evidence_source, recorded_by)
VALUES ('b0000000-0000-0000-0000-000000000003', 'b0000000-0000-0000-0000-000000000001', 1, 'ECOCASH', 'USD', 'USD', 'PSP_POOL',
  true, true, true, '{ECOCASH}', true, true, 'VERIFIED', 'sandbox contract v1 (test fixture)', 'a0000000-0000-0000-0000-000000000003');
-- VERIFIED but MERCHANT_SETTLEMENT (Model B in substance): must never be routable.
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  signed_webhooks, status_api, evidence_status, evidence_source, recorded_by)
VALUES ('b0000000-0000-0000-0000-000000000004', 'b0000000-0000-0000-0000-000000000001', 1, 'CARD', 'USD', 'USD', 'MERCHANT_SETTLEMENT',
  true, true, 'VERIFIED', 'sandbox contract v1 (test fixture)', 'a0000000-0000-0000-0000-000000000003');
-- UNVERIFIED claim: recorded but inert.
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  evidence_status, recorded_by)
VALUES ('b0000000-0000-0000-0000-000000000005', 'b0000000-0000-0000-0000-000000000001', 1, 'ONEMONEY', 'USD', 'USD', 'PSP_POOL',
  'UNVERIFIED', 'a0000000-0000-0000-0000-000000000003');

-- Platform fee schedule via maker-checker.
INSERT INTO app.fee_schedules (id, code, name, fee_kind) VALUES ('f0000000-0000-0000-0000-000000000001', 'platform_fee', 'Platform fee', 'PLATFORM_FEE');
INSERT INTO app.fee_schedule_change_requests (id, fee_schedule_id, currency, proposed_rate_bps, proposed_effective_from, justification,
  status, requested_by, expires_at, proposal_hash)
VALUES ('f0000000-0000-0000-0000-000000000002', 'f0000000-0000-0000-0000-000000000001', 'USD', 500, now() + interval '1 day',
  'initial schedule (test)', 'PENDING', 'a0000000-0000-0000-0000-000000000003', now() + interval '7 days', sha256('p1'::bytea));
UPDATE app.fee_schedule_change_requests SET status = 'APPROVED', decided_by = 'a0000000-0000-0000-0000-000000000004',
  decided_at = now(), decision_reason = 'ok', decided_step_up_at = now()
 WHERE id = 'f0000000-0000-0000-0000-000000000002';
INSERT INTO app.fee_schedule_versions (id, fee_schedule_id, version_no, currency, rate_bps, effective_from, change_request_id)
SELECT 'f0000000-0000-0000-0000-000000000003', fee_schedule_id, 1, currency, proposed_rate_bps, proposed_effective_from, id
  FROM app.fee_schedule_change_requests WHERE id = 'f0000000-0000-0000-0000-000000000002';

-- Helpers ---------------------------------------------------------------------------------------------
CREATE FUNCTION pg_temp.new_intent(p_id uuid, p_cap uuid DEFAULT 'b0000000-0000-0000-0000-000000000003',
    p_method text DEFAULT 'ECOCASH', p_amount bigint DEFAULT 5000, p_campaign uuid DEFAULT 'c0000000-0000-0000-0000-000000000001',
    p_routable boolean DEFAULT true, p_custody text DEFAULT 'PSP_POOL') RETURNS void LANGUAGE plpgsql AS $f$
BEGIN
  INSERT INTO app.payment_events (id, subject_type, payment_intent_id, from_status, to_status, applied, transition_code, source, actor_type)
  VALUES (gen_random_uuid(), 'PAYMENT_INTENT', p_id, NULL, 'CREATED', true, 'P1', 'INTERNAL', 'DONOR');
  INSERT INTO app.payment_intents (id, campaign_id, currency, amount_minor, method, provider_id, provider_account_id, capability_id,
    custody_model, capability_routable, routing_reason, platform_fee_minor, fee_schedule_version_id, status, status_rank,
    idempotency_scope, idempotency_key)
  VALUES (p_id, p_campaign, 'USD', p_amount, p_method, 'b0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000002',
    p_cap, p_custody, p_routable, 'priority list (test)', p_amount * 500 / 10000, 'f0000000-0000-0000-0000-000000000003',
    'CREATED', 0, 'test', p_id);
END $f$;

CREATE FUNCTION pg_temp.pi_move(p_id uuid, p_to text, p_source text DEFAULT 'WEBHOOK', p_actor uuid DEFAULT NULL)
RETURNS void LANGUAGE plpgsql AS $f$
DECLARE cur text;
BEGIN
  SELECT status INTO cur FROM app.payment_intents WHERE id = p_id;
  INSERT INTO app.payment_events (id, subject_type, payment_intent_id, from_status, to_status, applied, source, actor_type, actor_id, reason)
  VALUES (gen_random_uuid(), 'PAYMENT_INTENT', p_id, cur, p_to, true, p_source,
          CASE p_source WHEN 'STAFF' THEN 'STAFF' WHEN 'INTERNAL' THEN 'SYSTEM' ELSE 'PROVIDER' END, p_actor, 'test transition');
  UPDATE app.payment_intents
     SET status = p_to, status_rank = app.payment_status_rank(p_to), version = version + 1,
         unknown_since  = CASE WHEN p_to = 'UNKNOWN' THEN now() ELSE unknown_since END,
         unknown_reason = CASE WHEN p_to = 'UNKNOWN' THEN 'TIMEOUT' ELSE unknown_reason END,
         reversal_kind  = CASE WHEN p_to = 'CHARGED_BACK' THEN 'CARD_CHARGEBACK' ELSE reversal_kind END,
         last_authoritative_source = p_source
   WHERE id = p_id;
END $f$;

CREATE FUNCTION pg_temp.rr_move(p_id uuid, p_to text, p_decider uuid DEFAULT NULL) RETURNS void LANGUAGE plpgsql AS $f$
DECLARE r app.refund_requests%ROWTYPE;
BEGIN
  SELECT * INTO r FROM app.refund_requests WHERE id = p_id;
  INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_request_id, from_status, to_status, applied, source, actor_type, actor_id, reason)
  VALUES (gen_random_uuid(), 'REFUND_REQUEST', r.payment_intent_id, p_id, r.status, p_to, true,
          CASE WHEN p_decider IS NULL THEN 'INTERNAL' ELSE 'STAFF' END, CASE WHEN p_decider IS NULL THEN 'SYSTEM' ELSE 'STAFF' END,
          p_decider, 'test decision');
  UPDATE app.refund_requests
     SET status = p_to, version = version + 1,
         decided_by = COALESCE(p_decider, decided_by),
         decided_at = CASE WHEN p_decider IS NULL THEN decided_at ELSE now() END,
         decision_reason = CASE WHEN p_decider IS NULL THEN decision_reason ELSE 'test decision' END,
         decided_step_up_at = CASE WHEN p_decider IS NULL THEN decided_step_up_at ELSE now() END,
         funding_plan = CASE WHEN p_to = 'APPROVED' THEN 'CAMPAIGN' ELSE funding_plan END
   WHERE id = p_id;
END $f$;

CREATE FUNCTION pg_temp.new_rr(p_id uuid, p_intent uuid, p_amount bigint, p_currency char(3) DEFAULT 'USD') RETURNS void LANGUAGE plpgsql AS $f$
BEGIN
  INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_request_id, from_status, to_status, applied, source, actor_type, actor_id, reason)
  VALUES (gen_random_uuid(), 'REFUND_REQUEST', p_intent, p_id, NULL, 'REQUESTED', true, 'STAFF', 'STAFF', 'a0000000-0000-0000-0000-000000000002', 'donor asked');
  INSERT INTO app.refund_requests (id, payment_intent_id, campaign_id, currency, amount_minor, reason_code, channel, requested_by, status, idempotency_key, expires_at)
  SELECT p_id, p_intent, campaign_id, p_currency, p_amount, 'DONOR_REQUEST', 'SUPPORT', 'a0000000-0000-0000-0000-000000000002', 'REQUESTED', p_id, now() + interval '72 hours'
    FROM app.payment_intents WHERE id = p_intent;
  PERFORM pg_temp.rr_move(p_id, 'PENDING_APPROVAL');
END $f$;

-- Ledger fixtures (real 0006 rules; balanced, single-currency journals).
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

-- ===================================================================================== psp registry ====

-- @case capability_settlement_currency_must_equal_currency expect=error:ck_provider_capabilities_same_currency_settlement
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  evidence_status, recorded_by)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 1, 'ZIMSWITCH', 'ZWG', 'USD', 'PSP_POOL', 'UNVERIFIED',
  'a0000000-0000-0000-0000-000000000003');

-- @case capability_merchant_settlement_is_not_routable expect=ok
DO $$ BEGIN
  IF (SELECT routable FROM app.provider_capabilities WHERE id = 'b0000000-0000-0000-0000-000000000004') THEN
    RAISE EXCEPTION 'MERCHANT_SETTLEMENT capability is routable';
  END IF;
  IF (SELECT routable FROM app.provider_capabilities WHERE id = 'b0000000-0000-0000-0000-000000000005') THEN
    RAISE EXCEPTION 'UNVERIFIED capability is routable';
  END IF;
  IF NOT (SELECT routable FROM app.provider_capabilities WHERE id = 'b0000000-0000-0000-0000-000000000003') THEN
    RAISE EXCEPTION 'VERIFIED PSP_POOL capability is not routable';
  END IF;
END $$;

-- @case intent_on_merchant_settlement_capability_rejected expect=error:fk_payment_intents_capability
SELECT pg_temp.new_intent('10000000-0000-0000-0000-0000000000f1', 'b0000000-0000-0000-0000-000000000004', 'CARD', 5000,
  'c0000000-0000-0000-0000-000000000001', true, 'PSP_POOL');

-- @case intent_claiming_merchant_settlement_custody_rejected expect=error:ck_payment_intents_custody_model
SELECT pg_temp.new_intent('10000000-0000-0000-0000-0000000000f2', 'b0000000-0000-0000-0000-000000000004', 'CARD', 5000,
  'c0000000-0000-0000-0000-000000000001', false, 'MERCHANT_SETTLEMENT');

-- @case intent_on_unverified_capability_rejected expect=error:fk_payment_intents_capability
SELECT pg_temp.new_intent('10000000-0000-0000-0000-0000000000f3', 'b0000000-0000-0000-0000-000000000005', 'ONEMONEY');

-- @case intent_method_must_match_capability expect=error:fk_payment_intents_capability
SELECT pg_temp.new_intent('10000000-0000-0000-0000-0000000000f4', 'b0000000-0000-0000-0000-000000000003', 'INNBUCKS');

-- @case capabilities_are_append_only expect=error:append-only
UPDATE app.provider_capabilities SET evidence_status = 'VERIFIED' WHERE id = 'b0000000-0000-0000-0000-000000000005';

-- @case provider_account_rejects_raw_secret expect=error:ck_provider_accounts_api_secret_ref
INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'STAGING', 'leak', 'sk_live_51HxQ2aBcDeF');

-- @case sandbox_provider_never_in_production expect=error:sandbox provider cannot have a PRODUCTION account
INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'PRODUCTION', 'prod', 'secretref://prod/sandbox');

-- @case real_provider_cannot_be_enabled_while_selection_pending expect=error:ck_payment_providers_enabled_requires_selection
INSERT INTO app.payment_providers (id, code, display_name, status, confirmation_mode)
VALUES (gen_random_uuid(), 'candidate_x', 'Candidate X (selection PENDING)', 'ENABLED', 'WEBHOOK_SIGNED');

-- ================================================================================== webhook inbox ====

-- @case inbox_accepts_verified_event expect=ok
INSERT INTO app.provider_webhook_inbox (id, provider_id, provider_event_id, event_class, event_type_raw, signature_verified,
  verification_method, payload_redacted, raw_body_sha256)
VALUES ('40000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000001', 'evt_1', 'PAYMENT', 'payment.paid', true,
  'SIGNATURE', '{"status":"paid"}', sha256('body1'::bytea));

-- @case inbox_duplicate_event_rejected expect=error:uq_provider_webhook_inbox_event
INSERT INTO app.provider_webhook_inbox (id, provider_id, provider_event_id, event_class, event_type_raw, signature_verified,
  verification_method, payload_redacted, raw_body_sha256)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'evt_1', 'PAYMENT', 'payment.paid', true,
  'SIGNATURE', '{"status":"paid"}', sha256('body1'::bytea));

-- @case inbox_unsigned_requires_status_api_confirmation expect=error:ck_provider_webhook_inbox_verified
INSERT INTO app.provider_webhook_inbox (id, provider_id, provider_event_id, event_class, event_type_raw, signature_verified,
  verification_method, payload_redacted, raw_body_sha256)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'evt_2', 'PAYMENT', 'payment.paid', false,
  'SIGNATURE', '{}', sha256('body2'::bytea));

-- @case inbox_raw_payload_immutable expect=error:raw columns of app.provider_webhook_inbox are immutable
UPDATE app.provider_webhook_inbox SET payload_redacted = '{"status":"failed"}' WHERE id = '40000000-0000-0000-0000-000000000001';

-- @case inbox_processing_columns_mutable expect=ok
UPDATE app.provider_webhook_inbox SET status = 'DISPATCHED', dispatched_to = 'payments', attempts = 1
 WHERE id = '40000000-0000-0000-0000-000000000001';

-- @case inbox_processing_illegal_transition expect=error:illegal provider_webhook_inbox transition
UPDATE app.provider_webhook_inbox SET status = 'RECEIVED' WHERE id = '40000000-0000-0000-0000-000000000001';

-- @case inbox_parking_requires_subject expect=error:ck_provider_webhook_inbox_parked
UPDATE app.provider_webhook_inbox SET status = 'PARKED' WHERE id = '40000000-0000-0000-0000-000000000001';

-- @case inbox_delete_rejected expect=error:append-only
DELETE FROM app.provider_webhook_inbox WHERE id = '40000000-0000-0000-0000-000000000001';

-- @case inbox_unrecognised_event_kept_not_dropped expect=ok
INSERT INTO app.provider_webhook_inbox (id, provider_id, provider_event_id, event_class, event_type_raw, signature_verified,
  verification_method, payload_redacted, raw_body_sha256)
VALUES ('40000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000001', 'evt_3', 'OTHER', 'merchant.notice', true,
  'SIGNATURE', '{}', sha256('body3'::bytea));
UPDATE app.provider_webhook_inbox SET status = 'UNRECOGNISED', last_error_code = 'UNMAPPED_EVENT_TYPE'
 WHERE id = '40000000-0000-0000-0000-000000000002';

-- @case inbox_unrecognised_cannot_be_marked_processed expect=error:illegal provider_webhook_inbox transition
UPDATE app.provider_webhook_inbox SET status = 'PROCESSED', processed_at = now() WHERE id = '40000000-0000-0000-0000-000000000002';

-- ============================================================================ payment state machine ====

-- @case intent_created_with_event expect=ok
SELECT pg_temp.new_intent('10000000-0000-0000-0000-000000000001');
SELECT pg_temp.new_intent('10000000-0000-0000-0000-000000000002');
SELECT pg_temp.new_intent('10000000-0000-0000-0000-000000000003');
SELECT pg_temp.new_intent('10000000-0000-0000-0000-000000000004');

-- @case intent_without_event_rejected expect=error:missing payment_events row
INSERT INTO app.payment_intents (id, campaign_id, currency, amount_minor, method, provider_id, provider_account_id, capability_id,
  custody_model, capability_routable, routing_reason, platform_fee_minor, fee_schedule_version_id, status, status_rank,
  idempotency_scope, idempotency_key)
VALUES ('10000000-0000-0000-0000-0000000000e1', 'c0000000-0000-0000-0000-000000000001', 'USD', 5000, 'ECOCASH',
  'b0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000003',
  'PSP_POOL', true, 'test', 250, 'f0000000-0000-0000-0000-000000000003', 'CREATED', 0, 'test', gen_random_uuid());

-- @case intent_must_start_created expect=error:illegal payment_intent transition
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'PAYMENT_INTENT', '10000000-0000-0000-0000-0000000000e2', 'SUCCEEDED', true, 'WEBHOOK', 'PROVIDER');
INSERT INTO app.payment_intents (id, campaign_id, currency, amount_minor, method, provider_id, provider_account_id, capability_id,
  custody_model, capability_routable, routing_reason, platform_fee_minor, fee_schedule_version_id, status, status_rank,
  idempotency_scope, idempotency_key)
VALUES ('10000000-0000-0000-0000-0000000000e2', 'c0000000-0000-0000-0000-000000000001', 'USD', 5000, 'ECOCASH',
  'b0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000003',
  'PSP_POOL', true, 'test', 250, 'f0000000-0000-0000-0000-000000000003', 'SUCCEEDED', 60, 'test', gen_random_uuid());

-- @case intent_amount_must_be_positive expect=error:ck_payment_intents_amount
SELECT pg_temp.new_intent('10000000-0000-0000-0000-0000000000e3', p_amount => 0);

-- @case internal_pre_submit_rejection_from_created_allowed expect=ok
SELECT pg_temp.new_intent('10000000-0000-0000-0000-000000000005');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000005', 'FAILED', 'INTERNAL');

-- @case intent_pending_with_provider_ref expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'PENDING', 'SYNC');
UPDATE app.payment_intents SET provider_payment_ref = 'PSP-REF-1' WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case duplicate_provider_payment_ref_rejected expect=error:uq_payment_intents_provider_payment_ref
UPDATE app.payment_intents SET provider_payment_ref = 'PSP-REF-1' WHERE id = '10000000-0000-0000-0000-000000000002';

-- @case provider_payment_ref_immutable_once_set expect=error:provider_payment_ref is immutable
UPDATE app.payment_intents SET provider_payment_ref = 'PSP-REF-X' WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case status_change_without_event_rejected expect=error:missing payment_events row
UPDATE app.payment_intents SET status = 'REQUIRES_ACTION', status_rank = 20 WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case status_rank_must_match_status expect=error:ck_payment_intents_status_rank
UPDATE app.payment_intents SET status_rank = 60 WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case illegal_pending_to_created expect=error:illegal payment_intent transition
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'CREATED');

-- @case pending_to_failed_authoritative expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'FAILED', 'WEBHOOK');

-- @case illegal_failed_to_pending expect=error:illegal payment_intent transition
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'PENDING', 'WEBHOOK');

-- @case late_success_failed_to_succeeded_allowed expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'SUCCEEDED', 'WEBHOOK');

-- @case illegal_succeeded_to_failed expect=error:illegal payment_intent transition
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'FAILED', 'WEBHOOK');

-- @case amount_immutable expect=error:immutable
UPDATE app.payment_intents SET amount_minor = 9999 WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case currency_immutable expect=error:immutable
UPDATE app.payment_intents SET currency = 'ZWG' WHERE id = '10000000-0000-0000-0000-000000000001';

-- @case success_never_from_internal_source expect=error:ck_payment_events_success_source
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'PENDING', 'SYNC');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'SUCCEEDED', 'INTERNAL');

-- @case timeout_puts_intent_in_unknown_internal expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'UNKNOWN', 'INTERNAL');

-- @case external_event_cannot_push_into_unknown expect=error:ck_payment_events_unknown_internal
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000003', 'PENDING', 'SYNC');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000003', 'UNKNOWN', 'WEBHOOK');

-- @case unknown_requires_since_and_reason expect=error:ck_payment_intents_unknown
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'PAYMENT_INTENT', '10000000-0000-0000-0000-000000000003', 'CREATED', 'UNKNOWN', true, 'INTERNAL', 'SYSTEM');
UPDATE app.payment_intents SET status = 'UNKNOWN', status_rank = 5 WHERE id = '10000000-0000-0000-0000-000000000003';

-- I-21: STAFF is not an allowed FAILED source for payments (see payment-state-machine.md §6 note: conflicts
-- with transaction-lifecycle §7.3 step 6; raised with the lead). The evidence CHECK remains as a second layer.
-- @case staff_fail_without_provider_evidence_rejected expect=error:ck_payment_events_staff_failed_evidence
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'FAILED', 'STAFF', 'a0000000-0000-0000-0000-000000000003');

-- @case internal_cannot_fail_unknown_payment expect=error:ck_payment_events_failed_source
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'FAILED', 'INTERNAL');

-- @case unknown_resolved_by_poll_to_succeeded expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'SUCCEEDED', 'POLL');

-- @case not_applied_lower_rank_event_recorded expect=ok
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, from_status, to_status, applied, not_applied_reason, source, actor_type, raw_status)
VALUES (gen_random_uuid(), 'PAYMENT_INTENT', '10000000-0000-0000-0000-000000000002', 'SUCCEEDED', 'PENDING', false, 'LOWER_RANK', 'WEBHOOK', 'PROVIDER', 'initiated');

-- @case not_applied_event_needs_reason expect=error:ck_payment_events_not_applied
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'PAYMENT_INTENT', '10000000-0000-0000-0000-000000000002', 'SUCCEEDED', 'PENDING', false, 'WEBHOOK', 'PROVIDER');

-- @case payment_events_update_rejected expect=error:append-only
UPDATE app.payment_events SET reason = 'edited' WHERE payment_intent_id = '10000000-0000-0000-0000-000000000002';

-- @case payment_events_delete_rejected expect=error:append-only
DELETE FROM app.payment_events WHERE payment_intent_id = '10000000-0000-0000-0000-000000000002';

-- @case authorised_only_for_cards expect=error:ck_payment_intents_authorised_needs_capture
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000003', 'PENDING', 'SYNC');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000003', 'AUTHORISED', 'WEBHOOK');

-- ======================================================================= attempts & provider binding ====

-- @case attempt_reference_is_payment_id expect=error:ck_payment_attempts_reference
INSERT INTO app.payment_attempts (id, payment_intent_id, attempt_no, operation, provider_id, our_reference, correlation_id, started_at)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000004', 1, 'CREATE', 'b0000000-0000-0000-0000-000000000001',
  gen_random_uuid(), 'corr-1', now());

-- @case attempt_recorded_in_flight_then_completed expect=ok
INSERT INTO app.payment_attempts (id, payment_intent_id, attempt_no, operation, provider_id, our_reference, correlation_id, started_at)
VALUES ('10000000-0000-0000-0000-0000000000a1', '10000000-0000-0000-0000-000000000004', 1, 'CREATE', 'b0000000-0000-0000-0000-000000000001',
  '10000000-0000-0000-0000-000000000004', 'corr-1', now());
UPDATE app.payment_attempts SET classification = 'OUTCOME_UNKNOWN', unknown_reason = 'TIMEOUT', error_class = 'INDETERMINATE',
  completed_at = now(), latency_ms = 30000 WHERE id = '10000000-0000-0000-0000-0000000000a1';

-- @case crash_during_create_is_an_unknown_reason expect=ok
INSERT INTO app.payment_attempts (id, payment_intent_id, attempt_no, operation, provider_id, our_reference, correlation_id, started_at,
  classification, unknown_reason, completed_at)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000004', 2, 'GET_STATUS', 'b0000000-0000-0000-0000-000000000001',
  '10000000-0000-0000-0000-000000000004', 'corr-2', now(), 'OUTCOME_UNKNOWN', 'CRASH_DURING_CREATE', now());

-- @case completed_attempt_immutable expect=error:completed attempt
UPDATE app.payment_attempts SET classification = 'ACCEPTED', unknown_reason = NULL WHERE id = '10000000-0000-0000-0000-0000000000a1';

-- @case provider_rebinding_after_possible_send_rejected expect=error:bound to its provider
INSERT INTO app.payment_providers (id, code, display_name, is_sandbox, status, confirmation_mode)
VALUES ('b0000000-0000-0000-0000-000000000011', 'sandbox_two', 'Sandbox two', true, 'ENABLED', 'POLL_ONLY');
INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref)
VALUES ('b0000000-0000-0000-0000-000000000012', 'b0000000-0000-0000-0000-000000000011', 'SANDBOX', 'default', 'secretref://sandbox2/api');
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  status_api, evidence_status, evidence_source, recorded_by)
VALUES ('b0000000-0000-0000-0000-000000000013', 'b0000000-0000-0000-0000-000000000011', 1, 'ECOCASH', 'USD', 'USD', 'PSP_POOL',
  true, 'VERIFIED', 'fixture', 'a0000000-0000-0000-0000-000000000003');
UPDATE app.payment_intents SET provider_id = 'b0000000-0000-0000-0000-000000000011', provider_account_id = 'b0000000-0000-0000-0000-000000000012',
  capability_id = 'b0000000-0000-0000-0000-000000000013' WHERE id = '10000000-0000-0000-0000-000000000004';

-- ================================================================== provider transactions & refs ====

-- @case provider_transaction_raw_and_mapped expect=ok
INSERT INTO app.payment_transactions (id, payment_intent_id, provider_id, provider_transaction_ref, raw_status, mapped_status,
  mapping_version, last_source, reported_amount_minor, reported_currency, provider_fee_minor)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000001', 'TXN-1',
  'Paid', 'SUCCEEDED', 'sandbox-map-v1', 'WEBHOOK', 5000, 'USD', 175);

-- @case duplicate_provider_transaction_rejected expect=error:uq_payment_transactions_provider_ref
INSERT INTO app.payment_transactions (id, payment_intent_id, provider_id, provider_transaction_ref, raw_status, mapped_status,
  mapping_version, last_source)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000001', 'TXN-1',
  'Paid', 'SUCCEEDED', 'sandbox-map-v1', 'POLL');

-- @case provider_reference_unique_per_kind expect=error:uq_payment_provider_references
INSERT INTO app.payment_provider_references (id, provider_id, ref_kind, reference, payment_intent_id, source)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'TRANSACTION', 'TXN-1', '10000000-0000-0000-0000-000000000001', 'WEBHOOK'),
       (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'TRANSACTION', 'TXN-1', '10000000-0000-0000-0000-000000000002', 'POLL');

-- ======================================================================================= donations ====

-- @case guest_donation_ok expect=ok
INSERT INTO app.donations (id, payment_intent_id, campaign_id, guest_name, guest_email, is_anonymous, receipt_number,
  terms_version, privacy_notice_version, access_token_hash)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000001', 'Guest', 'guest@example.org',
  true, 'ABCDEFGH12', 'terms-v1', 'privacy-v1', sha256('guest-token-1'::bytea));

-- @case donation_one_per_intent expect=error:uq_donations_payment_intent_id
INSERT INTO app.donations (id, payment_intent_id, campaign_id, donor_user_id, receipt_number, terms_version, privacy_notice_version)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000001',
  'a0000000-0000-0000-0000-000000000001', 'ABCDEFGH13', 'terms-v1', 'privacy-v1');

-- @case donation_campaign_must_match_intent expect=error:fk_donations_intent_campaign
INSERT INTO app.donations (id, payment_intent_id, campaign_id, donor_user_id, receipt_number, terms_version, privacy_notice_version)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000002',
  'a0000000-0000-0000-0000-000000000001', 'ABCDEFGH14', 'terms-v1', 'privacy-v1');

-- @case donation_needs_donor_or_guest_contact expect=error:ck_donations_donor_identified
INSERT INTO app.donations (id, payment_intent_id, campaign_id, receipt_number, terms_version, privacy_notice_version)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'ABCDEFGH15', 'terms-v1', 'privacy-v1');

-- @case guest_donation_needs_access_token expect=error:ck_donations_guest_token
INSERT INTO app.donations (id, payment_intent_id, campaign_id, guest_email, receipt_number, terms_version, privacy_notice_version)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'g2@example.org',
  'ABCDEFGH16', 'terms-v1', 'privacy-v1');

-- @case donation_access_token_reissue_revokes_previous expect=ok
UPDATE app.donations SET access_token_hash = sha256('guest-token-2'::bytea) WHERE payment_intent_id = '10000000-0000-0000-0000-000000000001';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM app.donations WHERE access_token_hash = sha256('guest-token-1'::bytea)) THEN
    RAISE EXCEPTION 'old access token still valid';
  END IF;
END $$;

-- @case donation_access_token_cannot_be_removed_for_guest expect=error:ck_donations_guest_token
UPDATE app.donations SET access_token_hash = NULL WHERE payment_intent_id = '10000000-0000-0000-0000-000000000001';

-- ========================================================================================= refunds ====

-- @case refund_request_created expect=ok
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001', 2000);

-- @case refund_self_approval_rejected expect=error:ck_refund_requests_maker_checker
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000001', 'APPROVED', 'a0000000-0000-0000-0000-000000000002');

-- @case refund_currency_must_equal_payment_currency expect=error:fk_refund_requests_intent
SELECT pg_temp.new_rr('20000000-0000-0000-0000-0000000000e1', '10000000-0000-0000-0000-000000000001', 1000, 'ZWG');

-- @case refund_approved_with_reservation_journal expect=ok
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000001', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');
DO $$
DECLARE cap uuid; res uuid;
BEGIN
  cap := pg_temp.post('DONATION_CAPTURED', 'payment:10000000-0000-0000-0000-000000000001:capture', 'payment',
    '10000000-0000-0000-0000-000000000001', 'USD',
    '[{"class":"asset:psp_clearing","owner":"b0000000-0000-0000-0000-000000000001","dir":"DEBIT","amt":5000},
      {"class":"liability:campaign_unsettled","owner":"c0000000-0000-0000-0000-000000000001","dir":"CREDIT","amt":4750},
      {"class":"revenue:platform_fees","dir":"CREDIT","amt":250}]');
  res := pg_temp.post('REFUND_RESERVED', 'refund:21000000-0000-0000-0000-000000000001:reserved', 'refund',
    '21000000-0000-0000-0000-000000000001', 'USD',
    '[{"class":"liability:campaign_unsettled","owner":"c0000000-0000-0000-0000-000000000001","dir":"DEBIT","amt":1900},
      {"class":"revenue:platform_fees","dir":"DEBIT","amt":100},
      {"class":"liability:refund_payable","dir":"CREDIT","amt":2000}]');
  INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_transaction_id, to_status, applied, source, actor_type, ledger_transaction_ids)
  VALUES (gen_random_uuid(), 'REFUND_TRANSACTION', '10000000-0000-0000-0000-000000000001', '21000000-0000-0000-0000-000000000001',
          'CREATED', true, 'INTERNAL', 'SYSTEM', ARRAY[res]);
  INSERT INTO app.refund_transactions (id, refund_request_id, payment_intent_id, currency, amount_minor, execution_method, provider_id,
    status, ledger_reservation_txn_id)
  VALUES ('21000000-0000-0000-0000-000000000001', '20000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
          'USD', 2000, 'PROVIDER_REFUND', 'b0000000-0000-0000-0000-000000000001', 'CREATED', res);
END $$;

-- @case over_refund_rejected expect=error:over-refund
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001', 3500);
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000002', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');

-- @case refund_up_to_captured_allowed expect=ok
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000001', 3000);
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000003', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');

-- @case refund_transaction_amount_must_match_request expect=error:fk_refund_transactions_request
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_transaction_id, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'REFUND_TRANSACTION', '10000000-0000-0000-0000-000000000001', '21000000-0000-0000-0000-0000000000e1',
        'CREATED', true, 'INTERNAL', 'SYSTEM');
INSERT INTO app.refund_transactions (id, refund_request_id, payment_intent_id, currency, amount_minor, execution_method, provider_id,
  status, ledger_reservation_txn_id)
SELECT '21000000-0000-0000-0000-0000000000e1', '20000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000001',
       'USD', 2500, 'PROVIDER_REFUND', 'b0000000-0000-0000-0000-000000000001', 'CREATED', ledger_reservation_txn_id
  FROM app.refund_transactions WHERE id = '21000000-0000-0000-0000-000000000001';

-- @case one_more_minor_unit_is_over_refund expect=error:over-refund
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000004', '10000000-0000-0000-0000-000000000001', 1);
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000004', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');

-- @case refund_of_unsucceeded_payment_rejected expect=error:refund not allowed
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000005', '10000000-0000-0000-0000-000000000003', 1000);
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000005', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');

-- @case platform_funded_refund_needs_dual_approval expect=error:ck_refund_requests_c2_dual
SELECT pg_temp.new_rr('20000000-0000-0000-0000-000000000006', '10000000-0000-0000-0000-000000000002', 1000);
SELECT pg_temp.rr_move('20000000-0000-0000-0000-000000000006', 'APPROVED', 'a0000000-0000-0000-0000-000000000003');
UPDATE app.refund_requests SET funding_plan = 'PLATFORM_FUNDED_WITH_RECOVERY' WHERE id = '20000000-0000-0000-0000-000000000006';

-- @case refund_transaction_unknown_never_failed_by_timeout expect=ok
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_transaction_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'REFUND_TRANSACTION', '10000000-0000-0000-0000-000000000001', '21000000-0000-0000-0000-000000000001',
        'CREATED', 'UNKNOWN', true, 'INTERNAL', 'SYSTEM');
UPDATE app.refund_transactions SET status = 'UNKNOWN', unknown_since = now(), unknown_reason = 'TIMEOUT'
 WHERE id = '21000000-0000-0000-0000-000000000001';

-- @case refund_transaction_unknown_to_created_illegal expect=error:illegal refund_transaction transition
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_transaction_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'REFUND_TRANSACTION', '10000000-0000-0000-0000-000000000001', '21000000-0000-0000-0000-000000000001',
        'UNKNOWN', 'CREATED', true, 'INTERNAL', 'SYSTEM');
UPDATE app.refund_transactions SET status = 'CREATED' WHERE id = '21000000-0000-0000-0000-000000000001';

-- @case refund_succeeded_moves_payment_to_partially_refunded expect=ok
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, refund_transaction_id, from_status, to_status, applied, source, actor_type, raw_status)
VALUES (gen_random_uuid(), 'REFUND_TRANSACTION', '10000000-0000-0000-0000-000000000001', '21000000-0000-0000-0000-000000000001',
        'UNKNOWN', 'SUCCEEDED', true, 'POLL', 'PROVIDER', 'refunded');
UPDATE app.refund_transactions SET status = 'SUCCEEDED', provider_refund_ref = 'RF-1', raw_status = 'refunded'
 WHERE id = '21000000-0000-0000-0000-000000000001';
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'PARTIALLY_REFUNDED', 'POLL');

-- @case refunded_payment_cannot_move expect=error:illegal payment_intent transition
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'REFUNDED', 'WEBHOOK');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'DISPUTED', 'WEBHOOK');

-- ================================================================================ disputes, chargebacks ====

-- @case dispute_opened expect=ok
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, payment_dispute_id, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'PAYMENT_DISPUTE', '10000000-0000-0000-0000-000000000002', '30000000-0000-0000-0000-000000000001',
        'OPENED', true, 'WEBHOOK', 'PROVIDER');
INSERT INTO app.payment_disputes (id, payment_intent_id, campaign_id, currency, amount_minor, provider_id, provider_dispute_ref, kind,
  reason_category, debit_timing, evidence_due_at, status)
VALUES ('30000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'USD',
  5000, 'b0000000-0000-0000-0000-000000000001', 'DSP-1', 'CHARGEBACK', 'FRAUD_CLAIMED', 'ON_OPEN', now() + interval '10 days', 'OPENED');
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'DISPUTED', 'WEBHOOK');

-- @case dispute_currency_must_match_payment expect=error:fk_payment_disputes_intent
INSERT INTO app.payment_disputes (id, payment_intent_id, campaign_id, currency, amount_minor, provider_id, provider_dispute_ref, kind,
  reason_category, debit_timing, status)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'ZWG',
  5000, 'b0000000-0000-0000-0000-000000000001', 'DSP-2', 'CHARGEBACK', 'OTHER', 'ON_OPEN', 'OPENED');

-- @case dispute_accept_self_approval_rejected expect=error:ck_payment_disputes_accept_maker_checker
UPDATE app.payment_disputes SET accept_requested_by = 'a0000000-0000-0000-0000-000000000003',
  accept_approved_by = 'a0000000-0000-0000-0000-000000000003' WHERE id = '30000000-0000-0000-0000-000000000001';

-- @case dispute_opened_to_won_illegal expect=error:illegal payment_dispute transition
INSERT INTO app.payment_events (id, subject_type, payment_intent_id, payment_dispute_id, from_status, to_status, applied, source, actor_type)
VALUES (gen_random_uuid(), 'PAYMENT_DISPUTE', '10000000-0000-0000-0000-000000000002', '30000000-0000-0000-0000-000000000001',
        'OPENED', 'WON', true, 'WEBHOOK', 'PROVIDER');
UPDATE app.payment_disputes SET status = 'WON', outcome_at = now() WHERE id = '30000000-0000-0000-0000-000000000001';

-- @case dispute_evidence_due_never_edited expect=error:evidence_due_at is stored exactly as received
UPDATE app.payment_disputes SET evidence_due_at = evidence_due_at + interval '5 days' WHERE id = '30000000-0000-0000-0000-000000000001';

-- @case card_chargeback_requires_dispute expect=error:ck_chargebacks_dispute_link
INSERT INTO app.chargebacks (id, payment_intent_id, campaign_id, currency, amount_minor, reversal_kind, provider_id, provider_reversal_ref, occurred_at)
VALUES (gen_random_uuid(), '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'USD', 5000,
  'CARD_CHARGEBACK', 'b0000000-0000-0000-0000-000000000001', 'CB-1', now());

-- @case chargeback_recorded_and_payment_charged_back expect=ok
INSERT INTO app.chargebacks (id, payment_intent_id, campaign_id, currency, amount_minor, reversal_kind, payment_dispute_id, provider_id, provider_reversal_ref, occurred_at)
VALUES ('30000000-0000-0000-0000-0000000000c1', '10000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000001', 'USD', 5000,
  'CARD_CHARGEBACK', '30000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000001', 'CB-1', now());
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'CHARGED_BACK', 'WEBHOOK');

-- @case charged_back_is_terminal expect=error:illegal payment_intent transition
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000002', 'SUCCEEDED', 'WEBHOOK');

-- @case chargebacks_append_only expect=error:append-only
UPDATE app.chargebacks SET amount_minor = 1 WHERE id = '30000000-0000-0000-0000-0000000000c1';

-- ============================================================================================ fees ====

-- @case fee_change_self_approval_rejected expect=error:ck_fee_schedule_change_requests_maker_checker
INSERT INTO app.fee_schedule_change_requests (id, fee_schedule_id, currency, proposed_rate_bps, proposed_effective_from, justification,
  status, requested_by, expires_at, proposal_hash)
VALUES ('f0000000-0000-0000-0000-000000000012', 'f0000000-0000-0000-0000-000000000001', 'USD', 400, now() + interval '2 days',
  'reduce fee', 'PENDING', 'a0000000-0000-0000-0000-000000000003', now() + interval '7 days', sha256('p2'::bytea));
UPDATE app.fee_schedule_change_requests SET status = 'APPROVED', decided_by = 'a0000000-0000-0000-0000-000000000003',
  decided_at = now(), decision_reason = 'self', decided_step_up_at = now() WHERE id = 'f0000000-0000-0000-0000-000000000012';

-- @case fee_version_requires_approved_change_request expect=error:requires an APPROVED change request
INSERT INTO app.fee_schedule_change_requests (id, fee_schedule_id, currency, proposed_rate_bps, proposed_effective_from, justification,
  status, requested_by, expires_at, proposal_hash)
VALUES ('f0000000-0000-0000-0000-000000000013', 'f0000000-0000-0000-0000-000000000001', 'USD', 400, now() + interval '2 days',
  'reduce fee', 'PENDING', 'a0000000-0000-0000-0000-000000000003', now() + interval '7 days', sha256('p3'::bytea));
INSERT INTO app.fee_schedule_versions (id, fee_schedule_id, version_no, currency, rate_bps, effective_from, change_request_id)
SELECT gen_random_uuid(), fee_schedule_id, 2, currency, proposed_rate_bps, proposed_effective_from, id
  FROM app.fee_schedule_change_requests WHERE id = 'f0000000-0000-0000-0000-000000000013';

-- @case fee_version_never_retroactive expect=error:ck_fee_schedule_change_requests_future
INSERT INTO app.fee_schedule_change_requests (id, fee_schedule_id, currency, proposed_rate_bps, proposed_effective_from, justification,
  status, requested_by, expires_at, proposal_hash)
VALUES (gen_random_uuid(), 'f0000000-0000-0000-0000-000000000001', 'USD', 400, now() - interval '1 day',
  'backdate', 'PENDING', 'a0000000-0000-0000-0000-000000000003', now() + interval '7 days', sha256('p4'::bytea));

-- @case fee_version_must_equal_approved_proposal expect=error:differs from the approved change request
INSERT INTO app.fee_schedule_versions (id, fee_schedule_id, version_no, currency, rate_bps, effective_from, change_request_id)
SELECT gen_random_uuid(), fee_schedule_id, 2, currency, 900, proposed_effective_from, id
  FROM app.fee_schedule_change_requests WHERE id = 'f0000000-0000-0000-0000-000000000002';

-- @case fee_version_rate_not_editable expect=error:only a one-time cancellation
UPDATE app.fee_schedule_versions SET rate_bps = 450 WHERE id = 'f0000000-0000-0000-0000-000000000003';

-- @case fee_version_not_yet_effective_can_be_cancelled expect=ok
UPDATE app.fee_schedule_versions SET cancelled_at = now(), cancelled_by = 'a0000000-0000-0000-0000-000000000004'
 WHERE id = 'f0000000-0000-0000-0000-000000000003';

-- @case fee_version_delete_rejected expect=error:append-only
DELETE FROM app.fee_schedule_versions WHERE id = 'f0000000-0000-0000-0000-000000000003';

-- ================================================================================ provider health ====

-- @case settlement_freshness_event_recorded expect=ok
INSERT INTO app.provider_health_events (id, provider_id, operation, currency, event_kind, detail, observed_at)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'STATEMENT', 'USD', 'SETTLEMENT_RECONCILED',
  '{"reconciliation_run_id":"r1"}', now());

-- @case settlement_freshness_event_needs_currency expect=error:ck_provider_health_events_recon
INSERT INTO app.provider_health_events (id, provider_id, operation, event_kind, observed_at)
VALUES (gen_random_uuid(), 'b0000000-0000-0000-0000-000000000001', 'STATEMENT', 'RECON_SEV1_OPENED', now());

-- @case provider_health_events_append_only expect=error:append-only
UPDATE app.provider_health_events SET event_kind = 'RECON_SEV1_CLOSED' WHERE event_kind = 'SETTLEMENT_RECONCILED';

-- ================================================================================ Stage 2 lead additions ====

-- @case partially_refunded_payment_disputed expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'DISPUTED', 'WEBHOOK');

-- @case dispute_won_returns_to_partially_refunded expect=ok
SELECT pg_temp.pi_move('10000000-0000-0000-0000-000000000001', 'PARTIALLY_REFUNDED', 'WEBHOOK');
