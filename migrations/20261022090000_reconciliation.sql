-- Stage 17: financial reconciliation and settlement operations (ADR-049, implements ADR-030; derived from the Stage 2
-- draft design/sql/0017_reconciliation.sql). Owner module: reconciliation (schema recon). Runs as fundzim_migrator.
-- Forward-only in production (ADR-028); Down is for development.
--
-- Everything is TEST: sources are CHECK-limited to the TEST environment (no provider is selected, ADR-040), the only
-- statement formats are FundZim's canonical contracts, no settlement posting is made (ADR-049 §8). Matches and
-- exceptions reference payments/payouts by (subject_type, subject_id) only — those tables belong to other modules — and
-- ledger journals by a composite FK (ledger rows are immutable; ADR-030 §2.2, confirmed in ADR-049).
-- Also: storage accepts CSV/JSON for purpose RECONCILIATION_REPORT only; permissions; Stage 17 notification templates.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- Generic column guard: only the columns named in TG_ARGV may change on UPDATE (Stage 2 draft).
CREATE FUNCTION recon.guard_mutable_columns() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - TG_ARGV) IS DISTINCT FROM (to_jsonb(OLD) - TG_ARGV) THEN
    RAISE EXCEPTION '%.%: only columns % may change (row is otherwise immutable)', TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_ARGV
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

-- Rows in a terminal status are frozen. TG_ARGV = terminal statuses.
CREATE FUNCTION recon.freeze_terminal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = ANY (TG_ARGV) THEN
    RAISE EXCEPTION '%.% row % is final (%)', TG_TABLE_SCHEMA, TG_TABLE_NAME, OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

-- ---- 1. sources: configured statement streams (TEST only in Stage 17) ----------------------------------------------
CREATE TABLE recon.sources (
  id             uuid        NOT NULL,
  code           text        NOT NULL,
  source_kind    text        NOT NULL,
  provider_code  text,                       -- psp registry code; NULL only for FundZim's own bank statement
  authority_rank smallint    NOT NULL,       -- ADR-049 §3: higher = stronger evidence
  format         text        NOT NULL,
  environment    text        NOT NULL,
  description    text        NOT NULL,
  enabled        boolean     NOT NULL DEFAULT true,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_sources PRIMARY KEY (id),
  CONSTRAINT uq_recon_sources_code UNIQUE (code),
  CONSTRAINT ck_recon_sources_code CHECK (code ~ '^[a-z][a-z0-9_]{2,63}$'),
  CONSTRAINT ck_recon_sources_kind CHECK (source_kind IN ('PROVIDER_TRANSACTION_REPORT', 'PROVIDER_SETTLEMENT_REPORT', 'BANK_STATEMENT')),
  CONSTRAINT ck_recon_sources_provider CHECK ((provider_code IS NULL) = (source_kind = 'BANK_STATEMENT')),
  CONSTRAINT ck_recon_sources_rank CHECK (authority_rank = CASE source_kind WHEN 'PROVIDER_TRANSACTION_REPORT' THEN 2
                                                                         WHEN 'PROVIDER_SETTLEMENT_REPORT' THEN 3 ELSE 4 END),
  CONSTRAINT ck_recon_sources_format CHECK (format IN ('FUNDZIM_CANONICAL_V1')),
  -- no provider is selected (ADR-040): a PRODUCTION source needs a selection ADR, the provider's documented format and acceptance
  CONSTRAINT ck_recon_sources_test_only_stage17 CHECK (environment = 'TEST')
);
CREATE TRIGGER trg_recon_sources_no_mutation BEFORE UPDATE OR DELETE ON recon.sources
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO recon.sources (id, code, source_kind, provider_code, authority_rank, format, environment, description) VALUES
  (md5('recon_source:sandbox_transactions_test')::uuid, 'sandbox_transactions_test', 'PROVIDER_TRANSACTION_REPORT', 'sandbox', 2,
   'FUNDZIM_CANONICAL_V1', 'TEST', 'TEST transaction report of the sandbox simulator (canonical format; never a real provider statement)'),
  (md5('recon_source:sandbox_settlement_test')::uuid, 'sandbox_settlement_test', 'PROVIDER_SETTLEMENT_REPORT', 'sandbox', 3,
   'FUNDZIM_CANONICAL_V1', 'TEST', 'TEST settlement report of the sandbox simulator (canonical format; never a real settlement)'),
  (md5('recon_source:fundzim_operating_bank_test')::uuid, 'fundzim_operating_bank_test', 'BANK_STATEMENT', NULL, 4,
   'FUNDZIM_CANONICAL_V1', 'TEST', 'TEST statement of FundZim''s own operating account (Model A: platform-fee income only; never a real bank statement)');

-- ---- 2. matching rule sets (maker-checker; none seeded: ADR-049 §4) --------------------------------------------------
CREATE TABLE recon.matching_rule_sets (
  id             uuid        NOT NULL,
  version_no     integer     NOT NULL,
  status         text        NOT NULL,
  config         jsonb       NOT NULL,
  config_sha256  bytea       NOT NULL,
  change_reason  text        NOT NULL,
  proposed_by    uuid        NOT NULL,
  proposed_at    timestamptz NOT NULL,
  decided_by     uuid,
  decided_at     timestamptz,
  decision_note  text,
  effective_from timestamptz,
  retired_by     uuid,
  retired_at     timestamptz,
  version        integer     NOT NULL DEFAULT 1,
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_matching_rule_sets PRIMARY KEY (id),
  CONSTRAINT uq_recon_matching_rule_sets_version UNIQUE (version_no),
  CONSTRAINT fk_recon_rule_sets_proposer FOREIGN KEY (proposed_by) REFERENCES app.users (id),
  CONSTRAINT fk_recon_rule_sets_decider FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT fk_recon_rule_sets_retirer FOREIGN KEY (retired_by) REFERENCES app.users (id),
  CONSTRAINT ck_recon_rule_sets_status CHECK (status IN ('PENDING_APPROVAL', 'ACTIVE', 'REJECTED', 'WITHDRAWN', 'RETIRED')),
  -- closed, bounded keys (Go validates the same; docs/stage-17/matching-engine.md §4)
  CONSTRAINT ck_recon_rule_sets_config CHECK (jsonb_typeof(config) = 'object'
    AND config ?& ARRAY['date_window_days','settlement_horizon_days','stale_after_hours']
    AND (config - ARRAY['date_window_days','settlement_horizon_days','stale_after_hours','escalate_after_hours']) = '{}'::jsonb),
  CONSTRAINT ck_recon_rule_sets_hash CHECK (octet_length(config_sha256) = 32),
  CONSTRAINT ck_recon_rule_sets_reason CHECK (length(btrim(change_reason)) BETWEEN 10 AND 1000),
  CONSTRAINT ck_recon_rule_sets_maker_checker CHECK (decided_by IS NULL OR decided_by <> proposed_by),
  CONSTRAINT ck_recon_rule_sets_decided CHECK (status = 'PENDING_APPROVAL' OR status = 'WITHDRAWN'
    OR (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_recon_rule_sets_active CHECK (status NOT IN ('ACTIVE', 'RETIRED') OR effective_from IS NOT NULL),
  CONSTRAINT ck_recon_rule_sets_retired CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL))
);
CREATE UNIQUE INDEX uq_recon_matching_rule_sets_one_active ON recon.matching_rule_sets ((true)) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX uq_recon_matching_rule_sets_one_pending ON recon.matching_rule_sets ((true)) WHERE status = 'PENDING_APPROVAL';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_rule_set', '', 'PENDING_APPROVAL'),
  ('recon_rule_set', 'PENDING_APPROVAL', 'ACTIVE'),
  ('recon_rule_set', 'PENDING_APPROVAL', 'REJECTED'),
  ('recon_rule_set', 'PENDING_APPROVAL', 'WITHDRAWN'),
  ('recon_rule_set', 'ACTIVE', 'RETIRED');
CREATE TRIGGER trg_recon_rule_sets_guard_status BEFORE INSERT OR UPDATE OF status ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_rule_set');
CREATE TRIGGER trg_recon_rule_sets_guard BEFORE UPDATE ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'decided_by', 'decided_at', 'decision_note', 'effective_from',
    'retired_by', 'retired_at', 'version', 'updated_at');
