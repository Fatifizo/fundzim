-- Invariant tests for 0012_beneficiaries and 0014_campaigns (DESIGN DRAFTS).
-- Owner U31 (...0031), other user U32 (...0032); staff: reviewer S41, compliance S42, compliance S43.
-- Campaign C1 = ...c001 (medical, HIGH tier, USD).

-- @case fixtures expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('00000000-0000-7000-8000-000000000031', 'USER'),
  ('00000000-0000-7000-8000-000000000032', 'USER'),
  ('00000000-0000-7000-8000-000000000041', 'STAFF'),
  ('00000000-0000-7000-8000-000000000042', 'STAFF'),
  ('00000000-0000-7000-8000-000000000043', 'STAFF');
INSERT INTO app.organisations (id, display_name, slug, org_type, created_by_user_id)
VALUES ('00000000-0000-7000-8000-000000000051', 'Chitungwiza Clinic Friends', 'chitungwiza-clinic-friends', 'PVO', '00000000-0000-7000-8000-000000000031');
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES ('00000000-0000-7000-8000-000000000052', '00000000-0000-7000-8000-000000000051', '00000000-0000-7000-8000-000000000031', md5('org_role:ORG_ADMIN')::uuid);
INSERT INTO app.campaign_review_policies (id, category_id, risk_tier, version, checks, status, effective_from, proposed_by, approved_by, approval_reason)
VALUES ('00000000-0000-7000-8000-0000000000f1', md5('campaign_category:MEDICAL')::uuid, 'HIGH', 1,
        '[{"check_code":"OWNER_IDENTITY","required":true,"mode":"AUTOMATIC","failure_effect":"BLOCK"},
          {"check_code":"SUPPORTING_DOCS","required":true,"mode":"MANUAL","failure_effect":"REQUEST_CHANGES"}]',
        'PUBLISHED', now(), '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000043', 'initial policy (test)');

-- ===================================================================================== beneficiaries
-- @case beneficiary_with_two_owners_rejected expect=error:ck_beneficiaries_one_owner
INSERT INTO app.beneficiaries (id, owner_user_id, owner_organisation_id, beneficiary_type, display_name, full_name)
VALUES ('00000000-0000-7000-8000-00000000b001', '00000000-0000-7000-8000-000000000031', '00000000-0000-7000-8000-000000000051',
        'INDIVIDUAL_OTHER', 'Chipo', 'Chipo Moyo');

-- @case owner_cannot_be_a_non_self_beneficiary expect=error:ck_beneficiaries_owner_is_not_beneficiary
INSERT INTO app.beneficiaries (id, owner_user_id, beneficiary_type, display_name, full_name, beneficiary_user_id)
VALUES ('00000000-0000-7000-8000-00000000b002', '00000000-0000-7000-8000-000000000031', 'INDIVIDUAL_OTHER', 'Me', 'Owner Name',
        '00000000-0000-7000-8000-000000000031');

-- @case self_beneficiary_must_be_the_owner expect=error:ck_beneficiaries_self
INSERT INTO app.beneficiaries (id, owner_user_id, beneficiary_type, display_name, beneficiary_user_id)
VALUES ('00000000-0000-7000-8000-00000000b003', '00000000-0000-7000-8000-000000000031', 'SELF', 'Me', '00000000-0000-7000-8000-000000000032');

-- @case minor_requires_second_verifier expect=error:ck_beneficiaries_minor_second_verifier
INSERT INTO app.beneficiaries (id, owner_user_id, beneficiary_type, display_name, full_name)
VALUES ('00000000-0000-7000-8000-00000000b004', '00000000-0000-7000-8000-000000000031', 'MINOR', 'Tari', 'Tariro Moyo');

-- @case institution_beneficiary_requires_payee expect=error:ck_beneficiaries_institution_type
INSERT INTO app.beneficiaries (id, owner_user_id, beneficiary_type, display_name)
VALUES ('00000000-0000-7000-8000-00000000b005', '00000000-0000-7000-8000-000000000031', 'INSTITUTION', 'Parirenyatwa');

-- @case beneficiaries_ok expect=ok
INSERT INTO app.beneficiaries (id, owner_user_id, beneficiary_type, display_name, full_name, beneficiary_user_id, risk_tier, requires_second_verifier)
VALUES ('00000000-0000-7000-8000-00000000b011', '00000000-0000-7000-8000-000000000031', 'INDIVIDUAL_OTHER', 'Chipo', 'Chipo Moyo',
        '00000000-0000-7000-8000-000000000032', 'STANDARD', false),
       ('00000000-0000-7000-8000-00000000b012', '00000000-0000-7000-8000-000000000031', 'MINOR', 'Tari', 'Tariro Moyo',
        NULL, 'HIGH', true);

