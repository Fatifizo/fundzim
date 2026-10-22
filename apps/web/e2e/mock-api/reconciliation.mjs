// In-memory mock of the Stage 17 reconciliation staff API (api/openapi/fundzim-v1.yaml, tag "Reconciliation (Stage 17)")
// for the E2E suite. Shapes follow internal/reconciliation. It enforces what the UI must cope with: FINANCE permissions
// (COMPLIANCE may read and export), step-up on import / approve / rules approval / export, maker-checker resolutions and
// rule sets (the proposer never decides; the approval must carry the proposal hash), the exception lifecycle edges, and
// duplicate files refused by hash. TEST evidence only. NOT a reference implementation.
import { createHash, randomUUID } from "node:crypto";

export const ROLE_PERMISSIONS = {
  FINANCE: ["reconciliation.view", "reconciliation.import", "reconciliation.run", "reconciliation.investigate", "reconciliation.resolve",
    "reconciliation.rules.request", "reconciliation.rules.approve", "reconciliation.report.export"],
  COMPLIANCE: ["reconciliation.view", "reconciliation.report.export"],
};
const STEP_UP = new Set(["reconciliation.import", "reconciliation.resolve", "reconciliation.rules.approve", "reconciliation.report.export"]);
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const sha = (s) => createHash("sha256").update(s).digest("hex");
const EDGES = new Set(["OPEN>ASSIGNED", "REOPENED>ASSIGNED", "ASSIGNED>INVESTIGATING", "INVESTIGATING>AWAITING_EVIDENCE", "AWAITING_EVIDENCE>INVESTIGATING",
  "INVESTIGATING>PENDING_APPROVAL", "PENDING_APPROVAL>INVESTIGATING", "PENDING_APPROVAL>RESOLVED", "RESOLVED>CLOSED", "RESOLVED>REOPENED"]);
const SOURCES = [
  { id: randomUUID(), code: "sandbox_transactions_test", source_kind: "PROVIDER_TRANSACTION_REPORT", provider_code: "sandbox", authority_rank: 2 },
  { id: randomUUID(), code: "sandbox_settlement_test", source_kind: "PROVIDER_SETTLEMENT_REPORT", provider_code: "sandbox", authority_rank: 3 },
  { id: randomUUID(), code: "fundzim_operating_bank_test", source_kind: "BANK_STATEMENT", provider_code: null, authority_rank: 4 },
].map((s) => ({ ...s, format: "FUNDZIM_CANONICAL_V1", environment: "TEST", description: "TEST source", enabled: true }));

