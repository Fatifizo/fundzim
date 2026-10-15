/**
 * Defensive readers for the Stage 12 fee API (same style as lib/payouts/normalise.ts): documented shapes only,
 * malformed rows dropped, never throw. Amounts stay digit strings — never numbers — and a missing or malformed
 * amount is UNKNOWN (null), never zero. Nothing a payload says can make a fee non-TEST.
 */
import type {
  AccountingRef,
  ActivePolicy,
  Adjustment,
  Assessment,
  AssessmentDetail,
  CampaignFees,
  CurrencyRevenue,
  FeeEvent,
  FeeOverview,
  FeePolicy,
  FeeQuote,
  GroupRevenue,
  Paged,
  PeriodRevenue,
  PolicyVersion,
  ProviderSchedule,
  QuoteComponent,
  ReconItem,
  ReconPage,
  ReconRun,
  RevenueReport,
  Terms,
  Tier,
} from "./types";

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, fb = ""): string => (typeof v === "string" ? v : fb);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const int = (v: unknown, fb = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fb);
const intOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isInteger(v) ? v : null);
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);
const many = <T>(v: unknown, read: (x: unknown) => T | null): T[] => list(v).map(read).filter((x): x is T => x !== null);
const counts = (v: unknown): Record<string, number> =>
  Object.fromEntries(Object.entries(isRec(v) ? v : {}).filter(([, n]) => typeof n === "number" && Number.isInteger(n)) as [string, number][]);

const DIGITS = /^-?(0|[1-9]\d{0,18})$/;
/** A (signed) minor-unit digit string, or "0" when malformed (totals that are always known). */
export const minor = (v: unknown): string => (typeof v === "string" && DIGITS.test(v) ? v : "0");
/** A minor-unit digit string, or null = UNKNOWN. */
export const minorOrUnknown = (v: unknown): string | null => (typeof v === "string" && DIGITS.test(v) ? v : null);

function readTier(v: unknown): Tier | null {
  if (!isRec(v)) return null;
  return { up_to_minor: minorOrUnknown(v.up_to_minor), rate_bps: int(v.rate_bps), fixed_minor: minor(v.fixed_minor) };
}

export function readTerms(v: unknown): Terms {
  const t = isRec(v) ? v : {};
  return { model: str(t.model, "UNKNOWN"), rate_bps: intOrNull(t.rate_bps), fixed_minor: minorOrUnknown(t.fixed_minor),
    min_fee_minor: minorOrUnknown(t.min_fee_minor), max_fee_minor: minorOrUnknown(t.max_fee_minor), tiers: many(t.tiers, readTier),
    rounding_mode: str(t.rounding_mode) };
}

export function readVersion(v: unknown): PolicyVersion | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, policy_id: str(v.policy_id), category: str(v.category), version_no: int(v.version_no, 1), status: str(v.status, "UNKNOWN"),
    terms: readTerms(v.terms), responsibility: str(v.responsibility), donor_share_bps: intOrNull(v.donor_share_bps),
    refund_treatment: strOrNull(v.refund_treatment), effective_from: strOrNull(v.effective_from), effective_to: strOrNull(v.effective_to),
    algorithm_version: str(v.algorithm_version), terms_hash: strOrNull(v.terms_hash), fee_environment: "TEST", created_by: str(v.created_by),
    submitted_by: strOrNull(v.submitted_by), submitted_at: strOrNull(v.submitted_at), approved_by: strOrNull(v.approved_by),
    approved_at: strOrNull(v.approved_at), approval_reason: strOrNull(v.approval_reason), rejected_by: strOrNull(v.rejected_by),
    rejection_reason: strOrNull(v.rejection_reason), activated_by: strOrNull(v.activated_by), activated_at: strOrNull(v.activated_at),
    suspended_by: strOrNull(v.suspended_by), suspension_reason: strOrNull(v.suspension_reason), retired_by: strOrNull(v.retired_by),
    retirement_reason: strOrNull(v.retirement_reason), created_at: strOrNull(v.created_at) };
}