-- @case beneficiary_cannot_skip_to_verified expect=error:illegal beneficiary_verification transition
UPDATE app.beneficiaries SET status = 'VERIFIED' WHERE id = '00000000-0000-7000-8000-00000000b011';

-- @case relationship_consent_basis_requires_consent_ref expect=error:ck_beneficiary_relationships_consent
INSERT INTO app.beneficiary_relationships (id, beneficiary_id, related_user_id, relationship_code, authority_basis, declared_by_user_id, declared_at)
VALUES ('00000000-0000-7000-8000-00000000b101', '00000000-0000-7000-8000-00000000b011', '00000000-0000-7000-8000-000000000031',
        'FRIEND', 'BENEFICIARY_CONSENT', '00000000-0000-7000-8000-000000000031', now());

-- @case beneficiaries_declared_and_evidence_submitted_ok expect=ok
INSERT INTO app.beneficiary_relationships (id, beneficiary_id, related_user_id, relationship_code, authority_basis, consent_ref, declared_by_user_id, declared_at)
VALUES ('00000000-0000-7000-8000-00000000b102', '00000000-0000-7000-8000-00000000b011', '00000000-0000-7000-8000-000000000031',
        'FRIEND', 'BENEFICIARY_CONSENT', '00000000-0000-7000-8000-00000000c0c0', '00000000-0000-7000-8000-000000000031', now()),
       ('00000000-0000-7000-8000-00000000b103', '00000000-0000-7000-8000-00000000b012', '00000000-0000-7000-8000-000000000031',
        'PARENT', 'GUARDIANSHIP', NULL, '00000000-0000-7000-8000-000000000031', now());
UPDATE app.beneficiaries SET status = 'DECLARED' WHERE id IN ('00000000-0000-7000-8000-00000000b011', '00000000-0000-7000-8000-00000000b012');
UPDATE app.beneficiaries SET status = 'EVIDENCE_SUBMITTED' WHERE id IN ('00000000-0000-7000-8000-00000000b011', '00000000-0000-7000-8000-00000000b012');

-- @case verified_without_decision_rejected expect=error:requires a matching beneficiary_verifications decision
UPDATE app.beneficiaries SET status = 'VERIFIED' WHERE id = '00000000-0000-7000-8000-00000000b011';

-- @case minor_verified_without_second_verifier_rejected expect=error:ck_beneficiary_verifications_four_eyes
INSERT INTO app.beneficiary_verifications (id, beneficiary_id, decision, from_status, to_status, decided_by, requires_second_verifier,
                                           reason_code, justification, policy_version, audit_event_id, decided_at)
VALUES ('00000000-0000-7000-8000-00000000b201', '00000000-0000-7000-8000-00000000b012', 'VERIFIED', 'EVIDENCE_SUBMITTED', 'VERIFIED',
        '00000000-0000-7000-8000-000000000041', true, 'GUARDIANSHIP_CONFIRMED', 'birth certificate checked', 'bv-1',
        '00000000-0000-7000-8000-00000000a0a0', now());

-- @case decision_cannot_downgrade_four_eyes_flag expect=error:must match the beneficiary
INSERT INTO app.beneficiary_verifications (id, beneficiary_id, decision, from_status, to_status, decided_by, requires_second_verifier,
                                           reason_code, justification, policy_version, audit_event_id, decided_at)
VALUES ('00000000-0000-7000-8000-00000000b202', '00000000-0000-7000-8000-00000000b012', 'VERIFIED', 'EVIDENCE_SUBMITTED', 'VERIFIED',
        '00000000-0000-7000-8000-000000000041', false, 'GUARDIANSHIP_CONFIRMED', 'birth certificate checked', 'bv-1',
        '00000000-0000-7000-8000-00000000a0a0', now());

-- @case owner_cannot_verify_own_beneficiary expect=error:a verifier cannot be the beneficiary owner
INSERT INTO app.beneficiary_verifications (id, beneficiary_id, decision, from_status, to_status, decided_by, requires_second_verifier,
                                           reason_code, justification, policy_version, audit_event_id, decided_at)
VALUES ('00000000-0000-7000-8000-00000000b203', '00000000-0000-7000-8000-00000000b011', 'VERIFIED', 'EVIDENCE_SUBMITTED', 'VERIFIED',
        '00000000-0000-7000-8000-000000000031', false, 'CONSENT_CONFIRMED', 'self check', 'bv-1', '00000000-0000-7000-8000-00000000a0a0', now());

