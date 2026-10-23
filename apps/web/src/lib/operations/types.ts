/**
 * Wire types for the Stage 14 operations portal (ADR-046, api/openapi/fundzim-v1.yaml "Operations (Stage 14)"). The
 * API is the authority on every value: the portal never computes a metric, a status or a permission itself.
 */

/** How a metric was produced. TEST_ONLY and SIMULATED values are never business performance. */
export const NATURES = ["REAL", "SIMULATED", "TEST_ONLY", "UNKNOWN", "NOT_AVAILABLE", "BLOCKED"] as const;
export const HEALTH_STATUSES = ["HEALTHY", "DEGRADED", "UNAVAILABLE", "BLOCKED", "UNKNOWN", "NOT_CONFIGURED"] as const;

export interface Metric {
  key: string;
  label: string;
  module: string;
  value: number | null;
  nature: string;
  status: string | null;
  source: string;
  permission: string;
  href: string | null;
}

export interface SourceStatus {
  module: string;
  status: string;
  error: string | null;
}

export interface Alert {
  key: string;
  severity: string;
  title: string;
  source: string;
  observed_at: string;
  incident_id: string | null;
}

export interface Tasks {
  approvals_to_decide: number;
  support_cases_assigned: number;
  support_cases_overdue: number;
  exceptions_assigned: number;
  incidents_owned: number;
  access_review_items_pending: number;
  open_incidents: number;
}

export interface Overview {
  generated_at: string;
  environment: string;
  metrics: Metric[];
  sources: SourceStatus[];
  alerts: Alert[];
  tasks: Tasks;
  notice: string;
}

export interface Approval {
  id: string;
  source_module: string;
  source_record_id: string;
  action: string;
  summary: string;
  requested_by: string;
  requested_at: string;
  required_permission: string;
  status: string;
  risk_level: string | null;
  href: string;
  can_decide: boolean;
  blocked_reason: string | null;
  age_seconds: number;
}

export interface ExceptionItem {
  id: string;
  source_module: string;
  source_ref: string;
  severity: string;
  category: string;
  summary: string;
  created_at: string;
  domain_status: string;
  href: string;
  correlation_id: string | null;
  domain_open: boolean;
  triage_status: string;
  assigned_to: string | null;
  triage_resolution: string | null;
  triage_updated_at: string | null;
  triage_version: number;
}

export interface HistoryEntry {
  event_type: string;
  actor_id: string;
  from_status: string | null;
  to_status: string | null;
  note: string | null;
  occurred_at: string;
}

export interface HealthComponent {
  name: string;
  status: string;
  detail: string;
  source: string;
  observed_at: string;
  facts: Record<string, string>;
}

export interface HealthReport {
  generated_at: string;
  overall: string;
  components: HealthComponent[];
  notice: string;
}

export interface Incident {
  id: string;
  incident_number: number;
  title: string;
  severity: string;
  affected_module: string;
  status: string;
  owner_id: string | null;
  started_at: string;
  detected_by: string;
  alert_key: string | null;
  resolution: string | null;
  resolved_at: string | null;
  post_incident_notes: string | null;
  created_by: string;
  version: number;
  created_at: string;
}

