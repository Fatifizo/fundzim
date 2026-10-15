/**
 * Defensive readers for the Stage 11 payouts API (same style as lib/ledger/normalise.ts): documented shapes only,
 * malformed rows dropped, never throw. Amounts stay digit strings — never numbers. Anything that would claim
 * production availability is read conservatively (production stays BLOCKED, funds stay TEST_ONLY).
 */
import type {
  Availability,
  CheckResult,
  Decision,
  JournalLink,
  OwnerPayout,
  Paged,
  PayoutDetail,
  PayoutException,
  PayoutOverview,
  ReconReference,
  Reservation,
  Row,
  StaffPayout,
  TimelineItem,
} from "./types";

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const rec = (v: unknown): Rec => (isRec(v) ? v : {});
const str = (v: unknown, fb = ""): string => (typeof v === "string" ? v : fb);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const int = (v: unknown, fb = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fb);
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);
const many = <T>(v: unknown, read: (x: unknown) => T | null): T[] => list(v).map(read).filter((x): x is T => x !== null);

const DIGITS = /^-?(0|[1-9]\d{0,18})$/;
/** A (signed) minor-unit digit string, or "0" when malformed. */
export const minor = (v: unknown): string => (typeof v === "string" && DIGITS.test(v) ? v : "0");
const minorOrNull = (v: unknown): string | null => (typeof v === "string" && DIGITS.test(v) ? v : null);

export function readReservation(v: unknown): Reservation | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, payout_request_id: str(v.payout_request_id), campaign_id: str(v.campaign_id), currency: str(v.currency, "USD"),
    amount_minor: minor(v.amount_minor), account_scope: str(v.account_scope), status: str(v.status, "UNKNOWN"),
    ledger_reserve_transaction_id: strOrNull(v.ledger_reserve_transaction_id), ledger_submit_transaction_id: strOrNull(v.ledger_submit_transaction_id),
    ledger_release_transaction_id: strOrNull(v.ledger_release_transaction_id), ledger_consume_transaction_id: strOrNull(v.ledger_consume_transaction_id),
    expires_at: strOrNull(v.expires_at), created_at: strOrNull(v.created_at) };
}

export function readOwnerPayout(v: unknown): OwnerPayout | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, campaign_id: str(v.campaign_id), currency: str(v.currency, "USD"), amount_minor: minor(v.amount_minor), purpose: strOrNull(v.purpose),
    status: str(v.status, "UNKNOWN"), status_label: str(v.status_label, str(v.status)), destination_masked: str(v.destination_masked),
    destination_rail: str(v.destination_rail), reason_category: strOrNull(v.reason_category),
    // anything but an explicit false is a TEST payout; the label is never upgraded to real funds
    test_payout: v.test_payout !== false, funds_label: "TEST_ONLY", requested_at: strOrNull(v.requested_at), submitted_at: strOrNull(v.submitted_at),
    completed_at: strOrNull(v.completed_at), can_cancel: v.can_cancel === true, fee_estimate_minor: minorOrNull(v.fee_estimate_minor),
    fee_basis: strOrNull(v.fee_basis), fee_responsibility: strOrNull(v.fee_responsibility),
    beneficiary_receives_minor: minorOrNull(v.beneficiary_receives_minor) };
}

