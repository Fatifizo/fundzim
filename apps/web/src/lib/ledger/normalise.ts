/**
 * Defensive readers for the Stage 10 ledger API (same style as lib/payments/normalise.ts): documented shapes only,
 * malformed rows dropped, never throw. Amounts stay digit strings (optionally signed for balances) — never numbers.
 */
import type {
  JournalDetail,
  JournalLine,
  JournalSummary,
  LedgerAccount,
  LedgerAdjustment,
  LedgerCheck,
  LedgerOverview,
  LedgerPeriod,
  Paged,
  ReconException,
  SourceReference,
  TrialBalanceCurrency,
  TrialBalanceRow,
} from "./types";

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const rec = (v: unknown): Rec => (isRec(v) ? v : {});
const str = (v: unknown, fb = ""): string => (typeof v === "string" ? v : fb);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const bool = (v: unknown): boolean => v === true;
const int = (v: unknown, fb = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fb);
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);
const many = <T>(v: unknown, read: (x: unknown) => T | null): T[] => list(v).map(read).filter((x): x is T => x !== null);

const SIGNED = /^-?(0|[1-9]\d{0,18})$/;
/** A (signed) minor-unit digit string, or "0" when malformed. */
export const minor = (v: unknown): string => (typeof v === "string" && SIGNED.test(v) ? v : "0");
const minorOrNull = (v: unknown): string | null => (typeof v === "string" && SIGNED.test(v) ? v : null);

function readCheck(v: unknown): LedgerCheck | null {
  if (!isRec(v) || typeof v.code !== "string") return null;
  const status = ["PASSED", "FAILED", "ERROR"].includes(str(v.status)) ? str(v.status) : "ERROR";
  return { code: v.code, name: str(v.name), status, checked: Math.max(0, int(v.checked)), violations: Math.max(0, int(v.violations)),
    sample: (Array.isArray(v.sample) ? v.sample : []).filter((s): s is string => typeof s === "string") };
}

function readPeriod(v: unknown): LedgerPeriod | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, code: str(v.code), starts_at: strOrNull(v.starts_at), ends_at: strOrNull(v.ends_at), status: str(v.status, "UNKNOWN"),
    close_requested_by: strOrNull(v.close_requested_by), closed_by: strOrNull(v.closed_by), closed_at: strOrNull(v.closed_at) };
}

function readTBRow(v: unknown): TrialBalanceRow | null {
  if (!isRec(v) || typeof v.account_id !== "string") return null;
  return { account_id: v.account_id, code: str(v.code), account_class: str(v.account_class), type: str(v.type),
    debit_minor: minor(v.debit_minor), credit_minor: minor(v.credit_minor) };
}

function readTBCurrency(v: unknown): TrialBalanceCurrency | null {
  if (!isRec(v) || typeof v.currency !== "string") return null;
  const debit = minor(v.total_debit_minor), credit = minor(v.total_credit_minor);
  return { currency: v.currency, total_debit_minor: debit, total_credit_minor: credit, difference_minor: minor(v.difference_minor),
    // recomputed here too: a difference must never be hidden by a wrong flag
    balanced: bool(v.balanced) && debit === credit, rows: many(v.rows, readTBRow) };
}

export function readTrialBalance(v: unknown): { environment: string; currencies: TrialBalanceCurrency[] } {
  const d = rec(v);
  return { environment: str(d.environment, "UNKNOWN"), currencies: many(d.currencies, readTBCurrency) };
}

export function readOverview(v: unknown): LedgerOverview {
  const d = rec(v);
  return {
    environment: str(d.environment, "UNKNOWN"),
    // anything but an explicit false is shown as a warning-worthy state
    production_posting_enabled: d.production_posting_enabled !== false,
    journals: Math.max(0, int(d.journals)),
    accounts: Math.max(0, int(d.accounts)),
    open_exceptions: Math.max(0, int(d.open_exceptions)),
    pending_adjustments: Math.max(0, int(d.pending_adjustments)),
    trial_balance: many(d.trial_balance, readTBCurrency),
    integrity: many(d.integrity, readCheck),
    open_periods: many(d.open_periods, readPeriod),
    synthetic_fixtures_enabled: bool(d.synthetic_fixtures_enabled),
    notice: str(d.notice),
  };
}

export function readAccount(v: unknown): LedgerAccount | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, code: str(v.code), account_class: str(v.account_class), type: str(v.type), normal_balance: str(v.normal_balance),
    kind: str(v.kind), currency: str(v.currency), owner_type: str(v.owner_type), owner_id: strOrNull(v.owner_id), environment: str(v.environment, "UNKNOWN"),
    status: str(v.status, "UNKNOWN"), status_reason: strOrNull(v.status_reason), non_negative: bool(v.non_negative), balance_minor: minor(v.balance_minor),
    debit_total_minor: minor(v.debit_total_minor), credit_total_minor: minor(v.credit_total_minor), entry_count: Math.max(0, int(v.entry_count)) };
}