CREATE TRIGGER trg_recon_rule_sets_freeze BEFORE UPDATE ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('REJECTED', 'WITHDRAWN', 'RETIRED');
CREATE TRIGGER trg_recon_rule_sets_updated_at BEFORE UPDATE ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_rule_sets_no_delete BEFORE DELETE ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- 3. imports: one per statement file; content hash unique across sources ------------------------------------------
CREATE TABLE recon.imports (
  id                    uuid        NOT NULL,
  source_id             uuid        NOT NULL,
  origin                text        NOT NULL,
  content_sha256        bytea       NOT NULL,
  size_bytes            bigint      NOT NULL,
  media_type            text        NOT NULL,
  original_filename     text,                     -- display only; never an identity (ADR-049 §2)
  stored_object_id      uuid        NOT NULL,
  evidence_record_id    uuid        NOT NULL,
  statement_ref         text,
  period_start          timestamptz,
  period_end            timestamptz,
  currency              char(3),
  opening_balance_minor bigint,
  closing_balance_minor bigint,
  account_ref_hint      text,                     -- bank statements: last four characters only
  account_ref_hmac      bytea,                    -- bank statements: keyed hash, never the account number
  declared_lines        integer,
  lines_parsed          integer     NOT NULL DEFAULT 0,
  lines_rejected        integer     NOT NULL DEFAULT 0,
  lines_duplicate       integer     NOT NULL DEFAULT 0,
  lines_conflicting     integer     NOT NULL DEFAULT 0,
  batches_parsed        integer     NOT NULL DEFAULT 0,
  status                text        NOT NULL,
  failure_code          text,
  validation_report     jsonb       NOT NULL DEFAULT '[]'::jsonb,
  supersedes_import_id  uuid,
  environment           text        NOT NULL DEFAULT 'TEST',
  uploaded_by           uuid,
  received_at           timestamptz NOT NULL,
  parsed_at             timestamptz,
  version               integer     NOT NULL DEFAULT 1,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_imports PRIMARY KEY (id),
  CONSTRAINT uq_recon_imports_content_sha256 UNIQUE (content_sha256),
  CONSTRAINT uq_recon_imports_stored_object UNIQUE (stored_object_id),
  CONSTRAINT fk_recon_imports_source FOREIGN KEY (source_id) REFERENCES recon.sources (id),
  CONSTRAINT fk_recon_imports_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_recon_imports_supersedes FOREIGN KEY (supersedes_import_id) REFERENCES recon.imports (id),
  CONSTRAINT fk_recon_imports_uploader FOREIGN KEY (uploaded_by) REFERENCES app.users (id),
  CONSTRAINT fk_recon_imports_object FOREIGN KEY (stored_object_id) REFERENCES app.stored_objects (id),
  CONSTRAINT fk_recon_imports_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id),
  CONSTRAINT ck_recon_imports_origin CHECK (origin IN ('STAFF_UPLOAD', 'SANDBOX_GENERATOR')),
  CONSTRAINT ck_recon_imports_uploader CHECK ((origin = 'STAFF_UPLOAD') = (uploaded_by IS NOT NULL)),
  CONSTRAINT ck_recon_imports_hash CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_recon_imports_size CHECK (size_bytes > 0),
  CONSTRAINT ck_recon_imports_media CHECK (media_type IN ('text/csv', 'application/json')),
  CONSTRAINT ck_recon_imports_filename CHECK (original_filename IS NULL OR length(original_filename) BETWEEN 1 AND 200),
  CONSTRAINT ck_recon_imports_statement_ref CHECK (statement_ref IS NULL OR statement_ref ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,99}$'),
  CONSTRAINT ck_recon_imports_period CHECK (period_end IS NULL OR period_start IS NULL OR period_end > period_start),
  CONSTRAINT ck_recon_imports_balances CHECK ((opening_balance_minor IS NULL AND closing_balance_minor IS NULL) OR currency IS NOT NULL),
  CONSTRAINT ck_recon_imports_account CHECK ((account_ref_hint IS NULL) = (account_ref_hmac IS NULL)
    AND (account_ref_hint IS NULL OR length(account_ref_hint) <= 4) AND (account_ref_hmac IS NULL OR octet_length(account_ref_hmac) = 32)),
  CONSTRAINT ck_recon_imports_counts CHECK (lines_parsed >= 0 AND lines_rejected >= 0 AND lines_duplicate >= 0 AND lines_conflicting >= 0
    AND batches_parsed >= 0 AND (declared_lines IS NULL OR declared_lines >= 0)),
  CONSTRAINT ck_recon_imports_status CHECK (status IN ('RECEIVED', 'PARSING', 'PARSED', 'FAILED', 'SUPERSEDED')),
  CONSTRAINT ck_recon_imports_parsed CHECK (status NOT IN ('PARSED', 'SUPERSEDED')
    OR (parsed_at IS NOT NULL AND period_start IS NOT NULL AND period_end IS NOT NULL
        AND (declared_lines IS NULL OR lines_parsed + lines_rejected + lines_duplicate + lines_conflicting = declared_lines))),
  CONSTRAINT ck_recon_imports_failed CHECK ((status = 'FAILED') = (failure_code IS NOT NULL)),
  CONSTRAINT ck_recon_imports_report CHECK (jsonb_typeof(validation_report) = 'array' AND jsonb_array_length(validation_report) <= 200),
  CONSTRAINT ck_recon_imports_test_only_stage17 CHECK (environment = 'TEST')
);
-- a statement is imported once per source unless a corrected re-issue explicitly supersedes it
CREATE UNIQUE INDEX uq_recon_imports_statement ON recon.imports (source_id, statement_ref)
  WHERE statement_ref IS NOT NULL AND supersedes_import_id IS NULL AND status <> 'FAILED';
CREATE UNIQUE INDEX uq_recon_imports_superseded_once ON recon.imports (supersedes_import_id)
  WHERE supersedes_import_id IS NOT NULL AND status <> 'FAILED';
CREATE INDEX ix_recon_imports_source_period ON recon.imports (source_id, period_start);
CREATE INDEX ix_recon_imports_pending ON recon.imports (received_at) WHERE status IN ('RECEIVED', 'PARSING');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_import', '', 'RECEIVED'),
  ('recon_import', 'RECEIVED', 'PARSING'),
  ('recon_import', 'RECEIVED', 'FAILED'),
  ('recon_import', 'PARSING', 'PARSED'),
  ('recon_import', 'PARSING', 'FAILED'),
  ('recon_import', 'PARSED', 'SUPERSEDED');
CREATE TRIGGER trg_recon_imports_guard_status BEFORE INSERT OR UPDATE OF status ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_import');
CREATE TRIGGER trg_recon_imports_guard BEFORE UPDATE ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'statement_ref', 'period_start', 'period_end', 'currency',
    'opening_balance_minor', 'closing_balance_minor', 'account_ref_hint', 'account_ref_hmac', 'declared_lines', 'lines_parsed',
    'lines_rejected', 'lines_duplicate', 'lines_conflicting', 'batches_parsed', 'failure_code', 'validation_report', 'parsed_at',
    'version', 'updated_at');
CREATE TRIGGER trg_recon_imports_freeze BEFORE UPDATE ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('FAILED', 'SUPERSEDED');
-- statement metadata is written once, while PARSING (the parser sets it from the file); afterwards only the status moves
CREATE FUNCTION recon.imports_metadata_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'PARSING' AND (to_jsonb(NEW) - ARRAY['status','failure_code','version','updated_at'])
       IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['status','failure_code','version','updated_at']) THEN
    RAISE EXCEPTION 'recon.imports %: statement metadata is written only while PARSING', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recon_imports_metadata_once BEFORE UPDATE ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION recon.imports_metadata_once();