export interface SupportCase {
  id: string;
  case_number: number;
  subject: string;
  category: string;
  priority: string;
  status: string;
  requester_user_id: string | null;
  assigned_to: string | null;
  related_campaign_id: string | null;
  related_reference_type: string | null;
  related_reference_id: string | null;
  escalated_to: string | null;
  escalation_reference: string | null;
  resolution: string | null;
  resolved_at: string | null;
  due_at: string | null;
  created_by: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface StaffMember {
  id: string;
  display_name: string;
  status: string;
  personal_account_linked: boolean;
  roles: string[];
  mfa_confirmed: boolean;
  last_activity_at: string | null;
  active_sessions: number;
  created_at: string | null;
}

export interface RoleRequest {
  id: string;
  action: string;
  target_user_id: string;
  role_code: string;
  requested_by: string;
  justification: string;
  status: string;
  expires_at: string;
  created_at: string;
}

export interface RoleHistoryItem {
  role: string;
  granted_by: string | null;
  approved_by: string | null;
  valid_from: string;
  expires_at: string | null;
  revoked_at: string | null;
  revoke_reason: string | null;
}

export interface StaffDetail {
  member: StaffMember;
  permissions: string[];
  role_history: RoleHistoryItem[];
  pending_role_requests: RoleRequest[];
  recent_security_events: Array<{ type: string; occurred_at: string }>;
}

export interface RoleInventoryItem {
  code: string;
  name: string;
  permissions: string[];
  holders: number;
}

export interface ReviewItem {
  id: string;
  staff_user_id: string;
  role_code: string;
  decision: string;
  decided_by: string | null;
  decided_at: string | null;
  reason: string | null;
  revoke_request_id: string | null;
}

export interface AccessReview {
  id: string;
  title: string;
  scope_role: string | null;
  status: string;
  created_by: string;
  reviewer_id: string;
  due_at: string | null;
  completed_at: string | null;
  version: number;
  created_at: string;
  items: ReviewItem[];
  pending_items: number;
}

export interface SLATarget {
  id: string;
  metric_key: string;
  target_seconds: number;
  status: string;
  reason: string;
  proposed_by: string;
  proposed_at: string;
  decided_by: string | null;
  decided_at: string | null;
  decision_reason: string | null;
}

export interface AuditRecord {
  id: string;
  seq: number;
  occurred_at: string;
  actor_type: string;
  actor_id: string | null;
  actor_role: string | null;
  action: string;
  target_type: string;
  target_id: string | null;
  outcome: string;
  request_id: string | null;
  correlation_id: string | null;
  reason: string | null;
  break_glass: boolean;
  hash: string;
}

export interface ReportRow {
  group: string;
  label: string;
  value: number | null;
  unit: string;
  nature: string;
}

export interface Report {
  key: string;
  title: string;
  generated_at: string;
  from: string | null;
  to: string | null;
  environment: string;
  source: string;
  freshness: string;
  limitations: string[];
  rows: ReportRow[];
}

export interface SearchHit {
  kind: string;
  id: string;
  label: string;
  status: string | null;
  href: string;
}

export interface SearchResult {
  query: string;
  hits: SearchHit[];
  sources: SourceStatus[];
  notice: string;
}

// Closed vocabularies (identical to internal/operations; the API and the database guards are the authority)
export const SUPPORT_CATEGORIES = ["GENERAL", "ACCOUNT", "CAMPAIGN", "DONATION", "VERIFICATION", "WITHDRAWAL", "TECHNICAL"] as const;
export const SUPPORT_PRIORITIES = ["P1", "P2", "P3", "P4"] as const;
export const SUPPORT_STATUSES = ["OPEN", "TRIAGE", "IN_PROGRESS", "WAITING_FOR_CUSTOMER", "ESCALATED", "RESOLVED", "CLOSED", "REOPENED"] as const;
export const SUPPORT_REFERENCE_TYPES = ["DONATION_INTENT", "PAYMENT", "PAYOUT_REQUEST", "KYC_CASE", "KYB_CASE", "BENEFICIARY", "CAMPAIGN_REPORT", "OTHER"] as const;
export const ESCALATION_TARGETS = ["CAMPAIGN_MODERATION", "VERIFICATION", "RISK_COMPLIANCE", "PAYMENTS", "PAYOUTS", "FINANCIAL_OPERATIONS", "TECHNICAL_OPERATIONS"] as const;
export const SEVERITIES = ["SEV1", "SEV2", "SEV3", "SEV4"] as const;
export const INCIDENT_STATUSES = ["DETECTED", "ACKNOWLEDGED", "INVESTIGATING", "MITIGATING", "MONITORING", "RESOLVED", "CLOSED"] as const;
export const TRIAGE_STATUSES = ["UNTRIAGED", "OPEN", "ACKNOWLEDGED", "INVESTIGATING", "WAITING_EXTERNAL", "RESOLVED", "CLOSED", "REOPENED"] as const;
export const EXCEPTION_MODULES = ["campaigns", "kyc", "beneficiaries", "compliance", "risk", "payments", "providers", "ledger", "payouts", "fees"] as const;
export const APPROVAL_MODULES = ["identity", "campaigns", "kyc", "beneficiaries", "compliance", "risk", "payments", "ledger", "payouts", "fees"] as const;
export const REVIEW_DECISIONS = ["CERTIFIED", "REVOKE_RECOMMENDED"] as const;
export const SLA_METRICS = ["VERIFICATION_REVIEW_TURNAROUND", "CAMPAIGN_MODERATION_TURNAROUND", "SUPPORT_FIRST_RESPONSE", "SUPPORT_RESOLUTION",
  "RISK_INVESTIGATION_AGE", "APPROVAL_PROCESSING", "EXCEPTION_RESOLUTION", "INCIDENT_ACKNOWLEDGEMENT"] as const;

/** Lifecycle edges, identical to internal/operations (SupportEdges, IncidentEdges, ExceptionEdges) and the database guards. */
export const SUPPORT_EDGES: Record<string, readonly string[]> = {
  OPEN: ["TRIAGE"],
  REOPENED: ["TRIAGE"],
  TRIAGE: ["IN_PROGRESS", "ESCALATED", "RESOLVED"],
  IN_PROGRESS: ["WAITING_FOR_CUSTOMER", "ESCALATED", "RESOLVED"],
  WAITING_FOR_CUSTOMER: ["IN_PROGRESS", "RESOLVED"],
  ESCALATED: ["IN_PROGRESS", "RESOLVED"],
  RESOLVED: ["CLOSED", "REOPENED"],
  CLOSED: ["REOPENED"],
};
export const INCIDENT_EDGES: Record<string, readonly string[]> = {
  DETECTED: ["ACKNOWLEDGED"],
  ACKNOWLEDGED: ["INVESTIGATING"],
  INVESTIGATING: ["MITIGATING", "MONITORING", "RESOLVED"],
  MITIGATING: ["MONITORING", "RESOLVED"],
  MONITORING: ["INVESTIGATING", "RESOLVED"],
  RESOLVED: ["CLOSED"],
  CLOSED: [],
};
export const TRIAGE_EDGES: Record<string, readonly string[]> = {
  UNTRIAGED: ["ACKNOWLEDGED"],
  OPEN: ["ACKNOWLEDGED"],
  REOPENED: ["ACKNOWLEDGED"],
  ACKNOWLEDGED: ["INVESTIGATING", "RESOLVED"],
  INVESTIGATING: ["WAITING_EXTERNAL", "RESOLVED"],
  WAITING_EXTERNAL: ["INVESTIGATING", "RESOLVED"],
  RESOLVED: ["CLOSED", "REOPENED"],
  CLOSED: ["REOPENED"],
};

/** Stage 18 security alert (GET /api/v1/admin/security/alerts). */
export interface SecurityAlert {
  id: string;
  alert_key: string;
  dedupe_key: string;
  severity: "INFO" | "LOW" | "MEDIUM" | "HIGH" | "CRITICAL" | string;
  source_module: string;
  component: string;
  title: string;
  source: string;
  evidence: Record<string, unknown>;
  status: "OPEN" | "ACKNOWLEDGED" | "RESOLVED" | string;
  condition_active: boolean;
  occurrences: number;
  first_seen_at: string;
  last_seen_at: string;
  cleared_at: string | null;
  acknowledged_by: string | null;
  acknowledged_at: string | null;
  escalation_status: "NONE" | "ESCALATED" | string;
  incident_id: string | null;
  resolved_by: string | null;
  resolved_at: string | null;
  resolution_note: string | null;
  version: number;
}

export const ALERT_SEVERITIES = ["INFO", "LOW", "MEDIUM", "HIGH", "CRITICAL"] as const;
export const ALERT_STATUSES = ["LIVE", "OPEN", "ACKNOWLEDGED", "RESOLVED", "ALL"] as const;

export interface BackupEvidence {
  last_backup: string | null;
  last_failed_backup: string | null;
  last_verified_restore: string | null;
  runs: number;
}
