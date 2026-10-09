/**
 * Tolerant readers for the staff payloads whose shape the Stage 5 contract leaves open (§7.3 ReviewCase,
 * §7.4 compliance cases). They accept both the frontend's reading of the contract (types.ts) and the shape
 * the backend streams currently produce (reviewer detail nested under `case`, queue wrapped as
 * `{items, next_cursor}`, assignees and authors as bare ids, compliance `case_number`/`resolution.status`).
 * Unknown or missing fields degrade to "—" rather than breaking the page. Remove the second branch once the
 * lead fixes one shape in the OpenAPI contract.
 */
import type {
  ComplianceAction,
  ComplianceCase,
  ComplianceCaseSummary,
  ComplianceStatus,
  PersonRef,
  ReviewAction,
  ReviewCase,
  ReviewCaseSummary,
  ReviewHistoryEntry,
  RiskLevel,
  VerificationDocument,
} from "./types";

type Obj = Record<string, unknown>;

const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const arr = <T = unknown>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);

/** An assignee/author given as an object `{id, display_name}` or as a bare id. */
export function personRef(v: unknown, meId?: string): PersonRef | null {
  if (isObj(v) && str(v.id)) return { id: str(v.id)!, display_name: str(v.display_name) ?? (v.id === meId ? "You" : "Another staff member") };
  if (str(v)) return { id: v as string, display_name: v === meId ? "You" : "Another staff member" };
  return null;
}

// ---------------------------------------------------------------------------------------------------------
// Reviewer queue and case
// ---------------------------------------------------------------------------------------------------------

export function normaliseQueue(data: unknown, metaCursor?: string, meId?: string): { items: ReviewCaseSummary[]; nextCursor?: string } {
  const rawItems = Array.isArray(data) ? data : isObj(data) ? arr(data.items) : [];
  const nextCursor = (isObj(data) ? str(data.next_cursor) : null) ?? metaCursor ?? undefined;
  return { items: rawItems.filter(isObj).map((item) => normaliseSummary(item, meId)), nextCursor };
}

function normaliseSummary(raw: Obj, meId?: string): ReviewCaseSummary {
  const subject = isObj(raw.subject)
    ? { type: str(raw.subject.type) ?? "UNKNOWN", id: str(raw.subject.id) ?? "", display_name: str(raw.subject.display_name) }
    : { type: str(raw.subject_type) ?? "UNKNOWN", id: str(raw.subject_id) ?? "", display_name: null };
  const awaiting = raw.awaiting_second_approval === true;
  return {
    id: str(raw.id) ?? "",
    type: (str(raw.type) ?? "KYC") as ReviewCaseSummary["type"],
    status: awaiting ? "AWAITING_SECOND_APPROVAL" : str(raw.status) ?? "UNKNOWN",
    subject,
    assigned_to: personRef(raw.assigned_to, meId),
    risk_level: (str(raw.risk_level) as RiskLevel | null) ?? null,
    submitted_at: str(raw.submitted_at),
    updated_at: str(raw.updated_at) ?? str(raw.submitted_at) ?? "",
    requires_second_approval: raw.requires_second_approval === true,
  };
}

function normaliseHistory(entries: unknown[]): ReviewHistoryEntry[] {
  return entries.filter(isObj).map((h, i) => ({
    id: str(h.id) ?? `h-${i}`,
    action: str(h.action) ?? str(h.event_type) ?? "EVENT",
    actor: isObj(h.actor)
      ? { type: str(h.actor.type) ?? "STAFF", display_name: str(h.actor.display_name) }
      : { type: str(h.actor_type) ?? "SYSTEM", display_name: null },
    occurred_at: str(h.occurred_at) ?? "",
    reason_code: str(h.reason_code),
    note: str(h.note),
  }));
}

