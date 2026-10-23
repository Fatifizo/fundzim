-- Stage 19 — financial concurrency, load, failure and integrity validation (ADR-051). Owner module: payments. Runs as
-- fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
--
-- KI-S18-22: routine status polling is bounded for every awaiting payment status (payments.PollHorizon from creation,
-- payments.MaxStatusPolls per payment). A payment whose budget is spent keeps its status — an exhausted budget is
-- never evidence of failure — and gets a POLL_BUDGET_EXHAUSTED reconciliation reference (needs_reconciliation). A
-- later authoritative status (webhook, reconciliation evidence) resolves the reference exactly like the UNKNOWN horizon.
--
-- KI-S18-25 needs no schema change: the reservation invariants it relies on (ck_payout_reservations_*, the
-- payout_reservation machine and the set-once link trigger) are unchanged.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE app.payment_reconciliation_references DROP CONSTRAINT ck_payment_reconciliation_references_reason;
ALTER TABLE app.payment_reconciliation_references ADD CONSTRAINT ck_payment_reconciliation_references_reason CHECK (reason IN (
  'UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON','AMOUNT_MISMATCH','CURRENCY_MISMATCH','ANOMALY','LATE_SUCCESS',
  'DUPLICATE_SUCCESS','PARKED_DEADLINE_EXCEEDED','UNMAPPED_STATUS','CONTRADICTORY_EVIDENCE','POLL_BUDGET_EXHAUSTED'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- references already opened with the new reason stay (history is never deleted): the restored CHECK is NOT VALID
ALTER TABLE app.payment_reconciliation_references DROP CONSTRAINT ck_payment_reconciliation_references_reason;
ALTER TABLE app.payment_reconciliation_references ADD CONSTRAINT ck_payment_reconciliation_references_reason CHECK (reason IN (
  'UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON','AMOUNT_MISMATCH','CURRENCY_MISMATCH','ANOMALY','LATE_SUCCESS',
  'DUPLICATE_SUCCESS','PARKED_DEADLINE_EXCEEDED','UNMAPPED_STATUS','CONTRADICTORY_EVIDENCE')) NOT VALID;
-- +goose StatementEnd
