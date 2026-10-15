/**
 * Stage 12 fee API shapes (internal/fees, ADR-044). Every fee is a TEST fee: rates are synthetic configuration,
 * never FundZim's commercial rates, and production fee charging is BLOCKED. Amounts are minor-unit digit strings;
 * a null amount means UNKNOWN (never zero). Quoted, estimated, provider-reported and recognised amounts are distinct.
 */
export const TEST_LABEL = "TEST — synthetic configuration, not FundZim's commercial rates";

export const FEE_CATEGORIES = ["PLATFORM_FEE", "PAYMENT_PROCESSING_FEE", "PAYOUT_PROCESSING_FEE", "REFUND_FEE", "CHARGEBACK_FEE"] as const;
export const FEE_MODELS = ["ZERO_FEE", "PERCENTAGE", "FIXED", "PERCENTAGE_PLUS_FIXED", "TIERED", "CAPPED", "MINIMUM_FEE", "PROVIDER_PASS_THROUGH"] as const;
export const ROUNDING_MODES = ["HALF_UP", "HALF_EVEN", "FLOOR", "CEILING"] as const;
export const RESPONSIBILITIES = ["DONOR_COVERED", "ORGANISER_COVERED", "SHARED", "PLATFORM_ABSORBED"] as const;
export const REFUND_TREATMENTS = ["PROPORTIONAL", "PROPORTIONAL_EXCEPT_FIXED", "NON_REFUNDABLE"] as const;
export const PROVIDER_FEE_TYPES = ["TRANSACTION", "SETTLEMENT", "REFUND", "CHARGEBACK", "PAYOUT"] as const;
/** VERIFIED is never offered: no provider is selected and no contractual evidence exists (ADR-040). */
export const CONFIRMATION_STATUSES = ["SYNTHETIC", "UNVERIFIED"] as const;
export const MANUAL_ADJUSTMENT_REASONS = ["PROVIDER_CORRECTION", "POLICY_CORRECTION", "ACCOUNTING_ADJUSTMENT"] as const;

export interface Paged<T> {
  items: T[];
  next_cursor: string | null;
}

export interface Tier {
  up_to_minor: string | null;
  rate_bps: number;
  fixed_minor: string;
}

export interface Terms {
  model: string;
  rate_bps: number | null;
  fixed_minor: string | null;
  min_fee_minor: string | null;
  max_fee_minor: string | null;
  tiers: Tier[];
  rounding_mode: string;
}

export interface PolicyVersion {
  id: string;
  policy_id: string;
  category: string;
  version_no: number;
  status: string;
  terms: Terms;
  responsibility: string;
  donor_share_bps: number | null;
  refund_treatment: string | null;
  effective_from: string | null;
  effective_to: string | null;
  algorithm_version: string;
  terms_hash: string | null;
  fee_environment: string;
  created_by: string;
  submitted_by: string | null;
  submitted_at: string | null;
  approved_by: string | null;
  approved_at: string | null;
  approval_reason: string | null;
  rejected_by: string | null;
  rejection_reason: string | null;
  activated_by: string | null;
  activated_at: string | null;
  suspended_by: string | null;
  suspension_reason: string | null;
  retired_by: string | null;
  retirement_reason: string | null;
  created_at: string | null;
}

export interface FeePolicy {
  id: string;
  code: string;
  name: string;
  description: string;
  category: string;
  transaction_type: string;
  currency: string;
  payment_method: string | null;
  provider_id: string | null;
  campaign_category: string | null;
  campaign_id: string | null;
  specificity: number;
  fee_environment: string;
  created_by: string;
  created_at: string | null;
  versions: PolicyVersion[];
  active_version_id: string | null;
}

export interface ActivePolicy {
  version_id: string;
  policy_id: string;
  name: string;
  category: string;
  transaction_type: string;
  currency: string;
  scope: string;
  terms: Terms;
  responsibility: string;
  donor_share_bps: number | null;
  refund_treatment: string | null;
  effective_from: string | null;
  fee_environment: string;
}

export interface ProviderSchedule {
  id: string;
  provider_id: string;
  provider_is_sandbox: boolean;
  contract_reference: string;
  schedule_version: string;
  currency: string;
  payment_method: string | null;
  fee_type: string;
  terms: Terms;
  rounding_known: boolean;
  effective_from: string | null;
  effective_to: string | null;
  evidence_source: string;
  evidence_reference: string | null;
  confirmation_status: string;
  status: string;
  fee_environment: string;
  created_by: string;
  approved_by: string | null;
  created_at: string | null;
}

export interface QuoteComponent {
  category: string;
  basis: string;
  amount_minor: string | null;
  responsibility: string;
  donor_part_minor: string | null;
  organiser_part_minor: string | null;
  platform_part_minor: string | null;
  rounding_mode: string;
  note: string;
}

