/**
 * Defensive readers for the Stage 13 risk API (same style as lib/fees/normalise.ts): documented shapes only,
 * malformed rows dropped, never throw. Nothing a payload says can make risk data non-TEST, turn production screening
 * on or claim measured accuracy. JSON documents (facts, details, evidence content) are flattened to display strings.
 */
import type {
  Assignment,
  CaseDetail,
  Count,
  EvalAction,
  Evaluation,
  Evidence,
  Factor,
  HistoryEvent,
  Hold,
  MonitoringEvent,
  Paged,
  QualityReport,
  ReconCheck,
  ReconReport,
  RiskCase,
  RiskOverview,
  RiskRule,
  RiskSignal,
  RuleOutcome,
  RuleQuality,
  RuleSummary,
  RuleVersion,
  ScreeningCapabilities,
  ScreeningDetail,
  ScreeningMatch,
  ScreeningRequest,
  ScreeningResult,
  SignalReview,
  Simulation,
} from "./types";

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, fb = ""): string => (typeof v === "string" ? v : fb);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const int = (v: unknown, fb = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fb);
const intOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isInteger(v) ? v : null);
const bool = (v: unknown): boolean => v === true;
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);
const many = <T>(v: unknown, read: (x: unknown) => T | null): T[] => list(v).map(read).filter((x): x is T => x !== null);
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
const HASH = /^[0-9a-f]{64}$/;

/** A JSON object flattened to display strings (nested values as compact JSON). */
export function flat(v: unknown): Record<string, string> {
  if (!isRec(v)) return {};
  const out: Record<string, string> = {};
  for (const [k, x] of Object.entries(v)) {
    if (x === null || x === undefined) continue;
    out[k] = typeof x === "string" ? x : JSON.stringify(x);
  }
  return out;
}

function readCount(v: unknown): Count | null {
  if (!isRec(v) || typeof v.key !== "string") return null;
  return { key: v.key, count: int(v.count) };
}

export function readRiskOverview(v: unknown): RiskOverview {
  const o = isRec(v) ? v : {};
  return { generated_at: strOrNull(o.generated_at), environment: "TEST", signals_last_24h_by_category: many(o.signals_last_24h_by_category, readCount),
    evaluations_last_7d_by_classification: many(o.evaluations_last_7d_by_classification, readCount), open_cases_by_priority: many(o.open_cases_by_priority, readCount),
    open_cases_by_status: many(o.open_cases_by_status, readCount), overdue_cases: int(o.overdue_cases), open_holds_by_type: many(o.open_holds_by_type, readCount),
    screenings_by_status: many(o.screenings_by_status, readCount), monitoring_last_24h_by_type: many(o.monitoring_last_24h_by_type, readCount),
    active_rules: int(o.active_rules), pending_rule_approvals: int(o.pending_rule_approvals), pending_hold_activations: int(o.pending_hold_activations),
    pending_hold_releases: int(o.pending_hold_releases), pending_case_decisions: int(o.pending_case_decisions), screening_production: "BLOCKED",
    accuracy_metrics: "NOT_MEASURED" };
}

export function readSignal(v: unknown): RiskSignal | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, signal_type: str(v.signal_type, "UNKNOWN"), category: str(v.category), severity: str(v.severity), confidence: int(v.confidence),
    kind: str(v.kind, "OBSERVED"), subject_type: str(v.subject_type), subject_id: str(v.subject_id), source_module: str(v.source_module),
    source_event_id: str(v.source_event_id), observed_at: strOrNull(v.observed_at), recorded_at: strOrNull(v.recorded_at), facts: flat(v.facts),
    review_classifications: strings(v.review_classifications) };
}

function readFactor(v: unknown): Factor | null {
  if (!isRec(v) || typeof v.rule_code !== "string") return null;
  return { rule_code: v.rule_code, rule_name: str(v.rule_name, v.rule_code), contribution: int(v.contribution), severity: str(v.severity), summary: str(v.summary) };
}