-- @case beneficiaries_verified_ok expect=ok
INSERT INTO app.beneficiary_verifications (id, beneficiary_id, decision, from_status, to_status, decided_by, requires_second_verifier,
                                           reason_code, justification, policy_version, audit_event_id, decided_at)
VALUES ('00000000-0000-7000-8000-00000000b204', '00000000-0000-7000-8000-00000000b011', 'VERIFIED', 'EVIDENCE_SUBMITTED', 'VERIFIED',
        '00000000-0000-7000-8000-000000000041', false, 'CONSENT_CONFIRMED', 'OTP e-consent by beneficiary', 'bv-1',
        '00000000-0000-7000-8000-00000000a0a1', now());
UPDATE app.beneficiaries SET status = 'VERIFIED', latest_verification_id = '00000000-0000-7000-8000-00000000b204'
 WHERE id = '00000000-0000-7000-8000-00000000b011';
INSERT INTO app.beneficiary_verifications (id, beneficiary_id, decision, from_status, to_status, decided_by, second_verifier_id,
                                           requires_second_verifier, reason_code, justification, policy_version, audit_event_id, decided_at)
VALUES ('00000000-0000-7000-8000-00000000b205', '00000000-0000-7000-8000-00000000b012', 'VERIFIED', 'EVIDENCE_SUBMITTED', 'VERIFIED',
        '00000000-0000-7000-8000-000000000041', '00000000-0000-7000-8000-000000000042', true, 'GUARDIANSHIP_CONFIRMED',
        'birth certificate and school letter', 'bv-1', '00000000-0000-7000-8000-00000000a0a2', now());
UPDATE app.beneficiaries SET status = 'VERIFIED', latest_verification_id = '00000000-0000-7000-8000-00000000b205'
 WHERE id = '00000000-0000-7000-8000-00000000b012';

-- @case verification_decisions_append_only expect=error:append-only table
UPDATE app.beneficiary_verifications SET decision = 'REJECTED' WHERE id = '00000000-0000-7000-8000-00000000b204';

-- @case beneficiary_type_immutable expect=error:is immutable
UPDATE app.beneficiaries SET beneficiary_type = 'SELF' WHERE id = '00000000-0000-7000-8000-00000000b011';

-- ===================================================================================== campaign ownership & creation
-- @case campaign_with_both_owners_rejected expect=error:ck_campaigns_exactly_one_owner
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, owner_organisation_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c901', 'K7M2Q9X4TB', 'help-chipo', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000051', '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid,
        'Help Chipo', 'USD', '{USD}');

-- @case campaign_with_no_owner_rejected expect=error:ck_campaigns_exactly_one_owner
INSERT INTO app.campaigns (id, public_code, slug, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c902', 'K7M2Q9X4TC', 'help-chipo', '00000000-0000-7000-8000-000000000031',
        md5('campaign_category:MEDICAL')::uuid, 'Help Chipo', 'USD', '{USD}');

-- @case staff_account_cannot_own_campaign expect=error:fk_campaigns_owner_user
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c903', 'K7M2Q9X4TD', 'staff-campaign', '00000000-0000-7000-8000-000000000041',
        '00000000-0000-7000-8000-000000000041', md5('campaign_category:MEDICAL')::uuid, 'Staff campaign', 'USD', '{USD}');
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000d903', '00000000-0000-7000-8000-00000000c903', 1, '', 'DRAFT', 'staff', '00000000-0000-7000-8000-000000000041', 'CREATED', now());

-- @case campaign_cannot_be_created_active expect=error:illegal campaign transition
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies, status)
VALUES ('00000000-0000-7000-8000-00000000c904', 'K7M2Q9X4TE', 'shortcut', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'Shortcut', 'USD', '{USD}', 'ACTIVE');

-- @case campaign_creation_without_history_rejected expect=error:has no campaign_status_history row
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c905', 'K7M2Q9X4TF', 'no-history', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'No history', 'USD', '{USD}');

-- @case model_c_for_individual_rejected expect=error:ck_campaigns_model_c_org_only
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies, settlement_model)
VALUES ('00000000-0000-7000-8000-00000000c906', 'K7M2Q9X4TG', 'model-c', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'Model C', 'USD', '{USD}', 'MODEL_C');

-- @case ambiguous_public_code_rejected expect=error:ck_campaigns_public_code
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c907', 'ILOU000000', 'codes', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'Codes', 'USD', '{USD}');