export function createReconciliationMock({ send, err, stepUpTtlMs }) {
  const imports = new Map();
  const lines = new Map();
  const exceptions = new Map();
  const resolutions = new Map();
  const runs = new Map();
  const batches = new Map();
  const ruleSets = new Map();
  const hashes = new Map();
  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const event = (e, type, from, to, actor, note = null, evidence = null, resolution = null) =>
    e.events.push({ id: randomUUID(), event_type: type, from_status: from, to_status: to, actor_type: actor ? "STAFF" : "SYSTEM", actor_id: actor,
      run_id: null, resolution_id: resolution, note, evidence_ref: evidence, created_at: iso() });

  function addImport(source, statementRef, status, extra = {}) {
    const id = randomUUID();
    const content = `${statementRef}:${id}`;
    const imp = { id, source_id: SOURCES.find((s) => s.code === source)?.id, source_code: source, source_kind: SOURCES.find((s) => s.code === source)?.source_kind,
      origin: "STAFF_UPLOAD", content_sha256: sha(content), size_bytes: 512, media_type: "application/json", original_filename: "statement.json",
      stored_object_id: randomUUID(), evidence_record_id: randomUUID(), statement_ref: statementRef, period_start: iso(86400_000), period_end: iso(),
      currency: "USD", opening_balance_minor: null, closing_balance_minor: null, account_ref_hint: null, declared_lines: 0, lines_parsed: 0,
      lines_duplicate: 0, lines_conflicting: 0, batches_parsed: 0, status, failure_code: null, validation_report: [], supersedes_import_id: null,
      environment: "TEST", uploaded_by: null, received_at: iso(), parsed_at: status === "PARSED" ? iso() : null, label: "TEST_ONLY", ...extra };
    imports.set(id, imp);
    hashes.set(imp.content_sha256, id);
    return imp;
  }
  function addLine(imp, lineId, kind, status, amount, matchStatus, match = null) {
    const l = { id: randomUUID(), import_id: imp.id, external_line_id: lineId, revision: 1, line_number: lines.size + 1, item_kind: kind,
      direction: kind === "PAYOUT" ? "DEBIT" : "CREDIT", line_status: status, amount_minor: amount, fee_minor: null, net_minor: null, currency: "USD",
      raw_currency_code: "USD", provider_ref: `sbx_${lineId}`, merchant_ref: randomUUID(), batch_ref: null, settlement_batch_id: null, counterparty_ref: null,
      value_date: null, booked_at: iso(3_600_000), raw_line_sha256: sha(lineId), raw_line_locator: `json:$.lines[${lines.size}]`, match_status: matchStatus, match };
    lines.set(l.id, l);
    imp.lines_parsed += 1;
    imp.declared_lines += 1;
    return l;
  }
  function addException(type, severity, line, extra = {}) {
    const e = { id: randomUUID(), exception_type: type, severity, status: "OPEN", source_code: "sandbox_transactions_test", source_kind: "PROVIDER_TRANSACTION_REPORT",
      currency: "USD", occurrences: 1, generation: 1, item_id: line?.id ?? null, subject_type: null, subject_id: null, settlement_batch_id: null,
      ledger_transaction_id: null, expected_amount_minor: null, actual_amount_minor: line?.amount_minor ?? null, actual_currency_raw: null,
      detail: { reason: "seeded TEST exception" }, detected_at: iso(7_200_000), last_seen_at: iso(), age_hours: 2, assigned_to: null, assigned_at: null,
      due_at: null, escalated_at: null, resolved_at: null, closed_at: null, close_reason: null, reopen_count: 0, version: 1, label: "TEST_ONLY",
      events: [], ...extra };
    event(e, "OPENED", null, "OPEN", null);
    exceptions.set(e.id, e);
    return e;
  }

  function seed() {
    const imp = addImport("sandbox_transactions_test", `SEED-${randomUUID().slice(0, 8)}`, "PARSED");
    const matched = addLine(imp, `M-${randomUUID().slice(0, 6)}`, "PAYMENT", "SUCCEEDED", "1500", "MATCHED", {
      id: randomUUID(), run_id: randomUUID(), subject_type: "PAYMENT", subject_id: randomUUID(), confidence: "EXACT_MATCH", rule_code: "MERCHANT_AND_PROVIDER_REF",
      rule_set_version: 1, matcher_version: "MATCHER_V1", matched_amount_minor: "1500", ledger_status: "NOT_POSTED_TEST_PAYMENT", ledger_transaction_id: null,
      fee_status: "NOT_REPORTED", explanation: { merchant_ref: "AGREES", provider_ref: "AGREES", amount_checked: true }, created_at: iso() });
    const unknownLine = addLine(imp, `U-${randomUUID().slice(0, 6)}`, "PAYMENT", "SUCCEEDED", "1705", "EXCEPTION");
    const missingLine = addLine(imp, `X-${randomUUID().slice(0, 6)}`, "PAYMENT", "SUCCEEDED", "999", "EXCEPTION");
    const unknown = addException("UNKNOWN_PAYMENT_OUTCOME", "HIGH", unknownLine, { subject_type: "PAYMENT", subject_id: randomUUID(), expected_amount_minor: "1705",
      detail: { reason: "the statement reports success for a payment whose outcome is unknown; nothing is applied automatically", record_status: "UNKNOWN" } });
    const missing = addException("MISSING_PAYMENT", "HIGH", missingLine);
    const runId = randomUUID();
    runs.set(runId, { id: runId, source_code: "sandbox_transactions_test", source_kind: "PROVIDER_TRANSACTION_REPORT", currency: "USD", window_start: iso(86400_000),
      window_end: iso(), run_kind: "SCHEDULED", status: "COMPLETED", rule_set_id: null, rule_set_version: 1, matcher_version: "MATCHER_V1", replay_of_run_id: null,
      reason: null, triggered_by_type: "SYSTEM", triggered_by: null, items_considered: 3, items_matched: 1, exceptions_opened: 2, exceptions_seen: 0,
      exceptions_closed: 0, decisions_sha256: sha(runId), failure_code: null, queued_at: iso(60_000), started_at: iso(59_000), finished_at: iso(58_000),
      duration_ms: 1000, label: "TEST_ONLY" });
    const b = { id: randomUUID(), import_id: imp.id, source_code: "sandbox_settlement_test", provider_code: "sandbox", currency: "USD",
      batch_ref: `B-${randomUUID().slice(0, 6)}`, settlement_date: iso().slice(0, 10), gross_credits_minor: "3000", gross_debits_minor: "15", fees_minor: "90",
      net_minor: "2895", expected_net_minor: "2895", declared_lines: 2, lines: 2, lines_settled: 2, status: "MATCHED", last_run_id: runId, created_at: iso(), label: "TEST_ONLY" };
    batches.set(b.id, b);
    if (![...ruleSets.values()].some((r) => r.status === "ACTIVE")) {
      const id = randomUUID();
      const config = { date_window_days: 2, settlement_horizon_days: 1, stale_after_hours: 720 };
      ruleSets.set(id, { id, version_no: ruleSets.size + 1, status: "ACTIVE", config, config_sha256: sha(JSON.stringify(config)), change_reason: "seeded for the E2E suite",
        proposed_by: randomUUID(), proposed_at: iso(86400_000), decided_by: randomUUID(), decided_at: iso(80000_000), decision_note: "checked",
        effective_from: iso(80000_000), retired_at: null, version: 2 });
    }
    return { import_id: imp.id, unknown_exception_id: unknown.id, missing_exception_id: missing.id, matched_line_id: matched.id, batch_id: b.id, run_id: runId };
  }

  const view = (e) => ({ ...e, age_hours: Math.floor((Date.now() - Date.parse(e.detected_at)) / 3_600_000), item: e.item_id ? lines.get(e.item_id) : undefined,
    resolutions: [...resolutions.values()].filter((r) => r.exception_id === e.id) });
  const counts = (items, key) => {
    const m = new Map();
    for (const x of items) m.set(key(x), (m.get(key(x)) ?? 0) + 1);
    return [...m.entries()].map(([k, v]) => ({ key: k, value: v, nature: "TEST_ONLY" }));
  };
  const activeRuleSet = () => [...ruleSets.values()].find((r) => r.status === "ACTIVE") ?? null;

  async function handle({ res, method, path, url, user, session, body, raw }) {
    if (!path.startsWith("/api/v1/admin/reconciliation")) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && !(session && Date.now() - session.stepUpAt <= stepUpTtlMs)) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const A = "/api/v1/admin/reconciliation";
    let m;
    if (method === "GET") {
      if (path === `${A}/reports/export`) {
        if (!need("reconciliation.report.export")) return true;
        res.writeHead(200, { "Content-Type": "text/csv; charset=utf-8", "Content-Disposition": 'attachment; filename="fundzim-reconciliation-TEST.csv"', "Cache-Control": "private, no-store" });
        res.end("nature,kind\nTEST_ONLY,'-1\n");
        return true;
      }
      if (!need("reconciliation.view")) return true;
      const open = [...exceptions.values()].filter((e) => !["RESOLVED", "CLOSED"].includes(e.status));
      if (path === `${A}/overview`) {
        const rs = activeRuleSet();
        return send(res, 200, { environment: "TEST", nature: "TEST_ONLY", production: { imports: "BLOCKED", settlement_posting: "BLOCKED",
          adjustments: "BLOCKED_OUTSIDE_TEST_LEDGER", settlement_evidence: "NOT_AVAILABLE: no provider is selected (ADR-040)" },
          active_rule_set: rs, rule_set_status: rs ? "ACTIVE" : "NOT_CONFIGURED",
          sources: SOURCES.map((s) => ({ code: s.code, source_kind: s.source_kind, authority_rank: s.authority_rank, environment: "TEST",
            last_received_at: null, last_parsed_at: [...imports.values()].some((i) => i.source_code === s.code && i.status === "PARSED") ? iso() : null,
            last_completed_run_at: null, open_lines: 0, freshness: [...imports.values()].some((i) => i.source_code === s.code && i.status === "PARSED") ? "FRESH" : "NEVER" })),
          imports: counts([...imports.values()], (i) => i.status), lines: counts([...lines.values()], (l) => `${l.currency}:${l.match_status}`),
          exceptions_open_by_severity: counts(open, (e) => e.severity), exceptions_open_by_type: counts(open, (e) => e.exception_type),
          oldest_open_exception_hours: open.length ? 2 : null, resolutions_pending: [...resolutions.values()].filter((r) => r.status === "PENDING_REVIEW").length,
          settlement_batches: counts([...batches.values()], (b) => `${b.currency}:${b.status}`), unknown_outcomes_open: open.filter((e) => e.exception_type.startsWith("UNKNOWN_")).length,
          workers: ["reconciliation.parse", "reconciliation.match", "reconciliation.ageing"].map((j) => ({ job_kind: j, last_started_at: iso(30_000),
            last_succeeded_at: iso(29_000), last_failed_at: null, last_error_code: null, consecutive_failures: 0, status: "HEALTHY" })),
          recent_runs: [...runs.values()].slice(-10).reverse(), generated_at: iso() }), true;
      }
      if (path === `${A}/sources`) return send(res, 200, { items: SOURCES }), true;
      if (path === `${A}/imports`) return send(res, 200, { items: [...imports.values()].reverse() }), true;
      if ((m = new RegExp(`^${A}/imports/([0-9a-f-]{36})$`).exec(path))) {
        const imp = imports.get(m[1]);
        if (!imp) return err(res, 404, "NOT_FOUND"), true;
        const ls = [...lines.values()].filter((l) => l.import_id === imp.id);
        return send(res, 200, { import: imp, lines: ls, exceptions: [...exceptions.values()].filter((e) => ls.some((l) => l.id === e.item_id)).map(view) }), true;
      }
      if (path === `${A}/runs`) return send(res, 200, { items: [...runs.values()].reverse() }), true;
      if ((m = new RegExp(`^${A}/runs/([0-9a-f-]{36})$`).exec(path))) return (runs.has(m[1]) ? send(res, 200, runs.get(m[1])) : err(res, 404, "NOT_FOUND")), true;
      if (path === `${A}/exceptions`) {
        const st = url.searchParams.get("status");
        const sev = url.searchParams.get("severity");
        const onlyOpen = url.searchParams.get("open") === "true";
        const items = [...exceptions.values()].filter((e) => (!st || e.status === st) && (!sev || e.severity === sev) && (!onlyOpen || !["RESOLVED", "CLOSED"].includes(e.status)));
        return send(res, 200, { items: items.map(view) }), true;
      }
      if ((m = new RegExp(`^${A}/exceptions/([0-9a-f-]{36})$`).exec(path))) {
        const e = exceptions.get(m[1]);
        return (e ? send(res, 200, view(e)) : err(res, 404, "NOT_FOUND")), true;
      }
      if (path === `${A}/resolutions`) return send(res, 200, { items: [...resolutions.values()].filter((r) => r.status === "PENDING_REVIEW") }), true;
      if (path === `${A}/settlements`) return send(res, 200, { items: [...batches.values()] }), true;
      if ((m = new RegExp(`^${A}/settlements/([0-9a-f-]{36})$`).exec(path))) {
        const b = batches.get(m[1]);
        return (b ? send(res, 200, { ...b, items: [] }) : err(res, 404, "NOT_FOUND")), true;
      }
      if (path === `${A}/payouts`) return send(res, 200, { nature: "TEST_ONLY", queue: [{ id: randomUUID(), status: "UNKNOWN", amount_minor: "1004", currency: "USD",
        provider_reference: null, test_payout: true, unknown_since: iso(3_600_000), reservation_status: "BLOCKED", create_attempts: 1, not_sent_attempts: 0,
        last_attempt_classification: "OUTCOME_UNKNOWN", open_exceptions: ["UNKNOWN_PROVIDER_OUTCOME"], retry_halted: false }], unknown: 1, retry_halted: 0,
        matches_by_ledger_status: [{ key: "POSTED", value: 1, nature: "TEST_ONLY" }], exceptions: [],
        resubmission_rules: ["An UNKNOWN payout is never resubmitted; only a status query, a webhook or approved statement evidence resolves it.",
          "Reconciliation never releases or consumes a reservation: the payout's own transitions do."] }), true;
      if (path === `${A}/ledger`) return send(res, 200, { nature: "TEST_ONLY", settlement_posting: "BLOCKED: no settlement postings in Stage 17 (ADR-049 §8; accounting review KI-S10-02)",
        integrity: [{ code: "L1", name: "every journal balances", status: "PASSED", checked: 12, violations: 0, sample: [] }],
        trial_balance: [{ currency: "USD", total_debit_minor: "35000", total_credit_minor: "35000", difference_minor: "0", balanced: true }],
        ledger_exceptions_open: [], matches_by_ledger_status: [{ key: "PAYMENT:NOT_POSTED_TEST_PAYMENT", value: 1, nature: "TEST_ONLY" }], missing_ledger_entries: [] }), true;
      if (path === `${A}/fees`) return send(res, 200, { nature: "TEST_ONLY", provider_fee_evidence: "SYNTHETIC or UNVERIFIED until a provider is selected (ADR-040, PCR-016)",
        matches_by_fee_status: [{ key: "PAYMENT:NOT_REPORTED", value: 1, nature: "TEST_ONLY" }], fee_mismatches: [], fee_recompute_latest_run: null,
        revenue_recognition: "BLOCKED: no revenue is recognised from reconciliation" }), true;
      if (path === `${A}/reports`) {
        const kind = url.searchParams.get("kind") ?? "daily_summary";
        return send(res, 200, { kind, nature: "TEST_ONLY", from: iso(30 * 86400_000), to: iso(), generated_at: iso(), columns: ["day", "source", "currency", "runs"],
          rows: [[iso().slice(0, 10), "sandbox_transactions_test", "USD", String(runs.size)]], truncated: false }), true;
      }
      if (path === `${A}/rule-sets`) return send(res, 200, { items: [...ruleSets.values()].sort((a, b) => b.version_no - a.version_no) }), true;
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    if (method !== "POST") return err(res, 405, "METHOD_NOT_ALLOWED"), true;
    if (path === `${A}/imports`) {
      if (!need("reconciliation.import")) return true;
      const text = raw ? raw.toString("utf8") : "";
      const source = /name="source"\r\n\r\n([^\r]+)/.exec(text)?.[1];
      if (!SOURCES.some((s) => s.code === source)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "source", code: "UNKNOWN_SOURCE" }] }), true;
      const file = /name="file"; filename="([^"]*)"\r\nContent-Type: ([^\r]+)\r\n\r\n([\s\S]*?)\r\n--/.exec(text);
      if (!file || !["text/csv", "application/json"].includes(file[2])) return err(res, 422, "UNSUPPORTED_FILE_TYPE"), true;
      const h = sha(file[3]);
      if (hashes.has(h)) return err(res, 409, "DUPLICATE_IMPORT", { details: [{ field: "existing_import_id", code: hashes.get(h) }] }), true;
      const bad = /12\.50/.test(file[3]);
      const imp = addImport(source, bad ? null : `UP-${h.slice(0, 8)}`, bad ? "FAILED" : "PARSED", { content_sha256: h, original_filename: file[1], media_type: file[2],
        failure_code: bad ? "INVALID_RECORDS" : null, validation_report: bad ? [{ locator: "row:2", field: "amount_minor", code: "NOT_MINOR_UNITS" }] : [],
        uploaded_by: user.id, statement_ref: bad ? null : `UP-${h.slice(0, 8)}` });
      hashes.set(h, imp.id);
      return send(res, 202, imp), true;
    }
    if (path === `${A}/imports/sandbox`) {
      if (!need("reconciliation.import")) return true;
      const imp = addImport(body.source, `SBX-${randomUUID().slice(0, 8)}`, "PARSED", { origin: "SANDBOX_GENERATOR" });
      return send(res, 202, imp), true;
    }
    if (path === `${A}/runs`) {
      if (!need("reconciliation.run")) return true;
      if (!activeRuleSet()) return err(res, 409, "NO_ACTIVE_RULE_SET"), true;
      if (typeof body.reason !== "string" || [...body.reason.trim()].length < 5) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
      const id = randomUUID();
      const r = { id, source_code: body.source ?? "sandbox_transactions_test", source_kind: "PROVIDER_TRANSACTION_REPORT", currency: body.currency ?? "USD", window_start: iso(86400_000),
        window_end: iso(), run_kind: body.replay_of_run_id ? "REPLAY" : "ON_DEMAND", status: "COMPLETED", rule_set_id: activeRuleSet().id, rule_set_version: activeRuleSet().version_no,
        matcher_version: "MATCHER_V1", replay_of_run_id: body.replay_of_run_id ?? null, reason: body.reason, triggered_by_type: "STAFF", triggered_by: user.id,
        items_considered: 0, items_matched: 0, exceptions_opened: 0, exceptions_seen: 0, exceptions_closed: 0, decisions_sha256: sha(id), failure_code: null,
        queued_at: iso(), started_at: iso(), finished_at: iso(), duration_ms: 5, label: "TEST_ONLY" };
      runs.set(id, r);
      return send(res, 202, r), true;
    }
    if ((m = new RegExp(`^${A}/exceptions/([0-9a-f-]{36})/([a-z-]+)$`).exec(path))) {
      const e = exceptions.get(m[1]);
      if (!e) return err(res, 404, "NOT_FOUND"), true;
      const action = m[2];
      if (!need("reconciliation.investigate")) return true;
      if (body.version && body.version !== e.version) return err(res, 409, "STALE_VERSION"), true;
      const move = (to) => {
        if (!EDGES.has(`${e.status}>${to}`)) return false;
        event(e, "STATUS_CHANGED", e.status, to, user.id, body.note ?? null);
        e.status = to;
        e.version += 1;
        return true;
      };
      if (action === "resolutions") {
        if (e.status !== "INVESTIGATING") return err(res, 409, "INVALID_STATE"), true;
        if (typeof body.justification !== "string" || [...body.justification.trim()].length < 20) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "justification", code: "INVALID_LENGTH" }] }), true;
        const id = randomUUID();
        const proposal = { exception_type: e.exception_type, currency: e.currency, target: e.exception_type.startsWith("UNKNOWN_") ? "SUCCEEDED" : undefined };
        const r = { id, exception_id: e.id, resolution_type: body.resolution_type, status: "PENDING_REVIEW", proposal, proposal_sha256: sha(id + JSON.stringify(proposal)),
          justification: body.justification, evidence_refs: body.evidence_refs ?? [], proposed_by: user.id, proposed_at: iso(), expires_at: new Date(Date.now() + 86400_000).toISOString(),
          decided_by: null, decided_at: null, decision_note: null, ledger_adjustment_id: null, executed_at: null, execution_result: null, last_error: null, version: 1 };
        resolutions.set(id, r);
        move("PENDING_APPROVAL");
        event(e, "RESOLUTION_PROPOSED", null, null, user.id, body.justification, null, id);
        return send(res, 201, r), true;
      }
      if (action === "assign") {
        if (!/^[0-9a-f-]{36}$/.test(body.assignee_id ?? "")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "assignee_id", code: "INVALID_FORMAT" }] }), true;
        if (["OPEN", "REOPENED"].includes(e.status)) move("ASSIGNED");
        else if (!["ASSIGNED", "INVESTIGATING", "AWAITING_EVIDENCE"].includes(e.status)) return err(res, 409, "INVALID_STATE"), true;
        e.assigned_to = body.assignee_id;
        e.assigned_at = iso();
        event(e, "ASSIGNED", null, null, user.id, `assigned to ${body.assignee_id}`);
        return send(res, 200, view(e)), true;
      }
      const target = { start: "INVESTIGATING", "await-evidence": "AWAITING_EVIDENCE", resume: "INVESTIGATING", close: "CLOSED", reopen: "REOPENED" }[action];
      if (action === "comment") {
        event(e, body.evidence_ref ? "EVIDENCE_ADDED" : "COMMENT", null, null, user.id, body.note ?? null, body.evidence_ref ?? null);
        return send(res, 200, view(e)), true;
      }
      if (!target) return err(res, 404, "ROUTE_NOT_FOUND"), true;
      if (action === "start" && e.assigned_to !== user.id) return err(res, 403, "NOT_ASSIGNEE"), true;
      if (!move(target)) return err(res, 409, "INVALID_STATE"), true;
      if (target === "CLOSED") Object.assign(e, { closed_at: iso(), close_reason: "RESOLVED" });
      return send(res, 200, view(e)), true;
    }
    if ((m = new RegExp(`^${A}/resolutions/([0-9a-f-]{36})/(approve|reject|cancel)$`).exec(path))) {
      const r = resolutions.get(m[1]);
      if (!r) return err(res, 404, "NOT_FOUND"), true;
      const action = m[2];
      if (!need(action === "cancel" ? "reconciliation.investigate" : "reconciliation.resolve")) return true;
      if (typeof body.note !== "string" || [...body.note.trim()].length < 5) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
      if (r.status !== "PENDING_REVIEW") return err(res, 409, "INVALID_STATE"), true;
      const e = exceptions.get(r.exception_id);
      if (action === "cancel") {
        if (r.proposed_by !== user.id) return err(res, 403, "NOT_PROPOSER"), true;
        r.status = "CANCELLED";
        e.status = "INVESTIGATING";
        event(e, "RESOLUTION_WITHDRAWN", "PENDING_APPROVAL", "INVESTIGATING", user.id, body.note, null, r.id);
        return send(res, 200, r), true;
      }
      if (r.proposed_by === user.id) return err(res, 403, "SELF_DECISION_FORBIDDEN"), true;
      Object.assign(r, { decided_by: user.id, decided_at: iso(), decision_note: body.note });
      if (action === "reject") {
        r.status = "REJECTED";
        e.status = "INVESTIGATING";
        event(e, "RESOLUTION_REJECTED", "PENDING_APPROVAL", "INVESTIGATING", user.id, body.note, null, r.id);
        return send(res, 200, r), true;
      }
      if (body.proposal_sha256 !== r.proposal_sha256) return err(res, 409, "PROPOSAL_CHANGED"), true;
      Object.assign(r, { status: "EXECUTED", executed_at: iso(), execution_result: { applied: true, from: "UNKNOWN", to: "SUCCEEDED" } });
      e.status = "RESOLVED";
      e.resolved_at = iso();
      e.version += 1;
      event(e, "RESOLUTION_EXECUTED", "PENDING_APPROVAL", "RESOLVED", user.id, body.note, null, r.id);
      return send(res, 200, r), true;
    }
    if (path === `${A}/rule-sets`) {
      if (!need("reconciliation.rules.request")) return true;
      if ([...ruleSets.values()].some((r) => r.status === "PENDING_APPROVAL")) return err(res, 409, "RULE_SET_PENDING"), true;
      const c = body.config ?? {};
      if (!(c.date_window_days >= 0 && c.date_window_days <= 14 && c.settlement_horizon_days >= 1 && c.settlement_horizon_days <= 60 && c.stale_after_hours >= 1))
        return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "config.date_window_days", code: "OUT_OF_RANGE" }] }), true;
      const id = randomUUID();
      const r = { id, version_no: ruleSets.size + 1, status: "PENDING_APPROVAL", config: c, config_sha256: sha(JSON.stringify(c)), change_reason: body.change_reason,
        proposed_by: user.id, proposed_at: iso(), decided_by: null, decided_at: null, decision_note: null, effective_from: null, retired_at: null, version: 1 };
      ruleSets.set(id, r);
      return send(res, 201, r), true;
    }
    if ((m = new RegExp(`^${A}/rule-sets/([0-9a-f-]{36})/(approve|reject|retire|withdraw)$`).exec(path))) {
      const r = ruleSets.get(m[1]);
      if (!r) return err(res, 404, "NOT_FOUND"), true;
      const action = m[2];
      if (!need(action === "withdraw" ? "reconciliation.rules.request" : "reconciliation.rules.approve")) return true;
      if ((action === "approve" || action === "reject") && r.proposed_by === user.id) return err(res, 403, "SELF_DECISION_FORBIDDEN"), true;
      if (action === "approve") {
        for (const x of ruleSets.values()) if (x.status === "ACTIVE") Object.assign(x, { status: "RETIRED", retired_at: iso() });
        Object.assign(r, { status: "ACTIVE", decided_by: user.id, decided_at: iso(), decision_note: body.note, effective_from: iso() });
      } else if (action === "reject") Object.assign(r, { status: "REJECTED", decided_by: user.id, decided_at: iso(), decision_note: body.note });
      else if (action === "withdraw") Object.assign(r, { status: "WITHDRAWN" });
      else Object.assign(r, { status: "RETIRED", retired_at: iso() });
      return send(res, 200, r), true;
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  async function control(path, _body, res) {
    if (path !== "/api/v1/__mock/reconciliation/seed") return false;
    return send(res, 201, seed()), true;
  }
  return { handle, control };
}