export function readStaffPayout(v: unknown): StaffPayout | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, campaign_id: str(v.campaign_id), owner_user_id: strOrNull(v.owner_user_id), owner_organisation_id: strOrNull(v.owner_organisation_id),
    beneficiary_id: strOrNull(v.beneficiary_id), currency: str(v.currency, "USD"), amount_minor: minor(v.amount_minor), purpose: strOrNull(v.purpose),
    requested_by: str(v.requested_by), requested_at: strOrNull(v.requested_at), status: str(v.status, "UNKNOWN"),
    approval_policy: str(v.approval_policy, "DUAL"), approval_round: Math.max(1, int(v.approval_round, 1)),
    execution_environment: str(v.execution_environment, "UNKNOWN"), test_payout: v.test_payout !== false,
    ledger_environment: str(v.ledger_environment, "UNKNOWN"), destination_id: str(v.destination_id), destination_rail: str(v.destination_rail),
    destination_masked: str(v.destination_masked), destination_category: str(v.destination_category),
    provider_payout_ref: strOrNull(v.provider_payout_ref), unknown_since: strOrNull(v.unknown_since), unknown_reason: strOrNull(v.unknown_reason),
    needs_reconciliation: v.needs_reconciliation === true, submitted_at: strOrNull(v.submitted_at), completed_at: strOrNull(v.completed_at),
    failure_code: strOrNull(v.failure_code), decision_reason_code: strOrNull(v.decision_reason_code),
    reversed_amount_minor: minorOrNull(v.reversed_amount_minor), created_at: strOrNull(v.created_at), reservation: readReservation(v.reservation) };
}

export function readCheck(v: unknown): CheckResult | null {
  if (!isRec(v) || typeof v.check_id !== "string" || !/^EC-\d{2}$/.test(v.check_id)) return null;
  const result = ["PASS", "FAIL", "REVIEW", "DEFER", "NOT_APPLICABLE"].includes(str(v.result)) ? str(v.result) : "FAIL";
  return { check_id: v.check_id, result, detail_code: str(v.detail_code) };
}

function readDecision(v: unknown): Decision | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, phase: str(v.phase), outcome: str(v.outcome, "UNKNOWN"), approval_tier: strOrNull(v.approval_tier),
    evaluated_at: strOrNull(v.evaluated_at), record_hash: str(v.record_hash), results: many(v.results, readCheck) };
}

const scalar = (x: unknown): string | number | boolean | null =>
  typeof x === "string" || typeof x === "boolean" || (typeof x === "number" && Number.isFinite(x)) ? x : Array.isArray(x) ? x.filter((y) => typeof y === "string").join(", ") : null;

function readRow(v: unknown): Row | null {
  if (!isRec(v)) return null;
  const out: Row = {};
  for (const [k, x] of Object.entries(v)) out[k] = scalar(x);
  return out;
}

export function readException(v: unknown): PayoutException | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, payout_request_id: strOrNull(v.payout_request_id), campaign_id: strOrNull(v.campaign_id), exception_type: str(v.exception_type),
    severity: ["SEV1", "SEV2", "SEV3"].includes(str(v.severity)) ? str(v.severity) : "SEV1", source: str(v.source), status: str(v.status, "OPEN"),
    owner_id: strOrNull(v.owner_id), resolution_code: strOrNull(v.resolution_code), detected_at: strOrNull(v.detected_at),
    resolved_at: strOrNull(v.resolved_at), age_seconds: Math.max(0, int(v.age_seconds)), evidence: rec(v.evidence) };
}

function readJournal(v: unknown): JournalLink | null {
  if (!isRec(v) || typeof v.idempotency_key !== "string") return null;
  return { idempotency_key: v.idempotency_key, transaction_id: strOrNull(v.transaction_id) };
}

export function readDetail(v: unknown): PayoutDetail | null {
  const d = rec(v);
  const payout = readStaffPayout(d.payout);
  if (!payout) return null;
  return { payout, approvals: many(d.approvals, readRow), attempts: many(d.attempts, readRow), events: many(d.events, readRow),
    decisions: many(d.eligibility_decisions, readDecision), exceptions: many(d.exceptions, readException), journals: many(d.ledger_journals, readJournal) };
}

const counts = (v: unknown): Record<string, number> =>
  Object.fromEntries(Object.entries(rec(v)).filter((e): e is [string, number] => typeof e[1] === "number" && Number.isInteger(e[1])));