function destinationSummary(c: Obj): Array<{ label: string; value: string }> {
  const checks = isObj(c.checks) ? c.checks : {};
  return [
    { label: "Rail", value: str(c.rail) ?? "—" },
    { label: "Account", value: str(c.masked_identifier) ?? "—" },
    { label: "Holder name", value: str(c.holder_name) ?? "—" },
    { label: "Currency", value: str(c.currency) ?? "—" },
    { label: "Format check", value: checks.format_validated === true ? "Valid format" : "Not checked" },
    { label: "Ownership check", value: str(checks.ownership) ?? "—" },
    { label: "Compliance check", value: str(checks.compliance) ?? "—" },
  ];
}

function beneficiarySummary(c: Obj): Array<{ label: string; value: string }> {
  const rel = isObj(c.relationship) ? c.relationship : {};
  return [
    { label: "Display name", value: str(c.display_name) ?? "—" },
    { label: "Beneficiary type", value: str(c.beneficiary_type) ?? "—" },
    { label: "Full name", value: str(c.full_name) ?? "—" },
    { label: "Relationship", value: [str(rel.type), str(rel.description)].filter(Boolean).join(" — ") || "—" },
    { label: "Authority basis", value: str(c.authority_basis) ?? "—" },
  ];
}

export function normaliseReviewCase(raw: unknown, meId?: string): ReviewCase {
  const r = isObj(raw) ? raw : {};
  const c = isObj(r.case) ? r.case : r; // backend nests the subject's own view under `case`
  const verification = isObj(c.verification) ? c.verification : {};
  const baseStatus = str(r.status) ?? str(c.status) ?? str(verification.status);
  const summary = normaliseSummary({ ...c, ...r, status: baseStatus }, meId);
  const type = summary.type;
  const subjectSummary: Array<{ label: string; value: string }> = arr<{ label: string; value: string }>(r.subject_summary).filter((row) => isObj(row));
  if (subjectSummary.length === 0) {
    if (type === "PAYOUT_DESTINATION") subjectSummary.push(...destinationSummary(c));
    if (type === "BENEFICIARY") subjectSummary.push(...beneficiarySummary(c));
    if (isObj(r.subject) && str(r.subject.email_masked)) subjectSummary.push({ label: "Account email", value: str(r.subject.email_masked)! });
  }
  // KYC/KYB detail carries `review: {risk_level, requires_second_approval, pending_outcome, pending_decided_by}`.
  const review = isObj(r.review) ? r.review : {};
  const pending = isObj(r.pending_approval)
    ? { approved_by: personRef(r.pending_approval.approved_by, meId) ?? { id: "", display_name: "Another reviewer" }, approved_at: str(r.pending_approval.approved_at) ?? "" }
    : str(review.pending_outcome)
      ? { approved_by: personRef(review.pending_decided_by, meId) ?? { id: "", display_name: "Another reviewer" }, approved_at: "", outcome: str(review.pending_outcome) }
      : r.awaiting_second_approval === true || c.awaiting_second_approval === true
      ? { approved_by: personRef(r.first_approved_by ?? c.first_approved_by, meId) ?? { id: "", display_name: "Another reviewer" }, approved_at: str(r.first_approved_at ?? c.first_approved_at) ?? "" }
      : null;
  const risk = isObj(r.risk)
    ? {
        level: ((str(r.risk.level) ?? str(r.risk.rating)) as RiskLevel | null) ?? null,
        signals: arr(r.risk.signals)
          .filter(isObj)
          .map((s) => ({ code: str(s.code) ?? str(s.signal_type) ?? "SIGNAL", description: str(s.description) })),
      }
    : null;
  return {
    ...summary,
    // A pending first approval is carried by `pending_approval`; keep the underlying case status.
    status: pending ? baseStatus ?? summary.status : summary.status,
    risk_level: summary.risk_level ?? (str(review.risk_level) as RiskLevel | null) ?? risk?.level ?? (str(verification.risk_level) as RiskLevel | null) ?? null,
    requires_second_approval: summary.requires_second_approval || verification.requires_second_approval === true || review.requires_second_approval === true,
    subject_summary: subjectSummary,
    identity: isObj(c.identity) ? (c.identity as unknown as ReviewCase["identity"]) : null,
    details: isObj(c.details) ? (c.details as unknown as ReviewCase["details"]) : null,
    persons: arr(c.persons) as ReviewCase["persons"],
    documents: arr<VerificationDocument>(r.documents ?? c.documents),
    requirements: arr(c.requirements) as ReviewCase["requirements"],
    information_requests: arr(c.information_requests ?? verification.information_requests) as ReviewCase["information_requests"],
    decision: (isObj(c.decision) ? c.decision : isObj(verification.decision) ? verification.decision : null) as ReviewCase["decision"],
    pending_approval: pending,
    history: normaliseHistory(arr(r.history)),
    risk,
    allowed_actions: Array.isArray(r.allowed_actions) ? (r.allowed_actions as ReviewAction[]) : undefined,
  };
}