function readOutcome(v: unknown): RuleOutcome | null {
  if (!isRec(v) || typeof v.rule_code !== "string") return null;
  return { rule_version_id: str(v.rule_version_id), rule_code: v.rule_code, fired: bool(v.fired), contribution: int(v.contribution), action: str(v.action),
    conditions: strings(v.conditions), signal_ids: strings(v.signal_ids) };
}

function readEvalAction(v: unknown): EvalAction | null {
  if (!isRec(v) || typeof v.action_type !== "string") return null;
  return { action_type: v.action_type, automated: bool(v.automated), case_id: strOrNull(v.case_id), hold_id: strOrNull(v.hold_id) };
}

const NOTICE = "A risk score is a prioritisation aid from configured rules over recorded signals. It is not proof of fraud or of any wrongdoing.";

export function readEvaluation(v: unknown): Evaluation | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const hash = str(v.inputs_hash);
  return { id: v.id, subject_type: str(v.subject_type), subject_id: str(v.subject_id), trigger_signal_id: str(v.trigger_signal_id),
    engine_version: str(v.engine_version), rule_version_ids: strings(v.rule_version_ids), score: int(v.score), classification: str(v.classification, "UNKNOWN"),
    band_source: str(v.band_source), confidence: int(v.confidence), recommended_action: str(v.recommended_action, "UNKNOWN"),
    results: many(v.results, readOutcome), explanation: many(v.explanation, readFactor), inputs_hash: HASH.test(hash) ? hash : "",
    evaluated_at: strOrNull(v.evaluated_at), actions: many(v.actions, readEvalAction), notice: NOTICE };
}

export function readSimulation(v: unknown): Simulation {
  const o = isRec(v) ? v : {};
  return { score: int(o.score), classification: str(o.classification, "UNKNOWN"), band_source: str(o.band_source), confidence: int(o.confidence),
    recommended_action: str(o.recommended_action, "UNKNOWN"), results: many(o.results, readOutcome), explanation: many(o.explanation, readFactor), notice: NOTICE };
}

export function readMonitoring(v: unknown): MonitoringEvent | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, event_type: str(v.event_type, "UNKNOWN"), severity: str(v.severity), subject_type: strOrNull(v.subject_type),
    subject_id: strOrNull(v.subject_id), detail: flat(v.detail), occurred_at: strOrNull(v.occurred_at) };
}

function readRuleQuality(v: unknown): RuleQuality | null {
  if (!isRec(v) || typeof v.rule_code !== "string") return null;
  return { rule_code: v.rule_code, rule_version_id: str(v.rule_version_id), fired: int(v.fired), cases_resolved: int(v.cases_resolved),
    false_positive_outcomes: int(v.false_positive_outcomes), risk_confirmed_outcomes: int(v.risk_confirmed_outcomes),
    no_further_action_outcomes: int(v.no_further_action_outcomes), signal_false_positive_reviews: int(v.signal_false_positive_reviews) };
}

export function readQuality(v: unknown): QualityReport {
  const o = isRec(v) ? v : {};
  return { generated_at: strOrNull(o.generated_at), window_days: int(o.window_days, 30), rules: many(o.rules, readRuleQuality), accuracy: "NOT_MEASURED",
    notice: str(o.notice, "Counts of rule firings and human case outcomes. They are not accuracy, precision or recall.") };
}

function readCheck(v: unknown): ReconCheck | null {
  if (!isRec(v) || typeof v.code !== "string") return null;
  return { code: v.code, status: str(v.status, "UNKNOWN"), findings: int(v.findings), examples: strings(v.examples) };
}

export function readRecon(v: unknown): ReconReport {
  const o = isRec(v) ? v : {};
  return { run_at: strOrNull(o.run_at), checks: many(o.checks, readCheck) };
}

export function readHistory(v: unknown): HistoryEvent | null {
  if (!isRec(v) || typeof v.event_type !== "string") return null;
  return { event_type: v.event_type, actor_type: str(v.actor_type), actor_id: str(v.actor_id), detail: flat(v.detail), occurred_at: strOrNull(v.occurred_at) };
}