export function readPolicy(v: unknown): FeePolicy | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, code: str(v.code), name: str(v.name, str(v.code)), description: str(v.description), category: str(v.category),
    transaction_type: str(v.transaction_type), currency: str(v.currency, "USD"), payment_method: strOrNull(v.payment_method),
    provider_id: strOrNull(v.provider_id), campaign_category: strOrNull(v.campaign_category), campaign_id: strOrNull(v.campaign_id),
    specificity: int(v.specificity), fee_environment: "TEST", created_by: str(v.created_by), created_at: strOrNull(v.created_at),
    versions: many(v.versions, readVersion), active_version_id: strOrNull(v.active_version_id) };
}

export function readActivePolicy(v: unknown): ActivePolicy | null {
  if (!isRec(v) || typeof v.version_id !== "string") return null;
  return { version_id: v.version_id, policy_id: str(v.policy_id), name: str(v.name), category: str(v.category),
    transaction_type: str(v.transaction_type), currency: str(v.currency, "USD"), scope: str(v.scope, "platform-wide"), terms: readTerms(v.terms),
    responsibility: str(v.responsibility), donor_share_bps: intOrNull(v.donor_share_bps), refund_treatment: strOrNull(v.refund_treatment),
    effective_from: strOrNull(v.effective_from), fee_environment: "TEST" };
}

export function readSchedule(v: unknown): ProviderSchedule | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  // the API cannot hold VERIFIED evidence in Stage 12; anything else is shown as UNVERIFIED
  const conf = v.confirmation_status === "SYNTHETIC" ? "SYNTHETIC" : "UNVERIFIED";
  return { id: v.id, provider_id: str(v.provider_id), provider_is_sandbox: v.provider_is_sandbox === true, contract_reference: str(v.contract_reference),
    schedule_version: str(v.schedule_version), currency: str(v.currency, "USD"), payment_method: strOrNull(v.payment_method), fee_type: str(v.fee_type),
    terms: readTerms(v.terms), rounding_known: v.rounding_known === true, effective_from: strOrNull(v.effective_from),
    effective_to: strOrNull(v.effective_to), evidence_source: str(v.evidence_source), evidence_reference: strOrNull(v.evidence_reference),
    confirmation_status: conf, status: str(v.status, "UNKNOWN"), fee_environment: "TEST", created_by: str(v.created_by),
    approved_by: strOrNull(v.approved_by), created_at: strOrNull(v.created_at) };
}

function readComponent(v: unknown): QuoteComponent | null {
  if (!isRec(v) || typeof v.category !== "string") return null;
  const amount = minorOrUnknown(v.amount_minor);
  return { category: v.category, basis: amount === null && v.basis !== "NOT_CONFIGURED" ? "UNKNOWN" : str(v.basis, "UNKNOWN"), amount_minor: amount,
    responsibility: str(v.responsibility), donor_part_minor: minorOrUnknown(v.donor_part_minor), organiser_part_minor: minorOrUnknown(v.organiser_part_minor),
    platform_part_minor: minorOrUnknown(v.platform_part_minor), rounding_mode: str(v.rounding_mode), note: str(v.note) };
}

export function readQuote(v: unknown): FeeQuote | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const provider = minorOrUnknown(v.provider_fee_minor);
  return { id: v.id, campaign_id: str(v.campaign_id), currency: str(v.currency, "USD"), amount_minor: minor(v.amount_minor),
    payment_method: strOrNull(v.payment_method), platform_fee_minor: minor(v.platform_fee_minor), platform_fee_basis: str(v.platform_fee_basis, "NOT_CONFIGURED"),
    provider_fee_minor: provider, provider_fee_basis: provider === null ? "UNKNOWN" : str(v.provider_fee_basis, "ESTIMATED"),
    donor_charge_minor: minorOrUnknown(v.donor_charge_minor), campaign_allocation_minor: minorOrUnknown(v.campaign_allocation_minor),
    platform_net_minor: minorOrUnknown(v.platform_net_minor), components: many(v.components, readComponent), status: str(v.status, "UNKNOWN"),
    expires_at: strOrNull(v.expires_at), donation_intent_id: strOrNull(v.donation_intent_id), calculation_id: str(v.calculation_id),
    algorithm_version: str(v.algorithm_version), system_generated: v.system_generated === true, fee_environment: "TEST",
    notice: str(v.notice, "A fee quote is an estimate for a TEST donation. It is not a charge, and nothing has been paid."), created_at: strOrNull(v.created_at) };
}