// ---------------------------------------------------------------------------------------------------------
// Compliance
// ---------------------------------------------------------------------------------------------------------

function complianceSubject(raw: Obj): ComplianceCaseSummary["subject"] {
  if (isObj(raw.subject)) return { type: str(raw.subject.type) ?? "UNKNOWN", id: str(raw.subject.id) ?? "", display_name: str(raw.subject.display_name) };
  const links = arr(raw.links).filter(isObj);
  const primary = links.find((l) => l.role === "PRIMARY_SUBJECT") ?? links[0];
  return primary ? { type: str(primary.subject_type) ?? str(primary.type) ?? "UNKNOWN", id: str(primary.subject_id) ?? str(primary.id) ?? "", display_name: str(primary.label) } : null;
}

export function normaliseComplianceSummary(raw: unknown, meId?: string): ComplianceCaseSummary {
  const r = isObj(raw) ? raw : {};
  return {
    id: str(r.id) ?? "",
    reference: str(r.reference) ?? str(r.case_number),
    case_type: str(r.case_type) ?? "OTHER",
    status: (str(r.status) ?? "OPEN") as ComplianceStatus,
    severity: str(r.severity) ?? "—",
    subject: complianceSubject(r),
    assigned_to: personRef(r.assigned_to, meId),
    opened_at: str(r.opened_at) ?? str(r.created_at) ?? "",
    updated_at: str(r.updated_at) ?? "",
  };
}

export function normaliseComplianceList(data: unknown, metaCursor?: string, meId?: string): { items: ComplianceCaseSummary[]; nextCursor?: string } {
  const rawItems = Array.isArray(data) ? data : isObj(data) ? arr(data.items) : [];
  const nextCursor = (isObj(data) ? str(data.next_cursor) : null) ?? metaCursor ?? undefined;
  return { items: rawItems.map((item) => normaliseComplianceSummary(item, meId)), nextCursor };
}

export function normaliseComplianceCase(raw: unknown, meId?: string): ComplianceCase {
  const r = isObj(raw) ? raw : {};
  const res = isObj(r.resolution) ? r.resolution : null;
  return {
    ...normaliseComplianceSummary(r, meId),
    summary: str(r.summary),
    links: arr(r.links)
      .filter(isObj)
      .map((l) => ({ type: str(l.subject_type) ?? str(l.type) ?? "LINK", id: str(l.subject_id) ?? str(l.id) ?? "", label: str(l.label) ?? str(l.role) })),
    events: normaliseHistory(arr(r.events)),
    notes: arr(r.notes)
      .filter(isObj)
      .map((n, i) => ({ id: str(n.id) ?? `n-${i}`, author: personRef(n.author ?? n.author_id, meId), body: str(n.body) ?? "", created_at: str(n.created_at) ?? "" })),
    resolution: res
      ? {
          decision: str(res.decision) ?? "—",
          resolution_status: (str(res.resolution_status) ?? str(res.status) ?? "PROPOSED") as "PROPOSED" | "APPROVED",
          proposed_by: personRef(res.proposed_by ?? res.decided_by, meId),
          approved_by: personRef(res.approved_by, meId),
          note: str(res.note),
        }
      : null,
    allowed_actions: Array.isArray(r.allowed_actions) ? (r.allowed_actions as ComplianceAction[]) : undefined,
  };
}