export function readRuleSummary(v: unknown): RuleSummary | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, code: str(v.code), name: str(v.name, str(v.code)), subject_type: str(v.subject_type), category: str(v.category),
    active_version_no: intOrNull(v.active_version_no), pending_version_no: intOrNull(v.pending_version_no), active_action: strOrNull(v.active_action) };
}

export function readRuleVersion(v: unknown): RuleVersion | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const hash = strOrNull(v.definition_hash);
  return { id: v.id, rule_id: str(v.rule_id), version_no: int(v.version_no, 1), status: str(v.status, "UNKNOWN"),
    definition: isRec(v.definition) ? JSON.stringify(v.definition, null, 2) : "{}", contribution: int(v.contribution), severity: str(v.severity),
    recommended_action: str(v.recommended_action), hold_type: strOrNull(v.hold_type), hold_duration_seconds: intOrNull(v.hold_duration_seconds),
    case_priority: str(v.case_priority, "P4"), engine_version: str(v.engine_version), definition_hash: hash && HASH.test(hash) ? hash : null,
    created_by: str(v.created_by), submitted_by: strOrNull(v.submitted_by), approved_by: strOrNull(v.approved_by), approval_reason: strOrNull(v.approval_reason),
    rejected_by: strOrNull(v.rejected_by), rejection_reason: strOrNull(v.rejection_reason), activated_at: strOrNull(v.activated_at),
    suspended_by: strOrNull(v.suspended_by), suspension_reason: strOrNull(v.suspension_reason), retired_by: strOrNull(v.retired_by),
    retirement_reason: strOrNull(v.retirement_reason), version: int(v.version, 1), created_at: strOrNull(v.created_at) };
}

export function readRule(v: unknown): RiskRule | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, code: str(v.code), name: str(v.name, str(v.code)), description: str(v.description), subject_type: str(v.subject_type),
    category: str(v.category), created_by: str(v.created_by), created_at: strOrNull(v.created_at), versions: many(v.versions, readRuleVersion),
    active_version_id: strOrNull(v.active_version_id) };
}

export function readCase(v: unknown): RiskCase | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, case_number: int(v.case_number), case_type: str(v.case_type), subject_type: str(v.subject_type), subject_id: str(v.subject_id),
    status: str(v.status, "UNKNOWN"), priority: str(v.priority, "P4"), risk_score: intOrNull(v.risk_score), classification: strOrNull(v.classification),
    recommended_action: strOrNull(v.recommended_action), assigned_team: str(v.assigned_team), assigned_to: strOrNull(v.assigned_to), due_at: strOrNull(v.due_at),
    proposed_outcome: strOrNull(v.proposed_outcome), proposed_by: strOrNull(v.proposed_by), proposed_reason: strOrNull(v.proposed_reason),
    decision_outcome: strOrNull(v.decision_outcome), decided_by: strOrNull(v.decided_by), decision_reason: strOrNull(v.decision_reason),
    escalated_at: strOrNull(v.escalated_at), opened_by_type: str(v.opened_by_type), opened_by: str(v.opened_by), version: int(v.version, 1),
    created_at: strOrNull(v.created_at) };
}

export function readEvidence(v: unknown): Evidence | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const hash = str(v.content_hash);
  return { id: v.id, evidence_type: str(v.evidence_type, "UNKNOWN"), reference_type: strOrNull(v.reference_type), reference_id: strOrNull(v.reference_id),
    content: flat(v.content), content_hash: HASH.test(hash) ? hash : "", source: str(v.source), added_by_type: str(v.added_by_type), added_by: str(v.added_by),
    retention_class: str(v.retention_class, "RISK_RECORD_LEGAL_REVIEW_REQUIRED"), supersedes_id: strOrNull(v.supersedes_id),
    superseded_by: strOrNull(v.superseded_by), added_at: strOrNull(v.added_at) };
}

function readAssignment(v: unknown): Assignment | null {
  if (!isRec(v) || typeof v.assigned_to !== "string") return null;
  return { assigned_to: v.assigned_to, assigned_by: str(v.assigned_by), reason: str(v.reason), assigned_at: strOrNull(v.assigned_at) };
}