-- @case goal_currency_must_be_accepted expect=error:ck_campaigns_accepted_currencies
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c908', 'K7M2Q9X4TH', 'zig', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'ZiG goal', 'ZWG', '{USD}');

-- @case unregistered_accepted_currency_rejected expect=error:is not a registered currency
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c909', 'K7M2Q9X4TJ', 'eur', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'Euro too', 'USD', '{USD,EUR}');

-- @case campaign_created_as_draft_ok expect=ok
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, story, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c001', 'K7M2Q9X4TA', 'help-chipo-surgery', '00000000-0000-7000-8000-000000000031',
        '00000000-0000-7000-8000-000000000031', md5('campaign_category:MEDICAL')::uuid, 'Help Chipo get surgery', 'Story...', 'USD', '{USD}');
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000d001', '00000000-0000-7000-8000-00000000c001', 1, '', 'DRAFT', 'user', '00000000-0000-7000-8000-000000000031', 'CREATED', now());

-- @case no_raised_or_balance_column_on_campaign_tables expect=ok
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
              WHERE table_schema = 'app' AND table_name LIKE 'campaign%'
                AND (column_name LIKE '%raised%' OR column_name LIKE '%balance%' OR column_name LIKE '%total%'
                     OR (column_name LIKE '%amount_minor%' AND table_name <> 'campaign_goals'))) THEN
    RAISE EXCEPTION 'campaign tables must not store raised/balance amounts (derived from the ledger)';
  END IF;
END $$;

-- @case draft_cannot_be_public expect=error:ck_campaigns_visibility_published
UPDATE app.campaigns SET visibility = 'PUBLIC' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case campaign_owner_immutable expect=error:is immutable
UPDATE app.campaigns SET owner_user_id = '00000000-0000-7000-8000-000000000032' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- ===================================================================================== goals / money
-- @case negative_goal_rejected expect=error:ck_campaign_goals_amount_positive
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e001', '00000000-0000-7000-8000-00000000c001', 1, -500000, 'USD', '00000000-0000-7000-8000-000000000031');

-- @case zero_goal_rejected expect=error:ck_campaign_goals_amount_positive
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e002', '00000000-0000-7000-8000-00000000c001', 1, 0, 'USD', '00000000-0000-7000-8000-000000000031');

-- A decimal string (as a float-minded client would send) is not an integer count of minor units.
-- @case decimal_goal_amount_rejected expect=error:invalid input syntax for type bigint
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e003', '00000000-0000-7000-8000-00000000c001', 1, '5000.50', 'USD', '00000000-0000-7000-8000-000000000031');

-- @case goal_without_currency_rejected expect=error:null value in column "currency"
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e004', '00000000-0000-7000-8000-00000000c001', 1, 500000, '00000000-0000-7000-8000-000000000031');

-- @case goal_in_other_currency_rejected expect=error:does not match campaign goal currency
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e005', '00000000-0000-7000-8000-00000000c001', 1, 500000, 'ZWG', '00000000-0000-7000-8000-000000000031');

-- @case goal_ok expect=ok
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000e006', '00000000-0000-7000-8000-00000000c001', 1, 500000, 'USD', '00000000-0000-7000-8000-000000000031');

-- @case goal_rows_append_only expect=error:append-only table
UPDATE app.campaign_goals SET amount_minor = 900000 WHERE id = '00000000-0000-7000-8000-00000000e006';

-- ===================================================================================== submission
-- @case submit_without_beneficiary_rejected expect=error:without a primary beneficiary
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d002', id, version + 1, 'DRAFT', 'SUBMITTED', 'user', owner_user_id, 'OWNER_SUBMIT', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'SUBMITTED', submitted_at = now(), risk_tier = 'HIGH',
       fundraising_authority_ref = '00000000-0000-7000-8000-00000000fa01'
 WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case link_primary_beneficiary_ok expect=ok
INSERT INTO app.campaign_beneficiaries (id, campaign_id, beneficiary_id, linked_by_user_id, linked_at)
VALUES ('00000000-0000-7000-8000-00000000cb01', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000b011',
        '00000000-0000-7000-8000-000000000031', now());

-- @case second_primary_beneficiary_rejected expect=error:uq_campaign_beneficiaries_one_primary
INSERT INTO app.campaign_beneficiaries (id, campaign_id, beneficiary_id, linked_by_user_id, linked_at)
VALUES ('00000000-0000-7000-8000-00000000cb02', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000b012',
        '00000000-0000-7000-8000-000000000031', now());

