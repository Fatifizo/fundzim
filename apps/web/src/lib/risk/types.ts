/**
 * Stage 13 risk operations shapes (internal/risk, ADR-045). TEST data only: a risk score is a prioritisation aid,
 * never proof of fraud; production screening is BLOCKED (no provider is selected). Staff-only.
 */

export const RISK_CATEGORIES = [
  "IDENTITY_RISK",
  "CAMPAIGN_RISK",
  "PAYMENT_RISK",
  "WITHDRAWAL_RISK",
  "DESTINATION_RISK",
  "SESSION_RISK",
  "DEVICE_RISK",
  "VELOCITY_RISK",
  "COMPLIANCE_RISK",
  "PROVIDER_RISK",
  "FEE_MANIPULATION_RISK",
  "CHARGEBACK_RISK",
] as const;
export const SUBJECT_TYPES = ["USER", "ORGANISATION", "CAMPAIGN", "BENEFICIARY", "PAYOUT_DESTINATION", "DONATION_INTENT", "PAYOUT", "AUTHENTICATION_SESSION"] as const;
export const CLASSIFICATIONS = ["LOW", "MEDIUM", "HIGH", "CRITICAL"] as const;
export const RULE_ACTIONS = ["ALLOW_WITH_MONITORING", "REVIEW_REQUIRED", "TEMPORARY_HOLD", "ESCALATE", "BLOCK"] as const;
export const AUTOMATED_HOLD_TYPES = ["WITHDRAWAL_HOLD", "PAYOUT_DESTINATION_HOLD"] as const;
export const RULE_SEVERITIES = ["LOW", "MEDIUM", "HIGH", "CRITICAL"] as const;
export const PRIORITIES = ["P1", "P2", "P3", "P4"] as const;
export const CASE_TYPES = [
  "TRANSACTION_MONITORING",
  "CAMPAIGN_RISK",
  "ACCOUNT_TAKEOVER",
  "DESTINATION_RISK",
  "WITHDRAWAL_RISK",
  "SCREENING_REVIEW",
  "FEE_MANIPULATION",
  "IDENTITY_RISK",
  "OTHER",
] as const;
export const CASE_STATUSES = ["OPEN", "TRIAGE", "UNDER_INVESTIGATION", "AWAITING_INFORMATION", "ESCALATED", "DECISION_PENDING", "RESOLVED", "CLOSED", "REOPENED"] as const;
export const CASE_OUTCOMES = ["NO_FURTHER_ACTION", "FALSE_POSITIVE", "RISK_CONFIRMED_HOLD_MAINTAINED", "ESCALATED_TO_COMPLIANCE"] as const;
export const HOLD_TYPES = ["CAMPAIGN_HOLD", "DONATION_HOLD", "WITHDRAWAL_HOLD", "PAYOUT_DESTINATION_HOLD", "ACCOUNT_REVIEW_HOLD", "BENEFICIARY_HOLD"] as const;
export const HOLD_STATUSES = ["REQUESTED", "ACTIVE", "UNDER_REVIEW", "RELEASE_PENDING", "RELEASED", "EXPIRED", "REVOKED"] as const;
/** Which subjects a hold type may apply to (ck_risk_holds_subject). */
export const HOLD_SUBJECTS: Record<(typeof HOLD_TYPES)[number], readonly string[]> = {
  CAMPAIGN_HOLD: ["CAMPAIGN"],
  DONATION_HOLD: ["CAMPAIGN", "USER"],
  WITHDRAWAL_HOLD: ["CAMPAIGN", "USER", "ORGANISATION"],
  PAYOUT_DESTINATION_HOLD: ["PAYOUT_DESTINATION"],
  ACCOUNT_REVIEW_HOLD: ["USER", "ORGANISATION"],
  BENEFICIARY_HOLD: ["BENEFICIARY"],
};
export const SCREENING_STATUSES = ["PENDING", "POTENTIAL_MATCH", "NO_MATCH_REPORTED", "REVIEW_REQUIRED", "CONFIRMED_MATCH", "CLEARED", "ERROR"] as const;
export const SCREENING_SUBJECTS = ["USER", "ORGANISATION", "BENEFICIARY"] as const;
export const SCREENING_SCENARIOS = [
  "NO_MATCH",
  "POTENTIAL_MATCH",
  "MULTIPLE_MATCHES",
  "PROVIDER_TIMEOUT",
  "PROVIDER_ERROR",
  "DUPLICATE_CALLBACK",
  "OUT_OF_ORDER_CALLBACK",
  "INVALID_SIGNATURE",
  "DELAYED_RESULT",
] as const;
export const SIGNAL_TYPES = [
  "DOCUMENT_REJECTED",
  "KYC_REJECTED",
  "KYB_REJECTED",
  "DUPLICATE_IDENTITY",
  "BENEFICIARY_REJECTED",
  "BENEFICIARY_ESCALATED",
  "DESTINATION_CHANGED",
  "DESTINATION_REJECTED",
  "CAMPAIGN_CREATED",
  "CAMPAIGN_REPORTED",
  "CAMPAIGN_REVIEW_ESCALATED",
  "DONATION_INTENT_CREATED",
  "PAYMENT_FAILED",
  "PROVIDER_EVIDENCE_CONTRADICTION",
  "WITHDRAWAL_REQUESTED",
  "PAYOUT_FAILED",
  "PAYOUT_RETURNED",
  "MFA_CHANGED",
  "PASSWORD_CHANGED",
  "PASSWORD_RESET_REQUESTED",
  "ACCOUNT_SUSPENDED",
  "SIGNED_IN",
  "RESTRICTION_APPLIED",
  "FEE_POLICY_CHANGED",
  "FEE_ADJUSTMENT_REQUESTED",
  "SCREENING_POTENTIAL_MATCH",
  "SCREENING_CONFIRMED_MATCH",
] as const;
export const STAFF_EVIDENCE_TYPES = ["STAFF_NOTE", "VERIFICATION_REF", "CAMPAIGN_REF", "PAYMENT_REF", "PAYOUT_REF", "DOCUMENT_REF", "PROVIDER_EVIDENCE"] as const;
export const SIGNAL_REVIEW_CLASSES = ["FALSE_POSITIVE", "RELEVANT", "INCONCLUSIVE"] as const;

