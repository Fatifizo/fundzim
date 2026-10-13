-- Stage 10 (ADR-042; design ADR-006, ADR-014, ADR-023): the double-entry ledger, ported from the Stage 2 draft
-- design/sql/0006_ledger.sql (validated in PGlite) with the Stage 10 additions:
--   * ledger environment designation per database (ledger.ledger_environment; TEST in Stage 10, PRODUCTION refused:
--     ck_*_no_production_stage10) — every account and journal carries the environment and must equal it;
--   * versioned posting rules (code, version) — journals must use the current version;
--   * account status ACTIVE | FROZEN | CLOSED, checked under a row lock at posting time;
--   * journal evidence class, source system and source event id; synthetic fixtures are TEST + SIMULATED only;
--   * ledger_source_references (every source event considered for posting and the decision) and
--     ledger_reconciliation_references (exceptions found by reconciliation, incl. suspense investigations);
--   * a dedicated posting role fundzim_ledger: the ONLY role with write privileges on the ledger schema
--     (fundzim_app has none); audit and outbox through the SECURITY DEFINER gateways;
--   * permissions ledger.view, ledger.account.manage, ledger.period.close, ledger.reconciliation.operate,
--     ledger.synthetic.post.
-- Owner module: ledger. Schema: ledger (created by the foundation migration). Runs as fundzim_migrator.
-- Forward-only in production (ADR-028); Down drops the ledger objects for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- -----------------------------------------------------------------------------------------------------
-- 0. The fundzim_ledger role (LOGIN and password are set by the role administrator: deploy/docker/postgres/init).
-- -----------------------------------------------------------------------------------------------------
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_ledger') THEN
    RAISE EXCEPTION 'role fundzim_ledger is missing: create it as the role administrator first (deploy/docker/postgres/init/20-ledger-role.sh)';
  END IF;
END $$;

-- -----------------------------------------------------------------------------------------------------
-- 0.1 Ledger environment designation. One row per database, written by migrations only. A production database
--     is designated PRODUCTION by its own migration under an ADR; this database is TEST. Test and production
--     ledgers therefore never share a database: every account and journal must carry this database's value.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_environment (
  singleton     boolean     NOT NULL DEFAULT true,
  environment   text        NOT NULL,
  designated_at timestamptz NOT NULL DEFAULT now(),
  designated_by text        NOT NULL,
  note          text        NOT NULL,
  CONSTRAINT pk_ledger_environment PRIMARY KEY (singleton),
  CONSTRAINT ck_ledger_environment_singleton CHECK (singleton),
  CONSTRAINT ck_ledger_environment_value CHECK (environment IN ('TEST', 'PRODUCTION')),
  CONSTRAINT ck_ledger_environment_no_production_stage10 CHECK (environment <> 'PRODUCTION')
);
CREATE TRIGGER trg_ledger_environment_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_environment
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_environment_no_truncate BEFORE TRUNCATE ON ledger.ledger_environment
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
INSERT INTO ledger.ledger_environment (environment, designated_by, note)
VALUES ('TEST', 'migration 20261013120000_ledger', 'Stage 10: development/test ledger; synthetic fixtures only; production posting disabled');

CREATE FUNCTION ledger.database_environment() RETURNS text LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, ledger AS $$
  SELECT environment FROM ledger.ledger_environment WHERE singleton
$$;

CREATE FUNCTION ledger.enforce_environment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_TABLE_NAME = 'ledger_accounts' AND NOT EXISTS (
       SELECT 1 FROM app.currencies c WHERE c.code = NEW.currency AND c.minor_units_verified) THEN
    RAISE EXCEPTION 'currency % has unverified minor units; no ledger account may use it', NEW.currency USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.environment IS NULL THEN
    NEW.environment := ledger.database_environment();
  END IF;
  IF NEW.environment IS DISTINCT FROM ledger.database_environment() THEN
    RAISE EXCEPTION '% environment % differs from this database''s ledger environment %', TG_TABLE_NAME, NEW.environment,
      ledger.database_environment() USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ledger_account', '',       'ACTIVE'),
  ('ledger_account', 'ACTIVE', 'FROZEN'),
  ('ledger_account', 'FROZEN', 'ACTIVE'),
  ('ledger_account', 'ACTIVE', 'CLOSED'),
  ('ledger_account', 'FROZEN', 'CLOSED'),
  ('ledger_reconciliation_reference', '',              'OPEN'),
  ('ledger_reconciliation_reference', 'OPEN',          'INVESTIGATING'),
  ('ledger_reconciliation_reference', 'OPEN',          'RESOLVED'),
  ('ledger_reconciliation_reference', 'INVESTIGATING', 'RESOLVED');