function readSignalReview(v: unknown): SignalReview | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, signal_id: str(v.signal_id), classification: str(v.classification), explanation: str(v.explanation), reviewed_by: str(v.reviewed_by),
    reviewed_at: strOrNull(v.reviewed_at) };
}

export function readHold(v: unknown): Hold | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, hold_type: str(v.hold_type, "UNKNOWN"), subject_type: str(v.subject_type), subject_id: str(v.subject_id), status: str(v.status, "UNKNOWN"),
    reason_code: str(v.reason_code), description: str(v.description), automated: bool(v.automated), evaluation_id: strOrNull(v.evaluation_id),
    case_id: strOrNull(v.case_id), created_by_type: str(v.created_by_type), created_by: str(v.created_by), activated_at: strOrNull(v.activated_at),
    expires_at: strOrNull(v.expires_at), release_requested_by: strOrNull(v.release_requested_by), release_reason: strOrNull(v.release_reason),
    released_by: strOrNull(v.released_by), released_at: strOrNull(v.released_at), revocation_reason: strOrNull(v.revocation_reason), version: int(v.version, 1),
    created_at: strOrNull(v.created_at) };
}

export function readCaseDetail(v: unknown): CaseDetail | null {
  if (!isRec(v)) return null;
  const c = readCase(v.case);
  if (!c) return null;
  return { case: c, evidence: many(v.evidence, readEvidence), assignments: many(v.assignments, readAssignment), signal_reviews: many(v.signal_reviews, readSignalReview),
    holds: many(v.holds, readHold), events: many(v.events, readHistory) };
}

export function readScreening(v: unknown): ScreeningRequest | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, subject_type: str(v.subject_type), subject_id: str(v.subject_id), provider_code: str(v.provider_code), environment: "TEST",
    synthetic_reference: str(v.synthetic_reference), status: str(v.status, "UNKNOWN"), provider_reference: strOrNull(v.provider_reference),
    result_sequence: int(v.result_sequence), match_count: int(v.match_count), timeout_at: strOrNull(v.timeout_at), poll_attempts: int(v.poll_attempts),
    last_error: strOrNull(v.last_error), requested_by: str(v.requested_by), reviewed_by: strOrNull(v.reviewed_by), review_reason: strOrNull(v.review_reason),
    case_id: strOrNull(v.case_id), version: int(v.version, 1), created_at: strOrNull(v.created_at), label: "TEST_ONLY" };
}

function readMatch(v: unknown): ScreeningMatch | null {
  if (!isRec(v) || typeof v.match_id !== "string") return null;
  return { match_id: v.match_id, list: str(v.list), score: int(v.score) };
}

function readScreeningResult(v: unknown): ScreeningResult | null {
  if (!isRec(v) || typeof v.provider_event_id !== "string") return null;
  return { provider_event_id: v.provider_event_id, source: str(v.source), sequence: int(v.sequence), reported_status: str(v.reported_status, "UNKNOWN"),
    matches: many(v.matches, readMatch), applied: bool(v.applied), received_at: strOrNull(v.received_at), label: "TEST_ONLY" };
}

export function readScreeningDetail(v: unknown): ScreeningDetail | null {
  if (!isRec(v)) return null;
  const r = readScreening(v.request);
  return r ? { request: r, results: many(v.results, readScreeningResult) } : null;
}

export function readCapabilities(v: unknown): ScreeningCapabilities {
  const o = isRec(v) ? v : {};
  return { production_screening: "BLOCKED", provider_selected: false,
    providers: many(o.providers, (p) => (isRec(p) && typeof p.code === "string" ? { code: p.code, environment: str(p.environment, "TEST"), webhooks: bool(p.webhooks),
      polling: bool(p.polling), synthetic_only: p.synthetic_only !== false } : null)) };
}

export function readRiskPage<T>(v: unknown, read: (x: unknown) => T | null): Paged<T> {
  return { items: many(v, read), next_cursor: isRec(v) ? strOrNull(v.next_cursor) : null };
}