export interface Paged<T> {
  items: T[];
  next_cursor: string | null;
}

export interface Count {
  key: string;
  count: number;
}

export interface RiskOverview {
  generated_at: string | null;
  environment: "TEST";
  signals_last_24h_by_category: Count[];
  evaluations_last_7d_by_classification: Count[];
  open_cases_by_priority: Count[];
  open_cases_by_status: Count[];
  overdue_cases: number;
  open_holds_by_type: Count[];
  screenings_by_status: Count[];
  monitoring_last_24h_by_type: Count[];
  active_rules: number;
  pending_rule_approvals: number;
  pending_hold_activations: number;
  pending_hold_releases: number;
  pending_case_decisions: number;
  screening_production: "BLOCKED";
  accuracy_metrics: "NOT_MEASURED";
}

export interface RiskSignal {
  id: string;
  signal_type: string;
  category: string;
  severity: string;
  confidence: number;
  kind: string;
  subject_type: string;
  subject_id: string;
  source_module: string;
  source_event_id: string;
  observed_at: string | null;
  recorded_at: string | null;
  facts: Record<string, string>;
  review_classifications: string[];
}

export interface Factor {
  rule_code: string;
  rule_name: string;
  contribution: number;
  severity: string;
  summary: string;
}

export interface RuleOutcome {
  rule_version_id: string;
  rule_code: string;
  fired: boolean;
  contribution: number;
  action: string;
  conditions: string[];
  signal_ids: string[];
}

export interface EvalAction {
  action_type: string;
  automated: boolean;
  case_id: string | null;
  hold_id: string | null;
}

export interface Evaluation {
  id: string;
  subject_type: string;
  subject_id: string;
  trigger_signal_id: string;
  engine_version: string;
  rule_version_ids: string[];
  score: number;
  classification: string;
  band_source: string;
  confidence: number;
  recommended_action: string;
  results: RuleOutcome[];
  explanation: Factor[];
  inputs_hash: string;
  evaluated_at: string | null;
  actions: EvalAction[];
  notice: string;
}

export interface Simulation {
  score: number;
  classification: string;
  band_source: string;
  confidence: number;
  recommended_action: string;
  results: RuleOutcome[];
  explanation: Factor[];
  notice: string;
}

export interface MonitoringEvent {
  id: string;
  event_type: string;
  severity: string;
  subject_type: string | null;
  subject_id: string | null;
  detail: Record<string, string>;
  occurred_at: string | null;
}

export interface RuleQuality {
  rule_code: string;
  rule_version_id: string;
  fired: number;
  cases_resolved: number;
  false_positive_outcomes: number;
  risk_confirmed_outcomes: number;
  no_further_action_outcomes: number;
  signal_false_positive_reviews: number;
}

export interface QualityReport {
  generated_at: string | null;
  window_days: number;
  rules: RuleQuality[];
  accuracy: "NOT_MEASURED";
  notice: string;
}

export interface ReconCheck {
  code: string;
  status: string;
  findings: number;
  examples: string[];
}

