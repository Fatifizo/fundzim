/**
 * Stage 8 staff payment-operations shapes (docs/stage-8/interface-contracts.md §10). Money is always an
 * `amount_minor` digit string plus a currency code; nothing here is ever a number of money. Status values are
 * kept as plain strings so an unexpected value from the API is shown (humanised), never hidden.
 */

export interface ProviderRef {
  code: string;
  display_name: string;
  simulated: boolean;
}

/** `{amount_minor, currency}`; `null` when the API sent no valid amount. */
export interface PaymentMoney {
  amount_minor: string;
  currency: string;
}

export interface OverviewProvider extends ProviderRef {
  status: string;
  circuit: string;
}

export interface LiveExecution {
  enabled: boolean;
  constant: boolean;
  kill_switch: boolean;
}

export interface PaymentsOverview {
  attempts_by_status: Array<{ status: string; count: number }>;
  unknown_count: number;
  needs_reconciliation_count: number;
  inbox_by_status: Array<{ status: string; count: number }>;
  rejections_24h: number;
  open_refund_requests: number;
  providers: OverviewProvider[];
  live_execution: LiveExecution;
  sandbox_enabled: boolean;
}

export interface PaymentEvent {
  id: string;
  subject_type: string;
  from_status: string | null;
  to_status: string | null;
  applied: boolean;
  not_applied_reason: string | null;
  transition_code: string | null;
  source: string;
  provider_event_id: string | null;
  raw_status: string | null;
  actor_type: string;
  reason: string | null;
  correlation_id: string | null;
  occurred_at: string | null;
  recorded_at: string | null;
}

export interface AdminIntent {
  id: string;
  campaign_id: string;
  donor_user_id: string | null;
  amount: PaymentMoney | null;
  status: string;
  is_anonymous: boolean;
  simulated: boolean;
  expires_at: string | null;
  created_at: string | null;
  updated_at: string | null;
}

export interface AdminIntentDetail extends AdminIntent {
  attempts: AdminAttempt[];
  events: PaymentEvent[];
}

export interface AdminAttempt {
  id: string;
  donation_intent_id: string;
  campaign_id: string;
  attempt_number: number;
  method: string;
  method_category: string;
  status: string;
  amount: PaymentMoney | null;
  provider: ProviderRef;
  simulated: boolean;
  needs_reconciliation: boolean;
  unknown_since: string | null;
  unknown_reason: string | null;
  failure_code: string | null;
  created_at: string | null;
  updated_at: string | null;
}

export interface ProviderCall {
  id: string;
  attempt_no: number;
  operation: string;
  classification: string;
  error_class: string | null;
  /** Stage 9 normalised provider error category (AUTHENTICATION_FAILED … RECONCILIATION_REQUIRED). */
  error_category: string | null;
  unknown_reason: string | null;
  http_status: number | null;
  provider_status_raw: string | null;
  provider_reference_returned: string | null;
  latency_ms: number | null;
  correlation_id: string | null;
  started_at: string | null;
  deadline_at: string | null;
  completed_at: string | null;
}

export interface ProviderTransaction {
  provider_transaction_ref: string | null;
  raw_status: string | null;
  raw_status_at: string | null;
  mapped_status: string | null;
  mapping_version: string | null;
  last_source: string | null;
  reported_amount: PaymentMoney | null;
  payer_instrument_masked: string | null;
  updated_at: string | null;
}

export interface ProviderReference {
  ref_kind: string;
  reference: string;
  source: string;
  recorded_at: string | null;
}

export interface PaymentSession {
  id: string;
  kind: string;
  status: string;
  redirect_url: string | null;
  expires_at: string | null;
  returned_at: string | null;
}

export interface InboxItem {
  id: string;
  provider: ProviderRef;
  provider_event_id: string;
  event_class: string;
  event_type_raw: string;
  our_reference: string | null;
  provider_reference: string | null;
  status: string;
  attempts: number;
  last_error_code: string | null;
  simulated: boolean;
  signature_key_slot: string | null;
  verification_method: string | null;
  received_at: string | null;
  processed_at: string | null;
}

