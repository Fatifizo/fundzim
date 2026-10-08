-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 17 (reconciliation) under /migrations (goose), derived from this draft.
-- File 0017: reconciliation and settlement. Owner module: reconciliation. Schema: recon.
--
-- Normative sources: docs/LEDGER.md §10, docs/ledger/settlement-and-custody-model.md §5.3-§5.4 and §7,
-- docs/payments/refund-and-reversal-flows.md §3.8, docs/compliance/audit-evidence-model.md §4.9, ADR-014,
-- ADR-030 (Stage 2), docs/stage-2/design-baseline.md §5.18. Explanation: docs/database/reconciliation-schema.md.
--
-- Foreign-key policy (baseline §3, §10):
--   * recon -> app.currencies: reference data, allowed.
--   * recon -> ledger.ledger_transactions / ledger_adjustments / ledger_posting_batches: `reconciliation` may
--     import `ledger` (baseline §3 layer 8 -> 2); ledger rows are append-only and never deleted, so the FK
--     cannot block anything and guarantees no dangling "matched to journal X" reference.
--   * recon -> payments / payouts tables: NO FK. Those tables belong to other modules (baseline §5.16/§5.17);
--     matches reference them as (subject_type, subject_id). The matcher reads them through the payments /
--     payouts public services, and a dangling subject is itself detectable (MISSING_IN_FUNDZIM).
--   * recon -> audit.evidence_records: NO FK (audit module; evidence ids are verified by hash on read).
--   * provider identity: provider_code text (psp registry is another module; codes are stable identifiers).
-- Grants (0018): fundzim_app SELECT/INSERT/UPDATE on recon.* (no DELETE/TRUNCATE); triggers below restrict
-- which columns may change.
-- =====================================================================================================

-- Generic column guard: only the columns named in TG_ARGV may change on UPDATE. Generated columns must be
-- listed too: in a BEFORE trigger NEW holds NULL for them (they are derived from guarded columns anyway).
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

-- -----------------------------------------------------------------------------------------------------
-- 1. Sources: where statements come from (per provider and what they cover).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_sources (
  id               uuid        NOT NULL,
  code             text        NOT NULL,        -- e.g. 'pesepay_settlement_report_usd'
  source_kind      text        NOT NULL,
  provider_code    text,                        -- NULL only for FundZim's own bank statement
  covers           text        NOT NULL,        -- which ledger area the lines describe
  ingestion_method text        NOT NULL,
  currency         char(3),                     -- NULL = multi-currency source (each line carries its currency)
  parser_code      text        NOT NULL,
  parser_version   text        NOT NULL,
  enabled          boolean     NOT NULL DEFAULT true,
  version          integer     NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_sources PRIMARY KEY (id),
  CONSTRAINT uq_reconciliation_sources_code UNIQUE (code),
  CONSTRAINT fk_reconciliation_sources_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_sources_kind CHECK (source_kind IN ('PROVIDER_STATEMENT', 'PROVIDER_SETTLEMENT_REPORT', 'PROVIDER_API', 'BANK_STATEMENT')),
  CONSTRAINT ck_reconciliation_sources_covers CHECK (covers IN ('PROVIDER_COLLECTIONS', 'PROVIDER_POOL', 'PROVIDER_PAYOUTS', 'FUNDZIM_OPERATING_BANK')),
  CONSTRAINT ck_reconciliation_sources_method CHECK (ingestion_method IN ('API', 'SFTP', 'DASHBOARD_EXPORT', 'MANUAL_UPLOAD')),
  CONSTRAINT ck_reconciliation_sources_bank CHECK ((source_kind = 'BANK_STATEMENT') = (covers = 'FUNDZIM_OPERATING_BANK')),
  CONSTRAINT ck_reconciliation_sources_provider CHECK ((provider_code IS NULL) = (source_kind = 'BANK_STATEMENT'))
);
CREATE TRIGGER trg_reconciliation_sources_guard BEFORE UPDATE ON recon.reconciliation_sources
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('enabled', 'parser_version', 'version', 'updated_at');
CREATE TRIGGER trg_reconciliation_sources_updated_at BEFORE UPDATE ON recon.reconciliation_sources
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_sources_no_delete BEFORE DELETE ON recon.reconciliation_sources
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 2. Imports: one row per ingested file / API pull. content_sha256 UNIQUE prevents double import.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_imports (
  id                    uuid        NOT NULL,
  source_id             uuid        NOT NULL,
  content_sha256        bytea       NOT NULL,     -- hash of the file bytes / canonical API response
  statement_ref         text,                     -- provider's own statement or report id, if any
  period_start          timestamptz NOT NULL,
  period_end            timestamptz NOT NULL,
  currency              char(3),                  -- set when the file is single-currency
  opening_balance_minor bigint,                   -- statement balances (SC-3); signed, in `currency`
  closing_balance_minor bigint,
  rows_declared         integer,                  -- from the file trailer, when present
  rows_parsed           integer     NOT NULL DEFAULT 0,
  rows_rejected         integer     NOT NULL DEFAULT 0,
  status                text        NOT NULL,
  failure_reason        text,
  evidence_record_id    uuid        NOT NULL,     -- PSP_SETTLEMENT_REPORT / BANK_STATEMENT evidence (C2, hash-verified)
  supersedes_import_id  uuid,                     -- provider re-issued a corrected statement
  imported_by_type      text        NOT NULL,
  imported_by_id        text        NOT NULL,
  received_at           timestamptz NOT NULL,
  version               integer     NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_imports PRIMARY KEY (id),
  CONSTRAINT uq_reconciliation_imports_content_sha256 UNIQUE (content_sha256),
  CONSTRAINT uq_reconciliation_imports_id_source UNIQUE (id, source_id),
  CONSTRAINT fk_reconciliation_imports_source FOREIGN KEY (source_id) REFERENCES recon.reconciliation_sources (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_imports_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_imports_supersedes FOREIGN KEY (supersedes_import_id) REFERENCES recon.reconciliation_imports (id) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_imports_hash CHECK (length(content_sha256) = 32),
  CONSTRAINT ck_reconciliation_imports_period CHECK (period_end > period_start),
  CONSTRAINT ck_reconciliation_imports_balances CHECK (
    (opening_balance_minor IS NULL AND closing_balance_minor IS NULL) OR currency IS NOT NULL),
  CONSTRAINT ck_reconciliation_imports_counts CHECK (rows_parsed >= 0 AND rows_rejected >= 0 AND (rows_declared IS NULL OR rows_declared >= 0)),
  CONSTRAINT ck_reconciliation_imports_status CHECK (status IN ('RECEIVED', 'PARSING', 'PARSED', 'FAILED', 'SUPERSEDED')),
  CONSTRAINT ck_reconciliation_imports_parsed CHECK (
    status NOT IN ('PARSED', 'SUPERSEDED') OR rows_declared IS NULL OR rows_parsed + rows_rejected = rows_declared),
  CONSTRAINT ck_reconciliation_imports_failed CHECK (status <> 'FAILED' OR failure_reason IS NOT NULL),
  CONSTRAINT ck_reconciliation_imports_by CHECK (imported_by_type IN ('SYSTEM', 'STAFF'))
);
-- The same provider statement is imported once unless a corrected re-issue explicitly supersedes it.
CREATE UNIQUE INDEX uq_reconciliation_imports_statement ON recon.reconciliation_imports (source_id, statement_ref)
  WHERE statement_ref IS NOT NULL AND supersedes_import_id IS NULL;
CREATE INDEX ix_reconciliation_imports_source_period ON recon.reconciliation_imports (source_id, period_start);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('reconciliation_import', '',         'RECEIVED'),
  ('reconciliation_import', 'RECEIVED', 'PARSING'),
  ('reconciliation_import', 'RECEIVED', 'FAILED'),
  ('reconciliation_import', 'PARSING',  'PARSED'),
  ('reconciliation_import', 'PARSING',  'FAILED'),
  ('reconciliation_import', 'PARSED',   'SUPERSEDED');
CREATE TRIGGER trg_reconciliation_imports_guard_status BEFORE INSERT OR UPDATE OF status ON recon.reconciliation_imports
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('reconciliation_import');
CREATE TRIGGER trg_reconciliation_imports_guard BEFORE UPDATE ON recon.reconciliation_imports
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'rows_declared', 'rows_parsed', 'rows_rejected',
    'opening_balance_minor', 'closing_balance_minor', 'failure_reason', 'version', 'updated_at');