export interface FeeQuote {
  id: string;
  campaign_id: string;
  currency: string;
  amount_minor: string;
  payment_method: string | null;
  platform_fee_minor: string;
  platform_fee_basis: string;
  provider_fee_minor: string | null;
  provider_fee_basis: string;
  donor_charge_minor: string | null;
  campaign_allocation_minor: string | null;
  platform_net_minor: string | null;
  components: QuoteComponent[];
  status: string;
  expires_at: string | null;
  donation_intent_id: string | null;
  calculation_id: string;
  algorithm_version: string;
  system_generated: boolean;
  fee_environment: string;
  notice: string;
  created_at: string | null;
}

export interface Assessment {
  id: string;
  source_type: string;
  source_id: string;
  category: string;
  campaign_id: string | null;
  currency: string;
  basis_minor: string;
  fee_minor: string | null;
  net_fee_minor: string | null;
  basis: string;
  responsibility: string | null;
  donor_part_minor: string | null;
  organiser_part_minor: string | null;
  platform_part_minor: string | null;
  policy_version_id: string | null;
  provider_schedule_id: string | null;
  calculation_id: string;
  quote_id: string | null;
  payment_method: string | null;
  accounting_status: string;
  test_payment: boolean;
  synthetic: boolean;
  fee_environment: string;
  assessed_at: string | null;
}

export interface Adjustment {
  id: string;
  assessment_id: string;
  currency: string;
  reason: string;
  direction: string;
  amount_minor: string;
  source_type: string;
  basis_minor: string | null;
  note: string;
  status: string;
  actor_type: string;
  requested_by: string | null;
  decided_by: string | null;
  decided_at: string | null;
  decision_reason: string | null;
  accounting_status: string;
  ledger_adjustment_id: string | null;
  created_at: string | null;
}

export interface AccountingRef {
  id: string;
  ledger_transaction_id: string;
  journal_key: string;
  posting_rule: string;
  purpose: string;
  currency: string;
  fee_amount_minor: string;
  policy_version_id: string | null;
  recorded_at: string | null;
}

export interface FeeEvent {
  id: string;
  event_type: string;
  actor_type: string;
  actor_id: string;
  occurred_at: string | null;
}

export interface AssessmentDetail {
  assessment: Assessment;
  journals: AccountingRef[];
  adjustments: Adjustment[];
  events: FeeEvent[];
}

export interface ReconRun {
  id: string;
  triggered_by: string;
  started_at: string | null;
  finished_at: string | null;
  checked: number;
  matched: number;
  mismatches: number;
  settlement_reconciliation: string;
}

export interface ReconItem {
  id: string;
  kind: string;
  severity: string;
  assessment_id: string | null;
  currency: string | null;
  detail: string;
  status: string;
  detected_at: string | null;
  resolved_at: string | null;
  resolution_note: string | null;
}

export interface ReconPage {
  items: ReconItem[];
  latest_run: ReconRun | null;
  settlement_reconciliation: string;
}

export interface CurrencyRevenue {
  currency: string;
  quoted_platform_fees_minor: string;
  quotes: number;
  assessed_platform_fees_minor: string;
  platform_fee_assessments: number;
  assessed_on_test_payments_minor: string;
  assessed_on_synthetic_donations_minor: string;
  refunded_platform_fees_minor: string;
  chargeback_reversed_platform_fees_minor: string;
  manual_adjustments_net_minor: string;
  provider_fees_estimated_minor: string;
  provider_fees_unknown: number;
  payout_fees_minor: string;
  chargeback_fee_exposure_minor: string;
  recognised_revenue_minor: string;
  revenue_reversals_minor: string;
  provider_fee_expense_minor: string;
  net_revenue_minor: string;
  cash_received: string;
}

export interface PeriodRevenue {
  currency: string;
  month: string;
  recognised_revenue_minor: string;
  revenue_reversals_minor: string;
  provider_fee_expense_minor: string;
  journals: number;
}

export interface GroupRevenue {
  currency: string;
  key: string;
  assessed_platform_fees_minor: string;
  assessments: number;
}

export interface RevenueReport {
  fee_environment: string;
  label: string;
  from: string | null;
  to: string | null;
  currencies: CurrencyRevenue[];
  by_period: PeriodRevenue[];
  by_payment_method: GroupRevenue[];
  by_provider: GroupRevenue[];
  notes: string[];
}

export interface FeeOverview {
  fee_environment: string;
  production_fees: string;
  policy_versions_by_status: Record<string, number>;
  provider_schedules_by_status: Record<string, number>;
  active_quotes: number;
  assessments: number;
  pending_adjustments: number;
  open_reconciliation_items: number;
  latest_reconciliation: ReconRun | null;
  notes: string[];
}

export interface CampaignFees {
  campaign_id: string;
  fee_environment: string;
  policies: ActivePolicy[];
  assessments: Assessment[];
  adjustments: Adjustment[];
  restrictions: string[];
}