export interface InboxDetail extends InboxItem {
  payload_redacted: Record<string, unknown> | null;
  raw_body_sha256: string | null;
  dispatched_to: string | null;
  parked_subject_id: string | null;
  parked_until_status: string | null;
  park_deadline_at: string | null;
  next_attempt_at: string | null;
  headers_subset: Record<string, unknown> | null;
}

export interface WebhookRejection {
  id: string;
  provider_code: string;
  reason_code: string;
  body_bytes: number | null;
  body_sha256: string | null;
  received_at: string | null;
}

export interface ReconReference {
  id: string;
  payment_id: string;
  reason: string;
  status: string;
  expected: PaymentMoney | null;
  reported: PaymentMoney | null;
  provider_status_raw: string | null;
  provider_reference: string | null;
  evidence_at: string | null;
  created_at: string | null;
  resolved_at: string | null;
  resolution_note: string | null;
}

export interface PersonRef {
  id: string;
  display_name: string | null;
}

export interface RefundRequest {
  id: string;
  payment_id: string;
  amount: PaymentMoney | null;
  reason_code: string;
  reason_text: string;
  status: string;
  requested_by: PersonRef | null;
  requested_at: string | null;
  decided_by: PersonRef | null;
  decided_at: string | null;
  decision_reason: string | null;
  expires_at: string | null;
  /** Always "DISABLED" in Stage 8 (ADR-039 §4); shown verbatim. */
  execution: string;
}

export interface AttemptDetail extends AdminAttempt {
  provider_payment_ref: string | null;
  last_authoritative_source: string | null;
  poll_attempts: number;
  next_poll_at: string | null;
  poll_horizon_at: string | null;
  provider_calls: ProviderCall[];
  transaction: ProviderTransaction | null;
  references: ProviderReference[];
  session: PaymentSession | null;
  webhooks: InboxItem[];
  events: PaymentEvent[];
  reconciliation: ReconReference[];
  refund_requests: RefundRequest[];
}

export interface ProviderAccount {
  id: string;
  environment: string;
  label: string;
  status: string;
  api_credentials_secret_ref: string | null;
  webhook_secret_ref: string | null;
  webhook_secret_ref_previous: string | null;
  secrets_resolvable: boolean;
  /** Stage 9: every reference lives under the account's own environment (secretref://fundzim/<environment>/…). */
  credential_scope_ok: boolean;
  webhook_replay_window_seconds: number | null;
}

export interface CapabilityFlags {
  refund_api: boolean;
  partial_refund: boolean;
  idempotent_create: boolean;
  supports_cancel: boolean;
  signed_webhooks: boolean;
  status_api: boolean;
  separate_capture: boolean;
  documented_expiry: boolean;
}

export interface ProviderCapability {
  id: string;
  method: string;
  method_category: string;
  currency: string;
  custody_model: string;
  evidence_status: string;
  evidence_source: string | null;
  pcr_ref: string | null;
  routable: boolean;
  capability_version: number | null;
  flags: CapabilityFlags;
}

/** Stage 9 derived provider health (observations only; configuration alone is never HEALTHY). */
export interface ProviderHealth {
  status: string; // HEALTHY | DEGRADED | UNAVAILABLE | UNKNOWN
  reason: string;
  window_seconds: number;
  calls: number;
  errors: number;
  timeouts: number;
  webhooks_verified: number;
  p95_latency_ms: number | null;
  circuit: string;
  outage_declared: boolean;
  credentials_usable: boolean | null;
  last_observed_at: string | null;
}

/** Stage 9 adapter contract basis. */
export interface ProviderContract {
  basis: string; // OFFICIAL_PROVIDER_CONTRACT | PROVIDER_SANDBOX_OBSERVED | DOCUMENTED_BUT_UNVERIFIED | SIMULATED
  sources: string[];
  verified_on: string | null;
}

export interface ProviderRow {
  id: string;
  code: string;
  display_name: string;
  simulated: boolean;
  status: string;
  sandbox_status: string;
  selection_status: string;
  selection_adr: string | null;
  confirmation_mode: string;
  circuit: string;
  allowlisted: boolean;
  adapter_registered: boolean;
  usable: boolean;
  contract: ProviderContract | null;
  health: ProviderHealth;
  accounts: ProviderAccount[];
  capabilities: ProviderCapability[];
}