CREATE TRIGGER trg_recon_imports_updated_at BEFORE UPDATE ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_imports_no_delete BEFORE DELETE ON recon.imports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- 4. settlement batches (headers of a provider settlement report) --------------------------------------------------
CREATE TABLE recon.settlement_batches (
  id                  uuid        NOT NULL,
  import_id           uuid        NOT NULL,
  source_id           uuid        NOT NULL,
  provider_code       text        NOT NULL,
  currency            char(3)     NOT NULL,
  batch_ref           text        NOT NULL,
  settlement_date     date        NOT NULL,
  gross_credits_minor bigint      NOT NULL,   -- Σ collections and credit adjustments
  gross_debits_minor  bigint      NOT NULL,   -- Σ refunds, chargebacks, reversals and debit adjustments
  fees_minor          bigint      NOT NULL,   -- Σ provider fees deducted
  net_minor           bigint      NOT NULL,   -- signed: credits − debits − fees (validated at import, CHECK here)
  declared_lines      integer,
  status              text        NOT NULL,
  expected_net_minor  bigint,                 -- Σ matched lines' nets at the last run
  last_run_id         uuid,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_settlement_batches PRIMARY KEY (id),
  CONSTRAINT uq_recon_settlement_batches_ref UNIQUE (import_id, batch_ref),
  CONSTRAINT uq_recon_settlement_batches_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_recon_settlement_batches_import FOREIGN KEY (import_id) REFERENCES recon.imports (id),
  CONSTRAINT fk_recon_settlement_batches_source FOREIGN KEY (source_id) REFERENCES recon.sources (id),
  CONSTRAINT fk_recon_settlement_batches_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT ck_recon_settlement_batches_ref CHECK (batch_ref ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,99}$'),
  CONSTRAINT ck_recon_settlement_batches_amounts CHECK (gross_credits_minor >= 0 AND gross_debits_minor >= 0 AND fees_minor >= 0),
  CONSTRAINT ck_recon_settlement_batches_net CHECK (net_minor = gross_credits_minor - gross_debits_minor - fees_minor),
  CONSTRAINT ck_recon_settlement_batches_count CHECK (declared_lines IS NULL OR declared_lines >= 0),
  CONSTRAINT ck_recon_settlement_batches_status CHECK (status IN ('RECEIVED', 'MATCHED', 'DISCREPANCY', 'SUPERSEDED'))
);
CREATE INDEX ix_recon_settlement_batches_scope ON recon.settlement_batches (provider_code, currency, settlement_date);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_settlement_batch', '', 'RECEIVED'),
  ('recon_settlement_batch', 'RECEIVED', 'MATCHED'),
  ('recon_settlement_batch', 'RECEIVED', 'DISCREPANCY'),
  ('recon_settlement_batch', 'DISCREPANCY', 'MATCHED'),
  ('recon_settlement_batch', 'MATCHED', 'DISCREPANCY'),
  ('recon_settlement_batch', 'RECEIVED', 'SUPERSEDED'),
  ('recon_settlement_batch', 'MATCHED', 'SUPERSEDED'),
  ('recon_settlement_batch', 'DISCREPANCY', 'SUPERSEDED');