-- @case illegal_draft_to_active_rejected expect=error:illegal campaign transition: "DRAFT" -> "ACTIVE"
UPDATE app.campaigns SET status = 'ACTIVE', published_at = now() WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case submit_without_fundraising_authority_rejected expect=error:ck_campaigns_fundraising_authority
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d003', id, version + 1, 'DRAFT', 'SUBMITTED', 'user', owner_user_id, 'OWNER_SUBMIT', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'SUBMITTED', submitted_at = now(), risk_tier = 'HIGH' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case status_change_without_history_rejected expect=error:has no campaign_status_history row
UPDATE app.campaigns SET status = 'SUBMITTED', submitted_at = now(), risk_tier = 'HIGH',
       fundraising_authority_ref = '00000000-0000-7000-8000-00000000fa01'
 WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case legal_draft_to_submitted_ok expect=ok
INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, title, story, category_id, content_sha256, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000cf01', '00000000-0000-7000-8000-00000000c001', 1, 'SUBMISSION', 'Help Chipo get surgery', 'Story...',
        md5('campaign_category:MEDICAL')::uuid, sha256('snapshot v1'::bytea), '00000000-0000-7000-8000-000000000031');
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d004', id, version + 1, 'DRAFT', 'SUBMITTED', 'user', owner_user_id, 'OWNER_SUBMIT', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'SUBMITTED', submitted_at = now(), risk_tier = 'HIGH',
       fundraising_authority_ref = '00000000-0000-7000-8000-00000000fa01'
 WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case goal_currency_change_after_submission_rejected expect=error:cannot change after submission
UPDATE app.campaigns SET goal_currency = 'ZWG', accepted_currencies = '{ZWG}' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case campaign_versions_append_only expect=error:append-only table
UPDATE app.campaign_versions SET story = 'rewritten after review' WHERE id = '00000000-0000-7000-8000-00000000cf01';

-- ===================================================================================== review
-- @case high_tier_policy_without_manual_check_rejected expect=error:ck_campaign_review_policies_human_review
INSERT INTO app.campaign_review_policies (id, category_id, risk_tier, version, checks, proposed_by)
VALUES ('00000000-0000-7000-8000-0000000000f2', md5('campaign_category:PERSONAL')::uuid, 'HIGH', 1,
        '[{"check_code":"OWNER_IDENTITY","required":true,"mode":"AUTOMATIC","failure_effect":"BLOCK"}]', '00000000-0000-7000-8000-000000000042');

-- @case published_policy_immutable expect=error:published review policy
UPDATE app.campaign_review_policies SET checks = '[{"check_code":"X","mode":"MANUAL"}]' WHERE id = '00000000-0000-7000-8000-0000000000f1';

-- @case review_pinned_to_draft_policy_rejected expect=error:PUBLISHED review policy
INSERT INTO app.campaign_review_policies (id, category_id, risk_tier, version, checks, proposed_by)
VALUES ('00000000-0000-7000-8000-0000000000f3', md5('campaign_category:MEDICAL')::uuid, 'HIGH', 2,
        '[{"check_code":"SUPPORTING_DOCS","required":true,"mode":"MANUAL","failure_effect":"REQUEST_CHANGES"}]', '00000000-0000-7000-8000-000000000042');
INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, review_policy_id, review_kind, risk_tier_at_submission)
VALUES ('00000000-0000-7000-8000-00000000ab01', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000cf01',
        '00000000-0000-7000-8000-0000000000f3', 'INITIAL', 'HIGH');

-- @case review_claim_requires_coi_attestation expect=error:ck_campaign_reviews_claim
INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, review_policy_id, review_kind, risk_tier_at_submission,
                                  status, claimed_by, claimed_at)
VALUES ('00000000-0000-7000-8000-00000000ab02', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000cf01',
        '00000000-0000-7000-8000-0000000000f1', 'INITIAL', 'HIGH', 'CLAIMED', '00000000-0000-7000-8000-000000000041', now());

-- @case review_claimed_and_campaign_under_review_ok expect=ok
INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, review_policy_id, review_kind, risk_tier_at_submission,
                                  status, claimed_by, claimed_at, coi_attested_at)
VALUES ('00000000-0000-7000-8000-00000000ab03', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000cf01',
        '00000000-0000-7000-8000-0000000000f1', 'INITIAL', 'HIGH', 'CLAIMED', '00000000-0000-7000-8000-000000000041', now(), now());
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, actor_role,
                                         reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d005', id, version + 1, 'SUBMITTED', 'UNDER_REVIEW', 'staff', '00000000-0000-7000-8000-000000000041',
       'REVIEWER', 'REVIEW_CLAIMED', 'claimed from queue', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'UNDER_REVIEW' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case second_open_review_rejected expect=error:uq_campaign_reviews_open
INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, review_policy_id, review_kind, risk_tier_at_submission)
VALUES ('00000000-0000-7000-8000-00000000ab04', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000cf01',
        '00000000-0000-7000-8000-0000000000f1', 'INITIAL', 'HIGH');

-- @case staff_transition_requires_justification expect=error:ck_campaign_status_history_staff_justification
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d006', id, version + 1, 'UNDER_REVIEW', 'APPROVED', 'staff', '00000000-0000-7000-8000-000000000041', 'APPROVED', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case high_tier_approval_without_compliance_signoff_rejected expect=error:ck_campaign_reviews_high_four_eyes
UPDATE app.campaign_reviews SET status = 'DECIDED', outcome = 'APPROVE', reason_code = 'ALL_CHECKS_PASSED', justification = 'docs verified',
       decided_by = '00000000-0000-7000-8000-000000000041', decided_at = now(),
       check_results = '[{"check_code":"SUPPORTING_DOCS","result":"PASS"}]'
 WHERE id = '00000000-0000-7000-8000-00000000ab03';

-- @case approval_with_failed_check_rejected expect=error:ck_campaign_reviews_no_failed_check_approval
UPDATE app.campaign_reviews SET status = 'DECIDED', outcome = 'APPROVE', reason_code = 'ALL_CHECKS_PASSED', justification = 'docs verified',
       decided_by = '00000000-0000-7000-8000-000000000041', decided_at = now(),
       compliance_signoff_by = '00000000-0000-7000-8000-000000000042', compliance_signoff_at = now(),
       check_results = '[{"check_code":"SUPPORTING_DOCS","result":"FAIL"}]'
 WHERE id = '00000000-0000-7000-8000-00000000ab03';

-- @case approve_and_publish_ok expect=ok
UPDATE app.campaign_reviews SET status = 'DECIDED', outcome = 'APPROVE', reason_code = 'ALL_CHECKS_PASSED', justification = 'docs verified',
       decided_by = '00000000-0000-7000-8000-000000000041', decided_at = now(),
       compliance_signoff_by = '00000000-0000-7000-8000-000000000042', compliance_signoff_at = now(),
       check_results = '[{"check_code":"SUPPORTING_DOCS","result":"PASS"}]'
 WHERE id = '00000000-0000-7000-8000-00000000ab03';
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, actor_role,
                                         reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d007', id, version + 1, 'UNDER_REVIEW', 'APPROVED', 'staff', '00000000-0000-7000-8000-000000000041',
       'REVIEWER', 'ALL_CHECKS_PASSED', 'docs verified; compliance sign-off recorded', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'APPROVED', approved_at = now(), approved_version_id = '00000000-0000-7000-8000-00000000cf01'
 WHERE id = '00000000-0000-7000-8000-00000000c001';
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d008', id, version + 1, 'APPROVED', 'ACTIVE', 'user', owner_user_id, 'OWNER_PUBLISH', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'ACTIVE', published_at = now(), visibility = 'PUBLIC' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case decided_review_is_final expect=error:is final
UPDATE app.campaign_reviews SET outcome = 'REJECT' WHERE id = '00000000-0000-7000-8000-00000000ab03';

-- @case approved_version_must_belong_to_campaign expect=error:fk_campaigns_approved_version
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by_user_id, category_id, title, goal_currency, accepted_currencies)
VALUES ('00000000-0000-7000-8000-00000000c002', 'K7M2Q9X4TK', 'other', '00000000-0000-7000-8000-000000000032',
        '00000000-0000-7000-8000-000000000032', md5('campaign_category:SPORTS')::uuid, 'Team travel', 'USD', '{USD}');
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000d101', '00000000-0000-7000-8000-00000000c002', 1, '', 'DRAFT', 'user', '00000000-0000-7000-8000-000000000032', 'CREATED', now());
UPDATE app.campaigns SET approved_version_id = '00000000-0000-7000-8000-00000000cf01' WHERE id = '00000000-0000-7000-8000-00000000c002';