CREATE TRIGGER trg_reconciliation_imports_freeze BEFORE UPDATE ON recon.reconciliation_imports
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('FAILED', 'SUPERSEDED');
CREATE TRIGGER trg_reconciliation_imports_updated_at BEFORE UPDATE ON recon.reconciliation_imports
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_imports_no_delete BEFORE DELETE ON recon.reconciliation_imports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. Runs: one matching pass for (provider, currency, window).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_runs (
  id                        uuid        NOT NULL,
  provider_code             text        NOT NULL,   -- 'fundzim_bank' for the operating-bank run
  currency                  char(3)     NOT NULL,
  window_start              timestamptz NOT NULL,
  window_end                timestamptz NOT NULL,
  run_kind                  text        NOT NULL,
  run_number                integer     NOT NULL DEFAULT 1,
  status                    text        NOT NULL,
  matcher_version           text        NOT NULL,
  import_ids                uuid[]      NOT NULL DEFAULT '{}',   -- inputs (imports read by this run)
  items_considered          integer     NOT NULL DEFAULT 0,
  items_matched             integer     NOT NULL DEFAULT 0,
  discrepancies_opened      integer     NOT NULL DEFAULT 0,
  discrepancies_auto_closed integer     NOT NULL DEFAULT 0,
  ledger_postings           integer     NOT NULL DEFAULT 0,
  report_evidence_record_id uuid,                   -- run report snapshot (audit evidence)
  supersedes_run_id         uuid,
  triggered_by_type         text        NOT NULL,
  triggered_by_id           text        NOT NULL,
  job_id                    text,
  failure_reason            text,
  started_at                timestamptz,
  finished_at               timestamptz,
  version                   integer     NOT NULL DEFAULT 1,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_runs PRIMARY KEY (id),
  CONSTRAINT uq_reconciliation_runs_scope UNIQUE (provider_code, currency, window_start, window_end, run_number),
  CONSTRAINT fk_reconciliation_runs_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_runs_supersedes FOREIGN KEY (supersedes_run_id) REFERENCES recon.reconciliation_runs (id) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_runs_window CHECK (window_end > window_start),
  CONSTRAINT ck_reconciliation_runs_kind CHECK (run_kind IN ('SCHEDULED', 'ON_DEMAND', 'RERUN')),
  CONSTRAINT ck_reconciliation_runs_number CHECK (run_number >= 1),
  CONSTRAINT ck_reconciliation_runs_status CHECK (status IN ('QUEUED', 'RUNNING', 'COMPLETED', 'FAILED', 'CANCELLED')),
  CONSTRAINT ck_reconciliation_runs_counts CHECK (items_considered >= 0 AND items_matched >= 0 AND items_matched <= items_considered
    AND discrepancies_opened >= 0 AND discrepancies_auto_closed >= 0 AND ledger_postings >= 0),
  CONSTRAINT ck_reconciliation_runs_started CHECK (status IN ('QUEUED', 'CANCELLED') OR started_at IS NOT NULL),
  CONSTRAINT ck_reconciliation_runs_completed CHECK (
    status <> 'COMPLETED' OR (finished_at IS NOT NULL AND report_evidence_record_id IS NOT NULL)),
  CONSTRAINT ck_reconciliation_runs_failed CHECK (status <> 'FAILED' OR (finished_at IS NOT NULL AND failure_reason IS NOT NULL)),
  CONSTRAINT ck_reconciliation_runs_by CHECK (triggered_by_type IN ('SYSTEM', 'STAFF'))
);
-- At most one active run per provider and currency (the scheduler and an on-demand run cannot overlap).
CREATE UNIQUE INDEX uq_reconciliation_runs_one_active ON recon.reconciliation_runs (provider_code, currency)
  WHERE status IN ('QUEUED', 'RUNNING');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('reconciliation_run', '',        'QUEUED'),
  ('reconciliation_run', 'QUEUED',  'RUNNING'),
  ('reconciliation_run', 'QUEUED',  'CANCELLED'),
  ('reconciliation_run', 'RUNNING', 'COMPLETED'),
  ('reconciliation_run', 'RUNNING', 'FAILED');
