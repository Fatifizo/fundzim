/**
 * Wire types for the Stage 15 notifications API (ADR-047, api/openapi/fundzim-v1.yaml "Notifications (Stage 15)").
 * The API decides everything (routing, consent, suppression); the UI displays it.
 */
export const CATEGORIES = ["SECURITY", "TRANSACTIONAL", "CAMPAIGN", "VERIFICATION", "PAYMENT", "WITHDRAWAL", "PAYOUT", "RISK", "COMPLIANCE", "SUPPORT",
  "ADMINISTRATIVE", "MARKETING"] as const;
export const CHANNELS = ["IN_APP", "EMAIL", "SMS", "WHATSAPP"] as const;
export const DELIVERY_STATUSES = ["QUEUED", "PROCESSING", "SUBMITTED", "ACCEPTED", "DELIVERED", "READ", "FAILED_RETRYABLE", "FAILED_PERMANENT", "SUPPRESSED",
  "CANCELLED", "UNKNOWN"] as const;

export interface InboxItem {
  id: string;
  category: string;
  title: string;
  body: string;
  priority: "NORMAL" | "HIGH";
  deep_link: string | null;
  related_type: string | null;
  related_id: string | null;
  test_data: boolean;
  read_at: string | null;
  created_at: string;
}

export interface Preference {
  category: string;
  channel: string;
  enabled: boolean;
  mandatory: boolean;
  consent_required: boolean;
  available: boolean;
}

export interface Consent {
  channel: string;
  purpose: "SERVICE_MESSAGES" | "MARKETING";
  status: "NOT_REQUESTED" | "GRANTED" | "WITHDRAWN" | "EXPIRED" | "REQUIRES_RECONFIRMATION";
  policy_version: string | null;
  current_policy_version: string;
  granted_at: string | null;
  withdrawn_at: string | null;
  available: boolean;
}

export interface Delivery {
  id: string;
  notification_id: string;
  template_key: string;
  category: string;
  recipient_user_id: string;
  test_data: boolean;
  channel: string;
  status: string;
  provider_code: string | null;
  destination_masked: string | null;
  decision: string;
  suppression_reason: string | null;
  attempt_count: number;
  max_attempts: number;
  next_attempt_at: string | null;
  provider_message_id: string | null;
  submitted_at: string | null;
  accepted_at: string | null;
  delivered_at: string | null;
  read_at: string | null;
  failed_at: string | null;
  failure_code: string | null;
  conflicting_evidence: boolean;
  version: number;
  created_at: string;
}

export interface Attempt {
  attempt_no: number;
  provider_code: string;
  outcome: string;
  error_code: string | null;
  provider_message_id: string | null;
  latency_ms: number;
  started_at: string;
  finished_at: string;
}

export interface Evidence {
  provider_event_id: string;
  reported_status: string;
  occurred_at: string;
  received_at: string;
  applied: boolean;
  not_applied_reason: string | null;
  applied_at: string | null;
}

export interface DeliveryDetail {
  delivery: Delivery;
  attempts: Attempt[];
  provider_evidence: Evidence[];
  template_version_no: number;
}

export interface Provider {
  provider_code: string;
  channel: string;
  display_name: string;
  environment: string;
  readiness: string;
  simulated: boolean;
  capabilities: Record<string, boolean>;
  credential_ref: string | null;
  known_limitations: string;
  active_in_this_process: boolean;
  attempts_24h: number;
  failed_attempts_24h: number;
  health: string;
}

export interface CommsOverview {
  generated_at: string;
  environment: string;
  live_messaging_enabled: boolean;
  marketing_enabled: boolean;
  deliveries_by_status: Record<string, number>;
  deliveries_by_channel_24h: Record<string, number>;
  dead_letters_open: number;
  retry_backlog: number;
  oldest_retry_age_seconds: number | null;
  queue_backlog: number;
  oldest_queued_age_seconds: number | null;
  attempts_last_1h: number;
  failed_attempts_last_1h: number;
  error_rate_last_1h: number | null;
  latency_p50_ms_24h: number | null;
  latency_p95_ms_24h: number | null;
  consent_restricted_24h: number;
  webhook_rejections_24h: number;
  conflicting_evidence: number;
  workers: Array<{ worker_id: string; last_seen_at: string; status: string }>;
  providers: Provider[];
  notice: string;
}

export interface DeadLetter {
  id: string;
  delivery_id: string;
  failure_category: string;
  attempt_count: number;
  last_error: string | null;
  next_action: string;
  review_status: string;
  reviewed_by: string | null;
  reviewed_at: string | null;
  review_reason: string | null;
  created_at: string;
  delivery: Delivery;
}

export interface TemplateVersion {
  id: string;
  version_no: number;
  locale: string;
  status: string;
  email_subject: string | null;
  email_text: string | null;
  sms_text: string | null;
  whatsapp_template_name: string | null;
  whatsapp_language: string | null;
  whatsapp_provider_status: string;
  inapp_title: string;
  inapp_body: string;
  content_hash: string;
  approval_source: string;
  created_by: string | null;
  submitted_by: string | null;
  approved_by: string | null;
  decision_reason: string | null;
  activated_at: string | null;
  sms_segments: number | null;
  version: number;
  created_at: string;
}

export interface Template {
  id: string;
  key: string;
  category: string;
  sensitive: boolean;
  allowed_variables: string[];
  origin: string;
  active_version: number | null;
  pending_status: string | null;
  versions?: TemplateVersion[];
}

export interface Suppression {
  id: string;
  channel: string;
  user_id: string | null;
  reason: string;
  source: string;
  created_at: string;
  lifted_at: string | null;
  lift_reason: string | null;
}

export interface ReportRow {
  group: string;
  key: string;
  count: number;
  rate?: number;
}

export interface CommsReport {
  from: string;
  to: string;
  environment: string;
  rows: ReportRow[];
  latency_p50_ms: number | null;
  latency_p95_ms: number | null;
  limitations: string[];
}

/** Template lifecycle actions the UI offers per status (the API is the authority). */
export function templateActions(status: string): string[] {
  switch (status) {
    case "DRAFT":
      return ["submit", "retire"];
    case "PENDING_APPROVAL":
      return ["approve", "reject"];
    case "APPROVED":
      return ["activate", "retire"];
  }
  return [];
}