export interface ProvidersView {
  items: ProviderRow[];
  live_execution: LiveExecution;
  sandbox_enabled: boolean;
  execution_environment: string;
  allowlist: string[];
}

export interface HealthEvent {
  id: string;
  operation: string;
  method: string | null;
  currency: string | null;
  event_kind: string;
  error_class: string | null;
  latency_ms: number | null;
  staff_declared: boolean;
  observed_at: string | null;
}

export interface ProviderDetail extends ProviderRow {
  execution_environment: string;
  recent_health_events: HealthEvent[];
  error_categories_24h: Record<string, number>;
}

export interface HealthRow {
  id: string;
  provider: ProviderRef;
  usable: boolean;
  health: ProviderHealth;
}

export interface HealthView {
  execution_environment: string;
  live_execution: LiveExecution;
  secret_manager: string;
  items: HealthRow[];
}

/** Stage 9 append-only provider evidence (one row per provider observation). */
export interface EvidenceRow {
  id: string;
  source: string;
  execution_environment: string;
  provider_reference: string | null;
  provider_event_id: string | null;
  raw_status: string;
  mapped_status: string;
  mapped: boolean;
  mapping_version: string;
  reported: PaymentMoney | null;
  provider_occurred_at: string | null;
  observed_at: string | null;
  consistency: string; // FIRST | CONSISTENT | PROGRESSION | CONTRADICTS
  contradicts_evidence_id: string | null;
}

export type ProviderAction = "enable-sandbox" | "disable" | "outage" | "outage/resolve";

export interface SandboxState {
  provider_payment_ref: string | null;
  our_reference: string | null;
  scenario: string | null;
  raw_status: string | null;
  amount: PaymentMoney | null;
  method: string | null;
  created_at: string | null;
  updated_at: string | null;
  simulated: boolean;
}

// ---- filter values (DB CHECK constraints, migrations 20261012090000/20261012090100) ----

export const INTENT_STATUSES = ["OPEN", "PAID", "EXPIRED", "CANCELLED"] as const;
export const ATTEMPT_STATUSES = [
  "CREATED",
  "PENDING",
  "REQUIRES_ACTION",
  "AUTHORISED",
  "UNKNOWN",
  "SUCCEEDED",
  "FAILED",
  "EXPIRED",
  "CANCELLED",
  "PARTIALLY_REFUNDED",
  "REFUNDED",
  "DISPUTED",
  "CHARGED_BACK",
] as const;
export const METHODS = ["ECOCASH", "ONEMONEY", "INNBUCKS", "OMARI", "ZIMSWITCH", "BANK_TRANSFER", "CARD"] as const;
export const INBOX_STATUSES = ["RECEIVED", "DISPATCHED", "PARKED", "FAILED_RETRYING", "PROCESSED", "UNRECOGNISED", "DEAD_LETTERED"] as const;
export const REQUEUEABLE_INBOX_STATUSES: ReadonlySet<string> = new Set(["DEAD_LETTERED", "UNRECOGNISED"]);
export const RECON_STATUSES = ["OPEN", "AWAITING_SETTLEMENT_DATA", "RESOLVED_BY_PROVIDER_STATUS"] as const;
export const REFUND_STATUSES = ["REQUESTED", "PENDING_APPROVAL", "ON_HOLD", "APPROVED", "REJECTED", "WITHDRAWN"] as const;
export const REFUND_REASON_CODES = ["DONOR_REQUEST", "DUPLICATE_PAYMENT", "PAYMENT_ERROR", "CAMPAIGN_CANCELLED", "CAMPAIGN_FRAUD", "COMPLIANCE_ORDER", "OTHER"] as const;
/** Payment statuses for which a refund may be requested (contract §4). */
export const REFUNDABLE_PAYMENT_STATUSES: ReadonlySet<string> = new Set(["SUCCEEDED", "PARTIALLY_REFUNDED"]);
export const SANDBOX_WEBHOOK_STATUSES = ["pending", "action_required", "paid", "failed", "expired", "cancelled", "limbo"] as const;
export const SANDBOX_SIGN_WITH = ["current", "previous", "wrong"] as const;

export type RefundAction = "approve" | "reject" | "hold" | "resume" | "withdraw";
