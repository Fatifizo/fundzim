/** Stage 10 staff ledger API shapes (docs/stage-10/posting-engine.md, api/openapi "Ledger (Stage 10)"). Money is a digit string. */
export interface LedgerCheck {
  code: string;
  name: string;
  status: string; // PASSED | FAILED | ERROR
  checked: number;
  violations: number;
  sample: string[];
}

export interface LedgerPeriod {
  id: string;
  code: string;
  starts_at: string | null;
  ends_at: string | null;
  status: string; // OPEN | CLOSING | CLOSED
  close_requested_by: string | null;
  closed_by: string | null;
  closed_at: string | null;
}

export interface TrialBalanceRow {
  account_id: string;
  code: string;
  account_class: string;
  type: string;
  debit_minor: string;
  credit_minor: string;
}

export interface TrialBalanceCurrency {
  currency: string;
  total_debit_minor: string;
  total_credit_minor: string;
  difference_minor: string;
  balanced: boolean;
  rows: TrialBalanceRow[];
}

export interface LedgerOverview {
  environment: string; // TEST | PRODUCTION | UNKNOWN
  production_posting_enabled: boolean;
  journals: number;
  accounts: number;
  open_exceptions: number;
  pending_adjustments: number;
  trial_balance: TrialBalanceCurrency[];
  integrity: LedgerCheck[];
  open_periods: LedgerPeriod[];
  synthetic_fixtures_enabled: boolean;
  notice: string;
}

export interface LedgerAccount {
  id: string;
  code: string;
  account_class: string;
  type: string;
  normal_balance: string;
  kind: string;
  currency: string;
  owner_type: string;
  owner_id: string | null;
  environment: string;
  status: string; // ACTIVE | FROZEN | CLOSED
  status_reason: string | null;
  non_negative: boolean;
  balance_minor: string;
  debit_total_minor: string;
  credit_total_minor: string;
  entry_count: number;
}

export interface JournalSummary {
  id: string;
  posting_rule: string;
  posting_rule_version: number;
  environment: string;
  currency: string;
  total_minor: string;
  line_count: number;
  description: string;
  source_system: string;
  source_type: string;
  source_id: string;
  evidence_class: string;
  idempotency_key: string;
  occurred_at: string | null;
  posted_at: string | null;
  period_code: string;
  is_prior_period: boolean;
  reverses_transaction_id: string | null;
  reversed_by_transaction_id: string | null;
  adjustment_id: string | null;
  test_only: boolean;
}

export interface JournalLine {
  line_no: number;
  account_id: string;
  account_code: string;
  account_class: string;
  direction: string;
  debit_minor: string;
  credit_minor: string;
  currency: string;
}

export interface SourceReference {
  id: string;
  source_system: string;
  source_type: string;
  source_id: string;
  source_event_id: string;
  posting_rule: string;
  evidence_class: string;
  decision: string;
  reason_code: string;
  transaction_id: string | null;
  observed_at: string | null;
}

export interface JournalDetail extends JournalSummary {
  reason: string | null;
  created_by: string;
  metadata: Record<string, string>;
  lines: JournalLine[];
  source_references: SourceReference[];
  balanced: boolean;
}

export interface ReconException {
  id: string;
  environment: string;
  exception_type: string;
  source_type: string;
  source_id: string;
  transaction_id: string | null;
  expected_amount_minor: string | null;
  actual_amount_minor: string | null;
  currency: string | null;
  status: string; // OPEN | INVESTIGATING | RESOLVED
  owner_id: string | null;
  detected_at: string | null;
  age_seconds: number;
  resolution_note: string | null;
}

export interface LedgerAdjustment {
  id: string;
  kind: string;
  posting_rule: string;
  currency: string;
  target_transaction_id: string | null;
  reason_code: string;
  reason: string;
  status: string;
  requested_by: string;
  requested_at: string | null;
  expires_at: string | null;
  approved_by: string | null;
  reversal_transaction_id: string | null;
  resulting_transaction_id: string | null;
}

export interface Paged<T> {
  items: T[];
  next_cursor: string | null;
}