CREATE TRIGGER trg_recon_settlement_batches_guard_status BEFORE INSERT OR UPDATE OF status ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_settlement_batch');
CREATE TRIGGER trg_recon_settlement_batches_guard BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'expected_net_minor', 'last_run_id', 'version', 'updated_at');
CREATE TRIGGER trg_recon_settlement_batches_freeze BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('SUPERSEDED');
CREATE TRIGGER trg_recon_settlement_batches_updated_at BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_settlement_batches_no_delete BEFORE DELETE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- 5. items: normalised statement lines; immutable except match_status --------------------------------------------
CREATE TABLE recon.items (
  id                  uuid        NOT NULL,
  import_id           uuid        NOT NULL,
  source_id           uuid        NOT NULL,
  external_line_id    text        NOT NULL,      -- the provider's/bank's line id (identity with source and revision)
  revision            integer     NOT NULL DEFAULT 1,
  line_number         integer     NOT NULL,
  item_kind           text        NOT NULL,
  direction           text        NOT NULL,      -- CREDIT: into the covered account/pool; DEBIT: out of it
  line_status         text        NOT NULL,
  amount_minor        bigint      NOT NULL,      -- gross, > 0
  fee_minor           bigint,                    -- provider fee on the line, when reported
  net_minor           bigint,                    -- reported net, when reported
  currency            char(3)     NOT NULL,      -- normalised ISO 4217
  raw_currency_code   text        NOT NULL,      -- verbatim (CURRENCY_MISMATCH evidence)
  provider_ref        text,
  merchant_ref        text,                      -- our reference as sent to the provider (payment/payout id)
  batch_ref           text,
  settlement_batch_id uuid,
  counterparty_ref    text,                      -- bank lines: the transfer reference as printed
  value_date          date,
  booked_at           timestamptz NOT NULL,
  raw_line_sha256     bytea       NOT NULL,
  raw_line_locator    text        NOT NULL,      -- 'row:12' / 'json:$.lines[11]' inside the stored file
  match_status        text        NOT NULL DEFAULT 'UNMATCHED',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_items PRIMARY KEY (id),
  CONSTRAINT uq_recon_items_source_line UNIQUE (source_id, external_line_id, revision),
  CONSTRAINT uq_recon_items_import_line UNIQUE (import_id, line_number),
  CONSTRAINT uq_recon_items_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_recon_items_import FOREIGN KEY (import_id) REFERENCES recon.imports (id),
  CONSTRAINT fk_recon_items_source FOREIGN KEY (source_id) REFERENCES recon.sources (id),
  CONSTRAINT fk_recon_items_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_recon_items_batch FOREIGN KEY (settlement_batch_id, currency) REFERENCES recon.settlement_batches (id, currency),
  CONSTRAINT ck_recon_items_line_id CHECK (external_line_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,99}$'),
  CONSTRAINT ck_recon_items_revision CHECK (revision >= 1),
  CONSTRAINT ck_recon_items_line CHECK (line_number >= 1),
  CONSTRAINT ck_recon_items_kind CHECK (item_kind IN ('PAYMENT', 'REFUND', 'CHARGEBACK', 'REVERSAL', 'PAYOUT', 'PAYOUT_REVERSAL',
                                                      'FEE', 'FEE_REMITTANCE', 'ADJUSTMENT', 'BANK_TRANSFER')),
  CONSTRAINT ck_recon_items_direction CHECK (direction IN ('CREDIT', 'DEBIT')),
  CONSTRAINT ck_recon_items_status CHECK (line_status IN ('SUCCEEDED', 'FAILED', 'PENDING', 'COMPLETED', 'RETURNED', 'BOOKED')),
  CONSTRAINT ck_recon_items_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_recon_items_fee CHECK (fee_minor IS NULL OR fee_minor >= 0),
  CONSTRAINT ck_recon_items_net CHECK (net_minor IS NULL OR fee_minor IS NULL OR net_minor = amount_minor - fee_minor),
  CONSTRAINT ck_recon_items_refs CHECK ((provider_ref IS NULL OR length(provider_ref) BETWEEN 1 AND 128)
    AND (merchant_ref IS NULL OR length(merchant_ref) BETWEEN 1 AND 128) AND (batch_ref IS NULL OR length(batch_ref) BETWEEN 1 AND 100)
    AND (counterparty_ref IS NULL OR length(counterparty_ref) BETWEEN 1 AND 140)),
  CONSTRAINT ck_recon_items_hash CHECK (octet_length(raw_line_sha256) = 32),
  CONSTRAINT ck_recon_items_match_status CHECK (match_status IN ('UNMATCHED', 'MATCHED', 'EXCEPTION', 'CONFLICT', 'SUPERSEDED', 'IGNORED'))
);
CREATE INDEX ix_recon_items_provider_ref ON recon.items (provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX ix_recon_items_merchant_ref ON recon.items (merchant_ref) WHERE merchant_ref IS NOT NULL;
CREATE INDEX ix_recon_items_open ON recon.items (source_id, currency, booked_at) WHERE match_status IN ('UNMATCHED', 'EXCEPTION');
CREATE INDEX ix_recon_items_import ON recon.items (import_id);
CREATE INDEX ix_recon_items_batch ON recon.items (settlement_batch_id) WHERE settlement_batch_id IS NOT NULL;

-- lines are added only while their import is PARSING
CREATE FUNCTION recon.items_import_open() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM recon.imports WHERE id = NEW.import_id;
  IF s IS DISTINCT FROM 'PARSING' THEN
    RAISE EXCEPTION 'recon import % is % (lines can only be added while PARSING)', NEW.import_id, s USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recon_items_import_open BEFORE INSERT ON recon.items
  FOR EACH ROW EXECUTE FUNCTION recon.items_import_open();
CREATE TRIGGER trg_recon_items_guard BEFORE UPDATE ON recon.items
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('match_status', 'updated_at');
CREATE TRIGGER trg_recon_items_updated_at BEFORE UPDATE ON recon.items
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_items_no_delete BEFORE DELETE ON recon.items
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_recon_items_no_truncate BEFORE TRUNCATE ON recon.items
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- a batch can be MATCHED only when its lines add up to its header and every line is matched (or explained: IGNORED)
CREATE FUNCTION recon.settlement_batch_totals() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cr bigint; dr bigint; fe bigint; n integer; open_n integer;
BEGIN
  IF NEW.status = 'MATCHED' AND NEW.status IS DISTINCT FROM OLD.status THEN
    SELECT coalesce(sum(amount_minor) FILTER (WHERE direction = 'CREDIT'), 0), coalesce(sum(amount_minor) FILTER (WHERE direction = 'DEBIT'), 0),
           coalesce(sum(coalesce(fee_minor, 0)), 0), count(*), count(*) FILTER (WHERE match_status NOT IN ('MATCHED', 'IGNORED'))
      INTO cr, dr, fe, n, open_n
      FROM recon.items WHERE settlement_batch_id = NEW.id AND item_kind NOT IN ('FEE_REMITTANCE');
    IF cr <> NEW.gross_credits_minor OR dr <> NEW.gross_debits_minor OR fe <> NEW.fees_minor
       OR (NEW.declared_lines IS NOT NULL AND n <> NEW.declared_lines) OR open_n > 0 THEN
      RAISE EXCEPTION 'settlement batch % cannot be MATCHED (credits %/%, debits %/%, fees %/%, lines %/%, unmatched %)', NEW.batch_ref,
        cr, NEW.gross_credits_minor, dr, NEW.gross_debits_minor, fe, NEW.fees_minor, n, NEW.declared_lines, open_n USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recon_settlement_batches_totals BEFORE UPDATE OF status ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.settlement_batch_totals();

-- ---- 6. runs: one matching pass for (source, currency, window) --------------------------------------------------------
CREATE TABLE recon.runs (
  id                   uuid        NOT NULL,
  source_id            uuid        NOT NULL,
  currency             char(3)     NOT NULL,
  window_start         timestamptz NOT NULL,
  window_end           timestamptz NOT NULL,
  run_kind             text        NOT NULL,
  status               text        NOT NULL,
  rule_set_id          uuid,
  rule_set_version     integer,
  matcher_version      text        NOT NULL,
  replay_of_run_id     uuid,
  reason               text,
  triggered_by_type    text        NOT NULL,
  triggered_by         uuid,
  items_considered     integer     NOT NULL DEFAULT 0,
  items_matched        integer     NOT NULL DEFAULT 0,
  exceptions_opened    integer     NOT NULL DEFAULT 0,
  exceptions_seen      integer     NOT NULL DEFAULT 0,
  exceptions_closed    integer     NOT NULL DEFAULT 0,
  decisions_sha256     bytea,
  failure_code         text,
  heartbeat_at         timestamptz,
  queued_at            timestamptz NOT NULL,
  started_at           timestamptz,
  finished_at          timestamptz,
  version              integer     NOT NULL DEFAULT 1,
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_runs PRIMARY KEY (id),
  CONSTRAINT fk_recon_runs_source FOREIGN KEY (source_id) REFERENCES recon.sources (id),
  CONSTRAINT fk_recon_runs_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_recon_runs_rule_set FOREIGN KEY (rule_set_id) REFERENCES recon.matching_rule_sets (id),
  CONSTRAINT fk_recon_runs_replay FOREIGN KEY (replay_of_run_id) REFERENCES recon.runs (id),
  CONSTRAINT fk_recon_runs_staff FOREIGN KEY (triggered_by) REFERENCES app.users (id),
  CONSTRAINT ck_recon_runs_window CHECK (window_end > window_start),
  CONSTRAINT ck_recon_runs_kind CHECK (run_kind IN ('SCHEDULED', 'ON_DEMAND', 'REPLAY')),
  CONSTRAINT ck_recon_runs_status CHECK (status IN ('QUEUED', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')),
  CONSTRAINT ck_recon_runs_by CHECK ((triggered_by_type = 'SYSTEM' AND triggered_by IS NULL) OR (triggered_by_type = 'STAFF' AND triggered_by IS NOT NULL)),
  CONSTRAINT ck_recon_runs_staff_reason CHECK (triggered_by_type <> 'STAFF' OR length(btrim(coalesce(reason, ''))) >= 5),
  CONSTRAINT ck_recon_runs_replay CHECK ((run_kind = 'REPLAY') = (replay_of_run_id IS NOT NULL)),
  CONSTRAINT ck_recon_runs_counts CHECK (items_considered >= 0 AND items_matched BETWEEN 0 AND items_considered AND exceptions_opened >= 0
    AND exceptions_seen >= 0 AND exceptions_closed >= 0),
  CONSTRAINT ck_recon_runs_rules CHECK (status IN ('QUEUED', 'CANCELLED', 'FAILED') OR (rule_set_id IS NOT NULL AND rule_set_version IS NOT NULL)),
  CONSTRAINT ck_recon_runs_started CHECK (status IN ('QUEUED', 'CANCELLED') OR started_at IS NOT NULL OR status = 'FAILED'),
  CONSTRAINT ck_recon_runs_completed CHECK (status <> 'COMPLETED' OR (finished_at IS NOT NULL AND octet_length(decisions_sha256) = 32)),
  CONSTRAINT ck_recon_runs_failed CHECK ((status = 'FAILED') = (failure_code IS NOT NULL) AND (status <> 'FAILED' OR finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX uq_recon_runs_one_active ON recon.runs (source_id, currency) WHERE status IN ('QUEUED', 'RUNNING');
CREATE INDEX ix_recon_runs_recent ON recon.runs (queued_at DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_run', '', 'QUEUED'),
  ('recon_run', 'QUEUED', 'RUNNING'),
  ('recon_run', 'QUEUED', 'CANCELLED'),
  ('recon_run', 'QUEUED', 'FAILED'),
  ('recon_run', 'RUNNING', 'COMPLETED'),
  ('recon_run', 'RUNNING', 'FAILED');
CREATE TRIGGER trg_recon_runs_guard_status BEFORE INSERT OR UPDATE OF status ON recon.runs
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_run');
CREATE TRIGGER trg_recon_runs_freeze BEFORE UPDATE ON recon.runs
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('COMPLETED', 'FAILED', 'CANCELLED');
CREATE TRIGGER trg_recon_runs_guard BEFORE UPDATE ON recon.runs
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'rule_set_id', 'rule_set_version', 'items_considered', 'items_matched',
    'exceptions_opened', 'exceptions_seen', 'exceptions_closed', 'decisions_sha256', 'failure_code', 'heartbeat_at', 'started_at',
    'finished_at', 'version', 'updated_at');
CREATE TRIGGER trg_recon_runs_updated_at BEFORE UPDATE ON recon.runs
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_runs_no_delete BEFORE DELETE ON recon.runs
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- 7. matches: line ↔ FundZim subject ↔ ledger journal --------------------------------------------------------------
CREATE TABLE recon.matches (
  id                    uuid        NOT NULL,
  run_id                uuid,                    -- NULL for a MANUAL match (created by an approved resolution)
  item_id               uuid        NOT NULL,
  currency              char(3)     NOT NULL,
  subject_type          text        NOT NULL,
  subject_id            uuid        NOT NULL,
  confidence            text        NOT NULL,
  rule_set_id           uuid,
  rule_set_version      integer,
  matcher_version       text        NOT NULL,
  rule_code             text        NOT NULL,
  matched_amount_minor  bigint      NOT NULL,
  ledger_status         text        NOT NULL,
  ledger_transaction_id uuid,
  fee_status            text        NOT NULL,
  explanation           jsonb       NOT NULL,
  resolution_id         uuid,
  status                text        NOT NULL DEFAULT 'ACTIVE',
  revoked_at            timestamptz,
  revoke_reason         text,
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_matches PRIMARY KEY (id),
  CONSTRAINT fk_recon_matches_run FOREIGN KEY (run_id) REFERENCES recon.runs (id),
  CONSTRAINT fk_recon_matches_item FOREIGN KEY (item_id, currency) REFERENCES recon.items (id, currency),
  CONSTRAINT fk_recon_matches_rule_set FOREIGN KEY (rule_set_id) REFERENCES recon.matching_rule_sets (id),
  CONSTRAINT fk_recon_matches_ledger FOREIGN KEY (ledger_transaction_id, currency) REFERENCES ledger.ledger_transactions (id, currency),
  CONSTRAINT ck_recon_matches_subject CHECK (subject_type IN ('PAYMENT', 'PAYOUT', 'FEE_REMITTANCE_LINE')),
  CONSTRAINT ck_recon_matches_confidence CHECK (confidence IN ('EXACT_MATCH', 'RULE_BASED_MATCH', 'MANUAL')),
  CONSTRAINT ck_recon_matches_auto CHECK ((confidence = 'MANUAL') = (resolution_id IS NOT NULL)
    AND (confidence = 'MANUAL' OR (run_id IS NOT NULL AND rule_set_id IS NOT NULL AND rule_set_version IS NOT NULL))),
  CONSTRAINT ck_recon_matches_amount CHECK (matched_amount_minor > 0),
  CONSTRAINT ck_recon_matches_ledger CHECK (ledger_status IN ('POSTED', 'NOT_POSTED_TEST_PAYMENT', 'MISSING', 'NOT_APPLICABLE')
    AND ((ledger_status = 'POSTED') = (ledger_transaction_id IS NOT NULL))),
  CONSTRAINT ck_recon_matches_fee CHECK (fee_status IN ('MATCHED', 'MISMATCH', 'NOT_REPORTED', 'NO_EXPECTATION', 'NOT_APPLICABLE')),
  CONSTRAINT ck_recon_matches_explanation CHECK (jsonb_typeof(explanation) = 'object'),
  CONSTRAINT ck_recon_matches_status CHECK (status IN ('ACTIVE', 'REVOKED')),
  CONSTRAINT ck_recon_matches_revoked CHECK ((status = 'REVOKED') = (revoked_at IS NOT NULL AND revoke_reason IS NOT NULL))
);
-- one active match per line, and per subject per kind of evidence (a second line for the same payment is a duplicate)
CREATE UNIQUE INDEX uq_recon_matches_item_active ON recon.matches (item_id) WHERE status = 'ACTIVE';
CREATE INDEX ix_recon_matches_subject ON recon.matches (subject_type, subject_id) WHERE status = 'ACTIVE';
CREATE INDEX ix_recon_matches_run ON recon.matches (run_id);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_match', '', 'ACTIVE'),
  ('recon_match', 'ACTIVE', 'REVOKED');
CREATE TRIGGER trg_recon_matches_guard_status BEFORE INSERT OR UPDATE OF status ON recon.matches
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_match');
CREATE TRIGGER trg_recon_matches_freeze BEFORE UPDATE ON recon.matches
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('REVOKED');
CREATE TRIGGER trg_recon_matches_guard BEFORE UPDATE ON recon.matches
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'revoked_at', 'revoke_reason');
CREATE TRIGGER trg_recon_matches_no_delete BEFORE DELETE ON recon.matches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_recon_matches_no_truncate BEFORE TRUNCATE ON recon.matches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- ---- 8. exceptions (discrepancies) and their append-only history ------------------------------------------------------
CREATE TABLE recon.exceptions (
  id                    uuid        NOT NULL,
  dedupe_key            text        NOT NULL,
  generation            integer     NOT NULL DEFAULT 1,
  exception_type        text        NOT NULL,
  severity              text        NOT NULL,
  status                text        NOT NULL,
  source_id             uuid        NOT NULL,
  currency              char(3)     NOT NULL,
  first_run_id          uuid,
  last_run_id           uuid,
  occurrences           integer     NOT NULL DEFAULT 1,
  item_id               uuid,
  subject_type          text,
  subject_id            uuid,
  settlement_batch_id   uuid,
  ledger_transaction_id uuid,
  expected_amount_minor bigint,
  actual_amount_minor   bigint,
  actual_currency_raw   text,
  detail                jsonb       NOT NULL,
  detected_at           timestamptz NOT NULL,
  last_seen_at          timestamptz NOT NULL,
  assigned_to           uuid,
  assigned_by           uuid,
  assigned_at           timestamptz,
  due_at                timestamptz,
  escalated_at          timestamptz,
  resolved_at           timestamptz,
  closed_at             timestamptz,
  close_reason          text,
  reopen_count          integer     NOT NULL DEFAULT 0,
  version               integer     NOT NULL DEFAULT 1,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_exceptions PRIMARY KEY (id),
  CONSTRAINT fk_recon_exceptions_source FOREIGN KEY (source_id) REFERENCES recon.sources (id),
  CONSTRAINT fk_recon_exceptions_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_recon_exceptions_first_run FOREIGN KEY (first_run_id) REFERENCES recon.runs (id),
  CONSTRAINT fk_recon_exceptions_last_run FOREIGN KEY (last_run_id) REFERENCES recon.runs (id),
  CONSTRAINT fk_recon_exceptions_item FOREIGN KEY (item_id) REFERENCES recon.items (id),
  CONSTRAINT fk_recon_exceptions_batch FOREIGN KEY (settlement_batch_id) REFERENCES recon.settlement_batches (id),
  CONSTRAINT fk_recon_exceptions_ledger FOREIGN KEY (ledger_transaction_id) REFERENCES ledger.ledger_transactions (id),
  CONSTRAINT fk_recon_exceptions_assignee FOREIGN KEY (assigned_to) REFERENCES app.users (id),
  CONSTRAINT fk_recon_exceptions_assigner FOREIGN KEY (assigned_by) REFERENCES app.users (id),
  CONSTRAINT ck_recon_exceptions_type CHECK (exception_type IN ('MISSING_PAYMENT', 'MISSING_SETTLEMENT', 'MISSING_PAYOUT',
    'MISSING_LEDGER_ENTRY', 'MISSING_BANK_CREDIT', 'AMOUNT_MISMATCH', 'CURRENCY_MISMATCH', 'FEE_MISMATCH', 'PAYOUT_MISMATCH',
    'STATUS_MISMATCH', 'DUPLICATE_TRANSACTION', 'AMBIGUOUS_MATCH', 'UNKNOWN_PAYMENT_OUTCOME', 'UNKNOWN_PAYOUT_OUTCOME',
    'UNMATCHED_BANK_ENTRY', 'POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT', 'SETTLEMENT_SHORT', 'SETTLEMENT_OVER', 'CONFLICTING_EVIDENCE',
    'PROVIDER_ADJUSTMENT', 'STALE_RECONCILIATION')),
  CONSTRAINT ck_recon_exceptions_severity CHECK (severity IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
  CONSTRAINT ck_recon_exceptions_status CHECK (status IN ('OPEN', 'ASSIGNED', 'INVESTIGATING', 'AWAITING_EVIDENCE', 'PENDING_APPROVAL',
    'RESOLVED', 'REOPENED', 'CLOSED')),
  CONSTRAINT ck_recon_exceptions_dedupe CHECK (length(dedupe_key) BETWEEN 5 AND 300),
  CONSTRAINT ck_recon_exceptions_subject CHECK ((subject_type IS NULL) = (subject_id IS NULL)
    AND (subject_type IS NULL OR subject_type IN ('PAYMENT', 'PAYOUT', 'FEE_REMITTANCE_LINE'))),
  CONSTRAINT ck_recon_exceptions_amounts CHECK ((expected_amount_minor IS NULL OR expected_amount_minor >= 0)
    AND (actual_amount_minor IS NULL OR actual_amount_minor >= 0)),
  CONSTRAINT ck_recon_exceptions_detail CHECK (jsonb_typeof(detail) = 'object'),
  CONSTRAINT ck_recon_exceptions_occurrences CHECK (occurrences >= 1 AND generation >= 1 AND reopen_count >= 0),
  CONSTRAINT ck_recon_exceptions_assigned CHECK (status NOT IN ('ASSIGNED', 'INVESTIGATING', 'AWAITING_EVIDENCE', 'PENDING_APPROVAL')
    OR assigned_to IS NOT NULL),
  CONSTRAINT ck_recon_exceptions_resolved CHECK (status NOT IN ('RESOLVED') OR resolved_at IS NOT NULL),
  CONSTRAINT ck_recon_exceptions_closed CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL AND close_reason IS NOT NULL)),
  CONSTRAINT ck_recon_exceptions_close_reason CHECK (close_reason IS NULL OR close_reason IN ('RESOLVED', 'CONDITION_CLEARED')),
  -- the evidence each type must carry
  CONSTRAINT ck_recon_exceptions_shape CHECK (CASE exception_type
    WHEN 'MISSING_SETTLEMENT'     THEN subject_id IS NOT NULL AND item_id IS NULL
    WHEN 'MISSING_PAYMENT'        THEN item_id IS NOT NULL AND subject_id IS NULL
    WHEN 'MISSING_PAYOUT'         THEN item_id IS NOT NULL AND subject_id IS NULL
    WHEN 'UNMATCHED_BANK_ENTRY'   THEN item_id IS NOT NULL
    WHEN 'POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT' THEN item_id IS NOT NULL
    WHEN 'MISSING_BANK_CREDIT'    THEN item_id IS NOT NULL
    WHEN 'DUPLICATE_TRANSACTION'  THEN item_id IS NOT NULL
    WHEN 'AMBIGUOUS_MATCH'        THEN item_id IS NOT NULL
    WHEN 'CONFLICTING_EVIDENCE'   THEN item_id IS NOT NULL
    WHEN 'PROVIDER_ADJUSTMENT'    THEN item_id IS NOT NULL AND subject_id IS NULL
    WHEN 'CURRENCY_MISMATCH'      THEN item_id IS NOT NULL AND subject_id IS NOT NULL AND actual_currency_raw IS NOT NULL
    WHEN 'SETTLEMENT_SHORT'       THEN settlement_batch_id IS NOT NULL AND actual_amount_minor IS NOT NULL AND expected_amount_minor IS NOT NULL
    WHEN 'SETTLEMENT_OVER'        THEN settlement_batch_id IS NOT NULL AND actual_amount_minor IS NOT NULL AND expected_amount_minor IS NOT NULL
    WHEN 'STALE_RECONCILIATION'   THEN item_id IS NULL AND subject_id IS NULL
    WHEN 'AMOUNT_MISMATCH'        THEN item_id IS NOT NULL AND subject_id IS NOT NULL AND actual_amount_minor IS DISTINCT FROM expected_amount_minor
    WHEN 'FEE_MISMATCH'           THEN item_id IS NOT NULL AND subject_id IS NOT NULL AND actual_amount_minor IS DISTINCT FROM expected_amount_minor
    ELSE /* MISSING_LEDGER_ENTRY, PAYOUT_MISMATCH, STATUS_MISMATCH, UNKNOWN_*_OUTCOME */ subject_id IS NOT NULL
  END)
);
-- one live case per problem; a problem that comes back after its case was closed opens a new generation
CREATE UNIQUE INDEX uq_recon_exceptions_dedupe_live ON recon.exceptions (dedupe_key) WHERE status <> 'CLOSED';
CREATE UNIQUE INDEX uq_recon_exceptions_dedupe_generation ON recon.exceptions (dedupe_key, generation);
CREATE INDEX ix_recon_exceptions_queue ON recon.exceptions (severity, detected_at) WHERE status <> 'CLOSED';
CREATE INDEX ix_recon_exceptions_subject ON recon.exceptions (subject_type, subject_id) WHERE subject_id IS NOT NULL;
CREATE INDEX ix_recon_exceptions_assignee ON recon.exceptions (assigned_to) WHERE status <> 'CLOSED';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_exception', '', 'OPEN'),
  ('recon_exception', 'OPEN', 'ASSIGNED'),
  ('recon_exception', 'REOPENED', 'ASSIGNED'),
  ('recon_exception', 'ASSIGNED', 'INVESTIGATING'),
  ('recon_exception', 'INVESTIGATING', 'AWAITING_EVIDENCE'),
  ('recon_exception', 'AWAITING_EVIDENCE', 'INVESTIGATING'),
  ('recon_exception', 'INVESTIGATING', 'PENDING_APPROVAL'),
  ('recon_exception', 'PENDING_APPROVAL', 'INVESTIGATING'),   -- proposal rejected, withdrawn or expired
  ('recon_exception', 'PENDING_APPROVAL', 'RESOLVED'),
  ('recon_exception', 'RESOLVED', 'CLOSED'),
  ('recon_exception', 'RESOLVED', 'REOPENED'),                -- late or contradicting evidence
  ('recon_exception', 'OPEN', 'CLOSED'),                      -- SYSTEM only: a later run found the condition cleared
  ('recon_exception', 'ASSIGNED', 'CLOSED'),
  ('recon_exception', 'INVESTIGATING', 'CLOSED'),
  ('recon_exception', 'AWAITING_EVIDENCE', 'CLOSED'),
  ('recon_exception', 'REOPENED', 'CLOSED');
CREATE TRIGGER trg_recon_exceptions_guard_status BEFORE INSERT OR UPDATE OF status ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_exception');
CREATE TRIGGER trg_recon_exceptions_freeze BEFORE UPDATE ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('CLOSED');
CREATE TRIGGER trg_recon_exceptions_guard BEFORE UPDATE ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'severity', 'last_run_id', 'occurrences', 'last_seen_at',
    'assigned_to', 'assigned_by', 'assigned_at', 'due_at', 'escalated_at', 'resolved_at', 'closed_at', 'close_reason', 'reopen_count',
    'version', 'updated_at');
-- closing an unresolved case is only for a condition that cleared by itself (never while an approval is pending)
CREATE FUNCTION recon.exceptions_close_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'CLOSED' AND OLD.status <> 'CLOSED' THEN
    IF OLD.status = 'RESOLVED' AND NEW.close_reason <> 'RESOLVED' THEN
      RAISE EXCEPTION 'recon exception %: a resolved case closes as RESOLVED', OLD.id USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status <> 'RESOLVED' AND (NEW.close_reason <> 'CONDITION_CLEARED' OR OLD.exception_type NOT IN ('MISSING_SETTLEMENT',
        'MISSING_LEDGER_ENTRY', 'MISSING_BANK_CREDIT', 'STATUS_MISMATCH', 'UNKNOWN_PAYMENT_OUTCOME', 'UNKNOWN_PAYOUT_OUTCOME',
        'STALE_RECONCILIATION')) THEN
      RAISE EXCEPTION 'recon exception % (%) cannot close without a resolution', OLD.id, OLD.exception_type USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recon_exceptions_close_guard BEFORE UPDATE OF status ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION recon.exceptions_close_guard();
CREATE TRIGGER trg_recon_exceptions_updated_at BEFORE UPDATE ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_exceptions_no_delete BEFORE DELETE ON recon.exceptions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE recon.exception_events (
  id            uuid        NOT NULL,
  exception_id  uuid        NOT NULL,
  event_type    text        NOT NULL,
  from_status   text,
  to_status     text,
  actor_type    text        NOT NULL,
  actor_id      uuid,
  run_id        uuid,
  resolution_id uuid,
  note          text,
  evidence_ref  text,
  created_at    timestamptz NOT NULL,
  CONSTRAINT pk_recon_exception_events PRIMARY KEY (id),
  CONSTRAINT fk_recon_exception_events_exception FOREIGN KEY (exception_id) REFERENCES recon.exceptions (id),
  CONSTRAINT fk_recon_exception_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id),
  CONSTRAINT fk_recon_exception_events_run FOREIGN KEY (run_id) REFERENCES recon.runs (id),
  CONSTRAINT ck_recon_exception_events_type CHECK (event_type IN ('OPENED', 'SEEN_AGAIN', 'ASSIGNED', 'STATUS_CHANGED', 'COMMENT',
    'EVIDENCE_ADDED', 'DUE_DATE_SET', 'ESCALATED', 'RESOLUTION_PROPOSED', 'RESOLUTION_REJECTED', 'RESOLUTION_WITHDRAWN',
    'RESOLUTION_EXPIRED', 'RESOLUTION_APPROVED', 'RESOLUTION_EXECUTED', 'CLOSED', 'REOPENED')),
  CONSTRAINT ck_recon_exception_events_actor CHECK ((actor_type = 'SYSTEM' AND actor_id IS NULL) OR (actor_type = 'STAFF' AND actor_id IS NOT NULL)),
  CONSTRAINT ck_recon_exception_events_note CHECK (note IS NULL OR length(note) <= 2000),
  CONSTRAINT ck_recon_exception_events_evidence CHECK (evidence_ref IS NULL OR evidence_ref ~ '^[a-z_]+:[A-Za-z0-9_.:/-]{1,180}$')
);
CREATE INDEX ix_recon_exception_events_exception ON recon.exception_events (exception_id, created_at);
CREATE TRIGGER trg_recon_exception_events_no_mutation BEFORE UPDATE OR DELETE ON recon.exception_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_recon_exception_events_no_truncate BEFORE TRUNCATE ON recon.exception_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- ---- 9. resolutions: maker-checker; approving executes (ADR-049 §6) ---------------------------------------------------
CREATE TABLE recon.resolutions (
  id                   uuid        NOT NULL,
  exception_id         uuid        NOT NULL,
  resolution_type      text        NOT NULL,
  status               text        NOT NULL,
  proposal             jsonb       NOT NULL,
  proposal_sha256      bytea       NOT NULL,
  justification        text        NOT NULL,
  evidence_refs        text[]      NOT NULL DEFAULT '{}',
  proposed_by          uuid        NOT NULL,
  proposed_at          timestamptz NOT NULL,
  expires_at           timestamptz NOT NULL,
  decided_by           uuid,
  decided_at           timestamptz,
  decision_note        text,
  approved_sha256      bytea,
  ledger_adjustment_id uuid,
  executed_at          timestamptz,
  execution_result     jsonb,
  last_error           text,
  version              integer     NOT NULL DEFAULT 1,
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recon_resolutions PRIMARY KEY (id),
  CONSTRAINT fk_recon_resolutions_exception FOREIGN KEY (exception_id) REFERENCES recon.exceptions (id),
  CONSTRAINT fk_recon_resolutions_proposer FOREIGN KEY (proposed_by) REFERENCES app.users (id),
  CONSTRAINT fk_recon_resolutions_decider FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT fk_recon_resolutions_adjustment FOREIGN KEY (ledger_adjustment_id) REFERENCES ledger.ledger_adjustments (id),
  CONSTRAINT ck_recon_resolutions_type CHECK (resolution_type IN ('EXPLAINED_NO_ACTION', 'PROVIDER_CORRECTION_CONFIRMED', 'ESCALATED_TO_CASE',
    'MANUAL_MATCH', 'PROVIDER_OUTCOME_APPLIED', 'LEDGER_ADJUSTMENT')),
  CONSTRAINT ck_recon_resolutions_status CHECK (status IN ('PENDING_REVIEW', 'APPROVED', 'EXECUTED', 'REJECTED', 'CANCELLED', 'EXPIRED')),
  CONSTRAINT ck_recon_resolutions_proposal CHECK (jsonb_typeof(proposal) = 'object' AND octet_length(proposal_sha256) = 32),
  CONSTRAINT ck_recon_resolutions_justification CHECK (length(btrim(justification)) BETWEEN 20 AND 2000),
  CONSTRAINT ck_recon_resolutions_evidence CHECK (cardinality(evidence_refs) BETWEEN 1 AND 10),
  CONSTRAINT ck_recon_resolutions_expiry CHECK (expires_at > proposed_at AND expires_at <= proposed_at + interval '24 hours'),
  CONSTRAINT ck_recon_resolutions_maker_checker CHECK (decided_by IS NULL OR decided_by <> proposed_by),
  CONSTRAINT ck_recon_resolutions_decided CHECK (status IN ('PENDING_REVIEW', 'CANCELLED', 'EXPIRED')
    OR (decided_by IS NOT NULL AND decided_at IS NOT NULL AND length(btrim(coalesce(decision_note, ''))) >= 5)),
  CONSTRAINT ck_recon_resolutions_approved CHECK (status NOT IN ('APPROVED', 'EXECUTED')
    OR (approved_sha256 = proposal_sha256 AND decided_at <= expires_at)),
  CONSTRAINT ck_recon_resolutions_ledger CHECK ((resolution_type = 'LEDGER_ADJUSTMENT') = (ledger_adjustment_id IS NOT NULL)),
  CONSTRAINT ck_recon_resolutions_executed CHECK ((status = 'EXECUTED') = (executed_at IS NOT NULL AND execution_result IS NOT NULL))
);
-- at most one live resolution per exception
CREATE UNIQUE INDEX uq_recon_resolutions_live ON recon.resolutions (exception_id) WHERE status IN ('PENDING_REVIEW', 'APPROVED');
CREATE INDEX ix_recon_resolutions_pending ON recon.resolutions (expires_at) WHERE status IN ('PENDING_REVIEW', 'APPROVED');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recon_resolution', '', 'PENDING_REVIEW'),
  ('recon_resolution', 'PENDING_REVIEW', 'APPROVED'),
  ('recon_resolution', 'PENDING_REVIEW', 'REJECTED'),
  ('recon_resolution', 'PENDING_REVIEW', 'CANCELLED'),
  ('recon_resolution', 'PENDING_REVIEW', 'EXPIRED'),
  ('recon_resolution', 'APPROVED', 'EXECUTED');
CREATE TRIGGER trg_recon_resolutions_guard_status BEFORE INSERT OR UPDATE OF status ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recon_resolution');
CREATE TRIGGER trg_recon_resolutions_freeze BEFORE UPDATE ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('EXECUTED', 'REJECTED', 'CANCELLED', 'EXPIRED');
CREATE TRIGGER trg_recon_resolutions_guard BEFORE UPDATE ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'decided_by', 'decided_at', 'decision_note', 'approved_sha256',
    'executed_at', 'execution_result', 'last_error', 'version', 'updated_at');
-- the checker is never the proposer or one of the proposer's linked accounts (app.actor_identities, Stage 5)
CREATE FUNCTION recon.checker_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.decided_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.decided_by IS NULL)
     AND NEW.status IN ('APPROVED', 'REJECTED', 'ACTIVE')
     AND NEW.proposed_by = ANY (app.actor_identities(NEW.decided_by)) THEN
    RAISE EXCEPTION 'maker-checker: % cannot decide % %', NEW.decided_by, TG_TABLE_NAME, NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recon_resolutions_checker BEFORE INSERT OR UPDATE ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION recon.checker_guard();