export function readAssessment(v: unknown): Assessment | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const fee = minorOrUnknown(v.fee_minor);
  return { id: v.id, source_type: str(v.source_type), source_id: str(v.source_id), category: str(v.category), campaign_id: strOrNull(v.campaign_id),
    currency: str(v.currency, "USD"), basis_minor: minor(v.basis_minor), fee_minor: fee, net_fee_minor: fee === null ? null : minorOrUnknown(v.net_fee_minor),
    basis: fee === null ? "UNKNOWN" : str(v.basis, "UNKNOWN"), responsibility: strOrNull(v.responsibility), donor_part_minor: minorOrUnknown(v.donor_part_minor),
    organiser_part_minor: minorOrUnknown(v.organiser_part_minor), platform_part_minor: minorOrUnknown(v.platform_part_minor),
    policy_version_id: strOrNull(v.policy_version_id), provider_schedule_id: strOrNull(v.provider_schedule_id), calculation_id: str(v.calculation_id),
    quote_id: strOrNull(v.quote_id), payment_method: strOrNull(v.payment_method), accounting_status: str(v.accounting_status, "UNKNOWN"),
    test_payment: v.test_payment !== false, synthetic: v.synthetic === true, fee_environment: "TEST", assessed_at: strOrNull(v.assessed_at) };
}

export function readAdjustment(v: unknown): Adjustment | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, assessment_id: str(v.assessment_id), currency: str(v.currency, "USD"), reason: str(v.reason), direction: str(v.direction, "DECREASE"),
    amount_minor: minor(v.amount_minor), source_type: str(v.source_type), basis_minor: minorOrUnknown(v.basis_minor), note: str(v.note),
    status: str(v.status, "UNKNOWN"), actor_type: str(v.actor_type), requested_by: strOrNull(v.requested_by), decided_by: strOrNull(v.decided_by),
    decided_at: strOrNull(v.decided_at), decision_reason: strOrNull(v.decision_reason), accounting_status: str(v.accounting_status),
    ledger_adjustment_id: strOrNull(v.ledger_adjustment_id), created_at: strOrNull(v.created_at) };
}

function readRef(v: unknown): AccountingRef | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, ledger_transaction_id: str(v.ledger_transaction_id), journal_key: str(v.journal_key), posting_rule: str(v.posting_rule),
    purpose: str(v.purpose), currency: str(v.currency, "USD"), fee_amount_minor: minor(v.fee_amount_minor), policy_version_id: strOrNull(v.policy_version_id),
    recorded_at: strOrNull(v.recorded_at) };
}

function readEvent(v: unknown): FeeEvent | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, event_type: str(v.event_type), actor_type: str(v.actor_type), actor_id: str(v.actor_id), occurred_at: strOrNull(v.occurred_at) };
}

export function readAssessmentDetail(v: unknown): AssessmentDetail | null {
  if (!isRec(v)) return null;
  const a = readAssessment(v.assessment);
  if (!a) return null;
  return { assessment: a, journals: many(v.journals, readRef), adjustments: many(v.adjustments, readAdjustment), events: many(v.events, readEvent) };
}

export function readRun(v: unknown): ReconRun | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, triggered_by: str(v.triggered_by), started_at: strOrNull(v.started_at), finished_at: strOrNull(v.finished_at), checked: int(v.checked),
    matched: int(v.matched), mismatches: int(v.mismatches), settlement_reconciliation: "NOT_PERFORMED" };
}

function readReconItem(v: unknown): ReconItem | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, kind: str(v.kind), severity: str(v.severity, "SEV3"), assessment_id: strOrNull(v.assessment_id), currency: strOrNull(v.currency),
    detail: str(v.detail), status: str(v.status, "OPEN"), detected_at: strOrNull(v.detected_at), resolved_at: strOrNull(v.resolved_at),
    resolution_note: strOrNull(v.resolution_note) };
}

export function readReconPage(v: unknown): ReconPage {
  const o = isRec(v) ? v : {};
  return { items: many(o.items, readReconItem), latest_run: readRun(o.latest_run), settlement_reconciliation: "NOT_PERFORMED" };
}

