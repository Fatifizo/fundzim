/**
 * Staff reconciliation read models (GET /admin/reconciliation/*, Stage 17, ADR-049). Everything is TEST evidence:
 * statements are staff-supplied files in FundZim's canonical format or generated from the sandbox simulator — never a
 * provider's or bank's real statement. Read defensively: unknown shapes become empty values; amounts stay strings of
 * integer minor units (never parsed as floats).
 */
type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, f = ""): string => (typeof v === "string" ? v : f);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const int = (v: unknown): number => (typeof v === "number" && Number.isInteger(v) && v >= 0 ? v : 0);
const intOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isInteger(v) ? v : null);
const bool = (v: unknown): boolean => v === true;
const minor = (v: unknown): string | null => (typeof v === "string" && /^-?\d{1,19}$/.test(v) ? v : null);
const list = <T>(v: unknown, f: (x: Rec) => T): T[] => (Array.isArray(v) ? v.filter(isRec).map(f) : []);
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
const items = <T>(v: unknown, f: (x: Rec) => T): T[] => (isRec(v) ? list(v.items, f) : []);

export interface Count {
  key: string;
  value: number;
}
const counts = (v: unknown): Count[] => list(v, (x) => ({ key: str(x.key), value: int(x.value) })).filter((c) => /^[A-Za-z0-9_:]{1,80}$/.test(c.key));