export function readOverview(v: unknown): PayoutOverview {
  const d = rec(v);
  return {
    execution_environment: str(d.execution_environment, "UNKNOWN"),
    // anything but an explicit false is shown as a warning-worthy state
    live_execution: d.live_execution !== false,
    production_status: str(d.production_status, "BLOCKED"),
    ledger_environment: str(d.ledger_environment, "UNKNOWN"),
    by_status: counts(d.by_status),
    open_exceptions_by_severity: counts(d.open_exceptions_by_severity),
    active_reserved_minor_by_currency: Object.fromEntries(Object.entries(rec(d.active_reserved_minor_by_currency)).map(([k, x]) => [k, minor(x)])),
    settlement_reconciliation: str(d.settlement_reconciliation, "NOT_PERFORMED"),
  };
}

export function readRecon(v: unknown): ReconReference | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, payout_request_id: strOrNull(v.payout_request_id), mismatch_type: str(v.mismatch_type), expected_amount_minor: minorOrNull(v.expected_amount_minor),
    actual_amount_minor: minorOrNull(v.actual_amount_minor), currency: strOrNull(v.currency), provider_reference: strOrNull(v.provider_reference),
    detected_at: strOrNull(v.detected_at), detected_by_run: str(v.detected_by_run) };
}

export function readAvailability(v: unknown): Availability {
  const d = rec(v);
  return { campaign_id: str(d.campaign_id), currency: str(d.currency, "USD"), environment: str(d.environment, "UNKNOWN"), funds_label: "TEST_ONLY",
    settled_released_minor: minor(d.settled_released_minor), refund_exposure_minor: minor(d.refund_exposure_minor), reserved_minor: minor(d.reserved_minor),
    available_minor: minor(d.available_minor), payouts_enabled: d.payouts_enabled === true,
    // production availability is always shown as BLOCKED in Stage 11, whatever the payload says
    production_status: "BLOCKED", blockers: list(d.blockers).filter((b): b is string => typeof b === "string"), checks: many(d.checks, readCheck) };
}

export function readTimelineItem(v: unknown): TimelineItem | null {
  if (!isRec(v) || typeof v.status !== "string") return null;
  return { at: strOrNull(v.at), status: v.status, label: str(v.label, v.status) };
}

export function readPage<T>(v: unknown, read: (x: unknown) => T | null): Paged<T> {
  const d = rec(v);
  return { items: many(d.items, read), next_cursor: strOrNull(d.next_cursor) };
}

/** Plain-language owner message for a refusal reason category (never compliance or risk detail, LR-008). */
export function reasonMessage(category: string | null | undefined): string {
  switch (category) {
    case "INSUFFICIENT_FUNDS":
      return "There aren't enough settled funds available for this amount.";
    case "PAYOUT_IN_PROGRESS":
      return "Another withdrawal for this campaign is already in progress. Wait until it finishes.";
    case "VERIFICATION_REQUIRED":
      return "Your identity and payout verification must be complete before you can withdraw.";
    case "DESTINATION_NOT_READY":
      return "Choose a verified payout account that has finished its waiting period.";
    case "CAMPAIGN_NOT_ELIGIBLE":
      return "This campaign can't request withdrawals in its current status.";
    case "AMOUNT_NOT_ALLOWED":
      return "This amount isn't allowed. Try a different amount.";
    case "PAYOUTS_PAUSED":
      return "Withdrawals are paused for this campaign. Contact support.";
    case "PAYOUTS_UNAVAILABLE":
      return "Withdrawals are not available in this environment.";
    case "NOT_AUTHORISED":
      return "Only the campaign owner or an organisation admin can request withdrawals.";
    case "OWNER_CANCELLED":
      return "You cancelled this withdrawal.";
    case "REQUEST_EXPIRED":
      return "This request expired before it was sent.";
    case "PROVIDER_FAILED":
      return "The payout provider reported that this payout failed. The funds were returned to the campaign.";
    case "NOT_AVAILABLE":
    default:
      return "A withdrawal isn't available right now. Contact support if you need help.";
  }
}
