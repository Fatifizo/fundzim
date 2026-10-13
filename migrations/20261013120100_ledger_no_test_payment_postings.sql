-- Stage 10 (ADR-042 §3): a TEST ledger never holds a journal sourced from the payments system. Every payment that
-- can exist outside a PRODUCTION ledger is a test payment, and test payments are never posted; the ledger consumer
-- records them NOT_ELIGIBLE. Until this constraint, only Go enforced that (and an integration test posted
-- payments-sourced journals for fictitious payments into the development TEST ledger — those rows are append-only
-- and are kept; NOT VALID leaves them in place while every new row is checked; docs/stage-10/known-issues.md
-- KI-S10-17). Owner module: ledger. Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE ledger.ledger_transactions
  ADD CONSTRAINT ck_ledger_transactions_no_payment_postings_in_test
  CHECK (environment <> 'TEST' OR source_system <> 'payments') NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_no_payment_postings_in_test;
-- +goose StatementEnd