/** Format integer minor units with a currency, without floating point (2 decimal places for USD and ZWG). */
export function formatMinor(value: string | null | undefined, currency: string | null | undefined): string {
  if (value == null || !/^-?\d{1,19}$/.test(value)) return "—";
  const neg = value.startsWith("-");
  const digits = (neg ? value.slice(1) : value).padStart(3, "0");
  const whole = digits.slice(0, -2).replace(/^0+(?=\d)/, "").replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${neg ? "−" : ""}${currency ?? ""} ${whole}.${digits.slice(-2)}`.trim();
}

export interface SourceHealth {
  code: string;
  kind: string;
  rank: number;
  lastReceivedAt: string | null;
  lastParsedAt: string | null;
  lastRunAt: string | null;
  openLines: number;
  freshness: string;
}

export interface WorkerHealth {
  job: string;
  lastSucceededAt: string | null;
  lastFailedAt: string | null;
  lastError: string | null;
  consecutiveFailures: number;
  status: string;
}

export interface Run {
  id: string;
  sourceCode: string;
  currency: string;
  windowStart: string | null;
  windowEnd: string | null;
  kind: string;
  status: string;
  ruleSetVersion: number | null;
  matcherVersion: string;
  replayOf: string | null;
  reason: string | null;
  triggeredBy: string;
  considered: number;
  matched: number;
  opened: number;
  seen: number;
  closed: number;
  decisionsSha256: string | null;
  failureCode: string | null;
  queuedAt: string | null;
  finishedAt: string | null;
  durationMs: number | null;
}

export function readRun(x: Rec): Run {
  return {
    id: str(x.id), sourceCode: str(x.source_code), currency: str(x.currency), windowStart: strOrNull(x.window_start), windowEnd: strOrNull(x.window_end),
    kind: str(x.run_kind), status: str(x.status), ruleSetVersion: intOrNull(x.rule_set_version), matcherVersion: str(x.matcher_version),
    replayOf: strOrNull(x.replay_of_run_id), reason: strOrNull(x.reason), triggeredBy: str(x.triggered_by_type), considered: int(x.items_considered),
    matched: int(x.items_matched), opened: int(x.exceptions_opened), seen: int(x.exceptions_seen), closed: int(x.exceptions_closed),
    decisionsSha256: strOrNull(x.decisions_sha256), failureCode: strOrNull(x.failure_code), queuedAt: strOrNull(x.queued_at),
    finishedAt: strOrNull(x.finished_at), durationMs: intOrNull(x.duration_ms),
  };
}
export const readRuns = (v: unknown): Run[] => items(v, readRun);

export interface RuleSet {
  id: string;
  version: number;
  status: string;
  dateWindowDays: number;
  settlementHorizonDays: number;
  staleAfterHours: number;
  escalateAfterHours: number | null;
  sha256: string;
  reason: string;
  proposedBy: string;
  proposedAt: string | null;
  decidedBy: string | null;
  decisionNote: string | null;
  effectiveFrom: string | null;
  retiredAt: string | null;
}

export function readRuleSet(x: Rec): RuleSet {
  const c = isRec(x.config) ? x.config : {};
  return {
    id: str(x.id), version: int(x.version_no), status: str(x.status), dateWindowDays: int(c.date_window_days),
    settlementHorizonDays: int(c.settlement_horizon_days), staleAfterHours: int(c.stale_after_hours), escalateAfterHours: intOrNull(c.escalate_after_hours),
    sha256: str(x.config_sha256), reason: str(x.change_reason), proposedBy: str(x.proposed_by), proposedAt: strOrNull(x.proposed_at),
    decidedBy: strOrNull(x.decided_by), decisionNote: strOrNull(x.decision_note), effectiveFrom: strOrNull(x.effective_from), retiredAt: strOrNull(x.retired_at),
  };
}
export const readRuleSets = (v: unknown): RuleSet[] => items(v, readRuleSet);

export interface Overview {
  environment: string;
  production: Array<[string, string]>;
  ruleSet: RuleSet | null;
  ruleSetStatus: string;
  sources: SourceHealth[];
  imports: Count[];
  lines: Count[];
  bySeverity: Count[];
  byType: Count[];
  oldestOpenHours: number | null;
  pendingResolutions: number;
  batches: Count[];
  unknownOutcomes: number;
  workers: WorkerHealth[];
  recentRuns: Run[];
}

export function readOverview(v: unknown): Overview {
  const d = isRec(v) ? v : {};
  const prod = isRec(d.production) ? Object.entries(d.production).filter((e): e is [string, string] => typeof e[1] === "string") : [];
  return {
    environment: str(d.environment, "TEST"),
    production: prod.sort(([a], [b]) => a.localeCompare(b)),
    ruleSet: isRec(d.active_rule_set) ? readRuleSet(d.active_rule_set) : null,
    ruleSetStatus: str(d.rule_set_status, "NOT_CONFIGURED"),
    sources: list(d.sources, (x) => ({
      code: str(x.code), kind: str(x.source_kind), rank: int(x.authority_rank), lastReceivedAt: strOrNull(x.last_received_at),
      lastParsedAt: strOrNull(x.last_parsed_at), lastRunAt: strOrNull(x.last_completed_run_at), openLines: int(x.open_lines), freshness: str(x.freshness, "UNKNOWN"),
    })),
    imports: counts(d.imports),
    lines: counts(d.lines),
    bySeverity: counts(d.exceptions_open_by_severity),
    byType: counts(d.exceptions_open_by_type),
    oldestOpenHours: intOrNull(d.oldest_open_exception_hours),
    pendingResolutions: int(d.resolutions_pending),
    batches: counts(d.settlement_batches),
    unknownOutcomes: int(d.unknown_outcomes_open),
    workers: list(d.workers, (x) => ({
      job: str(x.job_kind), lastSucceededAt: strOrNull(x.last_succeeded_at), lastFailedAt: strOrNull(x.last_failed_at), lastError: strOrNull(x.last_error_code),
      consecutiveFailures: int(x.consecutive_failures), status: str(x.status, "UNKNOWN"),
    })),
    recentRuns: list(d.recent_runs, readRun),
  };
}

export interface ValidationError {
  locator: string;
  field: string;
  code: string;
}

export interface Import {
  id: string;
  sourceCode: string;
  sourceKind: string;
  origin: string;
  sha256: string;
  sizeBytes: number;
  mediaType: string;
  filename: string | null;
  evidenceRecordId: string;
  statementRef: string | null;
  periodStart: string | null;
  periodEnd: string | null;
  currency: string | null;
  openingBalance: string | null;
  closingBalance: string | null;
  accountHint: string | null;
  declaredLines: number | null;
  parsed: number;
  duplicate: number;
  conflicting: number;
  batches: number;
  status: string;
  failureCode: string | null;
  report: ValidationError[];
  supersedes: string | null;
  receivedAt: string | null;
  parsedAt: string | null;
}

export function readImport(x: Rec): Import {
  return {
    id: str(x.id), sourceCode: str(x.source_code), sourceKind: str(x.source_kind), origin: str(x.origin), sha256: str(x.content_sha256),
    sizeBytes: int(x.size_bytes), mediaType: str(x.media_type), filename: strOrNull(x.original_filename), evidenceRecordId: str(x.evidence_record_id),
    statementRef: strOrNull(x.statement_ref), periodStart: strOrNull(x.period_start), periodEnd: strOrNull(x.period_end), currency: strOrNull(x.currency),
    openingBalance: minor(x.opening_balance_minor), closingBalance: minor(x.closing_balance_minor), accountHint: strOrNull(x.account_ref_hint),
    declaredLines: intOrNull(x.declared_lines), parsed: int(x.lines_parsed), duplicate: int(x.lines_duplicate), conflicting: int(x.lines_conflicting),
    batches: int(x.batches_parsed), status: str(x.status), failureCode: strOrNull(x.failure_code),
    report: list(x.validation_report, (e) => ({ locator: str(e.locator), field: str(e.field), code: str(e.code) })),
    supersedes: strOrNull(x.supersedes_import_id), receivedAt: strOrNull(x.received_at), parsedAt: strOrNull(x.parsed_at),
  };
}
export const readImports = (v: unknown): Import[] => items(v, readImport);

export interface Match {
  id: string;
  subjectType: string;
  subjectId: string;
  confidence: string;
  ruleCode: string;
  ruleSetVersion: number | null;
  matcherVersion: string;
  amount: string | null;
  ledgerStatus: string;
  ledgerTransactionId: string | null;
  feeStatus: string;
  explanation: Array<[string, string]>;
}

export interface Line {
  id: string;
  importId: string;
  lineId: string;
  revision: number;
  number: number;
  kind: string;
  direction: string;
  status: string;
  amount: string | null;
  fee: string | null;
  currency: string;
  rawCurrency: string;
  providerRef: string | null;
  merchantRef: string | null;
  batchRef: string | null;
  counterpartyRef: string | null;
  bookedAt: string | null;
  sha256: string;
  locator: string;
  matchStatus: string;
  match: Match | null;
}

function explain(v: unknown): Array<[string, string]> {
  if (!isRec(v)) return [];
  return Object.entries(v)
    .filter(([k]) => /^[a-z_]{1,40}$/.test(k))
    .map(([k, x]): [string, string] => [k, typeof x === "string" ? x : typeof x === "number" || typeof x === "boolean" ? String(x) : Array.isArray(x) ? x.filter((y) => typeof y === "string").join(", ") : ""])
    .sort(([a], [b]) => a.localeCompare(b));
}

export function readLine(x: Rec): Line {
  const m = isRec(x.match) ? x.match : null;
  return {
    id: str(x.id), importId: str(x.import_id), lineId: str(x.external_line_id), revision: int(x.revision), number: int(x.line_number), kind: str(x.item_kind),
    direction: str(x.direction), status: str(x.line_status), amount: minor(x.amount_minor), fee: minor(x.fee_minor), currency: str(x.currency),
    rawCurrency: str(x.raw_currency_code), providerRef: strOrNull(x.provider_ref), merchantRef: strOrNull(x.merchant_ref), batchRef: strOrNull(x.batch_ref),
    counterpartyRef: strOrNull(x.counterparty_ref), bookedAt: strOrNull(x.booked_at), sha256: str(x.raw_line_sha256), locator: str(x.raw_line_locator),
    matchStatus: str(x.match_status),
    match: m
      ? {
          id: str(m.id), subjectType: str(m.subject_type), subjectId: str(m.subject_id), confidence: str(m.confidence), ruleCode: str(m.rule_code),
          ruleSetVersion: intOrNull(m.rule_set_version), matcherVersion: str(m.matcher_version), amount: minor(m.matched_amount_minor),
          ledgerStatus: str(m.ledger_status), ledgerTransactionId: strOrNull(m.ledger_transaction_id), feeStatus: str(m.fee_status), explanation: explain(m.explanation),
        }
      : null,
  };
}

export interface ExceptionEvent {
  id: string;
  type: string;
  from: string | null;
  to: string | null;
  actorType: string;
  actorId: string | null;
  note: string | null;
  evidenceRef: string | null;
  at: string | null;
}

export interface Resolution {
  id: string;
  type: string;
  status: string;
  proposal: Array<[string, string]>;
  sha256: string;
  justification: string;
  evidenceRefs: string[];
  proposedBy: string;
  proposedAt: string | null;
  expiresAt: string | null;
  decidedBy: string | null;
  decisionNote: string | null;
  ledgerAdjustmentId: string | null;
  executedAt: string | null;
  result: Array<[string, string]>;
  lastError: string | null;
}

export function readResolution(x: Rec): Resolution {
  return {
    id: str(x.id), type: str(x.resolution_type), status: str(x.status), proposal: explain(x.proposal), sha256: str(x.proposal_sha256),
    justification: str(x.justification), evidenceRefs: strings(x.evidence_refs), proposedBy: str(x.proposed_by), proposedAt: strOrNull(x.proposed_at),
    expiresAt: strOrNull(x.expires_at), decidedBy: strOrNull(x.decided_by), decisionNote: strOrNull(x.decision_note),
    ledgerAdjustmentId: strOrNull(x.ledger_adjustment_id), executedAt: strOrNull(x.executed_at), result: explain(x.execution_result), lastError: strOrNull(x.last_error),
  };
}
export const readResolutions = (v: unknown): Resolution[] => items(v, readResolution);

export interface ExceptionItem {
  id: string;
  type: string;
  severity: string;
  status: string;
  sourceCode: string;
  currency: string;
  occurrences: number;
  itemId: string | null;
  subjectType: string | null;
  subjectId: string | null;
  batchId: string | null;
  expected: string | null;
  actual: string | null;
  rawCurrency: string | null;
  detail: Array<[string, string]>;
  detectedAt: string | null;
  ageHours: number;
  assignedTo: string | null;
  dueAt: string | null;
  escalatedAt: string | null;
  closeReason: string | null;
  reopenCount: number;
  version: number;
  line: Line | null;
  events: ExceptionEvent[];
  resolutions: Resolution[];
}

export function readException(x: Rec): ExceptionItem {
  return {
    id: str(x.id), type: str(x.exception_type), severity: str(x.severity), status: str(x.status), sourceCode: str(x.source_code), currency: str(x.currency),
    occurrences: int(x.occurrences), itemId: strOrNull(x.item_id), subjectType: strOrNull(x.subject_type), subjectId: strOrNull(x.subject_id),
    batchId: strOrNull(x.settlement_batch_id), expected: minor(x.expected_amount_minor), actual: minor(x.actual_amount_minor), rawCurrency: strOrNull(x.actual_currency_raw),
    detail: explain(x.detail), detectedAt: strOrNull(x.detected_at), ageHours: int(x.age_hours), assignedTo: strOrNull(x.assigned_to), dueAt: strOrNull(x.due_at),
    escalatedAt: strOrNull(x.escalated_at), closeReason: strOrNull(x.close_reason), reopenCount: int(x.reopen_count), version: int(x.version),
    line: isRec(x.item) ? readLine(x.item) : null,
    events: list(x.events, (e) => ({
      id: str(e.id), type: str(e.event_type), from: strOrNull(e.from_status), to: strOrNull(e.to_status), actorType: str(e.actor_type),
      actorId: strOrNull(e.actor_id), note: strOrNull(e.note), evidenceRef: strOrNull(e.evidence_ref), at: strOrNull(e.created_at),
    })),
    resolutions: list(x.resolutions, readResolution),
  };
}
export const readExceptions = (v: unknown): ExceptionItem[] => items(v, readException);

export interface ImportDetail {
  imp: Import;
  lines: Line[];
  exceptions: ExceptionItem[];
}

export function readImportDetail(v: unknown): ImportDetail {
  const d = isRec(v) ? v : {};
  return { imp: readImport(isRec(d.import) ? d.import : {}), lines: list(d.lines, readLine), exceptions: list(d.exceptions, readException) };
}

export interface Batch {
  id: string;
  importId: string;
  sourceCode: string;
  currency: string;
  ref: string;
  date: string;
  credits: string | null;
  debits: string | null;
  fees: string | null;
  net: string | null;
  expectedNet: string | null;
  lines: number;
  settled: number;
  status: string;
  items: Line[];
}

export function readBatch(x: Rec): Batch {
  return {
    id: str(x.id), importId: str(x.import_id), sourceCode: str(x.source_code), currency: str(x.currency), ref: str(x.batch_ref), date: str(x.settlement_date),
    credits: minor(x.gross_credits_minor), debits: minor(x.gross_debits_minor), fees: minor(x.fees_minor), net: minor(x.net_minor),
    expectedNet: minor(x.expected_net_minor), lines: int(x.lines), settled: int(x.lines_settled), status: str(x.status), items: list(x.items, readLine),
  };
}
export const readBatches = (v: unknown): Batch[] => items(v, readBatch);

export interface PayoutQueueItem {
  id: string;
  status: string;
  amount: string | null;
  currency: string;
  providerRef: string | null;
  unknownSince: string | null;
  reservation: string | null;
  createAttempts: number;
  notSentAttempts: number;
  lastAttempt: string | null;
  openExceptions: string[];
  retryHalted: boolean;
}

export interface PayoutView {
  queue: PayoutQueueItem[];
  unknown: number;
  halted: number;
  matches: Count[];
  exceptions: ExceptionItem[];
  rules: string[];
}

export function readPayoutView(v: unknown): PayoutView {
  const d = isRec(v) ? v : {};
  return {
    queue: list(d.queue, (x) => ({
      id: str(x.id), status: str(x.status), amount: minor(x.amount_minor), currency: str(x.currency), providerRef: strOrNull(x.provider_reference),
      unknownSince: strOrNull(x.unknown_since), reservation: strOrNull(x.reservation_status), createAttempts: int(x.create_attempts),
      notSentAttempts: int(x.not_sent_attempts), lastAttempt: strOrNull(x.last_attempt_classification), openExceptions: strings(x.open_exceptions), retryHalted: bool(x.retry_halted),
    })),
    unknown: int(d.unknown), halted: int(d.retry_halted), matches: counts(d.matches_by_ledger_status), exceptions: list(d.exceptions, readException),
    rules: strings(d.resubmission_rules),
  };
}

export interface LedgerView {
  settlementPosting: string;
  integrity: Array<{ code: string; name: string; status: string; checked: number; violations: number }>;
  trialBalance: Array<{ currency: string; debit: string | null; credit: string | null; difference: string | null; balanced: boolean }>;
  ledgerExceptions: Array<{ id: string; type: string; status: string; sourceType: string; currency: string | null }>;
  matches: Count[];
  missing: ExceptionItem[];
}

export function readLedgerView(v: unknown): LedgerView {
  const d = isRec(v) ? v : {};
  return {
    settlementPosting: str(d.settlement_posting),
    integrity: list(d.integrity, (x) => ({ code: str(x.code), name: str(x.name), status: str(x.status), checked: int(x.checked), violations: int(x.violations) })),
    trialBalance: list(d.trial_balance, (x) => ({ currency: str(x.currency), debit: minor(x.total_debit_minor), credit: minor(x.total_credit_minor),
      difference: minor(x.difference_minor), balanced: bool(x.balanced) })),
    ledgerExceptions: list(d.ledger_exceptions_open, (x) => ({ id: str(x.id), type: str(x.exception_type), status: str(x.status), sourceType: str(x.source_type),
      currency: strOrNull(x.currency) })),
    matches: counts(d.matches_by_ledger_status),
    missing: list(d.missing_ledger_entries, readException),
  };
}

export interface FeeView {
  evidence: string;
  revenue: string;
  matches: Count[];
  mismatches: ExceptionItem[];
  recompute: { checked: number; matched: number; mismatches: number; finishedAt: string | null } | null;
}

export function readFeeView(v: unknown): FeeView {
  const d = isRec(v) ? v : {};
  const r = isRec(d.fee_recompute_latest_run) ? d.fee_recompute_latest_run : null;
  return {
    evidence: str(d.provider_fee_evidence), revenue: str(d.revenue_recognition), matches: counts(d.matches_by_fee_status),
    mismatches: list(d.fee_mismatches, readException),
    recompute: r ? { checked: int(r.checked), matched: int(r.matched), mismatches: int(r.mismatches), finishedAt: strOrNull(r.finished_at) } : null,
  };
}

export interface Report {
  kind: string;
  columns: string[];
  rows: string[][];
  truncated: boolean;
  from: string | null;
  to: string | null;
}

export function readReport(v: unknown): Report {
  const d = isRec(v) ? v : {};
  return {
    kind: str(d.kind), columns: strings(d.columns), truncated: bool(d.truncated), from: strOrNull(d.from), to: strOrNull(d.to),
    rows: Array.isArray(d.rows) ? d.rows.filter(Array.isArray).map((r) => (r as unknown[]).map((c) => (typeof c === "string" ? c : ""))) : [],
  };
}

export const REPORT_KINDS: ReadonlyArray<{ kind: string; label: string }> = [
  { kind: "daily_summary", label: "Daily summary" },
  { kind: "settlement_summary", label: "Settlement summary" },
  { kind: "unmatched_lines", label: "Unmatched lines" },
  { kind: "payout_exceptions", label: "Payout exceptions" },
  { kind: "fee_discrepancies", label: "Fee discrepancies" },
  { kind: "ledger_integrity", label: "Ledger integrity" },
  { kind: "exception_ageing", label: "Exception ageing" },
];

export const SOURCES: ReadonlyArray<{ code: string; label: string }> = [
  { code: "sandbox_transactions_test", label: "Sandbox transaction report (TEST)" },
  { code: "sandbox_settlement_test", label: "Sandbox settlement report (TEST)" },
  { code: "fundzim_operating_bank_test", label: "FundZim operating bank statement (TEST)" },
];

export const RESOLUTION_TYPES: ReadonlyArray<{ type: string; label: string; help: string }> = [
  { type: "EXPLAINED_NO_ACTION", label: "Explained, no action", help: "The difference is understood and needs no change." },
  { type: "PROVIDER_CORRECTION_CONFIRMED", label: "Provider correction confirmed", help: "The provider corrected it; no FundZim change." },
  { type: "ESCALATED_TO_CASE", label: "Escalated to a case", help: "Handed to an operations or compliance case (give its reference)." },
  { type: "MANUAL_MATCH", label: "Manual match", help: "Link this line to a payment, payout or remittance line with the same amount and currency." },
  { type: "PROVIDER_OUTCOME_APPLIED", label: "Apply the statement outcome", help: "For an unknown outcome: the owning module applies the statement's evidence." },
  { type: "LEDGER_ADJUSTMENT", label: "Ledger adjustment", help: "A balanced correction with a named rule, through the ledger's own approval (TEST ledger only)." },
];

export const EVIDENCE_REF = /^(import|item|run|evidence|batch|payment|payout|ledger_transaction|document):[A-Za-z0-9_.:/-]{1,180}$/;