CREATE TRIGGER trg_recon_rule_sets_checker BEFORE INSERT OR UPDATE ON recon.matching_rule_sets
  FOR EACH ROW EXECUTE FUNCTION recon.checker_guard();
CREATE TRIGGER trg_recon_resolutions_updated_at BEFORE UPDATE ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recon_resolutions_no_delete BEFORE DELETE ON recon.resolutions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

ALTER TABLE recon.matches ADD CONSTRAINT fk_recon_matches_resolution FOREIGN KEY (resolution_id) REFERENCES recon.resolutions (id);
ALTER TABLE recon.exception_events ADD CONSTRAINT fk_recon_exception_events_resolution FOREIGN KEY (resolution_id) REFERENCES recon.resolutions (id);

-- ---- 10. worker heartbeats ----------------------------------------------------------------------------------------------
CREATE TABLE recon.worker_heartbeats (
  job_kind             text        NOT NULL,
  last_started_at      timestamptz,
  last_succeeded_at    timestamptz,
  last_failed_at       timestamptz,
  last_error_code      text,
  consecutive_failures integer     NOT NULL DEFAULT 0,
  runs_total           bigint      NOT NULL DEFAULT 0,
  CONSTRAINT pk_recon_worker_heartbeats PRIMARY KEY (job_kind),
  CONSTRAINT ck_recon_worker_heartbeats_kind CHECK (job_kind ~ '^reconciliation\.[a-z_]+$'),
  CONSTRAINT ck_recon_worker_heartbeats_counts CHECK (consecutive_failures >= 0 AND runs_total >= 0),
  CONSTRAINT ck_recon_worker_heartbeats_error CHECK (last_error_code IS NULL OR last_error_code ~ '^[A-Z][A-Z0-9_]{1,63}$')
);
CREATE TRIGGER trg_recon_worker_heartbeats_no_delete BEFORE DELETE ON recon.worker_heartbeats
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- 11. storage: canonical statement files (CSV/JSON) for RECONCILIATION_REPORT only --------------------------------
ALTER TABLE app.stored_objects DROP CONSTRAINT ck_stored_objects_content_type;
ALTER TABLE app.stored_objects ADD CONSTRAINT ck_stored_objects_content_type CHECK (
  sniffed_content_type IS NULL
  OR (bucket_class = 'PUBLIC_MEDIA' AND sniffed_content_type IN ('image/jpeg', 'image/png'))
  OR (bucket_class IN ('PRIVATE_KYC', 'PRIVATE_EVIDENCE') AND sniffed_content_type IN ('image/jpeg', 'image/png', 'application/pdf'))
  OR (bucket_class = 'PRIVATE_EVIDENCE' AND purpose = 'RECONCILIATION_REPORT' AND sniffed_content_type IN ('text/csv', 'application/json')));