function readCurrencyRevenue(v: unknown): CurrencyRevenue | null {
  if (!isRec(v) || typeof v.currency !== "string") return null;
  const m = (k: string) => minor(v[k]);
  return { currency: v.currency, quoted_platform_fees_minor: m("quoted_platform_fees_minor"), quotes: int(v.quotes),
    assessed_platform_fees_minor: m("assessed_platform_fees_minor"), platform_fee_assessments: int(v.platform_fee_assessments),
    assessed_on_test_payments_minor: m("assessed_on_test_payments_minor"), assessed_on_synthetic_donations_minor: m("assessed_on_synthetic_donations_minor"),
    refunded_platform_fees_minor: m("refunded_platform_fees_minor"), chargeback_reversed_platform_fees_minor: m("chargeback_reversed_platform_fees_minor"),
    manual_adjustments_net_minor: m("manual_adjustments_net_minor"), provider_fees_estimated_minor: m("provider_fees_estimated_minor"),
    provider_fees_unknown: int(v.provider_fees_unknown), payout_fees_minor: m("payout_fees_minor"),
    chargeback_fee_exposure_minor: m("chargeback_fee_exposure_minor"), recognised_revenue_minor: m("recognised_revenue_minor"),
    revenue_reversals_minor: m("revenue_reversals_minor"), provider_fee_expense_minor: m("provider_fee_expense_minor"),
    net_revenue_minor: m("net_revenue_minor"), cash_received: "NOT_AVAILABLE" };
}

function readPeriod(v: unknown): PeriodRevenue | null {
  if (!isRec(v) || typeof v.currency !== "string") return null;
  return { currency: v.currency, month: str(v.month), recognised_revenue_minor: minor(v.recognised_revenue_minor),
    revenue_reversals_minor: minor(v.revenue_reversals_minor), provider_fee_expense_minor: minor(v.provider_fee_expense_minor), journals: int(v.journals) };
}

function readGroup(v: unknown): GroupRevenue | null {
  if (!isRec(v) || typeof v.currency !== "string") return null;
  return { currency: v.currency, key: str(v.key, "NONE"), assessed_platform_fees_minor: minor(v.assessed_platform_fees_minor), assessments: int(v.assessments) };
}

export function readRevenue(v: unknown): RevenueReport {
  const o = isRec(v) ? v : {};
  return { fee_environment: "TEST", label: str(o.label, "TEST — synthetic data, not FundZim's financial performance"), from: strOrNull(o.from),
    to: strOrNull(o.to), currencies: many(o.currencies, readCurrencyRevenue), by_period: many(o.by_period, readPeriod),
    by_payment_method: many(o.by_payment_method, readGroup), by_provider: many(o.by_provider, readGroup),
    notes: list(o.notes).filter((n): n is string => typeof n === "string") };
}

export function readFeeOverview(v: unknown): FeeOverview {
  const o = isRec(v) ? v : {};
  return { fee_environment: "TEST", production_fees: "BLOCKED", policy_versions_by_status: counts(o.policy_versions_by_status),
    provider_schedules_by_status: counts(o.provider_schedules_by_status), active_quotes: int(o.active_quotes), assessments: int(o.assessments),
    pending_adjustments: int(o.pending_adjustments), open_reconciliation_items: int(o.open_reconciliation_items),
    latest_reconciliation: readRun(o.latest_reconciliation), notes: list(o.notes).filter((n): n is string => typeof n === "string") };
}

export function readCampaignFees(v: unknown): CampaignFees {
  const o = isRec(v) ? v : {};
  return { campaign_id: str(o.campaign_id), fee_environment: "TEST", policies: many(o.policies, readActivePolicy),
    assessments: many(o.assessments, readAssessment), adjustments: many(o.adjustments, readAdjustment),
    restrictions: list(o.restrictions).filter((n): n is string => typeof n === "string") };
}

export function readFeePage<T>(v: unknown, read: (x: unknown) => T | null): Paged<T> {
  return { items: many(v, read), next_cursor: isRec(v) ? strOrNull(v.next_cursor) : null };
}
