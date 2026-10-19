-- Stage 15 finalisation (KI-S15-19; Stage 11 payouts, ADR-043): a bounded budget for automatic same-reference
-- resubmissions after DEFINITELY_NOT_SENT / RATE_LIMITED outcomes. When the budget is spent, payouts opens a
-- SUBMISSION_RETRIES_EXHAUSTED exception and automatic submission stops; while that exception is unresolved the database
-- refuses any further CREATE_PAYOUT for the payout (defence in depth behind the worker's own check). Resolving the
-- exception (operator, audited) grants a fresh budget. Nothing else changes: the payout stays SUBMITTED, the reservation
-- and the in-transit journal are untouched, an UNKNOWN outcome is still never resubmitted.
-- Owner module: payouts. Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE app.payout_exceptions DROP CONSTRAINT ck_payout_exceptions_type;
ALTER TABLE app.payout_exceptions ADD CONSTRAINT ck_payout_exceptions_type CHECK (exception_type IN ('UNKNOWN_PROVIDER_OUTCOME',
  'DESTINATION_MISMATCH','INSUFFICIENT_ELIGIBLE_FUNDS','COMPLIANCE_HOLD','APPROVAL_EXPIRED','PROVIDER_UNAVAILABLE','RECONCILIATION_MISMATCH',
  'DUPLICATE_SUBMISSION_ATTEMPT','LEDGER_POSTING_FAILED','LATE_COMPLETION_AFTER_FAILURE','PAYOUT_RETURNED','SUBMISSION_RETRIES_EXHAUSTED'));

CREATE OR REPLACE FUNCTION app.payout_attempts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
  submit_txn uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF NEW.provider_id <> p.provider_id THEN
    RAISE EXCEPTION 'payout attempt provider differs from the bound provider' USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.operation = 'CREATE_PAYOUT' THEN
    IF p.status <> 'SUBMITTED' THEN
      RAISE EXCEPTION 'payout resubmission forbidden: CREATE_PAYOUT only while SUBMITTED (payout % is %)', p.id, p.status
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT ledger_submit_txn_id INTO submit_txn FROM app.payout_reservations WHERE payout_request_id = p.id;
    IF submit_txn IS NULL THEN
      RAISE EXCEPTION 'payout % has no in-transit journal: the provider must not be called before it commits', p.id
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
                  AND a.classification <> 'DEFINITELY_NOT_SENT') THEN
      RAISE EXCEPTION 'payout resubmission forbidden: an earlier CREATE_PAYOUT for % may have reached the provider', p.id
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM app.payout_exceptions x WHERE x.payout_request_id = p.id
                  AND x.exception_type = 'SUBMISSION_RETRIES_EXHAUSTED' AND x.status <> 'RESOLVED') THEN
      RAISE EXCEPTION 'payout resubmission forbidden: automatic submission of % is halted (retry budget exhausted)', p.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM app.payout_exceptions WHERE exception_type = 'SUBMISSION_RETRIES_EXHAUSTED') THEN
    RAISE EXCEPTION 'SUBMISSION_RETRIES_EXHAUSTED exceptions exist: exception records are never deleted, so this migration cannot be rolled back';
  END IF;
END $$;
ALTER TABLE app.payout_exceptions DROP CONSTRAINT ck_payout_exceptions_type;
ALTER TABLE app.payout_exceptions ADD CONSTRAINT ck_payout_exceptions_type CHECK (exception_type IN ('UNKNOWN_PROVIDER_OUTCOME',
  'DESTINATION_MISMATCH','INSUFFICIENT_ELIGIBLE_FUNDS','COMPLIANCE_HOLD','APPROVAL_EXPIRED','PROVIDER_UNAVAILABLE','RECONCILIATION_MISMATCH',
  'DUPLICATE_SUBMISSION_ATTEMPT','LEDGER_POSTING_FAILED','LATE_COMPLETION_AFTER_FAILURE','PAYOUT_RETURNED'));
CREATE OR REPLACE FUNCTION app.payout_attempts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
  submit_txn uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF NEW.provider_id <> p.provider_id THEN
    RAISE EXCEPTION 'payout attempt provider differs from the bound provider' USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.operation = 'CREATE_PAYOUT' THEN
    IF p.status <> 'SUBMITTED' THEN
      RAISE EXCEPTION 'payout resubmission forbidden: CREATE_PAYOUT only while SUBMITTED (payout % is %)', p.id, p.status
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT ledger_submit_txn_id INTO submit_txn FROM app.payout_reservations WHERE payout_request_id = p.id;
    IF submit_txn IS NULL THEN
      RAISE EXCEPTION 'payout % has no in-transit journal: the provider must not be called before it commits', p.id
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
                  AND a.classification <> 'DEFINITELY_NOT_SENT') THEN
      RAISE EXCEPTION 'payout resubmission forbidden: an earlier CREATE_PAYOUT for % may have reached the provider', p.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