-- ---- 12. permissions (FINANCE; COMPLIANCE may read and export) -------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:reconciliation.view')::uuid, 'reconciliation.view', 'View reconciliation imports, runs, matches, exceptions, settlement batches and reports (TEST evidence)', true, false),
  (md5('permission:reconciliation.import')::uuid, 'reconciliation.import', 'Import a TEST statement (provider transaction, settlement or bank statement) as evidence', true, true),
  (md5('permission:reconciliation.investigate')::uuid, 'reconciliation.investigate', 'Assign, investigate and comment on reconciliation exceptions; propose resolutions (maker)', true, false),
  (md5('permission:reconciliation.rules.request')::uuid, 'reconciliation.rules.request', 'Propose a matching rule set (maker)', true, false),
  (md5('permission:reconciliation.rules.approve')::uuid, 'reconciliation.rules.approve', 'Approve, reject or retire a matching rule set (checker; never the proposer)', true, true),
  (md5('permission:reconciliation.report.export')::uuid, 'reconciliation.report.export', 'Export bounded reconciliation reports (audited)', true, true);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('FINANCE', 'reconciliation.view'), ('FINANCE', 'reconciliation.import'), ('FINANCE', 'reconciliation.investigate'),
             ('FINANCE', 'reconciliation.rules.request'), ('FINANCE', 'reconciliation.rules.approve'), ('FINANCE', 'reconciliation.report.export'),
             ('COMPLIANCE', 'reconciliation.view'), ('COMPLIANCE', 'reconciliation.report.export')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- ---- 13. notification templates (staff, in-app; Stage 15 engine) -------------------------------------------------------
