// In-memory mock of the Stage 13 risk operations API for the E2E suite (shapes as internal/risk). TEST data only:
// production screening is BLOCKED, accuracy is NOT_MEASURED, screening results are TEST_ONLY. COMPLIANCE holds every
// risk permission, FINANCE only risk.operations.view; every mutation needs a fresh step-up; a maker is never the
// checker of the same decision. Only good enough for the UI tests. NOT a reference implementation.
import { randomUUID } from "node:crypto";

const ALL = ["risk.operations.view", "risk.case.work", "risk.case.approve", "risk.rule.request", "risk.rule.approve", "risk.hold.create", "risk.hold.release",
  "risk.screening.request", "risk.screening.review"];
const ROLE_PERMISSIONS = { COMPLIANCE: ALL, FINANCE: ["risk.operations.view"], ADMIN: ["risk.sandbox.control"] };
const UUID = "[0-9a-f-]{36}";
const OTHER_STAFF = "0192a000-0000-7000-8000-00000000f102";
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const hash = () => randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");

export function createRiskMock({ send, err, stepUpTtlMs }) {
  const rules = new Map();
  const versions = new Map();
  const cases = new Map();
  const evidence = new Map();
  const holds = new Map();
  const screenings = new Map();
  const events = [];
  const monitoring = [];
  const idem = new Map();
  const actions = [];

  const history = (aggregate_id, event_type, actor_type, actor_id, detail = {}) => events.push({ aggregate_id, event_type, actor_type, actor_id, detail, occurred_at: iso() });
  const ruleView = (r) => {
    const vs = [...versions.values()].filter((v) => v.rule_id === r.id).sort((a, b) => b.version_no - a.version_no);
    return { ...r, versions: vs, active_version_id: vs.find((v) => v.status === "ACTIVE")?.id ?? null };
  };
  const newVersion = (ruleId, body, by) => {
    const v = { id: randomUUID(), rule_id: ruleId, version_no: [...versions.values()].filter((x) => x.rule_id === ruleId).length + 1, status: "DRAFT", definition: body.definition ?? {},
      contribution: body.contribution ?? 0, severity: body.severity ?? "MEDIUM", recommended_action: body.recommended_action ?? "REVIEW_REQUIRED", hold_type: body.hold_type ?? null,
      hold_duration_seconds: body.hold_duration_seconds ?? null, case_priority: body.case_priority ?? "P3", engine_version: "risk-engine-v2", definition_hash: null, created_by: by,
      submitted_by: null, approved_by: null, approval_reason: null, rejected_by: null, rejection_reason: null, activated_at: null, suspended_by: null, suspension_reason: null,
      retired_by: null, retirement_reason: null, version: 1, created_at: iso() };
    versions.set(v.id, v);
    history(v.id, "VERSION_DRAFTED", "STAFF", by);
    return v;
  };
  const newRule = (body, by) => {
    const r = { id: randomUUID(), code: body.code, name: body.name, description: body.description ?? "", subject_type: body.subject_type, category: body.category, created_by: by, created_at: iso() };
    rules.set(r.id, r);
    return r;
  };
  const newCase = (c) => {
    const x = { id: randomUUID(), case_number: cases.size + 1001, case_type: c.case_type, subject_type: c.subject_type, subject_id: c.subject_id, status: "OPEN", priority: c.priority ?? "P3",
      risk_score: c.risk_score ?? null, classification: c.classification ?? null, recommended_action: null, assigned_team: "RISK_OPERATIONS", assigned_to: null, due_at: iso(-72 * 3600_000),
      proposed_outcome: null, proposed_by: null, proposed_at: null, proposed_reason: null, decision_outcome: null, decided_by: null, decided_at: null, decision_reason: null,
      escalated_at: null, opened_by_type: c.opened_by_type ?? "SYSTEM", opened_by: c.opened_by ?? "risk.engine", version: 1, created_at: iso(), assignments: [], reviews: [] };
    cases.set(x.id, x);
    history(x.id, "CASE_OPENED", x.opened_by_type, x.opened_by);
    return x;
  };
  const addEvidence = (caseId, e) => {
    const x = { id: randomUUID(), case_id: caseId, evidence_type: e.evidence_type, reference_type: e.reference_type ?? null, reference_id: e.reference_id ?? null,
      content: e.content, content_hash: hash(), source: e.source ?? "staff", added_by_type: e.added_by_type ?? "STAFF", added_by: e.added_by, retention_class: "RISK_RECORD_LEGAL_REVIEW_REQUIRED",
      supersedes_id: e.supersedes_id ?? null, added_at: iso() };
    evidence.set(x.id, x);
    return x;
  };
  const evidenceView = (e) => ({ ...e, superseded_by: [...evidence.values()].find((n) => n.supersedes_id === e.id)?.id ?? null });
  const caseView = (c) => {
    const out = { ...c };
    delete out.assignments;
    delete out.reviews;
    return out;
  };
  const newHold = (h) => {
    const x = { id: randomUUID(), hold_type: h.hold_type, subject_type: h.subject_type, subject_id: h.subject_id, status: h.status ?? "ACTIVE", reason_code: h.reason_code,
      description: h.description ?? "", automated: false, evaluation_id: null, case_id: h.case_id ?? null, created_by_type: "STAFF", created_by: h.created_by, activated_at: h.status === "REQUESTED" ? null : iso(),
      activated_by: null, expires_at: null, release_requested_by: h.release_requested_by ?? null, release_requested_at: h.release_requested_by ? iso() : null, release_reason: h.release_reason ?? null,
      released_by: null, released_at: null, revoked_by: null, revoked_at: null, revocation_reason: null, version: 1, created_at: iso(), updated_at: iso() };
    holds.set(x.id, x);
    return x;
  };
  const newScreening = (s, by) => {
    const id = randomUUID();
    const x = { id, subject_type: s.subject_type, subject_id: s.subject_id, provider_code: "sandbox", environment: "TEST", synthetic_reference: `TEST-${s.scenario}-${id.slice(-12).toUpperCase()}`,
      status: s.status ?? "PENDING", provider_reference: `SBXS-${randomUUID()}`, result_sequence: 0, match_count: s.match_count ?? 0, timeout_at: iso(-600_000), poll_attempts: 0, last_error: null,
      requested_by: by, reviewed_by: null, reviewed_at: null, review_reason: null, case_id: s.case_id ?? null, version: 1, created_at: iso(), updated_at: iso(), label: "TEST_ONLY", results: s.results ?? [] };
    screenings.set(id, x);
    return x;
  };
  const screeningView = (x) => {
    const out = { ...x };
    delete out.results;
    return out;
  };

  function seed() {
    const subject = randomUUID();
    const r = newRule({ code: `SEED_RULE_${subject.slice(0, 6).toUpperCase()}`, name: `Seeded pending rule ${subject.slice(0, 8)}`, subject_type: "CAMPAIGN", category: "CAMPAIGN_RISK" }, OTHER_STAFF);
    const v = newVersion(r.id, { definition: { when: { signal_count: { signal_types: ["CAMPAIGN_REPORTED"], window_seconds: 3600, op: ">=", value: 2 } } }, contribution: 40, severity: "MEDIUM",
      recommended_action: "REVIEW_REQUIRED", case_priority: "P3" }, OTHER_STAFF);
    Object.assign(v, { status: "PENDING_APPROVAL", submitted_by: OTHER_STAFF, definition_hash: hash() });
    const c = newCase({ case_type: "CAMPAIGN_RISK", subject_type: "CAMPAIGN", subject_id: subject, priority: "P2", risk_score: 60, classification: "HIGH" });
    const sig = randomUUID();
    addEvidence(c.id, { evidence_type: "EVALUATION", reference_type: "risk_evaluation", reference_id: randomUUID(), content: { score: "60", classification: "HIGH" }, source: "risk.engine", added_by_type: "SYSTEM", added_by: "risk.engine" });
    addEvidence(c.id, { evidence_type: "SIGNAL", reference_type: "risk_signal", reference_id: sig, content: { rule_code: r.code }, source: "risk.engine", added_by_type: "SYSTEM", added_by: "risk.engine" });
    const h = newHold({ hold_type: "WITHDRAWAL_HOLD", subject_type: "CAMPAIGN", subject_id: subject, status: "RELEASE_PENDING", reason_code: "SEED_REVIEW", description: `Seeded hold ${subject.slice(0, 8)}`,
      created_by: OTHER_STAFF, release_requested_by: OTHER_STAFF, release_reason: "Seeded release request", case_id: c.id });
    const s = newScreening({ subject_type: "USER", subject_id: randomUUID(), scenario: "POTENTIAL_MATCH", status: "POTENTIAL_MATCH", match_count: 1,
      results: [{ provider_event_id: `sbx:${randomUUID()}:final`, source: "CALLBACK", sequence: 2, reported_status: "POTENTIAL_MATCH", matches: [{ match_id: "TEST-MATCH-1", list: "TEST_SYNTHETIC_LIST", score: 72, label: "TEST_ONLY" }], applied: true, label: "TEST_ONLY", received_at: iso() }] }, OTHER_STAFF);
    monitoring.push({ id: randomUUID(), event_type: "HOLD_EXPIRED", severity: "MEDIUM", subject_type: "CAMPAIGN", subject_id: subject, detail: { hold_type: "WITHDRAWAL_HOLD" }, occurred_at: iso() });
    return { rule_id: r.id, version_id: v.id, case_id: c.id, signal_id: sig, hold_id: h.id, screening_id: s.id, subject_id: subject };
  }

  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const fresh = (session) => !!session && Date.now() - session.stepUpAt <= stepUpTtlMs;
  const idemReplay = (req, res, scope) => {
    const key = String(req.headers["idempotency-key"] ?? "");
    if (!/^[0-9a-f-]{36}$/i.test(key)) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), { done: true };
    const prior = idem.get(`${scope}:${key}`);
    if (prior) return send(res, 201, prior()), { done: true };
    return { done: false, remember: (view) => idem.set(`${scope}:${key}`, view) };
  };
  const reasonOk = (r) => typeof r === "string" && [...r.trim()].length >= 5 && [...r.trim()].length <= 2000;
  const counts = (xs, key) => Object.entries(xs.reduce((o, x) => ((o[x[key]] = (o[x[key]] ?? 0) + 1), o), {})).map(([k, n]) => ({ key: k, count: n }));
  const open = (c) => c.status !== "RESOLVED" && c.status !== "CLOSED";
  const holdOpen = (h) => ["REQUESTED", "ACTIVE", "UNDER_REVIEW", "RELEASE_PENDING"].includes(h.status);

  async function handle({ req, res, method, path, url, user, session, body }) {
    if (!path.startsWith("/api/v1/admin/risk")) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (perm !== "risk.operations.view" && !fresh(session)) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const A = "/api/v1/admin/risk";
    let m;
    if (method === "GET") {
      if (!need("risk.operations.view")) return true;
      if (path === `${A}/overview`) {
        const cs = [...cases.values()].filter(open);
        return send(res, 200, { generated_at: iso(), environment: "TEST", signals_last_24h_by_category: [], evaluations_last_7d_by_classification: [], open_cases_by_priority: counts(cs, "priority"),
          open_cases_by_status: counts(cs, "status"), overdue_cases: 0, open_holds_by_type: counts([...holds.values()].filter(holdOpen), "hold_type"), screenings_by_status: counts([...screenings.values()], "status"),
          monitoring_last_24h_by_type: counts(monitoring, "event_type"), active_rules: [...versions.values()].filter((v) => v.status === "ACTIVE").length,
          pending_rule_approvals: [...versions.values()].filter((v) => v.status === "PENDING_APPROVAL").length, pending_hold_activations: [...holds.values()].filter((h) => h.status === "REQUESTED").length,
          pending_hold_releases: [...holds.values()].filter((h) => h.status === "RELEASE_PENDING").length, pending_case_decisions: cs.filter((c) => c.status === "DECISION_PENDING").length,
          screening_production: "BLOCKED", accuracy_metrics: "NOT_MEASURED" }), true;
      }
      if (path === `${A}/signals` || path === `${A}/evaluations`) return send(res, 200, { items: [], next_cursor: null }), true;
      if (path === `${A}/monitoring`) return send(res, 200, { items: [...monitoring].reverse(), next_cursor: null }), true;
      if (path === `${A}/quality`) return send(res, 200, { generated_at: iso(), window_days: 30, rules: [], accuracy: "NOT_MEASURED", notice: "Counts of rule firings and human case outcomes. They are not accuracy, precision or recall: no labelled evidence set exists." }), true;
      if (path === `${A}/rules`) {
        return send(res, 200, { items: [...rules.values()].reverse().map((r) => {
          const vs = [...versions.values()].filter((v) => v.rule_id === r.id);
          const a = vs.find((v) => v.status === "ACTIVE");
          return { id: r.id, code: r.code, name: r.name, subject_type: r.subject_type, category: r.category, active_version_no: a?.version_no ?? null,
            pending_version_no: vs.find((v) => v.status === "PENDING_APPROVAL")?.version_no ?? null, active_action: a?.recommended_action ?? null };
        }), next_cursor: null }), true;
      }
      if ((m = new RegExp(`^${A}/rules/(${UUID})$`).exec(path))) return (rules.has(m[1]) ? send(res, 200, ruleView(rules.get(m[1]))) : err(res, 404, "RISK_NOT_FOUND")), true;
      if ((m = new RegExp(`^${A}/rule-versions/(${UUID})/events$`).exec(path))) return send(res, 200, { items: events.filter((e) => e.aggregate_id === m[1]), next_cursor: null }), true;
      if (path === `${A}/cases`) {
        const st = url.searchParams.get("status");
        const items = [...cases.values()].filter((c) => !st || (st === "OPEN_ANY" ? open(c) : c.status === st)).reverse().map(caseView);
        return send(res, 200, { items, next_cursor: null }), true;
      }
      if ((m = new RegExp(`^${A}/cases/(${UUID})$`).exec(path))) {
        const c = cases.get(m[1]);
        if (!c) return err(res, 404, "RISK_NOT_FOUND"), true;
        return send(res, 200, { case: caseView(c), evidence: [...evidence.values()].filter((e) => e.case_id === c.id).map(evidenceView), assignments: c.assignments, signal_reviews: c.reviews,
          holds: [...holds.values()].filter((h) => h.case_id === c.id), events: events.filter((e) => e.aggregate_id === c.id) }), true;
      }
      if (path === `${A}/holds`) {
        const st = url.searchParams.get("status");
        return send(res, 200, { items: [...holds.values()].filter((h) => !st || (st === "OPEN" ? holdOpen(h) : h.status === st)).reverse(), next_cursor: null }), true;
      }
      if (path === `${A}/screening/capabilities`) return send(res, 200, { production_screening: "BLOCKED", provider_selected: false, providers: [{ code: "sandbox", environment: "TEST", webhooks: true, polling: true, synthetic_only: true }] }), true;
      if (path === `${A}/screening`) return send(res, 200, { items: [...screenings.values()].reverse().map(screeningView), next_cursor: null }), true;
      if ((m = new RegExp(`^${A}/screening/(${UUID})$`).exec(path))) {
        const s = screenings.get(m[1]);
        return (s ? send(res, 200, { request: screeningView(s), results: s.results }) : err(res, 404, "RISK_NOT_FOUND")), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    if (method === "PUT" && (m = new RegExp(`^${A}/rule-versions/(${UUID})$`).exec(path))) {
      if (!need("risk.rule.request")) return true;
      const v = versions.get(m[1]);
      if (!v) return err(res, 404, "RISK_NOT_FOUND"), true;
      if (v.status !== "DRAFT") return err(res, 409, "RISK_RULE_STATE"), true;
      Object.assign(v, { definition: body.definition, contribution: body.contribution, severity: body.severity, recommended_action: body.recommended_action, hold_type: body.hold_type ?? null,
        hold_duration_seconds: body.hold_duration_seconds ?? null, case_priority: body.case_priority, version: v.version + 1 });
      return send(res, 200, v), true;
    }
    if (method !== "POST") return err(res, 404, "ROUTE_NOT_FOUND"), true;

    if (path === `${A}/rules`) {
      if (!need("risk.rule.request")) return true;
      const r = idemReplay(req, res, "rule");
      if (r.done) return true;
      if (!/^[A-Z][A-Z0-9_]{2,63}$/.test(body.code ?? "")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "code", code: "INVALID_FORMAT" }] }), true;
      const rule = newRule(body, user.id);
      newVersion(rule.id, body.version ?? {}, user.id);
      actions.push({ kind: "rule.create", id: rule.id });
      r.remember(() => ruleView(rule));
      return send(res, 201, ruleView(rule)), true;
    }
    if ((m = new RegExp(`^${A}/rules/(${UUID})/versions$`).exec(path))) {
      if (!need("risk.rule.request")) return true;
      const r = idemReplay(req, res, "rule-version");
      if (r.done) return true;
      if (!rules.has(m[1])) return err(res, 404, "RISK_NOT_FOUND"), true;
      const v = newVersion(m[1], body, user.id);
      r.remember(() => v);
      return send(res, 201, v), true;
    }
    if ((m = new RegExp(`^${A}/rule-versions/(${UUID})/simulate$`).exec(path))) {
      if (!need("risk.rule.request")) return true;
      return send(res, 200, { score: 0, classification: "LOW", band_source: "ENGINEERING_DEFAULT", confidence: 100, recommended_action: "ALLOW", results: [], explanation: [], simulation: true }), true;
    }
    if ((m = new RegExp(`^${A}/rule-versions/(${UUID})/(submit|approve|reject|activate|suspend|reactivate|retire)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(["approve", "reject", "reactivate"].includes(act) ? "risk.rule.approve" : "risk.rule.request")) return true;
      const v = versions.get(id);
      if (!v) return err(res, 404, "RISK_NOT_FOUND"), true;
      const needReason = !["submit", "activate"].includes(act);
      if (needReason && !reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
      const from = { submit: ["DRAFT"], approve: ["PENDING_APPROVAL"], reject: ["PENDING_APPROVAL"], activate: ["APPROVED"], suspend: ["ACTIVE"], reactivate: ["SUSPENDED"], retire: ["DRAFT", "APPROVED", "ACTIVE", "SUSPENDED"] }[act];
      if (!from.includes(v.status)) return err(res, 409, "RISK_RULE_STATE"), true;
      if ((act === "approve" || act === "reject") && (v.created_by === user.id || v.submitted_by === user.id)) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (act === "reactivate" && v.suspended_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (act === "approve" && body.definition_hash && body.definition_hash !== v.definition_hash) return err(res, 409, "RISK_RULE_CHANGED"), true;
      const to = { submit: "PENDING_APPROVAL", approve: "APPROVED", reject: "REJECTED", activate: "ACTIVE", suspend: "SUSPENDED", reactivate: "ACTIVE", retire: "RETIRED" }[act];
      if (act === "submit") Object.assign(v, { submitted_by: user.id, definition_hash: hash() });
      if (act === "approve") Object.assign(v, { approved_by: user.id, approval_reason: body.reason });
      if (act === "reject") Object.assign(v, { rejected_by: user.id, rejection_reason: body.reason });
      if (act === "activate") {
        for (const o of versions.values()) if (o.rule_id === v.rule_id && (o.status === "ACTIVE" || o.status === "SUSPENDED")) o.status = "RETIRED";
        v.activated_at = iso();
      }
      if (act === "suspend") Object.assign(v, { suspended_by: user.id, suspension_reason: body.reason });
      if (act === "retire") Object.assign(v, { retired_by: user.id, retirement_reason: body.reason });
      v.status = to;
      v.version += 1;
      history(v.id, `VERSION_${to}`, "STAFF", user.id, { reason: body.reason ?? "" });
      actions.push({ kind: `rule.${act}`, id, definition_hash_bound: !!body.definition_hash });
      return send(res, 200, v), true;
    }
    if (path === `${A}/cases`) {
      if (!need("risk.case.work")) return true;
      const r = idemReplay(req, res, "case");
      if (r.done) return true;
      if (body.subject_id === user.id) return err(res, 403, "RISK_CONFLICT_OF_INTEREST"), true;
      const c = newCase({ ...body, opened_by_type: "STAFF", opened_by: user.id });
      addEvidence(c.id, { evidence_type: "STAFF_NOTE", content: { note: body.reason }, added_by: user.id });
      r.remember(() => caseView(c));
      return send(res, 201, caseView(c)), true;
    }
    if ((m = new RegExp(`^${A}/cases/(${UUID})/evidence$`).exec(path))) {
      if (!need("risk.case.work")) return true;
      const r = idemReplay(req, res, "evidence");
      if (r.done) return true;
      const c = cases.get(m[1]);
      if (!c) return err(res, 404, "RISK_NOT_FOUND"), true;
      const e = addEvidence(c.id, { evidence_type: body.evidence_type ?? "STAFF_NOTE", reference_type: body.reference_type, reference_id: body.reference_id, content: { note: body.note },
        supersedes_id: body.supersedes_id, added_by: user.id });
      const view = { case: caseView(c), evidence_id: e.id };
      r.remember(() => view);
      return send(res, 201, view), true;
    }
    if ((m = new RegExp(`^${A}/cases/(${UUID})/(assign|transition|signal-reviews|escalate|propose-decision|approve-decision|return-decision|reopen)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(act === "approve-decision" || act === "return-decision" ? "risk.case.approve" : "risk.case.work")) return true;
      const c = cases.get(id);
      if (!c) return err(res, 404, "RISK_NOT_FOUND"), true;
      const set = (to) => ((c.status = to), (c.version += 1));
      switch (act) {
        case "assign":
          if (!open(c)) return err(res, 409, "RISK_CASE_STATE"), true;
          c.assigned_to = body.assignee_id ?? user.id;
          c.assignments.push({ assigned_to: c.assigned_to, assigned_by: user.id, reason: body.reason ?? "assigned", assigned_at: iso() });
          if (c.status === "OPEN" || c.status === "REOPENED") set("TRIAGE");
          break;
        case "transition":
          set(body.to);
          break;
        case "signal-reviews":
          if (!reasonOk(body.explanation)) return err(res, 422, "VALIDATION_FAILED"), true;
          c.reviews.push({ id: randomUUID(), signal_id: body.signal_id, case_id: c.id, classification: body.classification, explanation: body.explanation, reviewed_by: user.id, reviewed_at: iso() });
          break;
        case "escalate":
          c.escalated_at = iso();
          c.assigned_team = "COMPLIANCE";
          set("ESCALATED");
          break;
        case "propose-decision":
          Object.assign(c, { proposed_outcome: body.outcome, proposed_by: user.id, proposed_at: iso(), proposed_reason: body.reason });
          set("DECISION_PENDING");
          break;
        case "approve-decision":
        case "return-decision":
          if (c.status !== "DECISION_PENDING") return err(res, 409, "RISK_CASE_STATE"), true;
          if (c.proposed_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
          if (act === "approve-decision") {
            Object.assign(c, { decision_outcome: c.proposed_outcome, decided_by: user.id, decided_at: iso(), decision_reason: body.reason });
            set("RESOLVED");
          } else {
            Object.assign(c, { proposed_outcome: null, proposed_by: null, proposed_at: null, proposed_reason: null });
            set("UNDER_INVESTIGATION");
          }
          break;
        case "reopen":
          set("REOPENED");
          break;
      }
      history(c.id, `CASE_${act.toUpperCase().replaceAll("-", "_")}`, "STAFF", user.id);
      actions.push({ kind: `case.${act}`, id });
      return send(res, 200, caseView(c)), true;
    }
    if (path === `${A}/holds`) {
      if (!need("risk.hold.create")) return true;
      const r = idemReplay(req, res, "hold");
      if (r.done) return true;
      if ([...holds.values()].some((h) => holdOpen(h) && h.hold_type === body.hold_type && h.subject_id === body.subject_id)) return err(res, 409, "RISK_HOLD_EXISTS"), true;
      const h = newHold({ ...body, status: ["CAMPAIGN_HOLD", "DONATION_HOLD", "ACCOUNT_REVIEW_HOLD"].includes(body.hold_type) ? "REQUESTED" : "ACTIVE", created_by: user.id });
      r.remember(() => h);
      return send(res, 201, h), true;
    }
    if ((m = new RegExp(`^${A}/holds/(${UUID})/(activate|revoke|review|request-release|approve-release|reject-release)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(act === "activate" || act === "approve-release" || act === "reject-release" ? "risk.hold.release" : "risk.hold.create")) return true;
      const h = holds.get(id);
      if (!h) return err(res, 404, "RISK_NOT_FOUND"), true;
      const from = { activate: ["REQUESTED"], revoke: ["REQUESTED"], review: ["ACTIVE"], "request-release": ["ACTIVE", "UNDER_REVIEW"], "approve-release": ["RELEASE_PENDING"], "reject-release": ["RELEASE_PENDING"] }[act];
      if (!from.includes(h.status)) return err(res, 409, "RISK_HOLD_STATE"), true;
      if (act === "activate" && h.created_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if ((act === "approve-release" || act === "reject-release") && h.release_requested_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (["revoke", "request-release", "approve-release", "reject-release"].includes(act) && !reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
      if (act === "activate") Object.assign(h, { status: "ACTIVE", activated_at: iso(), activated_by: user.id });
      if (act === "revoke") Object.assign(h, { status: "REVOKED", revoked_by: user.id, revocation_reason: body.reason });
      if (act === "review") h.status = "UNDER_REVIEW";
      if (act === "request-release") Object.assign(h, { status: "RELEASE_PENDING", release_requested_by: user.id, release_reason: body.reason });
      if (act === "approve-release") Object.assign(h, { status: "RELEASED", released_by: user.id, released_at: iso() });
      if (act === "reject-release") Object.assign(h, { status: "ACTIVE", release_requested_by: null, release_reason: null });
      h.version += 1;
      actions.push({ kind: `hold.${act}`, id });
      return send(res, 200, h), true;
    }
    if (path === `${A}/screening`) {
      if (!need("risk.screening.request")) return true;
      const r = idemReplay(req, res, "screening");
      if (r.done) return true;
      const s = newScreening(body, user.id);
      r.remember(() => screeningView(s));
      return send(res, 201, screeningView(s)), true;
    }
    if ((m = new RegExp(`^${A}/screening/(${UUID})/(review|retry)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(act === "review" ? "risk.screening.review" : "risk.screening.request")) return true;
      const s = screenings.get(id);
      if (!s) return err(res, 404, "RISK_NOT_FOUND"), true;
      if (act === "review") {
        if (!["POTENTIAL_MATCH", "REVIEW_REQUIRED"].includes(s.status)) return err(res, 409, "RISK_SCREENING_STATE"), true;
        if (s.requested_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        Object.assign(s, { status: body.decision, reviewed_by: user.id, reviewed_at: iso(), review_reason: body.reason });
      } else {
        if (s.status !== "ERROR") return err(res, 409, "RISK_SCREENING_STATE"), true;
        s.status = "PENDING";
      }
      s.version += 1;
      actions.push({ kind: `screening.${act}`, id, decision: body.decision ?? null });
      return send(res, 200, screeningView(s)), true;
    }
    if (path === `${A}/reconciliation/run`) {
      if (!need("risk.case.work")) return true;
      return send(res, 200, { run_at: iso(), checks: [{ code: "MISSING_EVALUATION", status: "PASS", findings: 0, examples: [] }, { code: "ORPHANED_HOLD", status: "FINDINGS", findings: 1, examples: [randomUUID()] },
        { code: "EVIDENCE_HASH", status: "PASS", findings: 0, examples: [] }] }), true;
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  async function control(path, body, res) {
    if (path === "/api/v1/__mock/risk/seed") return send(res, 201, seed(body ?? {})), true;
    if (path === "/api/v1/__mock/risk/log") return send(res, 200, { actions }), true;
    return false;
  }
  return { handle, control };
}