-- ===================================================================================== freeze / unfreeze
-- @case freeze_ok expect=ok
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, actor_role,
                                         reason_code, justification, risk_hold_id, ledger_transaction_id, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d009', id, version + 1, 'ACTIVE', 'FROZEN', 'staff', '00000000-0000-7000-8000-000000000042',
       'COMPLIANCE', 'FRAUD_REPORT', 'credible fraud report; case opened', '00000000-0000-7000-8000-00000000aa01',
       '00000000-0000-7000-8000-00000000aa02', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'FROZEN' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case frozen_to_completed_when_never_completed_rejected expect=error:only for a campaign that was COMPLETED before the freeze
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, requested_by, expires_at, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec01', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_REQUEST', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-000000000042', now() + interval '72 hours', 'CLEARED', 'investigation cleared', now());
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec02', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_APPROVE', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000043', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec01', '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000043', 'CLEARED', 'reviewed case file', now());
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         moderation_action_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d010', id, version + 1, 'FROZEN', 'COMPLETED', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', '00000000-0000-7000-8000-00000000ec02', 'CLEARED', 'cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'COMPLETED', completed_at = now() WHERE id = '00000000-0000-7000-8000-00000000c001';

-- (The BEFORE INSERT trigger rejects first; ck_campaign_status_history_unfreeze_checker/_distinct remain as a second layer.)
-- @case unfreeze_without_second_approver_rejected expect=error:requires an approved UNFREEZE_APPROVE moderation action
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id,
                                         reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d011', id, version + 1, 'FROZEN', 'ACTIVE', 'staff', '00000000-0000-7000-8000-000000000042',
       'CLEARED', 'investigation cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case unfreeze_without_moderation_approval_rejected expect=error:requires an approved UNFREEZE_APPROVE moderation action
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d018', id, version + 1, 'FROZEN', 'ACTIVE', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', 'CLEARED', 'investigation cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case unfreeze_moderation_self_approval_rejected expect=error:ck_campaign_moderation_actions_maker_checker
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, requested_by, expires_at, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec03', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_REQUEST', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-000000000042', now() + interval '72 hours', 'CLEARED', 'investigation cleared', now());
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec04', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_APPROVE', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec03', '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000042', 'CLEARED', 'reviewed case file', now());

-- @case unfreeze_self_approved_rejected expect=error:requires an approved UNFREEZE_APPROVE moderation action
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d012', id, version + 1, 'FROZEN', 'ACTIVE', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000042', 'CLEARED', 'investigation cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case unfreeze_with_checker_ok expect=ok
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, requested_by, expires_at, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec05', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_REQUEST', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-000000000042', now() + interval '72 hours', 'CLEARED', 'investigation cleared', now());
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec06', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_APPROVE', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000043', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec05', '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000043', 'CLEARED', 'reviewed case file', now());
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         moderation_action_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d013', id, version + 1, 'FROZEN', 'ACTIVE', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', '00000000-0000-7000-8000-00000000ec06', 'CLEARED', 'investigation cleared; case closed', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'ACTIVE' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case status_history_append_only expect=error:append-only table
UPDATE app.campaign_status_history SET justification = 'rewritten' WHERE id = '00000000-0000-7000-8000-00000000d013';

-- @case campaign_delete_rejected expect=error:append-only table
DELETE FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case complete_then_freeze_ok expect=ok
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d014', id, version + 1, 'ACTIVE', 'COMPLETED', 'user', owner_user_id, 'OWNER_CLOSED', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'COMPLETED', completed_at = now() WHERE id = '00000000-0000-7000-8000-00000000c001';
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d015', id, version + 1, 'COMPLETED', 'FROZEN', 'staff', '00000000-0000-7000-8000-000000000042',
       'PRE_PAYOUT_INVESTIGATION', 'chargeback cluster before final payout', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'FROZEN' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case completed_campaign_cannot_reactivate_via_freeze expect=error:returns to COMPLETED after a freeze
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, requested_by, expires_at, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec07', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_REQUEST', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-000000000042', now() + interval '72 hours', 'CLEARED', 'investigation cleared', now());
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec08', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_APPROVE', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000043', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec07', '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000043', 'CLEARED', 'reviewed case file', now());
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         moderation_action_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d016', id, version + 1, 'FROZEN', 'ACTIVE', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', '00000000-0000-7000-8000-00000000ec08', 'CLEARED', 'cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'ACTIVE' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case frozen_back_to_completed_ok expect=ok
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, requested_by, expires_at, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec09', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_REQUEST', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000042', 'COMPLIANCE', '00000000-0000-7000-8000-000000000042', now() + interval '72 hours', 'CLEARED', 'investigation cleared', now());
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec0a', '00000000-0000-7000-8000-00000000c001', 'UNFREEZE_APPROVE', 'CAMPAIGN', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000043', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec09', '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000043', 'CLEARED', 'reviewed case file', now());
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         moderation_action_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d017', id, version + 1, 'FROZEN', 'COMPLETED', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', '00000000-0000-7000-8000-00000000ec0a', 'CLEARED', 'investigation cleared', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';
UPDATE app.campaigns SET status = 'COMPLETED' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case completed_to_active_rejected expect=error:illegal campaign transition: "COMPLETED" -> "ACTIVE"
UPDATE app.campaigns SET status = 'ACTIVE' WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case all_product_transitions_registered expect=ok
DO $$ BEGIN
  IF (SELECT count(*) FROM app.status_transitions WHERE machine = 'campaign' AND from_status <> '') <> 22 THEN
    RAISE EXCEPTION 'expected the 22 PRODUCT §6.2 transitions';
  END IF;