-- -----------------------------------------------------------------------------------------------------
-- 1. Chart of accounts (settlement-and-custody-model §3.2), as an IMMUTABLE classification function.
--    account_class = the code without the owner suffix. Result: 'TYPE|NORMAL_BALANCE|KIND|OWNER_TYPE'.
--    KIND follows settlement model §3.1: CLAIM_ON_PROVIDER, PROVIDER_POOL, FUNDZIM_CASH, OBLIGATION,
--    RESULT, RECOVERABLE, SUSPENSE. Adding an account class is a migration + ADR-014 amendment.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION ledger.account_class_signature(p_class text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE p_class
    WHEN 'asset:psp_clearing'                  THEN 'ASSET|DEBIT|CLAIM_ON_PROVIDER|PROVIDER'
    WHEN 'asset:psp_settled'                   THEN 'ASSET|DEBIT|PROVIDER_POOL|PROVIDER'
    WHEN 'asset:fundzim_operating_bank'        THEN 'ASSET|DEBIT|FUNDZIM_CASH|PLATFORM'
    WHEN 'asset:chargeback_recoverable'        THEN 'ASSET|DEBIT|RECOVERABLE|PLATFORM'
    WHEN 'asset:refund_recoverable'            THEN 'ASSET|DEBIT|RECOVERABLE|CAMPAIGN'
    WHEN 'asset:psp_payout_float'              THEN 'ASSET|DEBIT|PROVIDER_POOL|PROVIDER'   -- not used in Model A
    WHEN 'liability:campaign_unsettled'        THEN 'LIABILITY|CREDIT|OBLIGATION|CAMPAIGN'
    WHEN 'liability:campaign_payable'          THEN 'LIABILITY|CREDIT|OBLIGATION|CAMPAIGN'
    WHEN 'liability:campaign_reserve'          THEN 'LIABILITY|CREDIT|OBLIGATION|CAMPAIGN'
    WHEN 'liability:campaign_payout_pending'   THEN 'LIABILITY|CREDIT|OBLIGATION|CAMPAIGN'
    WHEN 'liability:payout_in_transit'         THEN 'LIABILITY|CREDIT|OBLIGATION|PROVIDER'
    WHEN 'liability:campaign_held'             THEN 'LIABILITY|CREDIT|OBLIGATION|CAMPAIGN'
    WHEN 'liability:refund_payable'            THEN 'LIABILITY|CREDIT|OBLIGATION|PLATFORM'
    WHEN 'revenue:platform_fees'               THEN 'REVENUE|CREDIT|RESULT|PLATFORM'
    WHEN 'expense:psp_processing_fees'         THEN 'EXPENSE|DEBIT|RESULT|PLATFORM'
    WHEN 'expense:chargeback_losses'           THEN 'EXPENSE|DEBIT|RESULT|PLATFORM'
    WHEN 'expense:refund_losses'               THEN 'EXPENSE|DEBIT|RESULT|PLATFORM'
    WHEN 'equity:platform'                     THEN 'EQUITY|CREDIT|RESULT|PLATFORM'
    WHEN 'suspense:settlement_discrepancy'     THEN 'SUSPENSE|DEBIT|SUSPENSE|PROVIDER'
  END
$$;

-- -----------------------------------------------------------------------------------------------------
-- 2. Posting-rule registry (reference data; changed only by migrations).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_posting_rules (
  id                     uuid        NOT NULL,
  code                   text        NOT NULL,
  version                integer     NOT NULL DEFAULT 1,  -- Stage 10: rules are versioned; a change is a new row (migration)
  description            text        NOT NULL,
  idempotency_key_format text        NOT NULL,          -- documentation of the deterministic key, e.g. 'payment:{payment_id}:capture'
  allowed_source_types   text[]      NOT NULL,          -- ledger_transactions.source_type must be one of these
  line_check             text        NOT NULL DEFAULT 'RULE_LINES',
  requires_approval      boolean     NOT NULL DEFAULT false,  -- true: an APPROVED ledger_adjustments row is required (maker-checker)
  allows_prior_period    boolean     NOT NULL DEFAULT false,  -- true: occurred_at may fall in a CLOSED period (posted into the current OPEN one)
  single_owner_per_type  boolean     NOT NULL DEFAULT true,   -- true: at most one campaign and one provider per journal
  doc_ref                text        NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_posting_rules PRIMARY KEY (id),
  CONSTRAINT uq_ledger_posting_rules_code_version UNIQUE (code, version),
  CONSTRAINT ck_ledger_posting_rules_version CHECK (version >= 1),
  CONSTRAINT ck_ledger_posting_rules_code CHECK (code ~ '^[A-Z][A-Z0-9_]{2,62}$'),
  CONSTRAINT ck_ledger_posting_rules_line_check CHECK (line_check IN ('RULE_LINES', 'MIRROR_OF_REVERSED')),
  CONSTRAINT ck_ledger_posting_rules_mirror_is_reversal CHECK ((line_check = 'MIRROR_OF_REVERSED') = (code = 'REVERSAL')),
  CONSTRAINT ck_ledger_posting_rules_sources CHECK (cardinality(allowed_source_types) >= 1)
);
CREATE TRIGGER trg_ledger_posting_rules_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_posting_rules
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_posting_rules_no_truncate BEFORE TRUNCATE ON ledger.ledger_posting_rules
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Allowed (direction, account class) per rule. A journal line whose (direction, class) is not listed for its
-- rule is rejected at COMMIT (L12). is_required: the journal must contain at least one such line.
CREATE TABLE ledger.ledger_posting_rule_lines (
  id            uuid    NOT NULL,
  rule_code     text    NOT NULL,
  rule_version  integer NOT NULL DEFAULT 1,
  direction     text    NOT NULL,
  account_class text    NOT NULL,
  is_required   boolean NOT NULL DEFAULT false,
  CONSTRAINT pk_ledger_posting_rule_lines PRIMARY KEY (id),
  CONSTRAINT fk_ledger_posting_rule_lines_rule FOREIGN KEY (rule_code, rule_version) REFERENCES ledger.ledger_posting_rules (code, version) ON DELETE RESTRICT,
  CONSTRAINT uq_ledger_posting_rule_lines UNIQUE (rule_code, rule_version, direction, account_class),
  CONSTRAINT ck_ledger_posting_rule_lines_direction CHECK (direction IN ('DEBIT', 'CREDIT')),
  CONSTRAINT ck_ledger_posting_rule_lines_class CHECK (ledger.account_class_signature(account_class) IS NOT NULL),
  -- SC-1: a payout reservation may debit only campaign_payable.
  CONSTRAINT ck_ledger_posting_rule_lines_sc1 CHECK (
    NOT (rule_code = 'PAYOUT_RESERVED' AND direction = 'DEBIT') OR account_class = 'liability:campaign_payable'),
  -- SC-5: FundZim's own operating bank is touched only by fee remittance, fee true-up, pool funding,
  -- funding return, recovery and Model C direct-settlement rules (REVERSAL mirrors one of these).
  CONSTRAINT ck_ledger_posting_rule_lines_sc5 CHECK (
    account_class <> 'asset:fundzim_operating_bank'
    OR rule_code IN ('FEE_REMITTANCE', 'REFUND_FEE_TRUE_UP', 'REFUND_FEE_TRUE_UP_REVERSED', 'REFUND_FUND_SHORTFALL',
                     'DISPUTE_FUNDING', 'DISPUTE_FUNDING_RETURNED', 'RECOVERY_RECEIVED', 'SETTLEMENT_DIRECT')),
  -- Model A: the prefunded payout float is not used by any rule (LR-001, LR-003).
  CONSTRAINT ck_ledger_posting_rule_lines_no_float CHECK (account_class <> 'asset:psp_payout_float')
);
CREATE TRIGGER trg_ledger_posting_rule_lines_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_posting_rule_lines
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_posting_rule_lines_no_truncate BEFORE TRUNCATE ON ledger.ledger_posting_rule_lines
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. Accounting periods.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_periods (
  id                  uuid        NOT NULL,
  code                text        NOT NULL,          -- e.g. '2026-10'
  starts_at           timestamptz NOT NULL,
  ends_at             timestamptz NOT NULL,          -- exclusive
  status              text        NOT NULL,
  close_requested_by  uuid,                          -- staff user id (maker)
  close_requested_at  timestamptz,
  closed_by           uuid,                          -- staff user id (checker)
  closed_at           timestamptz,
  close_evidence_record_id uuid,                     -- trial balance + invariant run snapshot (audit.evidence_records)
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_periods PRIMARY KEY (id),
  CONSTRAINT uq_ledger_periods_code UNIQUE (code),
  CONSTRAINT ck_ledger_periods_range CHECK (ends_at > starts_at),
  CONSTRAINT ck_ledger_periods_status CHECK (status IN ('OPEN', 'CLOSING', 'CLOSED')),
  CONSTRAINT ck_ledger_periods_closing CHECK (status = 'OPEN' OR (close_requested_by IS NOT NULL AND close_requested_at IS NOT NULL)),
  CONSTRAINT ck_ledger_periods_closed CHECK (status <> 'CLOSED' OR (closed_by IS NOT NULL AND closed_at IS NOT NULL)),
  CONSTRAINT ck_ledger_periods_maker_checker CHECK (closed_by IS NULL OR closed_by <> close_requested_by),
  CONSTRAINT ex_ledger_periods_no_overlap EXCLUDE USING gist (tstzrange(starts_at, ends_at, '[)') WITH &&)
);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ledger_period', '',        'OPEN'),
  ('ledger_period', 'OPEN',    'CLOSING'),
  ('ledger_period', 'CLOSING', 'OPEN'),      -- close aborted (e.g. invariant run failed)
  ('ledger_period', 'CLOSING', 'CLOSED');    -- CLOSED is terminal; corrections are prior-period adjustments

CREATE FUNCTION ledger.guard_period_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id OR NEW.code <> OLD.code OR NEW.starts_at <> OLD.starts_at OR NEW.ends_at <> OLD.ends_at THEN
    RAISE EXCEPTION 'ledger period % boundaries are immutable', OLD.code USING ERRCODE = 'restrict_violation';
  END IF;
  NEW.version := OLD.version + 1;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_ledger_periods_guard_status BEFORE INSERT OR UPDATE OF status ON ledger.ledger_periods
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ledger_period');
CREATE TRIGGER trg_ledger_periods_guard_update BEFORE UPDATE ON ledger.ledger_periods
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_period_update();
CREATE TRIGGER trg_ledger_periods_keep_created_at BEFORE UPDATE ON ledger.ledger_periods
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_ledger_periods_updated_at BEFORE UPDATE ON ledger.ledger_periods
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ledger_periods_no_delete BEFORE DELETE ON ledger.ledger_periods
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_periods_no_truncate BEFORE TRUNCATE ON ledger.ledger_periods
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. Accounts (one row per code per currency; created lazily and idempotently on first posting:
--    INSERT ... ON CONFLICT ON CONSTRAINT uq_ledger_accounts_code_currency DO NOTHING).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_accounts (
  id             uuid        NOT NULL,
  code           text        NOT NULL,           -- e.g. 'liability:campaign_payable:{campaign_id}'
  account_class  text        NOT NULL,           -- e.g. 'liability:campaign_payable'
  type           text        NOT NULL,
  normal_balance text        NOT NULL,
  kind           text        NOT NULL,
  currency       char(3)     NOT NULL,
  owner_type     text        NOT NULL,           -- PLATFORM | CAMPAIGN | PROVIDER (no FK: ledger is domain-agnostic)
  owner_id       uuid,                           -- campaign id or psp provider id; NULL for PLATFORM
  environment    text        NOT NULL,           -- Stage 10: TEST | PRODUCTION; must equal the database's designation
  status         text        NOT NULL DEFAULT 'ACTIVE',  -- Stage 10: ACTIVE | FROZEN | CLOSED (status machine ledger_account)
  status_reason  text,
  status_changed_by uuid,                        -- staff user id
  status_changed_at timestamptz,
  version        integer     NOT NULL DEFAULT 1,
  updated_at     timestamptz NOT NULL DEFAULT now(),
  non_negative   boolean     GENERATED ALWAYS AS (kind IN ('OBLIGATION', 'RECOVERABLE')) STORED,  -- L8 / L13
  -- SYNC: projection row updated in the posting transaction under its row lock (decision-guarding accounts).
  -- DEFERRED: entries are appended only; ledger.fold_deferred_balances() folds them into the projection
  -- (platform-wide hot rows: revenue, PSP clearing/settled, suspense, operating bank, expenses, equity).
  projection_mode text       GENERATED ALWAYS AS (CASE WHEN kind IN ('OBLIGATION', 'RECOVERABLE') THEN 'SYNC'
                                                       ELSE 'DEFERRED' END) STORED,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_accounts PRIMARY KEY (id),
  CONSTRAINT uq_ledger_accounts_code_currency UNIQUE (code, currency),
  CONSTRAINT uq_ledger_accounts_id_currency UNIQUE (id, currency),          -- target of the L4 composite FK
  CONSTRAINT fk_ledger_accounts_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_accounts_environment CHECK (environment IN ('TEST', 'PRODUCTION')),
  CONSTRAINT ck_ledger_accounts_no_production_stage10 CHECK (environment <> 'PRODUCTION'),
  CONSTRAINT ck_ledger_accounts_status CHECK (status IN ('ACTIVE', 'FROZEN', 'CLOSED')),
  CONSTRAINT ck_ledger_accounts_status_change CHECK (status = 'ACTIVE' OR (status_reason IS NOT NULL AND status_changed_at IS NOT NULL)),
  CONSTRAINT ck_ledger_accounts_type CHECK (type IN ('ASSET', 'LIABILITY', 'EQUITY', 'REVENUE', 'EXPENSE', 'SUSPENSE')),
  CONSTRAINT ck_ledger_accounts_normal_balance CHECK (
       (type IN ('ASSET', 'EXPENSE', 'SUSPENSE') AND normal_balance = 'DEBIT')
    OR (type IN ('LIABILITY', 'EQUITY', 'REVENUE') AND normal_balance = 'CREDIT')),
  CONSTRAINT ck_ledger_accounts_kind CHECK (kind IN ('CLAIM_ON_PROVIDER', 'PROVIDER_POOL', 'FUNDZIM_CASH', 'OBLIGATION',
                                                     'RESULT', 'RECOVERABLE', 'SUSPENSE')),
  CONSTRAINT ck_ledger_accounts_owner CHECK (
       (owner_type = 'PLATFORM' AND owner_id IS NULL)
    OR (owner_type IN ('CAMPAIGN', 'PROVIDER') AND owner_id IS NOT NULL)),
  -- The class decides type, normal balance, kind and owner type (chart of accounts is closed).
  CONSTRAINT ck_ledger_accounts_class_spec CHECK (
    ledger.account_class_signature(account_class) IS NOT DISTINCT FROM
      (type || '|' || normal_balance || '|' || kind || '|' || owner_type)),
  -- L8 depends only on SYNC accounts: every account whose non-negativity guards a decision is SYNC.
  CONSTRAINT ck_ledger_accounts_guarded_is_sync CHECK (NOT non_negative OR projection_mode = 'SYNC'),
  -- The code is derived deterministically: class, plus ':' || owner_id for owned accounts.
  CONSTRAINT ck_ledger_accounts_code CHECK (
    code = account_class || CASE WHEN owner_id IS NULL THEN '' ELSE ':' || owner_id::text END)
);
CREATE INDEX ix_ledger_accounts_owner ON ledger.ledger_accounts (owner_type, owner_id) WHERE owner_id IS NOT NULL;
-- Accounts are immutable except their status (currency immutable is part of L4). Stage 10: only status,
-- status_reason, status_changed_by/at, version and updated_at may change; CLOSED is terminal; closing needs a zero
-- current balance.
CREATE FUNCTION ledger.guard_account_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  bal bigint;
BEGIN
  -- generated columns (non_negative, projection_mode) are not yet computed in a BEFORE trigger; they derive from kind
  IF (to_jsonb(NEW) - ARRAY['status','status_reason','status_changed_by','status_changed_at','version','updated_at','non_negative','projection_mode'])
     IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','status_reason','status_changed_by','status_changed_at','version','updated_at','non_negative','projection_mode']) THEN
    RAISE EXCEPTION 'ledger account % is immutable except its status', OLD.code USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status = 'CLOSED' AND OLD.status <> 'CLOSED' THEN
    SELECT balance_minor INTO bal FROM ledger.v_balances_current WHERE account_id = OLD.id;
    IF coalesce(bal, 0) <> 0 THEN
      RAISE EXCEPTION 'ledger account % cannot be closed with a non-zero balance (%)', OLD.code, bal USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  NEW.version := OLD.version + 1;
  NEW.updated_at := now();
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ledger_accounts_guard_update BEFORE UPDATE ON ledger.ledger_accounts
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_account_update();
CREATE TRIGGER trg_ledger_accounts_guard_status BEFORE INSERT OR UPDATE OF status ON ledger.ledger_accounts
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ledger_account');
CREATE TRIGGER trg_ledger_accounts_environment BEFORE INSERT ON ledger.ledger_accounts
  FOR EACH ROW EXECUTE FUNCTION ledger.enforce_environment();
CREATE TRIGGER trg_ledger_accounts_no_delete BEFORE DELETE ON ledger.ledger_accounts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_accounts_no_truncate BEFORE TRUNCATE ON ledger.ledger_accounts
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 5. Balance projection (LEDGER §8). Balance is stored in NORMAL-BALANCE terms: for a DEBIT-normal account
--    balance = debits - credits; for a CREDIT-normal account balance = credits - debits. One row per account,
--    created with the account.
--    SYNC rows are maintained by the entry trigger in the same DB transaction as the posting; the CHECK
--    ck_ledger_balances_non_negative is the database backstop for L8 (campaign_payable never below zero),
--    generalised as L13 to every OBLIGATION and RECOVERABLE account (all SYNC).
--    DEFERRED rows hold the balance of every entry whose DB transaction id (created_txid) is below
--    folded_through_txid; ledger.fold_deferred_balances() advances the watermark to the snapshot xmin, so no
--    in-flight transaction can ever commit below it (exact, no lost or double-counted entries).
--    Exact current balance of any account: ledger.v_balances_current (projection + unfolded tail).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_balances (
  account_id          uuid        NOT NULL,        -- 1:1 with ledger_accounts (LEDGER §8 names it account_id)
  currency            char(3)     NOT NULL,
  normal_balance      text        NOT NULL,
  non_negative        boolean     NOT NULL,
  projection_mode     text        NOT NULL,
  folded_through_txid xid8,                        -- DEFERRED only: entries with created_txid < this are included
  debit_total_minor   bigint      NOT NULL DEFAULT 0,
  credit_total_minor  bigint      NOT NULL DEFAULT 0,
  balance_minor       bigint      NOT NULL DEFAULT 0,
  entry_count         bigint      NOT NULL DEFAULT 0,
  last_entry_id       uuid,
  last_transaction_id uuid,
  version             bigint      NOT NULL DEFAULT 0,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_balances PRIMARY KEY (account_id),
  CONSTRAINT fk_ledger_balances_account FOREIGN KEY (account_id, currency)
    REFERENCES ledger.ledger_accounts (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_balances_totals CHECK (debit_total_minor >= 0 AND credit_total_minor >= 0 AND entry_count >= 0),
  CONSTRAINT ck_ledger_balances_consistent CHECK (
    balance_minor = CASE normal_balance WHEN 'DEBIT' THEN debit_total_minor - credit_total_minor
                                        ELSE credit_total_minor - debit_total_minor END),
  CONSTRAINT ck_ledger_balances_mode CHECK (
       (projection_mode = 'SYNC' AND folded_through_txid IS NULL)
    OR (projection_mode = 'DEFERRED' AND folded_through_txid IS NOT NULL AND NOT non_negative)),
  CONSTRAINT ck_ledger_balances_non_negative CHECK (NOT non_negative OR balance_minor >= 0)
);

CREATE FUNCTION ledger.create_balance_row() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, ledger AS $$
BEGIN
  -- A DEFERRED watermark starts at the creating transaction: no entry for the account can predate it.
  INSERT INTO ledger.ledger_balances (account_id, currency, normal_balance, non_negative, projection_mode, folded_through_txid)
  VALUES (NEW.id, NEW.currency, NEW.normal_balance, NEW.non_negative, NEW.projection_mode,
          CASE WHEN NEW.projection_mode = 'DEFERRED' THEN pg_current_xact_id() END);
  RETURN NULL;
END $$;
CREATE TRIGGER trg_ledger_accounts_create_balance AFTER INSERT ON ledger.ledger_accounts
  FOR EACH ROW EXECUTE FUNCTION ledger.create_balance_row();

-- Direct UPDATEs of the projection are rejected. Allowed only (a) from the entry trigger (trigger depth >= 2)
-- or (b) inside ledger.fold_deferred_balances() / ledger.rebuild_balance(), which set a transaction-local flag.
CREATE FUNCTION ledger.guard_balance_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- Stage 10: the maintenance flag counts only for the table owner (the SECURITY DEFINER fold/rebuild functions);
  -- any role can set a custom GUC, so the flag alone must never open the projection to a runtime role.
  IF pg_trigger_depth() < 2 AND NOT (coalesce(current_setting('ledger.projection_maintenance', true), '') = 'on'
       AND current_user = (SELECT pg_get_userbyid(c.relowner) FROM pg_class c WHERE c.oid = TG_RELID)) THEN
    RAISE EXCEPTION 'ledger_balances is a projection maintained only by ledger postings (direct UPDATE rejected)'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.account_id <> OLD.account_id OR NEW.currency <> OLD.currency OR NEW.normal_balance <> OLD.normal_balance
     OR NEW.non_negative <> OLD.non_negative OR NEW.projection_mode <> OLD.projection_mode THEN
    RAISE EXCEPTION 'ledger_balances identity columns are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ledger_balances_guard_update BEFORE UPDATE ON ledger.ledger_balances
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_balance_update();
CREATE TRIGGER trg_ledger_balances_no_delete BEFORE DELETE ON ledger.ledger_balances
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_balances_no_truncate BEFORE TRUNCATE ON ledger.ledger_balances
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 6. Posting batches (optional grouping, e.g. all SETTLEMENT_MATCHED journals of one provider settlement batch).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_posting_batches (
  id              uuid        NOT NULL,
  batch_kind      text        NOT NULL,
  currency        char(3)     NOT NULL,
  idempotency_key text        NOT NULL,         -- e.g. 'settlement_batch:{settlement_batch_id}'
  source_type     text        NOT NULL,         -- e.g. 'settlement_batch'
  source_id       uuid        NOT NULL,
  description     text        NOT NULL,
  created_by_type text        NOT NULL,
  created_by_id   text        NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_posting_batches PRIMARY KEY (id),
  CONSTRAINT uq_ledger_posting_batches_idempotency_key UNIQUE (idempotency_key),
  CONSTRAINT uq_ledger_posting_batches_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_ledger_posting_batches_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_posting_batches_kind CHECK (batch_kind IN ('SETTLEMENT', 'FEE_REMITTANCE', 'BULK_REFUND', 'OTHER')),
  CONSTRAINT ck_ledger_posting_batches_created_by CHECK (created_by_type IN ('SYSTEM', 'STAFF'))
);
CREATE TRIGGER trg_ledger_posting_batches_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_posting_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_posting_batches_no_truncate BEFORE TRUNCATE ON ledger.ledger_posting_batches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 7. Journal headers. One balanced, single-currency journal per business event (L1, L6, SC-6).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_transactions (
  id                      uuid        NOT NULL,
  posting_rule            text        NOT NULL,
  posting_rule_version    integer     NOT NULL,  -- Stage 10: must be the rule's current (highest) version
  environment             text        NOT NULL,  -- Stage 10: TEST | PRODUCTION; must equal the database's designation
  source_system           text        NOT NULL,  -- Stage 10: payments | reconciliation | staff | synthetic_fixture
  source_event_id         text,                  -- Stage 10: the business event behind the journal (e.g. outbox event id)
  evidence_class          text        NOT NULL,  -- Stage 10: SIMULATED | PROVIDER_REPORTED | PROVIDER_VERIFIED | SETTLEMENT_VERIFIED | MANUALLY_REVIEWED
  metadata                jsonb       NOT NULL DEFAULT '{}',  -- ids and labels only (e.g. {"fixture":"TEST_ONLY"})
  idempotency_key         text        NOT NULL,  -- deterministic from the business event (L6)
  currency                char(3)     NOT NULL,  -- single-currency journal (SC-6); every entry must match
  description             text        NOT NULL,
  source_type             text        NOT NULL,  -- payment | refund | dispute | payout | settlement_item | ...
  source_id               uuid        NOT NULL,  -- no FK: ledger is domain-agnostic
  provider_code           text,                  -- informational (provider registry lives in psp)
  provider_reference      text,
  fee_schedule_version_id uuid,                  -- fees.fee_schedule_versions id used (no FK)
  occurred_at             timestamptz NOT NULL,  -- business time (UTC)
  posted_at               timestamptz NOT NULL DEFAULT now(),  -- forced to now() by trigger
  accounting_period_id    uuid        NOT NULL,  -- set by trigger
  is_prior_period         boolean     NOT NULL DEFAULT false,  -- set by trigger: occurred_at in a CLOSED period
  reverses_transaction_id uuid,                  -- L7
  adjustment_id           uuid,                  -- maker-checker request that authorised this journal
  batch_id                uuid,
  created_by_type         text        NOT NULL,
  created_by_id           text        NOT NULL,  -- staff user id or system component name (e.g. 'payments.capture')
  reason                  text,
  request_id              text,
  correlation_id          text,
  created_txid            xid8        NOT NULL DEFAULT pg_current_xact_id(),  -- L11: entries only in this DB tx
  CONSTRAINT pk_ledger_transactions PRIMARY KEY (id),
  CONSTRAINT uq_ledger_transactions_idempotency_key UNIQUE (idempotency_key),
  CONSTRAINT uq_ledger_transactions_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_ledger_transactions_rule FOREIGN KEY (posting_rule, posting_rule_version) REFERENCES ledger.ledger_posting_rules (code, version) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_transactions_environment CHECK (environment IN ('TEST', 'PRODUCTION')),
  -- Stage 10: production posting is disabled. Lifting this needs an ADR, accounting and legal approval and owner acceptance.
  CONSTRAINT ck_ledger_transactions_no_production_stage10 CHECK (environment <> 'PRODUCTION'),
  CONSTRAINT ck_ledger_transactions_source_system CHECK (source_system IN ('payments', 'reconciliation', 'staff', 'synthetic_fixture')),
  CONSTRAINT ck_ledger_transactions_evidence_class CHECK (evidence_class IN ('SIMULATED', 'PROVIDER_REPORTED', 'PROVIDER_VERIFIED',
                                                                            'SETTLEMENT_VERIFIED', 'MANUALLY_REVIEWED')),
  -- Simulated evidence and synthetic fixtures never reach a production ledger; production needs verified evidence.
  CONSTRAINT ck_ledger_transactions_synthetic_is_test CHECK (source_system <> 'synthetic_fixture' OR (environment = 'TEST' AND evidence_class = 'SIMULATED')),
  CONSTRAINT ck_ledger_transactions_production_evidence CHECK (environment <> 'PRODUCTION'
    OR evidence_class IN ('PROVIDER_VERIFIED', 'SETTLEMENT_VERIFIED', 'MANUALLY_REVIEWED')),
  CONSTRAINT ck_ledger_transactions_metadata CHECK (jsonb_typeof(metadata) = 'object' AND octet_length(metadata::text) <= 4096),
  CONSTRAINT fk_ledger_transactions_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_transactions_period FOREIGN KEY (accounting_period_id) REFERENCES ledger.ledger_periods (id) ON DELETE RESTRICT,
  -- A reversal has the same currency as the original (composite FK).
  CONSTRAINT fk_ledger_transactions_reverses FOREIGN KEY (reverses_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_transactions_batch FOREIGN KEY (batch_id, currency)
    REFERENCES ledger.ledger_posting_batches (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_transactions_key CHECK (idempotency_key ~ '^[a-z_]+:[A-Za-z0-9:_.-]{1,250}$'),
  CONSTRAINT ck_ledger_transactions_not_self_reversal CHECK (reverses_transaction_id IS NULL OR reverses_transaction_id <> id),
  CONSTRAINT ck_ledger_transactions_reversal_rule CHECK ((posting_rule = 'REVERSAL') = (reverses_transaction_id IS NOT NULL)),
  CONSTRAINT ck_ledger_transactions_created_by CHECK (created_by_type IN ('SYSTEM', 'STAFF')),
  CONSTRAINT ck_ledger_transactions_reason CHECK (
    (reverses_transaction_id IS NULL AND adjustment_id IS NULL) OR length(btrim(coalesce(reason, ''))) >= 10),
  CONSTRAINT ck_ledger_transactions_source_type CHECK (source_type ~ '^[a-z_]{3,40}$')
);
-- L7: an original is reversed at most once.
CREATE UNIQUE INDEX uq_ledger_transactions_reverses ON ledger.ledger_transactions (reverses_transaction_id)
  WHERE reverses_transaction_id IS NOT NULL;
-- An adjustment produces at most one reversal journal and at most one non-reversal journal.
CREATE UNIQUE INDEX uq_ledger_transactions_adjustment_reversal ON ledger.ledger_transactions (adjustment_id)
  WHERE adjustment_id IS NOT NULL AND posting_rule = 'REVERSAL';
CREATE UNIQUE INDEX uq_ledger_transactions_adjustment_result ON ledger.ledger_transactions (adjustment_id)
  WHERE adjustment_id IS NOT NULL AND posting_rule <> 'REVERSAL';
CREATE INDEX ix_ledger_transactions_source ON ledger.ledger_transactions (source_type, source_id);
CREATE INDEX ix_ledger_transactions_rule_occurred ON ledger.ledger_transactions (posting_rule, occurred_at);
CREATE INDEX ix_ledger_transactions_period ON ledger.ledger_transactions (accounting_period_id);
CREATE INDEX ix_ledger_transactions_posted_at ON ledger.ledger_transactions (posted_at, id);
CREATE INDEX ix_ledger_transactions_batch ON ledger.ledger_transactions (batch_id) WHERE batch_id IS NOT NULL;

CREATE TRIGGER trg_ledger_transactions_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_transactions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_transactions_no_truncate BEFORE TRUNCATE ON ledger.ledger_transactions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 8. Entries (postings). amount > 0, direction carries the sign (L3).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_entries (
  id             uuid     NOT NULL,
  transaction_id uuid     NOT NULL,
  line_no        smallint NOT NULL,
  account_id     uuid     NOT NULL,
  direction      text     NOT NULL,
  amount_minor   bigint   NOT NULL,
  currency       char(3)  NOT NULL,
  created_txid   xid8     NOT NULL DEFAULT pg_current_xact_id(),  -- forced by trigger; = header created_txid (L11)
  CONSTRAINT pk_ledger_entries PRIMARY KEY (id),
  CONSTRAINT uq_ledger_entries_line UNIQUE (transaction_id, line_no),
  -- SC-6: entry currency = journal currency.
  CONSTRAINT fk_ledger_entries_transaction_currency FOREIGN KEY (transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  -- L4: entry currency = account currency.
  CONSTRAINT fk_ledger_entries_account_currency FOREIGN KEY (account_id, currency)
    REFERENCES ledger.ledger_accounts (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_entries_direction CHECK (direction IN ('DEBIT', 'CREDIT')),
  CONSTRAINT ck_ledger_entries_amount_positive CHECK (amount_minor > 0),
  CONSTRAINT ck_ledger_entries_line_no CHECK (line_no >= 1)
);
-- Account statement / recompute / DEFERRED fold path (commit-safe ordering by DB transaction id).
-- Replaces LEDGER §3.3's (account_id, transaction_id): journal lookups are served by uq_ledger_entries_line.
CREATE INDEX ix_ledger_entries_account_txid ON ledger.ledger_entries (account_id, created_txid);
CREATE TRIGGER trg_ledger_entries_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_entries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_entries_no_truncate BEFORE TRUNCATE ON ledger.ledger_entries
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 9. Adjustments (maker-checker requests for manual adjustments, reversals and corrections).
--    kind REVERSAL   : posts one REVERSAL journal mirroring target_transaction_id; proposed_lines = [].
--    kind CORRECTION : posts the REVERSAL of target_transaction_id AND a replacement journal under
--                      posting_rule whose lines equal proposed_lines.
--    kind MANUAL     : posts one journal under posting_rule (ADJUSTMENT, WRITE_OFF, *_FUNDING, ...) whose
--                      lines equal proposed_lines.
--    proposed_lines: [{"account_code": "...", "direction": "DEBIT"|"CREDIT", "amount_minor": "175"}, ...]
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_adjustments (
  id                      uuid        NOT NULL,
  kind                    text        NOT NULL,
  posting_rule            text        NOT NULL,
  posting_rule_version    integer     NOT NULL DEFAULT 1,
  currency                char(3)     NOT NULL,
  target_transaction_id   uuid,
  proposed_lines          jsonb       NOT NULL,
  proposed_lines_sha256   bytea       NOT NULL,
  reason_code             text        NOT NULL,     -- controlled list in Go (e.g. POSTING_ERROR, RECON_DISCREPANCY)
  reason                  text        NOT NULL,
  linked_subject_type     text,                     -- e.g. 'reconciliation_discrepancy', 'recovery_case'
  linked_subject_id       uuid,
  evidence_record_ids     uuid[]      NOT NULL DEFAULT '{}',
  status                  text        NOT NULL,
  requested_by            uuid        NOT NULL,     -- staff user id (maker)
  requested_at            timestamptz NOT NULL DEFAULT now(),
  expires_at              timestamptz NOT NULL,
  approved_by             uuid,                     -- staff user id (checker)
  approved_at             timestamptz,
  approved_lines_sha256   bytea,                    -- the hash the checker saw and approved
  approval_justification  text,
  rejected_by             uuid,
  rejected_at             timestamptz,
  rejection_reason        text,
  reversal_transaction_id uuid,
  resulting_transaction_id uuid,
  posted_at               timestamptz,
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_adjustments PRIMARY KEY (id),
  CONSTRAINT fk_ledger_adjustments_rule FOREIGN KEY (posting_rule, posting_rule_version) REFERENCES ledger.ledger_posting_rules (code, version) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_adjustments_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_adjustments_target FOREIGN KEY (target_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_adjustments_reversal_tx FOREIGN KEY (reversal_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_adjustments_result_tx FOREIGN KEY (resulting_transaction_id, currency)
    REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_adjustments_kind CHECK (kind IN ('REVERSAL', 'CORRECTION', 'MANUAL')),
  CONSTRAINT ck_ledger_adjustments_kind_rule CHECK ((kind = 'REVERSAL') = (posting_rule = 'REVERSAL')),
  CONSTRAINT ck_ledger_adjustments_target CHECK ((kind IN ('REVERSAL', 'CORRECTION')) = (target_transaction_id IS NOT NULL)),
  CONSTRAINT ck_ledger_adjustments_lines CHECK (
    jsonb_typeof(proposed_lines) = 'array'
    AND CASE WHEN kind = 'REVERSAL' THEN jsonb_array_length(proposed_lines) = 0
             ELSE jsonb_array_length(proposed_lines) >= 2 END),
  CONSTRAINT ck_ledger_adjustments_lines_hash CHECK (proposed_lines_sha256 = sha256(convert_to(proposed_lines::text, 'UTF8'))),
  CONSTRAINT ck_ledger_adjustments_reason CHECK (length(btrim(reason)) >= 10),
  CONSTRAINT ck_ledger_adjustments_status CHECK (status IN ('PENDING_APPROVAL', 'APPROVED', 'POSTED', 'REJECTED', 'WITHDRAWN', 'EXPIRED')),
  CONSTRAINT ck_ledger_adjustments_expiry CHECK (expires_at > requested_at),
  -- Maker-checker (CLAUDE.md, operational-controls §3/§4.1): the checker is never the maker.
  CONSTRAINT ck_ledger_adjustments_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by),
  CONSTRAINT ck_ledger_adjustments_rejecter CHECK (rejected_by IS NULL OR rejected_by <> requested_by),
  CONSTRAINT ck_ledger_adjustments_approval CHECK (
    status NOT IN ('APPROVED', 'POSTED')
    OR (approved_by IS NOT NULL AND approved_at IS NOT NULL AND approved_at <= expires_at
        AND approved_lines_sha256 = proposed_lines_sha256 AND length(btrim(coalesce(approval_justification, ''))) > 0)),
  CONSTRAINT ck_ledger_adjustments_rejection CHECK (status <> 'REJECTED' OR (rejected_by IS NOT NULL AND rejection_reason IS NOT NULL)),
  CONSTRAINT ck_ledger_adjustments_posted CHECK (
    status <> 'POSTED'
    OR (posted_at IS NOT NULL
        AND (kind = 'MANUAL' OR reversal_transaction_id IS NOT NULL)
        AND (kind = 'REVERSAL' OR resulting_transaction_id IS NOT NULL)))
);
CREATE INDEX ix_ledger_adjustments_open ON ledger.ledger_adjustments (status, expires_at)
  WHERE status IN ('PENDING_APPROVAL', 'APPROVED');
CREATE INDEX ix_ledger_adjustments_target ON ledger.ledger_adjustments (target_transaction_id) WHERE target_transaction_id IS NOT NULL;

ALTER TABLE ledger.ledger_transactions
  ADD CONSTRAINT fk_ledger_transactions_adjustment FOREIGN KEY (adjustment_id)
  REFERENCES ledger.ledger_adjustments (id) ON DELETE RESTRICT;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ledger_adjustment', '',                 'PENDING_APPROVAL'),
  ('ledger_adjustment', 'PENDING_APPROVAL', 'APPROVED'),
  ('ledger_adjustment', 'PENDING_APPROVAL', 'REJECTED'),
  ('ledger_adjustment', 'PENDING_APPROVAL', 'WITHDRAWN'),
  ('ledger_adjustment', 'PENDING_APPROVAL', 'EXPIRED'),
  ('ledger_adjustment', 'APPROVED',         'POSTED'),
  ('ledger_adjustment', 'APPROVED',         'EXPIRED');

-- The request (what the maker asked for and the checker approved) is immutable after creation.
CREATE FUNCTION ledger.guard_adjustment_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','approved_by','approved_at','approved_lines_sha256','approval_justification',
                            'rejected_by','rejected_at','rejection_reason','reversal_transaction_id',
                            'resulting_transaction_id','posted_at','version','updated_at'])
     IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','approved_by','approved_at','approved_lines_sha256','approval_justification',
                            'rejected_by','rejected_at','rejection_reason','reversal_transaction_id',
                            'resulting_transaction_id','posted_at','version','updated_at']) THEN
    RAISE EXCEPTION 'ledger adjustment % request fields are immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status IN ('POSTED', 'REJECTED', 'WITHDRAWN', 'EXPIRED') THEN
    RAISE EXCEPTION 'ledger adjustment % is final (%)', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  NEW.version := OLD.version + 1;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ledger_adjustments_guard_status BEFORE INSERT OR UPDATE OF status ON ledger.ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ledger_adjustment');
CREATE TRIGGER trg_ledger_adjustments_guard_update BEFORE UPDATE ON ledger.ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_adjustment_update();
CREATE TRIGGER trg_ledger_adjustments_updated_at BEFORE UPDATE ON ledger.ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ledger_adjustments_no_delete BEFORE DELETE ON ledger.ledger_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_adjustments_no_truncate BEFORE TRUNCATE ON ledger.ledger_adjustments
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 10. Invariant verification runs (L9, L10, SC-2, SC-3, SC-4 ...). One APPEND-ONLY row per finished run,
--     written by the verification job (docs/architecture/background-processing.md §6.6); a rerun is a new row.
--     In-progress state lives in the River job, not here.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_invariant_runs (
  id                      uuid        NOT NULL,
  invariant_code          text        NOT NULL,     -- 'L9', 'L10', 'SC-2', ...
  scope                   jsonb       NOT NULL DEFAULT '{}',  -- e.g. {"currency":"USD","provider_id":"..."}
  currency                char(3),
  status                  text        NOT NULL,
  started_at              timestamptz NOT NULL,
  finished_at             timestamptz NOT NULL,
  snapshot_posted_through timestamptz,             -- REPEATABLE READ snapshot boundary used
  checked_count           bigint      NOT NULL DEFAULT 0,
  violation_count         bigint      NOT NULL DEFAULT 0,
  violations_sample       jsonb       NOT NULL DEFAULT '[]',  -- ids and amounts only; never personal data
  evidence_record_id      uuid,                     -- SYSTEM_SNAPSHOT in audit.evidence_records
  triggered_by_type       text        NOT NULL,
  triggered_by_id         text        NOT NULL,
  job_id                  text,
  error_message           text,
  CONSTRAINT pk_ledger_invariant_runs PRIMARY KEY (id),
  CONSTRAINT fk_ledger_invariant_runs_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_invariant_runs_code CHECK (invariant_code ~ '^(L[0-9]{1,2}|SC-[0-9]{1,2})$'),
  CONSTRAINT ck_ledger_invariant_runs_status CHECK (status IN ('PASSED', 'FAILED', 'ERROR')),
  CONSTRAINT ck_ledger_invariant_runs_times CHECK (finished_at >= started_at),
  CONSTRAINT ck_ledger_invariant_runs_passed CHECK (status <> 'PASSED' OR violation_count = 0),
  CONSTRAINT ck_ledger_invariant_runs_failed CHECK (status <> 'FAILED' OR (violation_count > 0 AND evidence_record_id IS NOT NULL)),
  CONSTRAINT ck_ledger_invariant_runs_error CHECK (status <> 'ERROR' OR error_message IS NOT NULL),
  CONSTRAINT ck_ledger_invariant_runs_counts CHECK (checked_count >= 0 AND violation_count >= 0),
  CONSTRAINT ck_ledger_invariant_runs_triggered_by CHECK (triggered_by_type IN ('SYSTEM', 'STAFF'))
);
CREATE INDEX ix_ledger_invariant_runs_code_started ON ledger.ledger_invariant_runs (invariant_code, started_at DESC);
CREATE TRIGGER trg_ledger_invariant_runs_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_invariant_runs
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_invariant_runs_no_truncate BEFORE TRUNCATE ON ledger.ledger_invariant_runs
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 11. Posting triggers.
-- -----------------------------------------------------------------------------------------------------

-- 11.1 BEFORE INSERT on journal headers: source type, approval (maker-checker), period, posted_at, txid.
CREATE FUNCTION ledger.before_insert_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r   ledger.ledger_posting_rules%ROWTYPE;
  adj ledger.ledger_adjustments%ROWTYPE;
  occ ledger.ledger_periods%ROWTYPE;
  cur ledger.ledger_periods%ROWTYPE;
BEGIN
  NEW.posted_at    := now();                 -- FundZim recording time; cannot be back- or forward-dated
  NEW.created_txid := pg_current_xact_id();  -- L11 seal

  IF NEW.environment IS DISTINCT FROM ledger.database_environment() THEN
    RAISE EXCEPTION 'journal environment % differs from this database''s ledger environment %', NEW.environment,
      ledger.database_environment() USING ERRCODE = 'check_violation';
  END IF;
  SELECT * INTO r FROM ledger.ledger_posting_rules WHERE code = NEW.posting_rule AND version = NEW.posting_rule_version;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'unknown posting rule % v%', NEW.posting_rule, NEW.posting_rule_version USING ERRCODE = 'foreign_key_violation';
  END IF;
  -- Stage 10: only currencies with verified minor units (ZWG stays out: LR-043, PCR-018).
  IF NOT EXISTS (SELECT 1 FROM app.currencies c WHERE c.code = NEW.currency AND c.minor_units_verified) THEN
    RAISE EXCEPTION 'currency % has unverified minor units; it cannot be posted', NEW.currency USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.posting_rule_version <> (SELECT max(version) FROM ledger.ledger_posting_rules WHERE code = NEW.posting_rule) THEN
    RAISE EXCEPTION 'posting rule % v% is superseded', NEW.posting_rule, NEW.posting_rule_version USING ERRCODE = 'check_violation';
  END IF;
  IF NOT (NEW.source_type = ANY (r.allowed_source_types)) THEN
    RAISE EXCEPTION 'posting rule % does not accept source_type %', r.code, NEW.source_type USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.occurred_at > NEW.posted_at + interval '15 minutes' THEN
    RAISE EXCEPTION 'occurred_at % is in the future', NEW.occurred_at USING ERRCODE = 'check_violation';
  END IF;

  -- Maker-checker: rules that require approval need an APPROVED, unexpired adjustment of matching shape.
  IF NEW.adjustment_id IS NOT NULL THEN
    SELECT * INTO adj FROM ledger.ledger_adjustments WHERE id = NEW.adjustment_id FOR UPDATE;
    IF NOT FOUND OR adj.status <> 'APPROVED' THEN
      RAISE EXCEPTION 'journal % requires an APPROVED adjustment (adjustment % is %)',
        NEW.idempotency_key, NEW.adjustment_id, coalesce(adj.status, 'missing') USING ERRCODE = 'check_violation';
    END IF;
    IF adj.expires_at < now() THEN
      RAISE EXCEPTION 'adjustment % expired at %', adj.id, adj.expires_at USING ERRCODE = 'check_violation';
    END IF;
    IF adj.currency <> NEW.currency THEN
      RAISE EXCEPTION 'adjustment % currency % differs from journal currency %', adj.id, adj.currency, NEW.currency
        USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.posting_rule = 'REVERSAL' THEN
      IF adj.kind NOT IN ('REVERSAL', 'CORRECTION') OR NEW.reverses_transaction_id <> adj.target_transaction_id THEN
        RAISE EXCEPTION 'adjustment % does not authorise reversing %', adj.id, NEW.reverses_transaction_id
          USING ERRCODE = 'check_violation';
      END IF;
    ELSIF adj.kind = 'REVERSAL' OR NEW.posting_rule <> adj.posting_rule THEN
      RAISE EXCEPTION 'adjustment % authorises rule %, not %', adj.id, adj.posting_rule, NEW.posting_rule
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF r.requires_approval THEN
    RAISE EXCEPTION 'posting rule % requires an APPROVED adjustment (maker-checker)', r.code USING ERRCODE = 'check_violation';
  END IF;

  -- Periods. FOR SHARE serialises with a concurrent close (OPEN/CLOSING -> CLOSED needs the row lock).
  SELECT * INTO occ FROM ledger.ledger_periods
   WHERE NEW.occurred_at >= starts_at AND NEW.occurred_at < ends_at FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no ledger period covers occurred_at %', NEW.occurred_at USING ERRCODE = 'check_violation';
  END IF;
  IF occ.status <> 'CLOSED' THEN
    NEW.accounting_period_id := occ.id;
    NEW.is_prior_period := false;
  ELSIF r.allows_prior_period THEN
    SELECT * INTO cur FROM ledger.ledger_periods
     WHERE NEW.posted_at >= starts_at AND NEW.posted_at < ends_at AND status <> 'CLOSED' FOR SHARE;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'no open ledger period covers posted_at %', NEW.posted_at USING ERRCODE = 'check_violation';
    END IF;
    NEW.accounting_period_id := cur.id;
    NEW.is_prior_period := true;
  ELSE
    RAISE EXCEPTION 'ledger period % is CLOSED; posting rule % does not allow prior-period postings', occ.code, r.code
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ledger_transactions_before_insert BEFORE INSERT ON ledger.ledger_transactions
  FOR EACH ROW EXECUTE FUNCTION ledger.before_insert_transaction();

-- 11.2 BEFORE INSERT on entries: L11 — entries may only be added in the DB transaction that created the header.
CREATE FUNCTION ledger.before_insert_entry() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  txid   xid8;
  adj_id uuid;
  rule   text;
  st     text;
  code   text;
BEGIN
  NEW.created_txid := pg_current_xact_id();
  SELECT created_txid, adjustment_id, posting_rule INTO txid, adj_id, rule FROM ledger.ledger_transactions WHERE id = NEW.transaction_id;
  IF FOUND AND txid <> NEW.created_txid THEN
    RAISE EXCEPTION 'ledger transaction % is sealed: entries can only be added in the DB transaction that created it',
      NEW.transaction_id USING ERRCODE = 'restrict_violation';
  END IF;
  -- Stage 10 account status. FOR SHARE serialises with a concurrent freeze/close (UPDATE takes the row lock).
  -- FROZEN: only maker-checker approved journals (adjustment-backed). CLOSED: nothing.
  SELECT a.status, a.code INTO st, code FROM ledger.ledger_accounts a WHERE a.id = NEW.account_id FOR SHARE;
  IF st = 'CLOSED' THEN
    RAISE EXCEPTION 'ledger account % is CLOSED' , code USING ERRCODE = 'check_violation';
  ELSIF st = 'FROZEN' AND adj_id IS NULL THEN
    RAISE EXCEPTION 'ledger account % is FROZEN: only approved adjustments may post to it', code USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;  -- a missing header is reported by fk_ledger_entries_transaction_currency
END $$;
CREATE TRIGGER trg_ledger_entries_before_insert BEFORE INSERT ON ledger.ledger_entries
  FOR EACH ROW EXECUTE FUNCTION ledger.before_insert_entry();

-- 11.3 AFTER INSERT on entries: maintain SYNC projection rows in the same DB transaction — the UPDATE takes the
-- row lock on the account's projection row, and ck_ledger_balances_non_negative rejects any posting that would
-- take an OBLIGATION/RECOVERABLE account (campaign_payable in particular, L8) below zero. Because UPDATE re-reads
-- the latest committed row version under READ COMMITTED, two concurrent reservations cannot both pass.
-- DEFERRED accounts are skipped here (no lock on platform-wide hot rows); the fold job picks their entries up.
CREATE FUNCTION ledger.apply_entry_to_balance() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, ledger AS $$
DECLARE
  mode text;
BEGIN
  SELECT projection_mode INTO mode FROM ledger.ledger_accounts WHERE id = NEW.account_id;
  IF mode = 'DEFERRED' THEN
    RETURN NULL;
  END IF;
  UPDATE ledger.ledger_balances b
     SET debit_total_minor  = b.debit_total_minor  + CASE WHEN NEW.direction = 'DEBIT'  THEN NEW.amount_minor ELSE 0 END,
         credit_total_minor = b.credit_total_minor + CASE WHEN NEW.direction = 'CREDIT' THEN NEW.amount_minor ELSE 0 END,
         balance_minor      = b.balance_minor + CASE WHEN NEW.direction = b.normal_balance THEN NEW.amount_minor
                                                     ELSE -NEW.amount_minor END,
         entry_count        = b.entry_count + 1,
         last_entry_id      = NEW.id,
         last_transaction_id = NEW.transaction_id,
         version            = b.version + 1,
         updated_at         = now()
   WHERE b.account_id = NEW.account_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'missing ledger_balances row for account %', NEW.account_id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER trg_ledger_entries_apply_balance AFTER INSERT ON ledger.ledger_entries
  FOR EACH ROW EXECUTE FUNCTION ledger.apply_entry_to_balance();

-- 11.4 Deferred, once per journal (fires at COMMIT for each inserted header, not once per entry):
--   L1 balanced; L2 >= 2 entries; L12 lines conform to the posting rule (SC-1, SC-5); single owner per type;
--   L7 a reversal mirrors its original exactly; L14 adjustment journals equal the approved lines.
-- Complete because L11 forbids adding entries to a header created in another DB transaction.
CREATE FUNCTION ledger.check_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r       ledger.ledger_posting_rules%ROWTYPE;
  n       integer;
  dr      bigint;
  cr      bigint;
  bad     record;
  adj     ledger.ledger_adjustments%ROWTYPE;
BEGIN
  SELECT * INTO r FROM ledger.ledger_posting_rules WHERE code = NEW.posting_rule AND version = NEW.posting_rule_version;

  SELECT count(*),
         coalesce(sum(amount_minor) FILTER (WHERE direction = 'DEBIT'), 0),
         coalesce(sum(amount_minor) FILTER (WHERE direction = 'CREDIT'), 0)
    INTO n, dr, cr
    FROM ledger.ledger_entries WHERE transaction_id = NEW.id;

  IF n < 2 THEN
    RAISE EXCEPTION 'ledger transaction % (%) has % entries; at least 2 required (L2)', NEW.id, NEW.idempotency_key, n
      USING ERRCODE = 'check_violation';
  END IF;
  IF dr <> cr THEN
    RAISE EXCEPTION 'ledger transaction % (%) unbalanced in %: debits=% credits=% (L1)',
      NEW.id, NEW.idempotency_key, NEW.currency, dr, cr USING ERRCODE = 'check_violation';
  END IF;

  IF r.line_check = 'RULE_LINES' THEN
    SELECT e.line_no, e.direction, a.account_class INTO bad
      FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
     WHERE e.transaction_id = NEW.id
       AND NOT EXISTS (SELECT 1 FROM ledger.ledger_posting_rule_lines l
                        WHERE l.rule_code = r.code AND l.rule_version = r.version AND l.direction = e.direction
                          AND l.account_class = a.account_class)
     ORDER BY e.line_no LIMIT 1;
    IF FOUND THEN
      RAISE EXCEPTION 'posting rule % does not allow % on % (line %)', r.code, bad.direction, bad.account_class, bad.line_no
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT l.direction, l.account_class INTO bad
      FROM ledger.ledger_posting_rule_lines l
     WHERE l.rule_code = r.code AND l.rule_version = r.version AND l.is_required
       AND NOT EXISTS (SELECT 1 FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
                        WHERE e.transaction_id = NEW.id AND e.direction = l.direction AND a.account_class = l.account_class)
     LIMIT 1;
    IF FOUND THEN
      RAISE EXCEPTION 'posting rule % requires a % line on %', r.code, bad.direction, bad.account_class
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF r.single_owner_per_type THEN
    SELECT a.owner_type INTO bad
      FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
     WHERE e.transaction_id = NEW.id AND a.owner_type <> 'PLATFORM'
     GROUP BY a.owner_type HAVING count(DISTINCT a.owner_id) > 1 LIMIT 1;
    IF FOUND THEN
      RAISE EXCEPTION 'posting rule % journal touches more than one % owner', r.code, bad.owner_type
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF NEW.reverses_transaction_id IS NOT NULL THEN
    PERFORM 1 FROM (
      ( SELECT account_id, CASE direction WHEN 'DEBIT' THEN 'CREDIT' ELSE 'DEBIT' END AS direction, amount_minor
          FROM ledger.ledger_entries WHERE transaction_id = NEW.reverses_transaction_id
        EXCEPT ALL
        SELECT account_id, direction, amount_minor FROM ledger.ledger_entries WHERE transaction_id = NEW.id )
      UNION ALL
      ( SELECT account_id, direction, amount_minor FROM ledger.ledger_entries WHERE transaction_id = NEW.id
        EXCEPT ALL
        SELECT account_id, CASE direction WHEN 'DEBIT' THEN 'CREDIT' ELSE 'DEBIT' END, amount_minor
          FROM ledger.ledger_entries WHERE transaction_id = NEW.reverses_transaction_id )
    ) diff;
    IF FOUND THEN
      RAISE EXCEPTION 'reversal % does not mirror transaction % exactly (L7)', NEW.id, NEW.reverses_transaction_id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF NEW.adjustment_id IS NOT NULL AND NEW.reverses_transaction_id IS NULL THEN
    SELECT * INTO adj FROM ledger.ledger_adjustments WHERE id = NEW.adjustment_id;
    PERFORM 1 FROM (
      ( SELECT a.code AS account_code, e.direction, e.amount_minor
          FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
         WHERE e.transaction_id = NEW.id
        EXCEPT ALL
        SELECT l->>'account_code', l->>'direction', (l->>'amount_minor')::bigint
          FROM jsonb_array_elements(adj.proposed_lines) l )
      UNION ALL
      ( SELECT l->>'account_code', l->>'direction', (l->>'amount_minor')::bigint
          FROM jsonb_array_elements(adj.proposed_lines) l
        EXCEPT ALL
        SELECT a.code, e.direction, e.amount_minor
          FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
         WHERE e.transaction_id = NEW.id )
    ) diff;
    IF FOUND THEN
      RAISE EXCEPTION 'journal % lines differ from the approved lines of adjustment % (L14)', NEW.id, adj.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_ledger_transactions_check AFTER INSERT ON ledger.ledger_transactions
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger.check_transaction();

-- 11.5 DEFERRED fold (job `ledger.fold_deferred_balances`, every few seconds; one runner at a time).
-- Folds every entry of a DEFERRED account whose DB transaction id lies in [folded_through_txid, hi), where
-- hi = xmin of the current snapshot: every transaction below hi has committed or aborted, so nothing can
-- later appear below the new watermark. Idempotent; safe to rerun. Returns the number of rows advanced.
CREATE FUNCTION ledger.fold_deferred_balances() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, ledger AS $$
DECLARE
  hi xid8 := pg_snapshot_xmin(pg_current_snapshot());
  n  integer;
BEGIN
  PERFORM set_config('ledger.projection_maintenance', 'on', true);
  UPDATE ledger.ledger_balances b
     SET debit_total_minor   = b.debit_total_minor  + d.dr,
         credit_total_minor  = b.credit_total_minor + d.cr,
         balance_minor       = b.balance_minor + CASE b.normal_balance WHEN 'DEBIT' THEN d.dr - d.cr ELSE d.cr - d.dr END,
         entry_count         = b.entry_count + d.n,
         folded_through_txid = hi,
         version             = b.version + 1,
         updated_at          = now()
    FROM (SELECT b2.account_id,
                 coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0)  AS dr,
                 coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT'), 0) AS cr,
                 count(e.id) AS n
            FROM ledger.ledger_balances b2
            LEFT JOIN ledger.ledger_entries e
              ON e.account_id = b2.account_id AND e.created_txid >= b2.folded_through_txid AND e.created_txid < hi
           WHERE b2.projection_mode = 'DEFERRED' AND b2.folded_through_txid < hi
           GROUP BY b2.account_id) d
   WHERE b.account_id = d.account_id;
  GET DIAGNOSTICS n = ROW_COUNT;
  PERFORM set_config('ledger.projection_maintenance', 'off', true);
  RETURN n;
END $$;
REVOKE ALL ON FUNCTION ledger.fold_deferred_balances() FROM PUBLIC;

-- 11.6 L10 repair (break-glass only; incident + maker-checker): rebuild one projection row FROM entries.
CREATE FUNCTION ledger.rebuild_balance(p_account_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, ledger AS $$
DECLARE
  hi xid8 := pg_snapshot_xmin(pg_current_snapshot());
BEGIN
  PERFORM set_config('ledger.projection_maintenance', 'on', true);
  UPDATE ledger.ledger_balances b
     SET debit_total_minor  = s.dr, credit_total_minor = s.cr,
         balance_minor      = CASE b.normal_balance WHEN 'DEBIT' THEN s.dr - s.cr ELSE s.cr - s.dr END,
         entry_count        = s.n,
         folded_through_txid = CASE b.projection_mode WHEN 'DEFERRED' THEN hi END,
         version = b.version + 1, updated_at = now()
    FROM (SELECT coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0)  AS dr,
                 coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT'), 0) AS cr,
                 count(*) AS n
            FROM ledger.ledger_entries e JOIN ledger.ledger_balances bb ON bb.account_id = e.account_id
           WHERE e.account_id = p_account_id
             AND (bb.projection_mode = 'SYNC' OR e.created_txid < hi)) s
   WHERE b.account_id = p_account_id;
  PERFORM set_config('ledger.projection_maintenance', 'off', true);
END $$;
REVOKE ALL ON FUNCTION ledger.rebuild_balance(uuid) FROM PUBLIC;

-- -----------------------------------------------------------------------------------------------------
-- 12. Verification views (used by the invariant jobs; SELECT for fundzim_app / fundzim_readonly).
-- -----------------------------------------------------------------------------------------------------
-- L9: per currency, total debits = total credits.
CREATE VIEW ledger.v_trial_balance AS
SELECT e.currency,
       sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT')  AS debit_total_minor,
       sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT') AS credit_total_minor
  FROM ledger.ledger_entries e
 GROUP BY e.currency;

-- Exact current balance of every account: SYNC = projection; DEFERRED = projection + unfolded tail
-- (index ix_ledger_entries_account_txid keeps the tail read short when the fold job keeps up).
CREATE VIEW ledger.v_balances_current AS
SELECT b.account_id, b.currency, b.projection_mode,
       b.balance_minor + coalesce(t.delta, 0) AS balance_minor,
       b.balance_minor                        AS projected_minor,
       coalesce(t.delta, 0)                   AS unfolded_minor
  FROM ledger.ledger_balances b
  LEFT JOIN LATERAL (
    SELECT sum(CASE WHEN e.direction = b.normal_balance THEN e.amount_minor ELSE -e.amount_minor END) AS delta
      FROM ledger.ledger_entries e
     WHERE b.projection_mode = 'DEFERRED' AND e.account_id = b.account_id
       AND e.created_txid >= b.folded_through_txid) t ON true;

-- L10: projection rows that differ from a recompute over the entries they claim to include (must be empty).
-- SYNC rows: all entries. DEFERRED rows: entries below the fold watermark.
CREATE VIEW ledger.v_balance_drift AS
SELECT b.account_id, b.currency, b.projection_mode, b.balance_minor AS projected_minor, s.recomputed_minor
  FROM ledger.ledger_balances b
  JOIN LATERAL (
    SELECT coalesce(sum(CASE WHEN e.direction = b.normal_balance THEN e.amount_minor ELSE -e.amount_minor END), 0)
             AS recomputed_minor
      FROM ledger.ledger_entries e
     WHERE e.account_id = b.account_id
       AND (b.projection_mode = 'SYNC' OR e.created_txid < b.folded_through_txid)) s ON true
 WHERE b.balance_minor <> s.recomputed_minor;

-- SC-2 (aggregate form, per currency): campaign-side obligations <= provider claims + provider-held pool.
-- The per-provider form needs payment/payout attribution and is computed by the reconciliation job (see
-- docs/architecture/ledger-invariants.md SC-2).
-- Uses exact current balances (provider assets are DEFERRED).
CREATE VIEW ledger.v_pool_integrity AS
SELECT a.currency,
       sum(b.balance_minor) FILTER (WHERE a.kind = 'OBLIGATION')                           AS obligations_minor,
       sum(b.balance_minor) FILTER (WHERE a.kind IN ('CLAIM_ON_PROVIDER', 'PROVIDER_POOL')) AS provider_assets_minor
  FROM ledger.ledger_accounts a JOIN ledger.v_balances_current b ON b.account_id = a.id
 GROUP BY a.currency;

-- -----------------------------------------------------------------------------------------------------
-- 13. Posting-rule seed (named rules; docs/architecture/ledger-posting-model.md §4).
-- -----------------------------------------------------------------------------------------------------
INSERT INTO ledger.ledger_posting_rules
  (id, code, description, idempotency_key_format, allowed_source_types, line_check, requires_approval, allows_prior_period, single_owner_per_type, doc_ref)
VALUES
  (gen_random_uuid(), 'DONATION_CAPTURED', 'Authoritative capture at gross: claim on provider; net to campaign_unsettled; platform fee revenue',
     'payment:{payment_id}:capture', '{payment}', 'RULE_LINES', false, true, true, 'custody §5.1'),
  (gen_random_uuid(), 'PSP_FEE', 'PSP processing fee deducted at source (campaign or FundZim bears)',
     'payment:{payment_id}:psp_fee', '{payment}', 'RULE_LINES', false, true, true, 'custody §5.2'),
  (gen_random_uuid(), 'SETTLEMENT_MATCHED', 'Reconciliation-confirmed settlement: claim becomes provider-held pool',
     'settlement:{settlement_batch_id}:{payment_id}', '{settlement_item}', 'RULE_LINES', false, true, true, 'custody §5.3'),
  (gen_random_uuid(), 'SETTLEMENT_DISCREPANCY', 'Settlement differs from expectation, or unknown funds: difference to suspense',
     'settlement:{settlement_batch_id}:{payment_id} | settlement:{settlement_batch_id}:unmatched:{item_id}', '{settlement_item,settlement_batch}', 'RULE_LINES', false, true, true, 'custody §5.4'),
  (gen_random_uuid(), 'SETTLEMENT_DISCREPANCY_RESOLVED', 'Approved resolution clearing suspense',
     'discrepancy:{discrepancy_id}:resolved', '{discrepancy}', 'RULE_LINES', true, true, true, 'custody §5.4'),
  (gen_random_uuid(), 'SETTLEMENT_DIRECT', 'Model C direct beneficiary settlement (net to beneficiary, fee to FundZim)',
     'settlement:{settlement_batch_id}:{payment_id}:direct', '{settlement_item}', 'RULE_LINES', false, true, true, 'custody §8.2'),
  (gen_random_uuid(), 'RELEASE', 'Release net to available (or held when FROZEN)',
     'payment:{payment_id}:release', '{payment}', 'RULE_LINES', false, false, true, 'custody §5.5'),
  (gen_random_uuid(), 'RELEASE_WITH_RESERVE', 'Release split between available and reserve (PD-33)',
     'payment:{payment_id}:release', '{payment}', 'RULE_LINES', false, false, true, 'custody §5.6'),
  (gen_random_uuid(), 'RESERVE_RELEASE', 'Reserve holdback released after the reserve period',
     'reserve:{campaign_id}:{n}:released', '{campaign}', 'RULE_LINES', false, false, true, 'custody §5.6'),
  (gen_random_uuid(), 'FEE_REMITTANCE', 'PSP remits platform fees from the pool to FundZim operating bank',
     'fee_remittance:{provider_id}:{batch_id}', '{fee_remittance,settlement_batch}', 'RULE_LINES', false, true, true, 'custody §5.7'),
  (gen_random_uuid(), 'PAYOUT_RESERVED', 'Payout requested: available -> payout pending (SC-1)',
     'payout:{payout_id}:reserved', '{payout}', 'RULE_LINES', false, false, true, 'custody §5.8 / payout-lifecycle Y1'),
  (gen_random_uuid(), 'PAYOUT_SUBMITTED', 'Payout submitted to provider: pending -> in transit',
     'payout:{payout_id}:submitted', '{payout}', 'RULE_LINES', false, false, true, 'payout-lifecycle Y8'),
  (gen_random_uuid(), 'PAYOUT_COMPLETED', 'Authoritative payout completion: leaves the provider-held pool',
     'payout:{payout_id}:completed', '{payout}', 'RULE_LINES', false, true, true, 'payout-lifecycle Y10'),
  (gen_random_uuid(), 'PAYOUT_FAILED', 'Authoritative final failure after submission: back to available (or held)',
     'payout:{payout_id}:failed', '{payout}', 'RULE_LINES', false, true, true, 'payout-lifecycle Y11'),
  (gen_random_uuid(), 'PAYOUT_RELEASED', 'Rejected/cancelled before submission: back to available (or held)',
     'payout:{payout_id}:released', '{payout}', 'RULE_LINES', false, false, true, 'payout-lifecycle Y4/Y5'),
  (gen_random_uuid(), 'PAYOUT_REVERSED', 'Completed payout returned by the rail to the pool',
     'payout:{payout_id}:reversed', '{payout}', 'RULE_LINES', false, true, true, 'payout-lifecycle Y14'),
  (gen_random_uuid(), 'PAYOUT_FEE', 'Per-disbursement provider fee borne by FundZim (PD-23)',
     'payout:{payout_id}:fee', '{payout}', 'RULE_LINES', false, true, true, 'payout-lifecycle §3'),
  (gen_random_uuid(), 'REFUND_RESERVED', 'Approved refund reserved from where the money sits; platform fee reversed',
     'refund:{refund_id}:reserved', '{refund}', 'RULE_LINES', false, false, true, 'refund flows §3.3'),
  (gen_random_uuid(), 'REFUND_SETTLED', 'Provider confirms the refund',
     'refund:{refund_id}:settled', '{refund}', 'RULE_LINES', false, true, true, 'refund flows §3.3'),
  (gen_random_uuid(), 'REFUND_FAILED', 'Authoritative refund failure: exact inverse of the reservation',
     'refund:{refund_id}:failed', '{refund}', 'RULE_LINES', false, true, true, 'refund flows §3.3 D'),
  (gen_random_uuid(), 'REFUND_FEE_TRUE_UP', 'Return an already-remitted platform fee to the pool (LR-076)',
     'refund:{refund_id}:fee_true_up', '{refund}', 'RULE_LINES', false, false, true, 'refund flows §3.3 B'),
  (gen_random_uuid(), 'REFUND_FEE_TRUE_UP_REVERSED', 'Inverse of a fee true-up after a refund failure',
     'refund:{refund_id}:fee_true_up_reversed', '{refund}', 'RULE_LINES', false, true, true, 'refund flows §3.3 D'),
  (gen_random_uuid(), 'REFUND_FUND_SHORTFALL', 'FundZim funds a refund shortfall into the pool (C2; LR-076)',
     'refund:{refund_id}:funding', '{refund}', 'RULE_LINES', true, false, true, 'refund flows §3.5'),
  (gen_random_uuid(), 'DISPUTE_OPENED', 'Provider debits the pool on dispute open; campaign share recovered in order',
     'dispute:{dispute_id}:opened | reversal:{payment_id}:{provider_event_id}', '{dispute,chargeback}', 'RULE_LINES', false, true, true, 'refund flows §6 A/B/G'),
  (gen_random_uuid(), 'DISPUTE_HELD', 'Debit-on-loss provider: campaign share held while disputed',
     'dispute:{dispute_id}:held', '{dispute}', 'RULE_LINES', false, false, true, 'refund flows §6 E'),
  (gen_random_uuid(), 'DISPUTE_RELEASED', 'Debit-on-loss provider, dispute won: hold released',
     'dispute:{dispute_id}:released', '{dispute}', 'RULE_LINES', false, true, true, 'refund flows §6 E'),
  (gen_random_uuid(), 'DISPUTE_WON', 'Provider re-credits the pool: exact inverse of DISPUTE_OPENED',
     'dispute:{dispute_id}:won', '{dispute}', 'RULE_LINES', false, true, true, 'refund flows §6 C'),
  (gen_random_uuid(), 'DISPUTE_LOST', 'Debit-on-loss provider, dispute lost: provider debits the pool',
     'dispute:{dispute_id}:lost', '{dispute,chargeback}', 'RULE_LINES', false, true, true, 'refund flows §6 E'),
  (gen_random_uuid(), 'DISPUTE_FEE', 'Provider dispute/chargeback fee (FundZim or campaign bears, PD-20)',
     'dispute:{dispute_id}:fee', '{dispute,chargeback}', 'RULE_LINES', false, true, true, 'refund flows §6 F'),
  (gen_random_uuid(), 'DISPUTE_FUNDING', 'FundZim funds a chargeback shortfall into the pool (LR-076)',
     'dispute:{dispute_id}:funding', '{dispute,chargeback}', 'RULE_LINES', true, false, true, 'custody §5.11'),
  (gen_random_uuid(), 'DISPUTE_FUNDING_RETURNED', 'Surplus funding returned to FundZim after a won dispute',
     'dispute:{dispute_id}:funding_returned', '{dispute}', 'RULE_LINES', false, true, true, 'refund flows §6 C'),
  (gen_random_uuid(), 'CAMPAIGN_FROZEN', 'Freeze: available -> held',
     'campaign:{campaign_id}:freeze:{n}', '{campaign}', 'RULE_LINES', false, false, true, 'custody §5.12'),
  (gen_random_uuid(), 'CAMPAIGN_UNFROZEN', 'Unfreeze (second approver enforced by campaigns): held -> available',
     'campaign:{campaign_id}:unfreeze:{n}', '{campaign}', 'RULE_LINES', false, false, true, 'custody §5.12'),
  (gen_random_uuid(), 'RECOVERY_RECEIVED', 'Recovery of a refund/chargeback shortfall (repayment or approved set-off)',
     'recovery:{recovery_case_id}:{n}', '{recovery_case}', 'RULE_LINES', false, true, true, 'refund flows §3.5 / §6 D'),
  (gen_random_uuid(), 'WRITE_OFF', 'Write-off of an unrecoverable recoverable (maker-checker)',
     'dispute:{dispute_id}:written_off | recovery:{recovery_case_id}:written_off', '{dispute,recovery_case}', 'RULE_LINES', true, true, true, 'custody §5.11'),
  (gen_random_uuid(), 'ADJUSTMENT', 'Free-form staff adjustment (maker-checker; never touches the operating bank)',
     'adjustment:{adjustment_id}', '{adjustment,discrepancy}', 'RULE_LINES', true, true, false, 'LEDGER §5'),
  (gen_random_uuid(), 'REVERSAL', 'Exact mirror of an original journal (maker-checker)',
     'reversal:{original_transaction_id}', '{adjustment}', 'MIRROR_OF_REVERSED', true, true, false, 'LEDGER §6.9');

-- Rule lines. Class shorthands expanded in full for reviewability.
INSERT INTO ledger.ledger_posting_rule_lines (id, rule_code, direction, account_class, is_required)
SELECT gen_random_uuid(), v.rule_code, v.direction, v.account_class, v.is_required
FROM (VALUES
  ('DONATION_CAPTURED', 'DEBIT',  'asset:psp_clearing',               true),
  ('DONATION_CAPTURED', 'CREDIT', 'liability:campaign_unsettled',     true),
  ('DONATION_CAPTURED', 'CREDIT', 'revenue:platform_fees',            false),
  ('PSP_FEE',           'DEBIT',  'liability:campaign_unsettled',     false),
  ('PSP_FEE',           'DEBIT',  'expense:psp_processing_fees',      false),
  ('PSP_FEE',           'CREDIT', 'asset:psp_clearing',               true),
  ('SETTLEMENT_MATCHED','DEBIT',  'asset:psp_settled',                true),
  ('SETTLEMENT_MATCHED','CREDIT', 'asset:psp_clearing',               true),
  ('SETTLEMENT_DISCREPANCY','DEBIT',  'asset:psp_settled',            false),
  ('SETTLEMENT_DISCREPANCY','DEBIT',  'suspense:settlement_discrepancy', false),
  ('SETTLEMENT_DISCREPANCY','CREDIT', 'asset:psp_clearing',           false),
  ('SETTLEMENT_DISCREPANCY','CREDIT', 'suspense:settlement_discrepancy', false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','DEBIT',  'asset:psp_settled',              false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','DEBIT',  'asset:psp_clearing',             false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','DEBIT',  'liability:campaign_unsettled',   false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','DEBIT',  'expense:psp_processing_fees',    false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','DEBIT',  'suspense:settlement_discrepancy', false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','CREDIT', 'suspense:settlement_discrepancy', false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','CREDIT', 'asset:psp_settled',              false),
  ('SETTLEMENT_DISCREPANCY_RESOLVED','CREDIT', 'asset:psp_clearing',             false),
  ('SETTLEMENT_DIRECT', 'DEBIT',  'liability:campaign_unsettled',     true),
  ('SETTLEMENT_DIRECT', 'DEBIT',  'asset:fundzim_operating_bank',     false),
  ('SETTLEMENT_DIRECT', 'CREDIT', 'asset:psp_clearing',               true),
  ('RELEASE',           'DEBIT',  'liability:campaign_unsettled',     true),
  ('RELEASE',           'CREDIT', 'liability:campaign_payable',       false),
  ('RELEASE',           'CREDIT', 'liability:campaign_held',          false),
  ('RELEASE_WITH_RESERVE','DEBIT',  'liability:campaign_unsettled',   true),
  ('RELEASE_WITH_RESERVE','CREDIT', 'liability:campaign_payable',     false),
  ('RELEASE_WITH_RESERVE','CREDIT', 'liability:campaign_held',        false),
  ('RELEASE_WITH_RESERVE','CREDIT', 'liability:campaign_reserve',     true),
  ('RESERVE_RELEASE',   'DEBIT',  'liability:campaign_reserve',       true),
  ('RESERVE_RELEASE',   'CREDIT', 'liability:campaign_payable',       false),
  ('RESERVE_RELEASE',   'CREDIT', 'liability:campaign_held',          false),
  ('FEE_REMITTANCE',    'DEBIT',  'asset:fundzim_operating_bank',     true),
  ('FEE_REMITTANCE',    'CREDIT', 'asset:psp_settled',                true),
  ('PAYOUT_RESERVED',   'DEBIT',  'liability:campaign_payable',       true),
  ('PAYOUT_RESERVED',   'CREDIT', 'liability:campaign_payout_pending', true),
  ('PAYOUT_SUBMITTED',  'DEBIT',  'liability:campaign_payout_pending', true),
  ('PAYOUT_SUBMITTED',  'CREDIT', 'liability:payout_in_transit',      true),
  ('PAYOUT_COMPLETED',  'DEBIT',  'liability:payout_in_transit',      true),
  ('PAYOUT_COMPLETED',  'CREDIT', 'asset:psp_settled',                true),
  ('PAYOUT_FAILED',     'DEBIT',  'liability:payout_in_transit',      true),
  ('PAYOUT_FAILED',     'CREDIT', 'liability:campaign_payable',       false),
  ('PAYOUT_FAILED',     'CREDIT', 'liability:campaign_held',          false),
  ('PAYOUT_RELEASED',   'DEBIT',  'liability:campaign_payout_pending', true),
  ('PAYOUT_RELEASED',   'CREDIT', 'liability:campaign_payable',       false),
  ('PAYOUT_RELEASED',   'CREDIT', 'liability:campaign_held',          false),
  ('PAYOUT_REVERSED',   'DEBIT',  'asset:psp_settled',                true),
  ('PAYOUT_REVERSED',   'CREDIT', 'liability:campaign_payable',       false),
  ('PAYOUT_REVERSED',   'CREDIT', 'liability:campaign_held',          false),
  ('PAYOUT_FEE',        'DEBIT',  'expense:psp_processing_fees',      true),
  ('PAYOUT_FEE',        'CREDIT', 'asset:psp_settled',                true),
  ('REFUND_RESERVED',   'DEBIT',  'liability:campaign_unsettled',     false),
  ('REFUND_RESERVED',   'DEBIT',  'liability:campaign_payable',       false),
  ('REFUND_RESERVED',   'DEBIT',  'liability:campaign_reserve',       false),
  ('REFUND_RESERVED',   'DEBIT',  'liability:campaign_held',          false),
  ('REFUND_RESERVED',   'DEBIT',  'asset:refund_recoverable',         false),
  ('REFUND_RESERVED',   'DEBIT',  'revenue:platform_fees',            false),
  ('REFUND_RESERVED',   'CREDIT', 'liability:refund_payable',         true),
  ('REFUND_SETTLED',    'DEBIT',  'liability:refund_payable',         true),
  ('REFUND_SETTLED',    'CREDIT', 'asset:psp_clearing',               false),
  ('REFUND_SETTLED',    'CREDIT', 'asset:psp_settled',                false),
  ('REFUND_FAILED',     'DEBIT',  'liability:refund_payable',         true),
  ('REFUND_FAILED',     'CREDIT', 'liability:campaign_unsettled',     false),
  ('REFUND_FAILED',     'CREDIT', 'liability:campaign_payable',       false),
  ('REFUND_FAILED',     'CREDIT', 'liability:campaign_reserve',       false),
  ('REFUND_FAILED',     'CREDIT', 'liability:campaign_held',          false),
  ('REFUND_FAILED',     'CREDIT', 'asset:refund_recoverable',         false),
  ('REFUND_FAILED',     'CREDIT', 'revenue:platform_fees',            false),
  ('REFUND_FEE_TRUE_UP','DEBIT',  'asset:psp_settled',                true),
  ('REFUND_FEE_TRUE_UP','CREDIT', 'asset:fundzim_operating_bank',     true),
  ('REFUND_FEE_TRUE_UP_REVERSED','DEBIT',  'asset:fundzim_operating_bank', true),
  ('REFUND_FEE_TRUE_UP_REVERSED','CREDIT', 'asset:psp_settled',            true),
  ('REFUND_FUND_SHORTFALL','DEBIT',  'asset:psp_settled',             true),
  ('REFUND_FUND_SHORTFALL','CREDIT', 'asset:fundzim_operating_bank',  true),
  ('DISPUTE_OPENED',    'DEBIT',  'liability:campaign_held',          false),
  ('DISPUTE_OPENED',    'DEBIT',  'liability:campaign_payable',       false),
  ('DISPUTE_OPENED',    'DEBIT',  'liability:campaign_reserve',       false),
  ('DISPUTE_OPENED',    'DEBIT',  'liability:campaign_unsettled',     false),
  ('DISPUTE_OPENED',    'DEBIT',  'asset:chargeback_recoverable',     false),
  ('DISPUTE_OPENED',    'DEBIT',  'revenue:platform_fees',            false),
  ('DISPUTE_OPENED',    'CREDIT', 'asset:psp_settled',                false),
  ('DISPUTE_OPENED',    'CREDIT', 'asset:psp_clearing',               false),
  ('DISPUTE_HELD',      'DEBIT',  'liability:campaign_payable',       true),
  ('DISPUTE_HELD',      'CREDIT', 'liability:campaign_held',          true),
  ('DISPUTE_RELEASED',  'DEBIT',  'liability:campaign_held',          true),
  ('DISPUTE_RELEASED',  'CREDIT', 'liability:campaign_payable',       true),
  ('DISPUTE_WON',       'DEBIT',  'asset:psp_settled',                false),
  ('DISPUTE_WON',       'DEBIT',  'asset:psp_clearing',               false),
  ('DISPUTE_WON',       'CREDIT', 'liability:campaign_held',          false),
  ('DISPUTE_WON',       'CREDIT', 'liability:campaign_payable',       false),
  ('DISPUTE_WON',       'CREDIT', 'liability:campaign_reserve',       false),
  ('DISPUTE_WON',       'CREDIT', 'liability:campaign_unsettled',     false),
  ('DISPUTE_WON',       'CREDIT', 'asset:chargeback_recoverable',     false),
  ('DISPUTE_WON',       'CREDIT', 'revenue:platform_fees',            false),
  ('DISPUTE_LOST',      'DEBIT',  'liability:campaign_held',          false),
  ('DISPUTE_LOST',      'DEBIT',  'liability:campaign_payable',       false),
  ('DISPUTE_LOST',      'DEBIT',  'liability:campaign_reserve',       false),
  ('DISPUTE_LOST',      'DEBIT',  'liability:campaign_unsettled',     false),
  ('DISPUTE_LOST',      'DEBIT',  'asset:chargeback_recoverable',     false),
  ('DISPUTE_LOST',      'DEBIT',  'revenue:platform_fees',            false),
  ('DISPUTE_LOST',      'CREDIT', 'asset:psp_settled',                false),
  ('DISPUTE_LOST',      'CREDIT', 'asset:psp_clearing',               false),
  ('DISPUTE_FEE',       'DEBIT',  'expense:psp_processing_fees',      false),
  ('DISPUTE_FEE',       'DEBIT',  'liability:campaign_payable',       false),
  ('DISPUTE_FEE',       'CREDIT', 'asset:psp_settled',                true),
  ('DISPUTE_FUNDING',   'DEBIT',  'asset:psp_settled',                true),
  ('DISPUTE_FUNDING',   'CREDIT', 'asset:fundzim_operating_bank',     true),
  ('DISPUTE_FUNDING_RETURNED','DEBIT',  'asset:fundzim_operating_bank', true),
  ('DISPUTE_FUNDING_RETURNED','CREDIT', 'asset:psp_settled',            true),
  ('CAMPAIGN_FROZEN',   'DEBIT',  'liability:campaign_payable',       true),
  ('CAMPAIGN_FROZEN',   'CREDIT', 'liability:campaign_held',          true),
  ('CAMPAIGN_UNFROZEN', 'DEBIT',  'liability:campaign_held',          true),
  ('CAMPAIGN_UNFROZEN', 'CREDIT', 'liability:campaign_payable',       true),
  ('RECOVERY_RECEIVED', 'DEBIT',  'asset:fundzim_operating_bank',     false),
  ('RECOVERY_RECEIVED', 'DEBIT',  'liability:campaign_payable',       false),
  ('RECOVERY_RECEIVED', 'CREDIT', 'asset:chargeback_recoverable',     false),
  ('RECOVERY_RECEIVED', 'CREDIT', 'asset:refund_recoverable',         false),
  ('WRITE_OFF',         'DEBIT',  'expense:chargeback_losses',        false),
  ('WRITE_OFF',         'DEBIT',  'expense:refund_losses',            false),
  ('WRITE_OFF',         'CREDIT', 'asset:chargeback_recoverable',     false),
  ('WRITE_OFF',         'CREDIT', 'asset:refund_recoverable',         false)
) AS v (rule_code, direction, account_class, is_required);

-- ADJUSTMENT may touch every class on either side EXCEPT FundZim's operating bank (SC-5) and the unused
-- Model-A payout float. Its lines must also equal the approved proposed lines (L14).
INSERT INTO ledger.ledger_posting_rule_lines (id, rule_code, direction, account_class, is_required)
SELECT gen_random_uuid(), 'ADJUSTMENT', d.direction, c.account_class, false
  FROM (VALUES ('DEBIT'), ('CREDIT')) AS d (direction)
 CROSS JOIN (VALUES ('asset:psp_clearing'), ('asset:psp_settled'), ('asset:chargeback_recoverable'),
                    ('asset:refund_recoverable'), ('liability:campaign_unsettled'), ('liability:campaign_payable'),
                    ('liability:campaign_reserve'), ('liability:campaign_payout_pending'),
                    ('liability:payout_in_transit'), ('liability:campaign_held'), ('liability:refund_payable'),
                    ('revenue:platform_fees'), ('expense:psp_processing_fees'), ('expense:chargeback_losses'),
                    ('expense:refund_losses'), ('equity:platform'), ('suspense:settlement_discrepancy'))
        AS c (account_class);


-- -----------------------------------------------------------------------------------------------------
-- 14. Source references (Stage 10): every source event the ledger considered, with the decision. Append-only.
--     The business posting identity is (environment, source_system, source_event_id, posting_rule).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_source_references (
  id              uuid        NOT NULL,
  environment     text        NOT NULL,
  source_system   text        NOT NULL,
  source_type     text        NOT NULL,
  source_id       uuid        NOT NULL,
  source_event_id text        NOT NULL,
  posting_rule    text        NOT NULL,
  evidence_class  text        NOT NULL,
  decision        text        NOT NULL,
  reason_code     text        NOT NULL,
  transaction_id  uuid,
  amount_minor    bigint,
  currency        char(3),
  observed_at     timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_source_references PRIMARY KEY (id),
  CONSTRAINT uq_ledger_source_references_identity UNIQUE (environment, source_system, source_event_id, posting_rule),
  CONSTRAINT fk_ledger_source_references_transaction FOREIGN KEY (transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_source_references_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_source_references_environment CHECK (environment IN ('TEST', 'PRODUCTION')),
  CONSTRAINT ck_ledger_source_references_source_system CHECK (source_system IN ('payments', 'reconciliation', 'staff', 'synthetic_fixture')),
  CONSTRAINT ck_ledger_source_references_evidence CHECK (evidence_class IN ('SIMULATED', 'PROVIDER_REPORTED', 'PROVIDER_VERIFIED',
                                                                           'SETTLEMENT_VERIFIED', 'MANUALLY_REVIEWED', 'REJECTED')),
  CONSTRAINT ck_ledger_source_references_decision CHECK (decision IN ('POSTED', 'NOT_ELIGIBLE', 'BLOCKED', 'REJECTED')),
  CONSTRAINT ck_ledger_source_references_posted CHECK ((decision = 'POSTED') = (transaction_id IS NOT NULL)),
  CONSTRAINT ck_ledger_source_references_rejected_evidence CHECK (evidence_class <> 'REJECTED' OR decision <> 'POSTED'),
  CONSTRAINT ck_ledger_source_references_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,62}$'),
  CONSTRAINT ck_ledger_source_references_amount CHECK ((amount_minor IS NULL) = (currency IS NULL) AND (amount_minor IS NULL OR amount_minor > 0))
);
CREATE INDEX ix_ledger_source_references_source ON ledger.ledger_source_references (source_type, source_id);
CREATE INDEX ix_ledger_source_references_decision ON ledger.ledger_source_references (decision, created_at DESC);
CREATE TRIGGER trg_ledger_source_references_environment BEFORE INSERT ON ledger.ledger_source_references
  FOR EACH ROW EXECUTE FUNCTION ledger.enforce_environment();
CREATE TRIGGER trg_ledger_source_references_no_mutation BEFORE UPDATE OR DELETE ON ledger.ledger_source_references
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_source_references_no_truncate BEFORE TRUNCATE ON ledger.ledger_source_references
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 15. Reconciliation references (Stage 10): exceptions found by ledger reconciliation, and suspense
--     investigations. Detection fields immutable; workflow OPEN -> INVESTIGATING -> RESOLVED. Never a correction:
--     corrections are maker-checker adjustments that the resolution references.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE ledger.ledger_reconciliation_references (
  id                   uuid        NOT NULL,
  environment          text        NOT NULL,
  exception_type       text        NOT NULL,
  source_type          text        NOT NULL,
  source_id            uuid        NOT NULL,
  transaction_id       uuid,
  account_id           uuid,
  expected_amount_minor bigint,
  actual_amount_minor  bigint,
  currency             char(3),
  detail               jsonb       NOT NULL DEFAULT '{}',
  status               text        NOT NULL DEFAULT 'OPEN',
  owner_id             uuid,               -- investigation owner (staff user id)
  detected_at          timestamptz NOT NULL,
  detected_by_run      text        NOT NULL,
  resolved_by          uuid,
  resolved_at          timestamptz,
  resolution_note      text,
  resolution_adjustment_id uuid,
  version              integer     NOT NULL DEFAULT 1,
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ledger_reconciliation_references PRIMARY KEY (id),
  CONSTRAINT fk_ledger_recon_refs_transaction FOREIGN KEY (transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_recon_refs_account FOREIGN KEY (account_id) REFERENCES ledger.ledger_accounts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_recon_refs_adjustment FOREIGN KEY (resolution_adjustment_id) REFERENCES ledger.ledger_adjustments (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ledger_recon_refs_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_ledger_recon_refs_environment CHECK (environment IN ('TEST', 'PRODUCTION')),
  CONSTRAINT ck_ledger_recon_refs_type CHECK (exception_type IN ('MISSING_POSTING', 'DUPLICATE_POSTING', 'AMOUNT_MISMATCH',
    'CURRENCY_MISMATCH', 'UNKNOWN_OUTCOME', 'UNMATCHED_PROVIDER_REFERENCE', 'ORPHAN_POSTING', 'SUSPENSE_BALANCE')),
  CONSTRAINT ck_ledger_recon_refs_status CHECK (status IN ('OPEN', 'INVESTIGATING', 'RESOLVED')),
  CONSTRAINT ck_ledger_recon_refs_investigating CHECK (status <> 'INVESTIGATING' OR owner_id IS NOT NULL),
  CONSTRAINT ck_ledger_recon_refs_resolved CHECK (status <> 'RESOLVED'
    OR (resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND length(btrim(coalesce(resolution_note, ''))) >= 10)),
  CONSTRAINT ck_ledger_recon_refs_detail CHECK (jsonb_typeof(detail) = 'object' AND octet_length(detail::text) <= 4096)
);
-- one unresolved reference per (exception type, source)
CREATE UNIQUE INDEX uq_ledger_recon_refs_open ON ledger.ledger_reconciliation_references (exception_type, source_type, source_id)
  WHERE status <> 'RESOLVED';
CREATE INDEX ix_ledger_recon_refs_status ON ledger.ledger_reconciliation_references (status, detected_at DESC, id);
CREATE FUNCTION ledger.guard_recon_reference_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(NEW) - ARRAY['status','owner_id','resolved_by','resolved_at','resolution_note','resolution_adjustment_id','version','updated_at'])
     IS DISTINCT FROM
     (to_jsonb(OLD) - ARRAY['status','owner_id','resolved_by','resolved_at','resolution_note','resolution_adjustment_id','version','updated_at']) THEN
    RAISE EXCEPTION 'reconciliation reference % detection fields are immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status = 'RESOLVED' THEN
    RAISE EXCEPTION 'reconciliation reference % is resolved', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  NEW.version := OLD.version + 1;
  NEW.updated_at := now();
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ledger_recon_refs_environment BEFORE INSERT ON ledger.ledger_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION ledger.enforce_environment();
CREATE TRIGGER trg_ledger_recon_refs_guard_status BEFORE INSERT OR UPDATE OF status ON ledger.ledger_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ledger_reconciliation_reference');
CREATE TRIGGER trg_ledger_recon_refs_guard_update BEFORE UPDATE ON ledger.ledger_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION ledger.guard_recon_reference_update();
CREATE TRIGGER trg_ledger_recon_refs_no_delete BEFORE DELETE ON ledger.ledger_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_recon_refs_no_truncate BEFORE TRUNCATE ON ledger.ledger_reconciliation_references
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Reporting index: account activity by time.
CREATE INDEX ix_ledger_entries_transaction ON ledger.ledger_entries (transaction_id);

-- -----------------------------------------------------------------------------------------------------
-- 16. Permissions.
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:ledger.view')::uuid, 'ledger.view', 'View the ledger: accounts, journals, balances, trial balance, reconciliation', true, false),
  (md5('permission:ledger.account.manage')::uuid, 'ledger.account.manage', 'Freeze, unfreeze or close a ledger account', true, true),
  (md5('permission:ledger.period.close')::uuid, 'ledger.period.close', 'Request or approve an accounting period close (maker-checker)', true, true),
  (md5('permission:ledger.reconciliation.operate')::uuid, 'ledger.reconciliation.operate', 'Run ledger reconciliation and work its exceptions', true, true),
  (md5('permission:ledger.synthetic.post')::uuid, 'ledger.synthetic.post', 'Post TEST_ONLY synthetic ledger fixtures (TEST ledger, non-production only)', false, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('FINANCE', 'ledger.view'), ('FINANCE', 'ledger.account.manage'), ('FINANCE', 'ledger.period.close'),
             ('FINANCE', 'ledger.reconciliation.operate'), ('COMPLIANCE', 'ledger.view'), ('ADMIN', 'ledger.synthetic.post')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- -----------------------------------------------------------------------------------------------------
-- 17. Gateways: fundzim_ledger may append ledger.* audit actions and outbox events, and record evidence.
-- -----------------------------------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION audit.gateway_prefix_ok(p_kind text, p_value text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'action'
      THEN p_value ~ '^(kyc|kyb|beneficiary|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'event'
      THEN p_value ~ '^kyc\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'action'
      THEN p_value ~ '^(compliance|risk|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'event'
      THEN p_value ~ '^compliance\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_ledger' AND p_kind = 'action'
      THEN p_value ~ '^(ledger|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_ledger' AND p_kind = 'event'
      THEN p_value ~ '^ledger\.[a-z_]+(\.[a-z_]+)*$'
    ELSE false
  END
$$;

-- record_evidence gateway: the ledger role may record invariant-run evidence (Stage 10).
CREATE OR REPLACE FUNCTION audit.record_evidence(
  p_id uuid, p_evidence_type text, p_subject_type text, p_subject_id uuid, p_related_refs jsonb,
  p_collected_at timestamptz, p_collected_by_type text, p_collected_by_id uuid, p_source text, p_storage_ref text,
  p_content_sha256 bytea, p_size_bytes bigint, p_media_type text, p_classification text, p_retention_class text,
  p_audit_event_id uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF session_user NOT IN ('fundzim_kyc', 'fundzim_compliance', 'fundzim_ledger')
     OR NOT audit.gateway_payload_ok(coalesce(p_related_refs, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.record_evidence: rejected input (caller %)', session_user USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, related_refs, collected_at,
    collected_by_type, collected_by_id, source, storage_ref, content_sha256, size_bytes, media_type, classification,
    retention_class, audit_event_id)
  VALUES (p_id, p_evidence_type, p_subject_type, p_subject_id, coalesce(p_related_refs, '{}'::jsonb), p_collected_at,
    p_collected_by_type, p_collected_by_id, p_source, p_storage_ref, p_content_sha256, p_size_bytes, p_media_type,
    p_classification, p_retention_class, p_audit_event_id);
  RETURN p_id;
END $$;

CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  -- Stage 10: the ledger schema belongs to fundzim_ledger alone (fundzim_app has no ledger privileges at all).
  ledger_reference constant text[] := ARRAY['ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines','ledger.ledger_environment'];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, audit, risk, recon. Never kyc, compliance or ledger (Stage 10).
  FOREACH s IN ARRAY ARRAY['app','queue','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- Stage 10: fundzim_ledger — the ledger schema (append-only tables: SELECT, INSERT; status tables also UPDATE; the
  -- balance projection SELECT only, written by SECURITY DEFINER functions), reference data SELECT, and what shared
  -- triggers read in app. No DELETE or TRUNCATE anywhere. fundzim_readonly: the reporting views.
  IF to_regnamespace('ledger') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA ledger TO fundzim_ledger;
    GRANT USAGE ON SCHEMA app TO fundzim_ledger;
    GRANT SELECT ON app.status_transitions, app.currencies TO fundzim_ledger;
    GRANT USAGE ON SCHEMA audit TO fundzim_ledger;
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             format('%s.%s', n.nspname, c.relname) AS fq,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = 'ledger' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger, fundzim_readonly', r.rel);
      ELSIF r.fq = ANY (ledger_reference) THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger', r.rel);
      ELSIF r.tbl = 'ledger_balances' THEN
        -- UPDATE only so the posting path can lock projection rows (SELECT ... FOR UPDATE, ascending account id);
        -- trg_ledger_balances_guard_update rejects every direct UPDATE (writes come from SECURITY DEFINER functions)
        EXECUTE format('GRANT SELECT, UPDATE ON %s TO fundzim_ledger', r.rel);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_ledger', r.rel);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_ledger', r.rel);
      END IF;
    END LOOP;
    EXECUTE 'GRANT USAGE ON SCHEMA ledger TO fundzim_readonly';
  END IF;

  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- Stage 10: the ledger posting role
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_ledger'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_ledger'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_ledger'),
      ('ledger.fold_deferred_balances()', 'fundzim_ledger'),
      ('ledger.database_environment()', 'fundzim_ledger'),
      ('ledger.account_class_signature(text)', 'fundzim_ledger'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS ledger.v_pool_integrity, ledger.v_balance_drift, ledger.v_balances_current, ledger.v_trial_balance;
DROP TABLE IF EXISTS ledger.ledger_reconciliation_references, ledger.ledger_source_references, ledger.ledger_invariant_runs CASCADE;
ALTER TABLE IF EXISTS ledger.ledger_transactions DROP CONSTRAINT IF EXISTS fk_ledger_transactions_adjustment;
DROP TABLE IF EXISTS ledger.ledger_adjustments, ledger.ledger_entries, ledger.ledger_transactions, ledger.ledger_posting_batches,
  ledger.ledger_balances, ledger.ledger_accounts, ledger.ledger_periods, ledger.ledger_posting_rule_lines,
  ledger.ledger_posting_rules, ledger.ledger_environment CASCADE;
DROP FUNCTION IF EXISTS ledger.guard_recon_reference_update(), ledger.rebuild_balance(uuid), ledger.fold_deferred_balances(),
  ledger.check_transaction(), ledger.apply_entry_to_balance(), ledger.before_insert_entry(), ledger.before_insert_transaction(),
  ledger.guard_adjustment_update(), ledger.guard_balance_update(), ledger.create_balance_row(), ledger.guard_account_update(),
  ledger.guard_period_update(), ledger.enforce_environment(), ledger.database_environment(), ledger.account_class_signature(text);
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('ledger_period', 'ledger_adjustment', 'ledger_account', 'ledger_reconciliation_reference');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('ledger.view', 'ledger.account.manage',
  'ledger.period.close', 'ledger.reconciliation.operate', 'ledger.synthetic.post'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('ledger.view', 'ledger.account.manage', 'ledger.period.close', 'ledger.reconciliation.operate',
  'ledger.synthetic.post');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
CREATE OR REPLACE FUNCTION audit.gateway_prefix_ok(p_kind text, p_value text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'action'
      THEN p_value ~ '^(kyc|kyb|beneficiary|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'event'
      THEN p_value ~ '^kyc\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'action'
      THEN p_value ~ '^(compliance|risk|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'event'
      THEN p_value ~ '^compliance\.[a-z_]+(\.[a-z_]+)*$'
    ELSE false
  END
$$;
CREATE OR REPLACE FUNCTION audit.record_evidence(
  p_id uuid, p_evidence_type text, p_subject_type text, p_subject_id uuid, p_related_refs jsonb,
  p_collected_at timestamptz, p_collected_by_type text, p_collected_by_id uuid, p_source text, p_storage_ref text,
  p_content_sha256 bytea, p_size_bytes bigint, p_media_type text, p_classification text, p_retention_class text,
  p_audit_event_id uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF session_user NOT IN ('fundzim_kyc', 'fundzim_compliance')
     OR NOT audit.gateway_payload_ok(coalesce(p_related_refs, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.record_evidence: rejected input (caller %)', session_user USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, related_refs, collected_at,
    collected_by_type, collected_by_id, source, storage_ref, content_sha256, size_bytes, media_type, classification,
    retention_class, audit_event_id)
  VALUES (p_id, p_evidence_type, p_subject_type, p_subject_id, coalesce(p_related_refs, '{}'::jsonb), p_collected_at,
    p_collected_by_type, p_collected_by_id, p_source, p_storage_ref, p_content_sha256, p_size_bytes, p_media_type,
    p_classification, p_retention_class, p_audit_event_id);
  RETURN p_id;
END $$;
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, ledger, audit, risk, recon. Never kyc or compliance.
  FOREACH s IN ARRAY ARRAY['app','queue','ledger','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','ledger','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'ledger' THEN
      privs := 'SELECT, INSERT';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