export interface ReconReport {
  run_at: string | null;
  checks: ReconCheck[];
}

export interface HistoryEvent {
  event_type: string;
  actor_type: string;
  actor_id: string;
  detail: Record<string, string>;
  occurred_at: string | null;
}

export interface RuleSummary {
  id: string;
  code: string;
  name: string;
  subject_type: string;
  category: string;
  active_version_no: number | null;
  pending_version_no: number | null;
  active_action: string | null;
}

export interface RuleVersion {
  id: string;
  rule_id: string;
  version_no: number;
  status: string;
  definition: string;
  contribution: number;
  severity: string;
  recommended_action: string;
  hold_type: string | null;
  hold_duration_seconds: number | null;
  case_priority: string;
  engine_version: string;
  definition_hash: string | null;
  created_by: string;
  submitted_by: string | null;
  approved_by: string | null;
  approval_reason: string | null;
  rejected_by: string | null;
  rejection_reason: string | null;
  activated_at: string | null;
  suspended_by: string | null;
  suspension_reason: string | null;
  retired_by: string | null;
  retirement_reason: string | null;
  version: number;
  created_at: string | null;
}

export interface RiskRule {
  id: string;
  code: string;
  name: string;
  description: string;
  subject_type: string;
  category: string;
  created_by: string;
  created_at: string | null;
  versions: RuleVersion[];
  active_version_id: string | null;
}

export interface RiskCase {
  id: string;
  case_number: number;
  case_type: string;
  subject_type: string;
  subject_id: string;
  status: string;
  priority: string;
  risk_score: number | null;
  classification: string | null;
  recommended_action: string | null;
  assigned_team: string;
  assigned_to: string | null;
  due_at: string | null;
  proposed_outcome: string | null;
  proposed_by: string | null;
  proposed_reason: string | null;
  decision_outcome: string | null;
  decided_by: string | null;
  decision_reason: string | null;
  escalated_at: string | null;
  opened_by_type: string;
  opened_by: string;
  version: number;
  created_at: string | null;
}

export interface Evidence {
  id: string;
  evidence_type: string;
  reference_type: string | null;
  reference_id: string | null;
  content: Record<string, string>;
  content_hash: string;
  source: string;
  added_by_type: string;
  added_by: string;
  retention_class: string;
  supersedes_id: string | null;
  superseded_by: string | null;
  added_at: string | null;
}

export interface Assignment {
  assigned_to: string;
  assigned_by: string;
  reason: string;
  assigned_at: string | null;
}

export interface SignalReview {
  id: string;
  signal_id: string;
  classification: string;
  explanation: string;
  reviewed_by: string;
  reviewed_at: string | null;
}

export interface Hold {
  id: string;
  hold_type: string;
  subject_type: string;
  subject_id: string;
  status: string;
  reason_code: string;
  description: string;
  automated: boolean;
  evaluation_id: string | null;
  case_id: string | null;
  created_by_type: string;
  created_by: string;
  activated_at: string | null;
  expires_at: string | null;
  release_requested_by: string | null;
  release_reason: string | null;
  released_by: string | null;
  released_at: string | null;
  revocation_reason: string | null;
  version: number;
  created_at: string | null;
}

export interface CaseDetail {
  case: RiskCase;
  evidence: Evidence[];
  assignments: Assignment[];
  signal_reviews: SignalReview[];
  holds: Hold[];
  events: HistoryEvent[];
}

export interface ScreeningRequest {
  id: string;
  subject_type: string;
  subject_id: string;
  provider_code: string;
  environment: "TEST";
  synthetic_reference: string;
  status: string;
  provider_reference: string | null;
  result_sequence: number;
  match_count: number;
  timeout_at: string | null;
  poll_attempts: number;
  last_error: string | null;
  requested_by: string;
  reviewed_by: string | null;
  review_reason: string | null;
  case_id: string | null;
  version: number;
  created_at: string | null;
  label: "TEST_ONLY";
}

export interface ScreeningMatch {
  match_id: string;
  list: string;
  score: number;
}

export interface ScreeningResult {
  provider_event_id: string;
  source: string;
  sequence: number;
  reported_status: string;
  matches: ScreeningMatch[];
  applied: boolean;
  received_at: string | null;
  label: "TEST_ONLY";
}

export interface ScreeningDetail {
  request: ScreeningRequest;
  results: ScreeningResult[];
}

export interface ProviderCapabilities {
  code: string;
  environment: string;
  webhooks: boolean;
  polling: boolean;
  synthetic_only: boolean;
}

export interface ScreeningCapabilities {
  production_screening: "BLOCKED";
  provider_selected: boolean;
  providers: ProviderCapabilities[];
}