END $$;

-- ===================================================================================== media / reports / moderation
-- @case private_object_cannot_be_campaign_media expect=error:fk_campaign_media_stored_object
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class, encryption_key_id)
VALUES ('00000000-0000-7000-8000-00000000ee01', 'PRIVATE_EVIDENCE', 'campaigns', 'quarantine/1d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('medical letter'::bytea), 100, 'application/pdf', 'C3', 'CASE', 'kms-evidence');
INSERT INTO app.campaign_media (id, campaign_id, stored_object_id, media_kind, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000ef01', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000ee01',
        'GALLERY_IMAGE', '00000000-0000-7000-8000-000000000031');

-- @case minor_photo_requires_guardian_consent expect=error:ck_campaign_media_minor_consent
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class)
VALUES ('00000000-0000-7000-8000-00000000ee02', 'PUBLIC_MEDIA', 'campaigns', 'quarantine/2d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('child photo'::bytea), 100, 'image/jpeg', 'C1', 'OPERATIONAL');
INSERT INTO app.campaign_media (id, campaign_id, stored_object_id, media_kind, depicts_minor, status, created_by_user_id)
VALUES ('00000000-0000-7000-8000-00000000ef02', '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000ee02',
        'COVER_IMAGE', true, 'READY', '00000000-0000-7000-8000-000000000031');

-- @case report_and_moderation_ok expect=ok
INSERT INTO app.campaign_reports (id, campaign_id, reason_code, details)
VALUES ('00000000-0000-7000-8000-00000000ea01', '00000000-0000-7000-8000-00000000c001', 'MISLEADING', 'Same photos as another campaign');
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, campaign_report_id, actor_id, actor_role,
                                             reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000eb01', '00000000-0000-7000-8000-00000000c001', 'FLAG_RE_REVIEW', 'CAMPAIGN',
        '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-00000000ea01', '00000000-0000-7000-8000-000000000041', 'REVIEWER',
        'IMAGE_REUSE', 'image reuse reported', now());

-- @case report_content_immutable expect=error:is immutable
UPDATE app.campaign_reports SET details = 'edited by staff' WHERE id = '00000000-0000-7000-8000-00000000ea01';

-- @case moderation_actions_append_only expect=error:append-only table
DELETE FROM app.campaign_moderation_actions WHERE id = '00000000-0000-7000-8000-00000000eb01';

-- @case moderation_approval_used_twice_rejected expect=error:already used
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, from_status, to_status, actor_type, actor_id, approved_by,
                                         moderation_action_id, reason_code, justification, occurred_at)
SELECT '00000000-0000-7000-8000-00000000d019', id, version + 1, 'FROZEN', 'COMPLETED', 'staff', '00000000-0000-7000-8000-000000000042',
       '00000000-0000-7000-8000-000000000043', '00000000-0000-7000-8000-00000000ec0a', 'CLEARED', 'reuse', now()
  FROM app.campaigns WHERE id = '00000000-0000-7000-8000-00000000c001';

-- @case approval_of_wrong_request_kind_rejected expect=error:approval does not match
INSERT INTO app.campaign_moderation_actions (id, campaign_id, action, target_type, target_id, actor_id, actor_role, request_action_id, requested_by, approved_by, reason_code, justification, occurred_at)
VALUES ('00000000-0000-7000-8000-00000000ec0b', '00000000-0000-7000-8000-00000000c001', 'CANCEL_FROM_FROZEN_APPROVE', 'CAMPAIGN',
        '00000000-0000-7000-8000-00000000c001', '00000000-0000-7000-8000-000000000041', 'COMPLIANCE', '00000000-0000-7000-8000-00000000ec05',
        '00000000-0000-7000-8000-000000000042', '00000000-0000-7000-8000-000000000041', 'X', 'wrong kind', now());
