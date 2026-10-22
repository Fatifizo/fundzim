-- Stage 17 payout reconciliation follow-up (KI-S17-01; ADR-049 §11; Stage 11 payouts, ADR-043). A CREATE_PAYOUT
-- classified RATE_LIMITED was refused by the provider before processing (psp.CategoryRetry: AFTER_BACKOFF), exactly like
-- DEFINITELY_NOT_SENT — but the attempts guard treated it as "may have reached the provider", so the payout was never
-- re-dispatched, never counted against the KI-S15-19 retry budget and was status-polled indefinitely. RATE_LIMITED now
-- counts as not sent: same reference, within the budget, halted by SUBMISSION_RETRIES_EXHAUSTED when spent. An outcome
-- that may have reached the provider (ACCEPTED, OUTCOME_UNKNOWN, IN_FLIGHT, …) still forbids any resubmission.
-- Owner module: payouts. Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

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
                  AND a.classification NOT IN ('DEFINITELY_NOT_SENT', 'RATE_LIMITED')) THEN
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