ALTER TABLE app.notification_templates DROP CONSTRAINT ck_notification_templates_origin;
ALTER TABLE app.notification_templates ADD CONSTRAINT ck_notification_templates_origin CHECK (origin IN ('STAGE_4_NOTICE','STAGE_5_NOTICE',
  'STAGE_6_NOTICE','STAGE_15_BASELINE','STAGE_17_BASELINE','STAFF'));
INSERT INTO app.notification_templates (id, template_key, category, sensitive, allowed_variables, origin) VALUES
  (md5('notification_template:RECON_EXCEPTION_OPENED')::uuid, 'RECON_EXCEPTION_OPENED', 'ADMINISTRATIVE', false, ARRAY['exception_type','severity']::text[], 'STAGE_17_BASELINE'),
  (md5('notification_template:RECON_EXCEPTION_ASSIGNED')::uuid, 'RECON_EXCEPTION_ASSIGNED', 'ADMINISTRATIVE', false, ARRAY['exception_type','severity']::text[], 'STAGE_17_BASELINE'),
  (md5('notification_template:RECON_IMPORT_FAILED')::uuid, 'RECON_IMPORT_FAILED', 'ADMINISTRATIVE', false, ARRAY['failure_code']::text[], 'STAGE_17_BASELINE'),
  (md5('notification_template:RECON_RESOLUTION_AWAITING_APPROVAL')::uuid, 'RECON_RESOLUTION_AWAITING_APPROVAL', 'ADMINISTRATIVE', false, ARRAY['resolution_type']::text[], 'STAGE_17_BASELINE'),
  (md5('notification_template:RECON_UNKNOWN_OUTCOME_ESCALATED')::uuid, 'RECON_UNKNOWN_OUTCOME_ESCALATED', 'ADMINISTRATIVE', false, ARRAY['exception_type']::text[], 'STAGE_17_BASELINE')