CREATE TRIGGER trg_reconciliation_runs_guard_status BEFORE INSERT OR UPDATE OF status ON recon.reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('reconciliation_run');
CREATE TRIGGER trg_reconciliation_runs_freeze BEFORE UPDATE ON recon.reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('COMPLETED', 'FAILED', 'CANCELLED');
CREATE TRIGGER trg_reconciliation_runs_guard BEFORE UPDATE ON recon.reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'import_ids', 'items_considered', 'items_matched',
    'discrepancies_opened', 'discrepancies_auto_closed', 'ledger_postings', 'report_evidence_record_id', 'job_id',
    'failure_reason', 'started_at', 'finished_at', 'version', 'updated_at');
CREATE TRIGGER trg_reconciliation_runs_updated_at BEFORE UPDATE ON recon.reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_runs_no_delete BEFORE DELETE ON recon.reconciliation_runs
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. Items: normalised statement lines. Immutable except match_status.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_items (
  id                 uuid        NOT NULL,
  import_id          uuid        NOT NULL,
  source_id          uuid        NOT NULL,      -- denormalised from the import for the dedupe key
  external_line_id   text        NOT NULL,      -- provider's line/transaction id, or a deterministic line hash
  line_number        integer     NOT NULL,
  item_kind          text        NOT NULL,
  direction          text        NOT NULL,      -- CREDIT = into the covered account/pool; DEBIT = out of it
  amount_minor       bigint      NOT NULL,      -- gross line amount, > 0
  fee_minor          bigint,                    -- provider fee on the line, if reported
  net_minor          bigint,                    -- provider-reported net, if reported
  currency           char(3)     NOT NULL,      -- normalised (e.g. provider 'ZiG' -> 'ZWG')
  raw_currency_code  text        NOT NULL,      -- verbatim provider code (CURRENCY_MISMATCH evidence)
  provider_ref       text,                      -- provider transaction / payout / refund id
  merchant_ref       text,                      -- our reference as sent to the provider (payment_id, payout_id, refund_id)
  provider_batch_ref text,                      -- settlement batch the line belongs to
  provider_status    text,                      -- verbatim
  value_date         date,
  booked_at          timestamptz,
  raw_line_sha256    bytea       NOT NULL,      -- hash of the raw line inside the evidence object
  raw_line_locator   text        NOT NULL,      -- e.g. 'row:123' / 'json:$.data[4]' within the import's evidence object
  match_status       text        NOT NULL DEFAULT 'UNMATCHED',
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_items PRIMARY KEY (id),
  CONSTRAINT uq_reconciliation_items_source_line UNIQUE (source_id, external_line_id),
  CONSTRAINT uq_reconciliation_items_import_line UNIQUE (import_id, line_number),
  CONSTRAINT uq_reconciliation_items_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_reconciliation_items_import FOREIGN KEY (import_id, source_id)
    REFERENCES recon.reconciliation_imports (id, source_id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_items_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_items_kind CHECK (item_kind IN ('PAYMENT', 'REFUND', 'PAYOUT', 'PAYOUT_REVERSAL', 'FEE',
                                                               'CHARGEBACK', 'SETTLEMENT', 'FEE_REMITTANCE', 'ADJUSTMENT')),
  CONSTRAINT ck_reconciliation_items_direction CHECK (direction IN ('CREDIT', 'DEBIT')),
  CONSTRAINT ck_reconciliation_items_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_reconciliation_items_fee CHECK (fee_minor IS NULL OR fee_minor >= 0),
  CONSTRAINT ck_reconciliation_items_net CHECK (net_minor IS NULL OR fee_minor IS NULL OR net_minor = amount_minor - fee_minor),
  CONSTRAINT ck_reconciliation_items_line CHECK (line_number >= 1),
  CONSTRAINT ck_reconciliation_items_hash CHECK (length(raw_line_sha256) = 32),
  CONSTRAINT ck_reconciliation_items_match_status CHECK (match_status IN ('UNMATCHED', 'MATCHED', 'PARTIALLY_MATCHED', 'DISCREPANCY', 'IGNORED'))
);
CREATE INDEX ix_reconciliation_items_provider_ref ON recon.reconciliation_items (provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX ix_reconciliation_items_merchant_ref ON recon.reconciliation_items (merchant_ref) WHERE merchant_ref IS NOT NULL;
CREATE INDEX ix_reconciliation_items_unmatched ON recon.reconciliation_items (source_id, value_date) WHERE match_status = 'UNMATCHED';
CREATE INDEX ix_reconciliation_items_import ON recon.reconciliation_items (import_id);

-- Lines are added only while their import is PARSING.
CREATE FUNCTION recon.check_item_import_open() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM recon.reconciliation_imports WHERE id = NEW.import_id;
  IF s IS DISTINCT FROM 'PARSING' THEN
    RAISE EXCEPTION 'reconciliation import % is % (items can only be added while PARSING)', NEW.import_id, s
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_reconciliation_items_import_open BEFORE INSERT ON recon.reconciliation_items
  FOR EACH ROW EXECUTE FUNCTION recon.check_item_import_open();
CREATE TRIGGER trg_reconciliation_items_guard BEFORE UPDATE ON recon.reconciliation_items
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('match_status', 'updated_at');
CREATE TRIGGER trg_reconciliation_items_updated_at BEFORE UPDATE ON recon.reconciliation_items
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_items_no_delete BEFORE DELETE ON recon.reconciliation_items
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_reconciliation_items_no_truncate BEFORE TRUNCATE ON recon.reconciliation_items
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 5. Matches: item <-> FundZim subject <-> ledger journal (three-way match, custody model §7).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_matches (
  id                    uuid        NOT NULL,
  run_id                uuid        NOT NULL,
  item_id               uuid        NOT NULL,
  currency              char(3)     NOT NULL,   -- = item currency (composite FK)
  subject_type          text        NOT NULL,
  subject_id            uuid        NOT NULL,   -- id in the owning module (no FK: other module's table)
  ledger_transaction_id uuid        NOT NULL,   -- the journal that records the subject's effect
  match_type            text        NOT NULL,
  match_rule_code       text,                   -- matcher rule for RULE matches (e.g. 'REF_AND_AMOUNT', 'BATCH_NETTING')
  matched_amount_minor  bigint      NOT NULL,
  created_by_type       text        NOT NULL,
  created_by_staff_id   uuid,                   -- MANUAL: proposing staff (maker)
  approved_by           uuid,                   -- MANUAL: approving staff (checker)
  evidence_record_ids   uuid[]      NOT NULL DEFAULT '{}',
  status                text        NOT NULL DEFAULT 'ACTIVE',
  revoked_at            timestamptz,
  revoked_by            uuid,
  revoke_reason         text,
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_matches PRIMARY KEY (id),
  CONSTRAINT fk_reconciliation_matches_run FOREIGN KEY (run_id) REFERENCES recon.reconciliation_runs (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_matches_item_currency FOREIGN KEY (item_id, currency)
    REFERENCES recon.reconciliation_items (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_matches_ledger_transaction FOREIGN KEY (ledger_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_matches_subject_type CHECK (subject_type IN ('PAYMENT_INTENT', 'REFUND_TRANSACTION', 'PAYOUT_REQUEST',
    'CHARGEBACK', 'PAYMENT_DISPUTE', 'SETTLEMENT_BATCH', 'SETTLEMENT_ITEM', 'FEE_REMITTANCE')),
  CONSTRAINT ck_reconciliation_matches_type CHECK (match_type IN ('EXACT', 'RULE', 'MANUAL')),
  CONSTRAINT ck_reconciliation_matches_rule CHECK (match_type <> 'RULE' OR match_rule_code IS NOT NULL),
  CONSTRAINT ck_reconciliation_matches_amount CHECK (matched_amount_minor > 0),
  CONSTRAINT ck_reconciliation_matches_by CHECK (created_by_type IN ('SYSTEM', 'STAFF')),
  -- A manual match is a judgement: maker-checker and evidence.
  CONSTRAINT ck_reconciliation_matches_manual CHECK (
    match_type <> 'MANUAL'
    OR (created_by_type = 'STAFF' AND created_by_staff_id IS NOT NULL AND approved_by IS NOT NULL
        AND approved_by <> created_by_staff_id AND cardinality(evidence_record_ids) >= 1)),
  CONSTRAINT ck_reconciliation_matches_status CHECK (status IN ('ACTIVE', 'REVOKED')),
  CONSTRAINT ck_reconciliation_matches_revoked CHECK (
    (status = 'REVOKED') = (revoked_at IS NOT NULL AND revoked_by IS NOT NULL AND revoke_reason IS NOT NULL))
);
CREATE UNIQUE INDEX uq_reconciliation_matches_active ON recon.reconciliation_matches (item_id, subject_type, subject_id)
  WHERE status = 'ACTIVE';
CREATE INDEX ix_reconciliation_matches_subject ON recon.reconciliation_matches (subject_type, subject_id);
CREATE INDEX ix_reconciliation_matches_ledger ON recon.reconciliation_matches (ledger_transaction_id);
CREATE INDEX ix_reconciliation_matches_run ON recon.reconciliation_matches (run_id);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('reconciliation_match', '',       'ACTIVE'),
  ('reconciliation_match', 'ACTIVE', 'REVOKED');
CREATE TRIGGER trg_reconciliation_matches_guard_status BEFORE INSERT OR UPDATE OF status ON recon.reconciliation_matches
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('reconciliation_match');
CREATE TRIGGER trg_reconciliation_matches_freeze BEFORE UPDATE ON recon.reconciliation_matches
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('REVOKED');
CREATE TRIGGER trg_reconciliation_matches_guard BEFORE UPDATE ON recon.reconciliation_matches
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'revoked_at', 'revoked_by', 'revoke_reason');
CREATE TRIGGER trg_reconciliation_matches_no_delete BEFORE DELETE ON recon.reconciliation_matches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_reconciliation_matches_no_truncate BEFORE TRUNCATE ON recon.reconciliation_matches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 6. Discrepancies (mismatch cases). One row per distinct problem (dedupe_key), re-seen by later runs.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_discrepancies (
  id                      uuid        NOT NULL,
  dedupe_key              text        NOT NULL,   -- e.g. 'MISSING_IN_PROVIDER:PAYMENT_INTENT:{id}'
  discrepancy_type        text        NOT NULL,
  severity                text        NOT NULL,
  status                  text        NOT NULL,
  provider_code           text        NOT NULL,
  currency                char(3)     NOT NULL,   -- the FundZim-side (or normalised) currency
  first_run_id            uuid        NOT NULL,
  last_seen_run_id        uuid        NOT NULL,
  occurrences             integer     NOT NULL DEFAULT 1,
  item_id                 uuid,                   -- provider side (NULL when missing in provider)
  subject_type            text,                   -- FundZim side (NULL when missing in FundZim)
  subject_id              uuid,
  ledger_transaction_id   uuid,                   -- FundZim journal involved, if any
  suspense_transaction_id uuid,                   -- SETTLEMENT_DISCREPANCY journal that parked the difference
  expected_amount_minor   bigint,
  actual_amount_minor     bigint,
  difference_minor        bigint GENERATED ALWAYS AS (actual_amount_minor - expected_amount_minor) STORED,
  actual_currency_raw     text,                   -- CURRENCY_MISMATCH: provider-reported code verbatim
  detected_at             timestamptz NOT NULL,
  sla_due_at              timestamptz NOT NULL,   -- from severity (configuration); age = now() - detected_at
  escalated_at            timestamptz,
  assigned_to             uuid,
  case_ref                uuid,                   -- ops / compliance case id (no FK: other module)
  resolved_at             timestamptz,
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_discrepancies PRIMARY KEY (id),
  CONSTRAINT uq_reconciliation_discrepancies_dedupe UNIQUE (dedupe_key),
  CONSTRAINT fk_reconciliation_discrepancies_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_discrepancies_first_run FOREIGN KEY (first_run_id) REFERENCES recon.reconciliation_runs (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_discrepancies_last_run FOREIGN KEY (last_seen_run_id) REFERENCES recon.reconciliation_runs (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_discrepancies_item FOREIGN KEY (item_id) REFERENCES recon.reconciliation_items (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_discrepancies_ledger_tx FOREIGN KEY (ledger_transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_discrepancies_suspense_tx FOREIGN KEY (suspense_transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_discrepancies_type CHECK (discrepancy_type IN ('MISSING_IN_PROVIDER', 'MISSING_IN_FUNDZIM', 'DUPLICATE',
    'AMOUNT_MISMATCH', 'CURRENCY_MISMATCH', 'FEE_MISMATCH', 'PAYOUT_MISMATCH', 'STATUS_MISMATCH', 'SETTLEMENT_SHORT', 'SETTLEMENT_OVER')),
  CONSTRAINT ck_reconciliation_discrepancies_severity CHECK (severity IN ('SEV1', 'SEV2', 'SEV3', 'SEV4')),
  CONSTRAINT ck_reconciliation_discrepancies_status CHECK (status IN ('OPEN', 'INVESTIGATING', 'RESOLUTION_PROPOSED', 'RESOLVED', 'AUTO_CLOSED')),
  CONSTRAINT ck_reconciliation_discrepancies_amounts CHECK (
    (expected_amount_minor IS NULL OR expected_amount_minor >= 0) AND (actual_amount_minor IS NULL OR actual_amount_minor >= 0)),
  CONSTRAINT ck_reconciliation_discrepancies_subject CHECK ((subject_type IS NULL) = (subject_id IS NULL)),
  CONSTRAINT ck_reconciliation_discrepancies_occurrences CHECK (occurrences >= 1),
  CONSTRAINT ck_reconciliation_discrepancies_sla CHECK (sla_due_at > detected_at),
  CONSTRAINT ck_reconciliation_discrepancies_resolved CHECK ((status IN ('RESOLVED', 'AUTO_CLOSED')) = (resolved_at IS NOT NULL)),
  -- The evidence each type must carry.
  CONSTRAINT ck_reconciliation_discrepancies_shape CHECK (CASE discrepancy_type
    WHEN 'MISSING_IN_PROVIDER' THEN subject_id IS NOT NULL AND item_id IS NULL
    WHEN 'MISSING_IN_FUNDZIM'  THEN item_id IS NOT NULL AND subject_id IS NULL
    WHEN 'DUPLICATE'           THEN item_id IS NOT NULL
    WHEN 'CURRENCY_MISMATCH'   THEN item_id IS NOT NULL AND subject_id IS NOT NULL AND actual_currency_raw IS NOT NULL
    WHEN 'STATUS_MISMATCH'     THEN item_id IS NOT NULL AND subject_id IS NOT NULL
    WHEN 'SETTLEMENT_SHORT'    THEN item_id IS NOT NULL AND actual_amount_minor < expected_amount_minor
    WHEN 'SETTLEMENT_OVER'     THEN item_id IS NOT NULL AND actual_amount_minor > expected_amount_minor
    ELSE /* AMOUNT_, FEE_, PAYOUT_MISMATCH */ item_id IS NOT NULL AND subject_id IS NOT NULL
         AND actual_amount_minor IS DISTINCT FROM expected_amount_minor
         AND actual_amount_minor IS NOT NULL AND expected_amount_minor IS NOT NULL
  END)
);
CREATE INDEX ix_reconciliation_discrepancies_open ON recon.reconciliation_discrepancies (severity, detected_at)
  WHERE status IN ('OPEN', 'INVESTIGATING', 'RESOLUTION_PROPOSED');
CREATE INDEX ix_reconciliation_discrepancies_subject ON recon.reconciliation_discrepancies (subject_type, subject_id) WHERE subject_id IS NOT NULL;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('reconciliation_discrepancy', '',                    'OPEN'),
  ('reconciliation_discrepancy', 'OPEN',                'INVESTIGATING'),
  ('reconciliation_discrepancy', 'OPEN',                'RESOLUTION_PROPOSED'),
  ('reconciliation_discrepancy', 'INVESTIGATING',       'RESOLUTION_PROPOSED'),
  ('reconciliation_discrepancy', 'RESOLUTION_PROPOSED', 'RESOLVED'),
  ('reconciliation_discrepancy', 'RESOLUTION_PROPOSED', 'INVESTIGATING'),   -- proposal rejected/expired
  ('reconciliation_discrepancy', 'OPEN',                'AUTO_CLOSED'),     -- later run found the counterpart
  ('reconciliation_discrepancy', 'INVESTIGATING',       'AUTO_CLOSED');
CREATE TRIGGER trg_reconciliation_discrepancies_guard_status BEFORE INSERT OR UPDATE OF status ON recon.reconciliation_discrepancies
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('reconciliation_discrepancy');
CREATE TRIGGER trg_reconciliation_discrepancies_freeze BEFORE UPDATE ON recon.reconciliation_discrepancies
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('RESOLVED', 'AUTO_CLOSED');
CREATE TRIGGER trg_reconciliation_discrepancies_guard BEFORE UPDATE ON recon.reconciliation_discrepancies
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('difference_minor', 'status', 'severity', 'last_seen_run_id', 'occurrences',
    'suspense_transaction_id', 'sla_due_at', 'escalated_at', 'assigned_to', 'case_ref', 'resolved_at', 'version', 'updated_at');
CREATE TRIGGER trg_reconciliation_discrepancies_updated_at BEFORE UPDATE ON recon.reconciliation_discrepancies
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_discrepancies_no_delete BEFORE DELETE ON recon.reconciliation_discrepancies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 7. Resolutions (maker-checker). A ledger effect is always a ledger_adjustments request + journal.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.reconciliation_resolutions (
  id                       uuid        NOT NULL,
  discrepancy_id           uuid        NOT NULL,
  resolution_type          text        NOT NULL,
  status                   text        NOT NULL,
  justification            text        NOT NULL,
  evidence_record_ids      uuid[]      NOT NULL DEFAULT '{}',   -- ADJUSTMENT_SUPPORT, provider confirmations
  proposed_by              uuid        NOT NULL,                -- staff (maker)
  proposed_at              timestamptz NOT NULL DEFAULT now(),
  expires_at               timestamptz NOT NULL,
  approved_by              uuid,                                -- staff (checker)
  approved_at              timestamptz,
  approval_justification   text,
  rejected_by              uuid,
  rejected_at              timestamptz,
  rejection_reason         text,
  ledger_adjustment_id     uuid,                                -- maker-checker request in the ledger
  resulting_transaction_id uuid,                                -- the journal that applied it
  match_id                 uuid,                                -- MANUAL_MATCH result
  applied_at               timestamptz,
  version                  integer     NOT NULL DEFAULT 1,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_reconciliation_resolutions PRIMARY KEY (id),
  CONSTRAINT fk_reconciliation_resolutions_discrepancy FOREIGN KEY (discrepancy_id) REFERENCES recon.reconciliation_discrepancies (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_resolutions_adjustment FOREIGN KEY (ledger_adjustment_id) REFERENCES ledger.ledger_adjustments (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_resolutions_transaction FOREIGN KEY (resulting_transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_reconciliation_resolutions_match FOREIGN KEY (match_id) REFERENCES recon.reconciliation_matches (id) ON DELETE RESTRICT,
  CONSTRAINT ck_reconciliation_resolutions_type CHECK (resolution_type IN ('LEDGER_ADJUSTMENT', 'SUSPENSE_CLEARED', 'WRITE_OFF',
    'MANUAL_MATCH', 'PROVIDER_CORRECTION_CONFIRMED', 'EXPLAINED_NO_ACTION', 'ESCALATED_TO_CASE')),
  CONSTRAINT ck_reconciliation_resolutions_status CHECK (status IN ('PROPOSED', 'APPROVED', 'APPLIED', 'REJECTED', 'WITHDRAWN', 'EXPIRED')),
  CONSTRAINT ck_reconciliation_resolutions_justification CHECK (length(btrim(justification)) >= 20),
  CONSTRAINT ck_reconciliation_resolutions_expiry CHECK (expires_at > proposed_at),
  CONSTRAINT ck_reconciliation_resolutions_maker_checker CHECK (approved_by IS NULL OR approved_by <> proposed_by),
  CONSTRAINT ck_reconciliation_resolutions_rejecter CHECK (rejected_by IS NULL OR rejected_by <> proposed_by),
  CONSTRAINT ck_reconciliation_resolutions_approval CHECK (
    status NOT IN ('APPROVED', 'APPLIED')
    OR (approved_by IS NOT NULL AND approved_at IS NOT NULL AND approved_at <= expires_at
        AND cardinality(evidence_record_ids) >= 1 AND length(btrim(coalesce(approval_justification, ''))) > 0)),
  CONSTRAINT ck_reconciliation_resolutions_rejection CHECK (status <> 'REJECTED' OR (rejected_by IS NOT NULL AND rejection_reason IS NOT NULL)),
  CONSTRAINT ck_reconciliation_resolutions_ledger CHECK (
    resolution_type NOT IN ('LEDGER_ADJUSTMENT', 'SUSPENSE_CLEARED', 'WRITE_OFF') OR ledger_adjustment_id IS NOT NULL),
  CONSTRAINT ck_reconciliation_resolutions_applied CHECK (
    status <> 'APPLIED'
    OR (applied_at IS NOT NULL
        AND (resolution_type NOT IN ('LEDGER_ADJUSTMENT', 'SUSPENSE_CLEARED', 'WRITE_OFF') OR resulting_transaction_id IS NOT NULL)
        AND (resolution_type <> 'MANUAL_MATCH' OR match_id IS NOT NULL)))
);
-- At most one live resolution per discrepancy.
CREATE UNIQUE INDEX uq_reconciliation_resolutions_live ON recon.reconciliation_resolutions (discrepancy_id)
  WHERE status IN ('PROPOSED', 'APPROVED', 'APPLIED');
CREATE INDEX ix_reconciliation_resolutions_pending ON recon.reconciliation_resolutions (status, expires_at)
  WHERE status IN ('PROPOSED', 'APPROVED');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('reconciliation_resolution', '',         'PROPOSED'),
  ('reconciliation_resolution', 'PROPOSED', 'APPROVED'),
  ('reconciliation_resolution', 'PROPOSED', 'REJECTED'),
  ('reconciliation_resolution', 'PROPOSED', 'WITHDRAWN'),
  ('reconciliation_resolution', 'PROPOSED', 'EXPIRED'),
  ('reconciliation_resolution', 'APPROVED', 'APPLIED'),
  ('reconciliation_resolution', 'APPROVED', 'EXPIRED');
CREATE TRIGGER trg_reconciliation_resolutions_guard_status BEFORE INSERT OR UPDATE OF status ON recon.reconciliation_resolutions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('reconciliation_resolution');
CREATE TRIGGER trg_reconciliation_resolutions_freeze BEFORE UPDATE ON recon.reconciliation_resolutions
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('APPLIED', 'REJECTED', 'WITHDRAWN', 'EXPIRED');
CREATE TRIGGER trg_reconciliation_resolutions_guard BEFORE UPDATE ON recon.reconciliation_resolutions
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'approved_by', 'approved_at', 'approval_justification',
    'rejected_by', 'rejected_at', 'rejection_reason', 'ledger_adjustment_id', 'resulting_transaction_id', 'match_id',
    'applied_at', 'version', 'updated_at');
CREATE TRIGGER trg_reconciliation_resolutions_updated_at BEFORE UPDATE ON recon.reconciliation_resolutions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_reconciliation_resolutions_no_delete BEFORE DELETE ON recon.reconciliation_resolutions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 8. Settlement batches and their items (provider settlement report structure, custody model §5.3).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE recon.settlement_batches (
  id                      uuid        NOT NULL,
  provider_code           text        NOT NULL,
  currency                char(3)     NOT NULL,
  provider_batch_ref      text        NOT NULL,
  import_id               uuid,
  reconciliation_item_id  uuid,                    -- the SETTLEMENT line on the pool statement, once matched
  settlement_date         date        NOT NULL,
  gross_credits_minor     bigint      NOT NULL,    -- Σ collections in the batch
  gross_debits_minor      bigint      NOT NULL,    -- Σ refunds, chargebacks, adjustments out
  fees_minor              bigint      NOT NULL,    -- Σ provider fees deducted
  net_minor               bigint      NOT NULL,    -- signed: credits - debits - fees
  declared_item_count     integer,
  status                  text        NOT NULL,
  ledger_posting_batch_id uuid,                    -- groups the SETTLEMENT_MATCHED journals
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_settlement_batches PRIMARY KEY (id),
  CONSTRAINT uq_settlement_batches_provider_ref UNIQUE (provider_code, provider_batch_ref),
  CONSTRAINT uq_settlement_batches_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_settlement_batches_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_settlement_batches_import FOREIGN KEY (import_id) REFERENCES recon.reconciliation_imports (id) ON DELETE RESTRICT,
  CONSTRAINT fk_settlement_batches_item FOREIGN KEY (reconciliation_item_id, currency)
    REFERENCES recon.reconciliation_items (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_settlement_batches_ledger_batch FOREIGN KEY (ledger_posting_batch_id, currency)
    REFERENCES ledger.ledger_posting_batches (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_settlement_batches_amounts CHECK (gross_credits_minor >= 0 AND gross_debits_minor >= 0 AND fees_minor >= 0),
  CONSTRAINT ck_settlement_batches_net CHECK (net_minor = gross_credits_minor - gross_debits_minor - fees_minor),
  CONSTRAINT ck_settlement_batches_count CHECK (declared_item_count IS NULL OR declared_item_count >= 0),
  CONSTRAINT ck_settlement_batches_status CHECK (status IN ('RECEIVED', 'MATCHING', 'MATCHED', 'DISCREPANCY', 'CLOSED'))
);

CREATE TABLE recon.settlement_items (
  id                     uuid        NOT NULL,
  settlement_batch_id    uuid        NOT NULL,
  currency               char(3)     NOT NULL,     -- must equal the batch currency (composite FK)
  line_ref               text        NOT NULL,     -- provider's line id within the batch
  item_kind              text        NOT NULL,
  direction              text        NOT NULL,     -- CREDIT = collection into the pool
  gross_minor            bigint      NOT NULL,
  fee_minor              bigint      NOT NULL DEFAULT 0,
  net_signed_minor       bigint      GENERATED ALWAYS AS (
                           CASE direction WHEN 'CREDIT' THEN gross_minor - fee_minor ELSE -(gross_minor + fee_minor) END) STORED,
  provider_ref           text,
  merchant_ref           text,
  reconciliation_item_id uuid,
  subject_type           text,                     -- set when matched (PAYMENT_INTENT, REFUND_TRANSACTION, CHARGEBACK ...)
  subject_id             uuid,
  ledger_transaction_id  uuid,                     -- e.g. the SETTLEMENT_MATCHED journal for this line
  match_status           text        NOT NULL DEFAULT 'UNMATCHED',
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_settlement_items PRIMARY KEY (id),
  CONSTRAINT uq_settlement_items_line UNIQUE (settlement_batch_id, line_ref),
  CONSTRAINT fk_settlement_items_batch_currency FOREIGN KEY (settlement_batch_id, currency)
    REFERENCES recon.settlement_batches (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_settlement_items_item FOREIGN KEY (reconciliation_item_id, currency)
    REFERENCES recon.reconciliation_items (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_settlement_items_ledger_tx FOREIGN KEY (ledger_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_settlement_items_kind CHECK (item_kind IN ('PAYMENT', 'REFUND', 'CHARGEBACK', 'FEE', 'FEE_REMITTANCE', 'ADJUSTMENT')),
  CONSTRAINT ck_settlement_items_direction CHECK (direction IN ('CREDIT', 'DEBIT')),
  CONSTRAINT ck_settlement_items_amounts CHECK (gross_minor > 0 AND fee_minor >= 0),
  CONSTRAINT ck_settlement_items_subject CHECK ((subject_type IS NULL) = (subject_id IS NULL)),
  CONSTRAINT ck_settlement_items_match_status CHECK (match_status IN ('UNMATCHED', 'MATCHED', 'DISCREPANCY'))
);
CREATE INDEX ix_settlement_items_merchant_ref ON recon.settlement_items (merchant_ref) WHERE merchant_ref IS NOT NULL;
CREATE INDEX ix_settlement_items_subject ON recon.settlement_items (subject_type, subject_id) WHERE subject_id IS NOT NULL;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('settlement_batch', '',            'RECEIVED'),
  ('settlement_batch', 'RECEIVED',    'MATCHING'),
  ('settlement_batch', 'MATCHING',    'MATCHED'),
  ('settlement_batch', 'MATCHING',    'DISCREPANCY'),
  ('settlement_batch', 'DISCREPANCY', 'MATCHING'),   -- after the discrepancy is resolved
  ('settlement_batch', 'MATCHED',     'CLOSED');

-- A batch can only become MATCHED when its items add up to its header (sums per currency are guaranteed
-- single-currency by fk_settlement_items_batch_currency).
CREATE FUNCTION recon.check_settlement_batch_totals() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  cr bigint; dr bigint; fe bigint; n integer;
BEGIN
  IF NEW.status IN ('MATCHED', 'CLOSED') AND NEW.status IS DISTINCT FROM OLD.status THEN
    SELECT coalesce(sum(gross_minor) FILTER (WHERE direction = 'CREDIT'), 0),
           coalesce(sum(gross_minor) FILTER (WHERE direction = 'DEBIT'), 0),
           coalesce(sum(fee_minor), 0), count(*)
      INTO cr, dr, fe, n
      FROM recon.settlement_items WHERE settlement_batch_id = NEW.id;
    IF cr <> NEW.gross_credits_minor OR dr <> NEW.gross_debits_minor OR fe <> NEW.fees_minor
       OR (NEW.declared_item_count IS NOT NULL AND n <> NEW.declared_item_count) THEN
      RAISE EXCEPTION 'settlement batch % totals do not match its items (credits %/%, debits %/%, fees %/%, items %/%)',
        NEW.provider_batch_ref, cr, NEW.gross_credits_minor, dr, NEW.gross_debits_minor, fe, NEW.fees_minor,
        n, NEW.declared_item_count USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM recon.settlement_items WHERE settlement_batch_id = NEW.id AND match_status <> 'MATCHED') THEN
      RAISE EXCEPTION 'settlement batch % has unmatched items', NEW.provider_batch_ref USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;

-- Items are added only while the batch is RECEIVED or MATCHING.
CREATE FUNCTION recon.check_settlement_item_batch_open() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE s text;
BEGIN
  SELECT status INTO s FROM recon.settlement_batches WHERE id = NEW.settlement_batch_id;
  IF s IS NOT NULL AND s NOT IN ('RECEIVED', 'MATCHING') THEN
    RAISE EXCEPTION 'settlement batch % is %; items cannot be added', NEW.settlement_batch_id, s USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_settlement_batches_guard_status BEFORE INSERT OR UPDATE OF status ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('settlement_batch');
CREATE TRIGGER trg_settlement_batches_totals BEFORE UPDATE OF status ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.check_settlement_batch_totals();
CREATE TRIGGER trg_settlement_batches_freeze BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.freeze_terminal('CLOSED');
CREATE TRIGGER trg_settlement_batches_guard BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('status', 'reconciliation_item_id', 'ledger_posting_batch_id',
    'version', 'updated_at');
CREATE TRIGGER trg_settlement_batches_updated_at BEFORE UPDATE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_settlement_batches_no_delete BEFORE DELETE ON recon.settlement_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TRIGGER trg_settlement_items_batch_open BEFORE INSERT ON recon.settlement_items
  FOR EACH ROW EXECUTE FUNCTION recon.check_settlement_item_batch_open();
CREATE TRIGGER trg_settlement_items_guard BEFORE UPDATE ON recon.settlement_items
  FOR EACH ROW EXECUTE FUNCTION recon.guard_mutable_columns('net_signed_minor', 'reconciliation_item_id', 'subject_type', 'subject_id',
    'ledger_transaction_id', 'match_status', 'updated_at');
CREATE TRIGGER trg_settlement_items_updated_at BEFORE UPDATE ON recon.settlement_items
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_settlement_items_no_delete BEFORE DELETE ON recon.settlement_items
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_settlement_items_no_truncate BEFORE TRUNCATE ON recon.settlement_items
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
