/**
 * Stage 11 payout (withdrawal) API shapes (internal/payouts, OpenAPI tags "Payouts (Stage 11)" / "Payout operations
 * (Stage 11)"). Amounts are minor-unit digit strings. Every payout in Stage 11 is a TEST payout of synthetic
 * TEST_ONLY ledger funds executed by the sandbox simulator; production payouts are BLOCKED.
 */
export const PAYOUT_STATUSES = ["PAYOUT_REQUESTED", "PENDING_REVIEW", "APPROVED", "SUBMITTED", "PROCESSING", "UNKNOWN", "COMPLETED", "FAILED",
  "REJECTED", "CANCELLED", "REVERSED"] as const;
export type PayoutStatus = (typeof PAYOUT_STATUSES)[number] | string;

export interface Paged<T> {
  items: T[];
  next_cursor: string | null;
}

export interface Reservation {
  id: string;
  payout_request_id: string;
  campaign_id: string;
  currency: string;
  amount_minor: string;
  account_scope: string;
  status: string;
  ledger_reserve_transaction_id: string | null;
  ledger_submit_transaction_id: string | null;
  ledger_release_transaction_id: string | null;
  ledger_consume_transaction_id: string | null;
  expires_at: string | null;
  created_at: string | null;
}

export interface OwnerPayout {
  id: string;
  campaign_id: string;
  currency: string;
  amount_minor: string;
  purpose: string | null;
  status: PayoutStatus;
  status_label: string;
  destination_masked: string;
  destination_rail: string;
  reason_category: string | null;
  test_payout: boolean;
  funds_label: string;
  requested_at: string | null;
  submitted_at: string | null;
  completed_at: string | null;
  can_cancel: boolean;
}

export interface StaffPayout {
  id: string;
  campaign_id: string;
  owner_user_id: string | null;
  owner_organisation_id: string | null;
  beneficiary_id: string | null;
  currency: string;
  amount_minor: string;
  purpose: string | null;
  requested_by: string;
  requested_at: string | null;
  status: PayoutStatus;
  approval_policy: string;
  approval_round: number;
  execution_environment: string;
  test_payout: boolean;
  ledger_environment: string;
  destination_id: string;
  destination_rail: string;
  destination_masked: string;
  destination_category: string;
  provider_payout_ref: string | null;
  unknown_since: string | null;
  unknown_reason: string | null;
  needs_reconciliation: boolean;
  submitted_at: string | null;
  completed_at: string | null;
  failure_code: string | null;
  decision_reason_code: string | null;
  reversed_amount_minor: string | null;
  created_at: string | null;
  reservation: Reservation | null;
}

export interface CheckResult {
  check_id: string;
  result: string;
  detail_code: string;
}

export interface Decision {
  id: string;
  phase: string;
  outcome: string;
  approval_tier: string | null;
  evaluated_at: string | null;
  record_hash: string;
  results: CheckResult[];
}

export type Row = Record<string, string | number | boolean | null>;

export interface JournalLink {
  idempotency_key: string;
  transaction_id: string | null;
}

export interface PayoutException {
  id: string;
  payout_request_id: string | null;
  campaign_id: string | null;
  exception_type: string;
  severity: string;
  source: string;
  status: string;
  owner_id: string | null;
  resolution_code: string | null;
  detected_at: string | null;
  resolved_at: string | null;
  age_seconds: number;
  evidence: Record<string, unknown>;
}

export interface PayoutDetail {
  payout: StaffPayout;
  approvals: Row[];
  attempts: Row[];
  events: Row[];
  decisions: Decision[];
  exceptions: PayoutException[];
  journals: JournalLink[];
}

export interface PayoutOverview {
  execution_environment: string;
  live_execution: boolean;
  production_status: string;
  ledger_environment: string;
  by_status: Record<string, number>;
  open_exceptions_by_severity: Record<string, number>;
  active_reserved_minor_by_currency: Record<string, string>;
  settlement_reconciliation: string;
}

export interface ReconReference {
  id: string;
  payout_request_id: string | null;
  mismatch_type: string;
  expected_amount_minor: string | null;
  actual_amount_minor: string | null;
  currency: string | null;
  provider_reference: string | null;
  detected_at: string | null;
  detected_by_run: string;
}

export interface Availability {
  campaign_id: string;
  currency: string;
  environment: string;
  funds_label: string;
  settled_released_minor: string;
  refund_exposure_minor: string;
  reserved_minor: string;
  available_minor: string;
  payouts_enabled: boolean;
  production_status: string;
  blockers: string[];
  checks: CheckResult[];
}

export interface TimelineItem {
  at: string | null;
  status: string;
  label: string;
}