export function readJournal(v: unknown): JournalSummary | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const environment = str(v.environment, "UNKNOWN");
  return { id: v.id, posting_rule: str(v.posting_rule), posting_rule_version: int(v.posting_rule_version, 1), environment, currency: str(v.currency),
    total_minor: minor(v.total_minor), line_count: int(v.line_count), description: str(v.description), source_system: str(v.source_system),
    source_type: str(v.source_type), source_id: str(v.source_id), evidence_class: str(v.evidence_class), idempotency_key: str(v.idempotency_key),
    occurred_at: strOrNull(v.occurred_at), posted_at: strOrNull(v.posted_at), period_code: str(v.period_code), is_prior_period: bool(v.is_prior_period),
    reverses_transaction_id: strOrNull(v.reverses_transaction_id), reversed_by_transaction_id: strOrNull(v.reversed_by_transaction_id),
    adjustment_id: strOrNull(v.adjustment_id),
    // anything that is not explicitly PRODUCTION is shown as test data
    test_only: environment !== "PRODUCTION" || bool(v.test_only) };
}

function readLine(v: unknown): JournalLine | null {
  if (!isRec(v) || typeof v.account_id !== "string") return null;
  return { line_no: int(v.line_no), account_id: v.account_id, account_code: str(v.account_code), account_class: str(v.account_class),
    direction: str(v.direction), debit_minor: minor(v.debit_minor), credit_minor: minor(v.credit_minor), currency: str(v.currency) };
}

function readSource(v: unknown): SourceReference | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, source_system: str(v.source_system), source_type: str(v.source_type), source_id: str(v.source_id), source_event_id: str(v.source_event_id),
    posting_rule: str(v.posting_rule), evidence_class: str(v.evidence_class), decision: str(v.decision), reason_code: str(v.reason_code),
    transaction_id: strOrNull(v.transaction_id), observed_at: strOrNull(v.observed_at) };
}

export function readJournalDetail(v: unknown): JournalDetail | null {
  const s = readJournal(v);
  if (!s) return null;
  const d = rec(v);
  const lines = many(d.lines, readLine);
  const md: Record<string, string> = {};
  for (const [k, val] of Object.entries(rec(d.metadata))) if (typeof val === "string") md[k] = val;
  let dr = 0n, cr = 0n;
  for (const l of lines) {
    dr += BigInt(l.debit_minor);
    cr += BigInt(l.credit_minor);
  }
  return { ...s, reason: strOrNull(d.reason), created_by: str(d.created_by), metadata: md, lines, source_references: many(d.source_references, readSource),
    // recomputed from the lines; a journal is shown balanced only if its lines add up
    balanced: bool(d.balanced) && dr === cr && lines.length >= 2 };
}

export function readException(v: unknown): ReconException | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, environment: str(v.environment, "UNKNOWN"), exception_type: str(v.exception_type), source_type: str(v.source_type),
    source_id: str(v.source_id), transaction_id: strOrNull(v.transaction_id), expected_amount_minor: minorOrNull(v.expected_amount_minor),
    actual_amount_minor: minorOrNull(v.actual_amount_minor), currency: strOrNull(v.currency), status: str(v.status, "UNKNOWN"),
    owner_id: strOrNull(v.owner_id), detected_at: strOrNull(v.detected_at), age_seconds: Math.max(0, int(v.age_seconds)),
    resolution_note: strOrNull(v.resolution_note) };
}

export function readAdjustment(v: unknown): LedgerAdjustment | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, kind: str(v.kind), posting_rule: str(v.posting_rule), currency: str(v.currency), target_transaction_id: strOrNull(v.target_transaction_id),
    reason_code: str(v.reason_code), reason: str(v.reason), status: str(v.status, "UNKNOWN"), requested_by: str(v.requested_by),
    requested_at: strOrNull(v.requested_at), expires_at: strOrNull(v.expires_at), approved_by: strOrNull(v.approved_by),
    reversal_transaction_id: strOrNull(v.reversal_transaction_id), resulting_transaction_id: strOrNull(v.resulting_transaction_id) };
}

export function readPage<T>(v: unknown, read: (x: unknown) => T | null): Paged<T> {
  const d = rec(v);
  return { items: many(d, read), next_cursor: strOrNull(d.next_cursor) };
}

export { readCheck, readPeriod, readSource };