ON CONFLICT (id) DO NOTHING;
INSERT INTO app.notification_template_versions (id, template_id, email_subject, email_text, sms_text, inapp_title, inapp_body, content_hash)
SELECT v.id, v.template_id, v.s, v.b, NULL, v.it, v.ib,
  sha256(convert_to(coalesce(v.s,'') || chr(31) || coalesce(v.b,'') || chr(31) || '' || chr(31) || coalesce(v.it,'') || chr(31) || coalesce(v.ib,''), 'UTF8'))
FROM (VALUES
  (md5('notification_template_version:RECON_EXCEPTION_OPENED:1')::uuid, md5('notification_template:RECON_EXCEPTION_OPENED')::uuid,
   'A reconciliation exception needs attention ({{severity}})', 'A TEST reconciliation exception of type {{exception_type}} was opened (severity {{severity}}).',
   'A reconciliation exception needs attention ({{severity}})', 'A TEST reconciliation exception of type {{exception_type}} was opened (severity {{severity}}).'),
  (md5('notification_template_version:RECON_EXCEPTION_ASSIGNED:1')::uuid, md5('notification_template:RECON_EXCEPTION_ASSIGNED')::uuid,
   'A reconciliation exception was assigned to you', 'A TEST reconciliation exception of type {{exception_type}} (severity {{severity}}) was assigned to you.',
   'A reconciliation exception was assigned to you', 'A TEST reconciliation exception of type {{exception_type}} (severity {{severity}}) was assigned to you.'),
  (md5('notification_template_version:RECON_IMPORT_FAILED:1')::uuid, md5('notification_template:RECON_IMPORT_FAILED')::uuid,
   'A statement import failed', 'A TEST statement import failed validation ({{failure_code}}). Nothing from it was recorded.',
   'A statement import failed', 'A TEST statement import failed validation ({{failure_code}}). Nothing from it was recorded.'),
  (md5('notification_template_version:RECON_RESOLUTION_AWAITING_APPROVAL:1')::uuid, md5('notification_template:RECON_RESOLUTION_AWAITING_APPROVAL')::uuid,
   'A reconciliation resolution is waiting for a checker', 'A TEST reconciliation resolution ({{resolution_type}}) needs a second person''s decision.',
   'A reconciliation resolution is waiting for a checker', 'A TEST reconciliation resolution ({{resolution_type}}) needs a second person''s decision.'),
  (md5('notification_template_version:RECON_UNKNOWN_OUTCOME_ESCALATED:1')::uuid, md5('notification_template:RECON_UNKNOWN_OUTCOME_ESCALATED')::uuid,
   'Statement evidence exists for an unknown outcome', 'A TEST statement reports an outcome for a payment or payout whose outcome is unknown ({{exception_type}}). Nothing was applied automatically.',
   'Statement evidence exists for an unknown outcome', 'A TEST statement reports an outcome for a payment or payout whose outcome is unknown ({{exception_type}}). Nothing was applied automatically.')
) AS v(id, template_id, s, b, it, ib)
ON CONFLICT (id) DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- the Stage 17 templates are kept: notifications already sent reference their versions (communication history is never
-- deleted), so the narrower origin CHECK is restored NOT VALID
ALTER TABLE app.notification_templates DROP CONSTRAINT ck_notification_templates_origin;
ALTER TABLE app.notification_templates ADD CONSTRAINT ck_notification_templates_origin CHECK (origin IN ('STAGE_4_NOTICE','STAGE_5_NOTICE',
  'STAGE_6_NOTICE','STAGE_15_BASELINE','STAFF')) NOT VALID;
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('reconciliation.view',
  'reconciliation.import','reconciliation.investigate','reconciliation.rules.request','reconciliation.rules.approve','reconciliation.report.export'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('reconciliation.view','reconciliation.import','reconciliation.investigate',
  'reconciliation.rules.request','reconciliation.rules.approve','reconciliation.report.export');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
-- existing CSV/JSON statement objects are kept (never deleted): the narrower CHECK is restored NOT VALID
ALTER TABLE app.stored_objects DROP CONSTRAINT ck_stored_objects_content_type;
ALTER TABLE app.stored_objects ADD CONSTRAINT ck_stored_objects_content_type CHECK (
  sniffed_content_type IS NULL
  OR (bucket_class = 'PUBLIC_MEDIA' AND sniffed_content_type IN ('image/jpeg', 'image/png'))
  OR (bucket_class IN ('PRIVATE_KYC', 'PRIVATE_EVIDENCE') AND sniffed_content_type IN ('image/jpeg', 'image/png', 'application/pdf'))) NOT VALID;
DROP TABLE recon.worker_heartbeats;
ALTER TABLE recon.matches DROP CONSTRAINT fk_recon_matches_resolution;
ALTER TABLE recon.exception_events DROP CONSTRAINT fk_recon_exception_events_resolution;
DROP TABLE recon.resolutions;
DROP FUNCTION recon.checker_guard() CASCADE;
DROP TABLE recon.exception_events;
DROP TABLE recon.exceptions;
DROP FUNCTION recon.exceptions_close_guard();
DROP TABLE recon.matches;
DROP TABLE recon.runs;
DROP TABLE recon.items;
DROP FUNCTION recon.items_import_open();
DROP TABLE recon.settlement_batches;
DROP FUNCTION recon.settlement_batch_totals();
DROP TABLE recon.imports;
DROP FUNCTION recon.imports_metadata_once();
DROP TABLE recon.matching_rule_sets;
DROP TABLE recon.sources;
DROP FUNCTION recon.freeze_terminal();
DROP FUNCTION recon.guard_mutable_columns();
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('recon_rule_set', 'recon_import', 'recon_settlement_batch', 'recon_run', 'recon_match',
  'recon_exception', 'recon_resolution');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
-- +goose StatementEnd
