// In-memory mock of the Stage 14 operations portal API for the E2E suite (shapes as internal/operations and the
// auth staff routes). TEST data only. Role → permission mapping as migration 20261017090100 for the operations
// permissions; every operations mutation needs a fresh step-up (their permissions require it). Only good enough for
// the UI tests. NOT a reference implementation.
import { randomUUID } from "node:crypto";

export const ROLE_PERMISSIONS = {
  ADMIN: ["ops.dashboard.view", "ops.exception.work", "ops.incident.manage", "ops.incident.view", "ops.sla.request"],
  BUSINESS_APPROVER: ["ops.dashboard.view", "ops.sla.approve"],
  COMPLIANCE: ["audit.export", "audit.read", "ops.dashboard.view", "ops.exception.work", "ops.incident.view", "ops.sla.request", "ops.support.view"],
  FINANCE: ["audit.read", "ops.dashboard.view", "ops.exception.work", "ops.incident.view", "ops.support.view"],
  KYC_REVIEWER: ["ops.dashboard.view", "ops.support.view"],
  REVIEWER: ["ops.dashboard.view", "ops.support.view"],
  SECURITY_ADMIN: ["access.review.certify", "access.review.run", "ops.dashboard.view", "ops.exception.work", "ops.incident.manage", "ops.incident.view",
    "security_audit.read", "session.revoke", "staff.invite", "staff.suspend", "staff.view"],
  SUPER_ADMIN: ["access.review.certify", "audit.export", "audit.read", "ops.dashboard.view", "ops.incident.view", "role.assign.approve", "role.assign.request",
    "security_audit.read", "staff.invite", "staff.view"],
  SUPPORT: ["ops.dashboard.view", "ops.incident.view", "ops.support.view", "ops.support.work", "user.view"],
};
const STEP_UP = new Set(["ops.support.work", "ops.exception.work", "ops.incident.manage", "access.review.certify", "access.review.run", "audit.export", "ops.sla.request",
  "ops.sla.approve", "session.revoke", "role.assign.approve"]);
const OTHER_STAFF = "0192a000-0000-7000-8000-00000000f102";
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const SUPPORT_EDGES = { OPEN: ["TRIAGE"], REOPENED: ["TRIAGE"], TRIAGE: ["IN_PROGRESS", "ESCALATED", "RESOLVED"], IN_PROGRESS: ["WAITING_FOR_CUSTOMER", "ESCALATED", "RESOLVED"],
  WAITING_FOR_CUSTOMER: ["IN_PROGRESS", "RESOLVED"], ESCALATED: ["IN_PROGRESS", "RESOLVED"], RESOLVED: ["CLOSED", "REOPENED"], CLOSED: ["REOPENED"] };
const INCIDENT_EDGES = { DETECTED: ["ACKNOWLEDGED"], ACKNOWLEDGED: ["INVESTIGATING"], INVESTIGATING: ["MITIGATING", "MONITORING", "RESOLVED"], MITIGATING: ["MONITORING", "RESOLVED"],
  MONITORING: ["INVESTIGATING", "RESOLVED"], RESOLVED: ["CLOSED"] };
const TRIAGE_EDGES = { OPEN: ["ACKNOWLEDGED"], REOPENED: ["ACKNOWLEDGED"], ACKNOWLEDGED: ["INVESTIGATING", "RESOLVED"], INVESTIGATING: ["WAITING_EXTERNAL", "RESOLVED"],
  WAITING_EXTERNAL: ["INVESTIGATING", "RESOLVED"], RESOLVED: ["CLOSED", "REOPENED"], CLOSED: ["REOPENED"] };

export function createOperationsMock({ send, err, stepUpTtlMs, staffUsers, permissionsOf }) {
  const supportCases = new Map();
  const supportEvents = [];
  const incidents = new Map();
  const incidentEvents = [];
  const triage = new Map(); // exception id → assignment
  const triageEvents = [];
  const slaTargets = new Map();
  const reviews = new Map();
  const roleRequests = new Map();
  const revokedStaff = new Set();
  const audit = Array.from({ length: 120 }, (_, i) => ({ id: randomUUID(), seq: 1000 + i, occurred_at: iso((120 - i) * 60_000), actor_type: "staff", actor_id: OTHER_STAFF,
    actor_role: "COMPLIANCE", action: i % 2 ? "risk.case.assigned" : "campaign.review.decided", target_type: "risk_case", target_id: randomUUID(), outcome: "success",
    request_id: randomUUID(), correlation_id: i % 10 === 0 ? "0192a000-0000-7000-8000-0000000c0001" : null, reason: null, justification: null, metadata: {}, break_glass: false,
    hash: "ab".repeat(32) })).reverse();
  const exceptions = [
    { id: "risk:screening_error:0192a000-0000-7000-8000-00000000e001", source_module: "risk", source_ref: "0192a000-0000-7000-8000-00000000e001", severity: "SEV2",
      category: "SCREENING_PROVIDER_ERROR", summary: "TEST screening ended in error (never treated as cleared)", created_at: iso(3 * 3600_000), domain_status: "ERROR",
      permission: "risk.operations.view", href: "/admin/risk/screening/0192a000-0000-7000-8000-00000000e001", domain_open: true },
    { id: "payments:unknown:0192a000-0000-7000-8000-00000000e002", source_module: "payments", source_ref: "0192a000-0000-7000-8000-00000000e002", severity: "SEV2",
      category: "PAYMENT_UNKNOWN", summary: "TEST payment outcome unknown (never failed by assumption)", created_at: iso(5 * 3600_000), domain_status: "UNKNOWN",
      permission: "payment.view", href: "/admin/payments/unknown", domain_open: true },
  ];
  const approvals = () => [
    { id: "risk:0192a000-0000-7000-8000-00000000a001", source_module: "risk", source_record_id: "0192a000-0000-7000-8000-00000000a001", action: "RULE_VERSION_APPROVAL",
      summary: "Risk rule version awaiting a checker", requested_by: OTHER_STAFF, makers: [OTHER_STAFF], requested_at: iso(26 * 3600_000), required_permission: "risk.rule.approve",
      status: "PENDING", risk_level: "MEDIUM", href: "/admin/risk/rules/0192a000-0000-7000-8000-00000000a0aa" },
    { id: "ledger:0192a000-0000-7000-8000-00000000a002", source_module: "ledger", source_record_id: "0192a000-0000-7000-8000-00000000a002", action: "LEDGER_ADJUSTMENT_APPROVAL",
      summary: "Ledger adjustment (USD, TEST ledger)", requested_by: OTHER_STAFF, makers: [OTHER_STAFF], requested_at: iso(2 * 3600_000), required_permission: "ledger.adjustment.approve",
      status: "PENDING", href: "/admin/finance/ledger" },
    ...[...roleRequests.values()].filter((r) => r.status === "PENDING").map((r) => ({ id: `identity:${r.id}`, source_module: "identity", source_record_id: r.id,
      action: `ROLE_${r.action}`, summary: `${r.action} ${r.role_code}`, requested_by: r.requested_by, makers: [r.requested_by, r.target_user_id], requested_at: r.created_at,
      required_permission: "role.assign.approve", status: "PENDING", href: `/admin/staff/${r.target_user_id}` })),
  ];
  const metricsFor = (p) => {
    const m = (module, key, label, value, nature, permission, source, href = null, status = null) => ({ key, label, module, value, nature, status, source, permission, href });
    const all = [
      m("campaigns", "campaigns.reviews.pending", "Campaign reviews pending", 3, "REAL", "campaign.review", "campaigns.campaign_reviews PENDING", "/admin/campaigns/review"),
      m("kyc", "kyc.reviews.pending", "KYC cases waiting for review", 4, "REAL", "kyc.case.review", "kyc.kyc_cases SUBMITTED/IN_REVIEW", "/admin/verification"),
      m("risk", "risk.cases.open", "Open risk cases", 2, "REAL", "risk.operations.view", "risk.risk_cases not RESOLVED/CLOSED", "/admin/risk/cases"),
      m("risk", "risk.screening.production", "Production screening", null, "BLOCKED", "risk.operations.view", "constant risk.ScreeningProductionEnabled = false", null, "BLOCKED"),
      m("payments", "payments.unknown", "TEST payments with an unknown outcome", 1, "TEST_ONLY", "payment.view", "app.payments UNKNOWN (TEST)", "/admin/payments/unknown"),
      m("payments", "payments.live_execution", "Live payment execution", null, "BLOCKED", "payment.view", "constant psp.LiveExecutionEnabled = false", null, "BLOCKED"),
      m("ledger", "ledger.integrity", "Ledger integrity (TEST ledger)", null, "TEST_ONLY", "ledger.view", "ledger.ledger_invariant_runs latest", "/admin/finance/ledger", "HEALTHY"),
      m("payouts", "payouts.awaiting_approval", "TEST withdrawals awaiting approval", 2, "TEST_ONLY", "payout.view", "app.payout_requests PENDING_APPROVAL", "/admin/finance/payouts/approvals"),
      m("operations", "support.cases.open", "Open support cases", [...supportCases.values()].filter((c) => !["RESOLVED", "CLOSED"].includes(c.status)).length, "REAL",
        "ops.support.view", "app.ops_support_cases not RESOLVED/CLOSED", "/admin/support"),
    ];
    return all.filter((x) => p.has(x.permission));
  };
  const viewerApprovals = (user, p) =>
    approvals().filter((a) => p.has("ops.dashboard.view") && (p.has(a.required_permission) || a.makers.includes(user.id))).map(({ makers, ...a }) => {
      const maker = makers.includes(user.id);
      const can = !maker && p.has(a.required_permission);
      return { ...a, can_decide: can, blocked_reason: can ? undefined : maker ? "MAKER" : "PERMISSION", age_seconds: Math.floor((Date.now() - Date.parse(a.requested_at)) / 1000) };
    });
  const viewerExceptions = (p) =>
    exceptions.filter((e) => p.has(e.permission)).map((e) => {
      const t = triage.get(e.id);
      return { ...e, triage_id: t?.id ?? null, triage_status: t?.status ?? "UNTRIAGED", assigned_to: t?.assigned_to ?? null, triage_resolution: t?.resolution ?? null,
        triage_updated_at: t?.updated_at ?? null, triage_version: t?.version ?? 0 };
    });
  const tasks = (user, p) => ({
    approvals_to_decide: viewerApprovals(user, p).filter((a) => a.can_decide).length,
    support_cases_assigned: [...supportCases.values()].filter((c) => c.assigned_to === user.id && !["RESOLVED", "CLOSED"].includes(c.status)).length,
    support_cases_overdue: 0,
    exceptions_assigned: [...triage.values()].filter((t) => t.assigned_to === user.id).length,
    incidents_owned: [...incidents.values()].filter((x) => x.owner_id === user.id && x.status !== "CLOSED").length,
    access_review_items_pending: [...reviews.values()].filter((r) => r.reviewer_id === user.id && r.status === "OPEN").reduce((n, r) => n + r.items.filter((i) => i.decision === "PENDING").length, 0),
    open_incidents: [...incidents.values()].filter((x) => !["RESOLVED", "CLOSED"].includes(x.status)).length,
  });
  const without = (o, key) => {
    const c = { ...o };
    delete c[key];
    return c;
  };
  const historyOf = (events, id) => events.filter((e) => e.id === id).map((e) => without(e, "id"));
  const len = (s) => (typeof s === "string" ? [...s.trim()].length : 0);
  const UUIDRE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  const staffMember = (u) => ({ id: u.id, display_name: u.display_name, status: "ACTIVE", personal_account_linked: false, suspended_at: null, created_at: u.created_at,
    roles: u.roles ?? [], mfa_confirmed: true, last_activity_at: iso(60_000), active_sessions: revokedStaff.has(u.id) ? 0 : 1 });

  async function control(path, body, res) {
    if (path === "/api/v1/__mock/ops/seed-exception") {
      const ref = randomUUID();
      const e = { id: `risk:screening_error:${ref}`, source_module: "risk", source_ref: ref, severity: "SEV2", category: "SCREENING_PROVIDER_ERROR",
        summary: `TEST screening ended in error ${ref.slice(0, 8)} (never treated as cleared)`, created_at: iso(3600_000), domain_status: "ERROR", permission: "risk.operations.view",
        href: `/admin/risk/screening/${ref}`, domain_open: true };
      exceptions.push(e);
      return send(res, 201, { id: e.id, token: ref.slice(0, 8) }), true;
    }
    if (path === "/api/v1/__mock/ops/role-request") {
      const r = { id: randomUUID(), action: body.action ?? "GRANT", target_user_id: body.target_user_id, role_code: body.role_code ?? "SUPPORT", requested_by: body.requested_by ?? OTHER_STAFF,
        justification: "Mock role request for the E2E suite", status: "PENDING", expires_at: iso(-3 * 86400_000), created_at: iso(3600_000) };
      roleRequests.set(r.id, r);
      return send(res, 201, r), true;
    }
    return false;
  }

  async function handle({ res, method, path, url, user, session, body }) {
    const ours = path.startsWith("/api/v1/admin/operations") || path.startsWith("/api/v1/admin/approvals") || path.startsWith("/api/v1/admin/reports") ||
      path.startsWith("/api/v1/admin/support") || path.startsWith("/api/v1/admin/staff") || path.startsWith("/api/v1/admin/access-reviews") ||
      path.startsWith("/api/v1/admin/audit") || path.startsWith("/api/v1/admin/role-assignment-requests");
    if (!ours) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = permissionsOf(user);
    const fresh = !!session && Date.now() - session.stepUpAt <= stepUpTtlMs;
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && method !== "GET" && !fresh) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const q = url.searchParams;
    const O = "/api/v1/admin/operations";
    let m;
    // ---- command center ----
    if (method === "GET" && path === `${O}/overview`) {
      if (!need("ops.dashboard.view")) return true;
      const alerts = [{ key: "OUTBOX_BACKLOG", severity: "SEV3", title: "Outbox events waiting longer than 5 minutes", source: "app.outbox_events", observed_at: iso(),
        incident_id: [...incidents.values()].find((x) => x.alert_key === "OUTBOX_BACKLOG" && x.status !== "CLOSED")?.id ?? null }];
      return send(res, 200, { generated_at: iso(), environment: "development", metrics: metricsFor(p),
        sources: ["identity", "campaigns", "kyc", "risk", "payments", "providers", "ledger", "payouts", "fees"].map((module) => ({ module, status: "HEALTHY" })).concat([{ module: "beneficiaries", status: "UNAVAILABLE", error: "timeout" }]),
        alerts, tasks: tasks(user, p), notice: "TEST environment: payments, ledger, fees and payouts are TEST or SIMULATED data — never business performance." }), true;
    }
    if (method === "GET" && path === `${O}/tasks`) return need("ops.dashboard.view") && send(res, 200, tasks(user, p)), true;
    if (method === "GET" && path === `${O}/health`) {
      if (!need("ops.dashboard.view")) return true;
      const c = (name, status, detail, source) => ({ name, status, detail, source, observed_at: iso() });
      return send(res, 200, { generated_at: iso(), overall: "DEGRADED", notice: "Measured now. Application error rates are NOT_AVAILABLE (no error tracking is configured).",
        components: [c("database", "HEALTHY", "ping ok", "SELECT 1 on the application pool"), c("outbox", "DEGRADED", "3 events older than 5 minutes", "app.outbox_events"),
          c("workers", "HEALTHY", "last job completed 20 s ago", "queue.river_job"), c("payments_live_execution", "BLOCKED", "disabled by constant and kill switch", "psp.LiveExecutionEnabled"),
          c("screening_provider", "NOT_CONFIGURED", "no screening provider is selected", "risk.ScreeningProductionEnabled"),
          c("application_errors", "UNKNOWN", "no error tracking is configured", "none")] }), true;
    }
    if (method === "GET" && path === `${O}/alerts`) {
      if (!need("ops.dashboard.view")) return true;
      const open = [...incidents.values()].find((x) => x.alert_key === "OUTBOX_BACKLOG" && x.status !== "CLOSED");
      return send(res, 200, { items: [{ key: "OUTBOX_BACKLOG", severity: "SEV3", title: "Outbox events waiting longer than 5 minutes", source: "app.outbox_events", observed_at: iso(), incident_id: open?.id ?? null }], next_cursor: null }), true;
    }
    if (method === "GET" && path === `${O}/search`) {
      if (!need("ops.dashboard.view")) return true;
      const s = (q.get("q") ?? "").trim();
      if ([...s].length < 3 || [...s].length > 100) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "q", code: "INVALID_LENGTH" }] }), true;
      const hits = [];
      for (const c of supportCases.values()) if (p.has("ops.support.view") && (c.id === s || String(c.case_number) === s)) hits.push({ kind: "SUPPORT_CASE", id: c.id, label: `Support case ${c.case_number}`, status: c.status, permission: "ops.support.view", href: `/admin/support/${c.id}` });
      if (p.has("risk.operations.view") && s === "0192a000-0000-7000-8000-00000000c0de") hits.push({ kind: "RISK_CASE", id: s, label: "Risk case 1001 (OTHER)", status: "OPEN", permission: "risk.operations.view", href: `/admin/risk/cases/${s}` });
      return send(res, 200, { query: s, hits, sources: [], notice: "Exact ids and record numbers only; no search by email, phone or document." }), true;
    }
    // ---- approvals ----
    if (method === "GET" && path === "/api/v1/admin/approvals") {
      if (!need("ops.dashboard.view")) return true;
      let items = viewerApprovals(user, p);
      if (q.get("module")) items = items.filter((a) => a.source_module === q.get("module"));
      if (q.get("decidable") === "true") items = items.filter((a) => a.can_decide);
      return send(res, 200, { items, sources: [], next_cursor: null }), true;
    }
    // ---- exceptions ----
    if (method === "GET" && path === `${O}/exceptions`) {
      if (!need("ops.dashboard.view")) return true;
      let items = viewerExceptions(p);
      if (q.get("module")) items = items.filter((e) => e.source_module === q.get("module"));
      if (q.get("assigned_to") === "me") items = items.filter((e) => e.assigned_to === user.id);
      return send(res, 200, { items, sources: [], next_cursor: null }), true;
    }
    if ((m = new RegExp(`^${O}/exceptions/([a-z_]+)/([^/]+)/(history|triage)$`).exec(path))) {
      const id = decodeURIComponent(m[2]);
      const e = exceptions.find((x) => x.id === id && x.source_module === m[1]);
      if (m[3] === "history") {
        if (!need("ops.dashboard.view")) return true;
        if (!p.has("ops.exception.work") || !e || !p.has(e.permission)) return err(res, 404, "OPS_NOT_FOUND"), true;
        return send(res, 200, { items: historyOf(triageEvents, id), next_cursor: null }), true;
      }
      if (method !== "POST" || !need("ops.exception.work")) return true;
      if (!e || !p.has(e.permission)) return err(res, 404, "OPS_EXCEPTION_NOT_OPEN"), true;
      let t = triage.get(id);
      if (!t) {
        t = { id: randomUUID(), status: "OPEN", assigned_to: null, resolution: null, version: 1, updated_at: iso() };
        triage.set(id, t);
        triageEvents.push({ id, event_type: "TRIAGE_OPENED", actor_id: user.id, to_status: "OPEN", occurred_at: iso() });
      } else if (body.version && body.version !== t.version) return err(res, 409, "OPS_RECORD_CHANGED"), true;
      let to = body.to ?? "";
      if (body.assignee_id) {
        const assignee = staffUsers().find((u) => u.id === body.assignee_id);
        if (!assignee || !permissionsOf(assignee).has("ops.exception.work")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "assignee_id", code: "NOT_ELIGIBLE" }] }), true;
        t.assigned_to = body.assignee_id;
        triageEvents.push({ id, event_type: "ASSIGNED", actor_id: user.id, note: `assigned to ${body.assignee_id}`, occurred_at: iso() });
        if (t.status === "OPEN" && !to) to = "ACKNOWLEDGED";
      }
      if (to) {
        if (!(TRIAGE_EDGES[t.status] ?? []).includes(to)) return err(res, 409, "OPS_STATE"), true;
        if (to === "RESOLVED" && e.domain_open) return err(res, 409, "OPS_EXCEPTION_DOMAIN_OPEN"), true;
        triageEvents.push({ id, event_type: `STATUS_${to}`, actor_id: user.id, from_status: t.status, to_status: to, note: body.note, occurred_at: iso() });
        t.status = to;
      } else if (body.note) triageEvents.push({ id, event_type: "NOTE_ADDED", actor_id: user.id, note: body.note, occurred_at: iso() });
      t.version += 1;
      t.updated_at = iso();
      return send(res, 200, viewerExceptions(p).find((x) => x.id === id)), true;
    }
    // ---- incidents ----
    if (path === `${O}/incidents` && method === "GET") {
      if (!need("ops.incident.view")) return true;
      const st = q.get("status");
      const items = [...incidents.values()].filter((x) => !st || x.status === st || (st === "OPEN_ANY" && !["RESOLVED", "CLOSED"].includes(x.status)));
      return send(res, 200, { items, next_cursor: null }), true;
    }
    if (path === `${O}/incidents` && method === "POST") {
      if (!need("ops.incident.manage")) return true;
      if (len(body.title) < 3 || !["SEV1", "SEV2", "SEV3", "SEV4"].includes(body.severity) || !/^[a-z][a-z_]{1,40}$/.test(body.affected_module ?? ""))
        return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "title", code: "INVALID" }] }), true;
      const existing = body.alert_key && [...incidents.values()].find((x) => x.alert_key === body.alert_key && x.status !== "CLOSED");
      if (existing) return send(res, 201, existing), true;
      const x = { id: randomUUID(), incident_number: incidents.size + 1, title: body.title, severity: body.severity, affected_module: body.affected_module, status: "DETECTED",
        owner_id: user.id, started_at: iso(), detected_by: body.alert_key ? "ALERT" : "STAFF", alert_key: body.alert_key ?? null, resolution: null, resolved_at: null,
        post_incident_notes: null, created_by: user.id, version: 1, created_at: iso(), updated_at: iso() };
      incidents.set(x.id, x);
      incidentEvents.push({ id: x.id, event_type: "DECLARED", actor_id: user.id, to_status: "DETECTED", note: body.note, occurred_at: iso() });
      return send(res, 201, x), true;
    }
    if ((m = new RegExp(`^${O}/incidents/([0-9a-f-]{36})(/update)?$`).exec(path))) {
      const x = incidents.get(m[1]);
      if (!m[2]) {
        if (!need("ops.incident.view")) return true;
        if (!x) return err(res, 404, "OPS_NOT_FOUND"), true;
        return send(res, 200, { incident: x, timeline: historyOf(incidentEvents, x.id) }), true;
      }
      if (method !== "POST" || !need("ops.incident.manage")) return true;
      if (!x) return err(res, 404, "OPS_NOT_FOUND"), true;
      if (body.to) {
        if (!(INCIDENT_EDGES[x.status] ?? []).includes(body.to)) return err(res, 409, "OPS_STATE"), true;
        if (body.to === "RESOLVED" && len(body.note) < 5) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "REQUIRED" }] }), true;
        incidentEvents.push({ id: x.id, event_type: `STATUS_${body.to}`, actor_id: user.id, from_status: x.status, to_status: body.to, note: body.note, occurred_at: iso() });
        if (body.to === "RESOLVED") x.resolution = body.note;
        x.status = body.to;
      } else if (body.note) {
        incidentEvents.push({ id: x.id, event_type: x.status === "CLOSED" ? "POST_INCIDENT_NOTE" : "NOTE_ADDED", actor_id: user.id, note: body.note, occurred_at: iso() });
        if (x.status === "CLOSED") x.post_incident_notes = [x.post_incident_notes, body.note].filter(Boolean).join("\n");
      }
      if (body.severity) x.severity = body.severity;
      if (body.owner_id) x.owner_id = body.owner_id;
      x.version += 1;
      return send(res, 200, x), true;
    }
    // ---- SLA targets ----
    if (path === `${O}/sla-targets` && method === "GET") {
      if (!need("ops.dashboard.view")) return true;
      return send(res, 200, { items: [...slaTargets.values()].filter((t) => ["ACTIVE", "PROPOSED"].includes(t.status)), metrics: [], notice: "Internal operational targets, approved by a second person. Never contractual or regulatory deadlines.", next_cursor: null }), true;
    }
    if (path === `${O}/sla-targets` && method === "POST") {
      if (!need("ops.sla.request")) return true;
      if ([...slaTargets.values()].some((t) => t.metric_key === body.metric_key && t.status === "PROPOSED")) return err(res, 409, "OPS_SLA_TARGET_PENDING"), true;
      const t = { id: randomUUID(), metric_key: body.metric_key, target_seconds: body.target_seconds, status: "PROPOSED", reason: body.reason, proposed_by: user.id, proposed_at: iso(),
        decided_by: null, decided_at: null, decision_reason: null };
      slaTargets.set(t.id, t);
      return send(res, 201, t), true;
    }
    if ((m = new RegExp(`^${O}/sla-targets/([0-9a-f-]{36})/(approve|reject)$`).exec(path)) && method === "POST") {
      if (!need("ops.sla.approve")) return true;
      const t = slaTargets.get(m[1]);
      if (!t || t.status !== "PROPOSED") return err(res, 409, "OPS_STATE"), true;
      if (t.proposed_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (m[2] === "approve") for (const o of slaTargets.values()) if (o.metric_key === t.metric_key && o.status === "ACTIVE") o.status = "SUPERSEDED";
      Object.assign(t, { status: m[2] === "approve" ? "ACTIVE" : "REJECTED", decided_by: user.id, decided_at: iso(), decision_reason: body.reason });
      return send(res, 200, t), true;
    }
    // ---- reports ----
    if (method === "GET" && path === "/api/v1/admin/reports") {
      if (!need("ops.dashboard.view")) return true;
      const from = q.get("from"), to = q.get("to");
      if (from && to && Date.parse(to) <= Date.parse(from)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "to", code: "BEFORE_FROM" }] }), true;
      const rep = (key, title, perm, rows, limitations) => ({ key, title, perm, generated_at: iso(), from, to, filters: {}, environment: "TEST", source: `${key} module snapshot`, freshness: "point in time",
        limitations, rows });
      const all = [
        rep("support_case_volumes", "Support case volumes", "ops.support.view", [{ group: "status", label: "OPEN", value: supportCases.size, unit: "count", nature: "REAL" }], []),
        rep("risk_investigation_backlog", "Risk investigation backlog", "risk.operations.view", [{ group: "priority", label: "P2", value: 2, unit: "count", nature: "REAL" }, { group: "age", label: "Oldest open case", value: 93_600, unit: "seconds", nature: "REAL" }], ["Counts only; a risk score is never proof of fraud"]),
        rep("financial_reconciliation", "Financial reconciliation status (TEST)", "ledger.view", [{ group: "reconciliation", label: "Ledger reconciliation open", value: 0, unit: "count", nature: "TEST_ONLY" }], ["settlement reconciliation is NOT_PERFORMED (no settlement evidence)"]),
      ];
      return send(res, 200, { items: all.filter((r) => p.has(r.perm)).map((r) => without(r, "perm")), next_cursor: null }), true;
    }
    // ---- support ----
    const SUP = "/api/v1/admin/support/cases";
    if (path === SUP && method === "GET") {
      if (!need("ops.support.view")) return true;
      const st = q.get("status");
      let items = [...supportCases.values()].reverse().filter((c) => !st || c.status === st || (st === "OPEN_ANY" && !["RESOLVED", "CLOSED"].includes(c.status)));
      if (q.get("assigned_to") === "me") items = items.filter((c) => c.assigned_to === user.id);
      return send(res, 200, { items, next_cursor: null }), true;
    }
    if (path === SUP && method === "POST") {
      if (!need("ops.support.work")) return true;
      if (len(body.subject) < 3 || len(body.subject) > 200) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "subject", code: "INVALID_LENGTH" }] }), true;
      const c = { id: randomUUID(), case_number: supportCases.size + 501, subject: body.subject.trim(), category: body.category, priority: body.priority ?? "P3", status: "OPEN",
        requester_user_id: body.requester_user_id ?? null, assigned_to: null, related_campaign_id: body.related_campaign_id ?? null, related_reference_type: body.related_reference_type ?? null,
        related_reference_id: body.related_reference_id ?? null, escalated_to: null, escalation_reference: null, resolution: null, resolved_at: null,
        due_at: [...slaTargets.values()].find((t) => t.metric_key === "SUPPORT_RESOLUTION" && t.status === "ACTIVE") ? iso(-3 * 86400_000) : null, created_by: user.id, version: 1,
        created_at: iso(), updated_at: iso() };
      supportCases.set(c.id, c);
      supportEvents.push({ id: c.id, event_type: "OPENED", actor_id: user.id, to_status: "OPEN", note: body.note, occurred_at: iso() });
      return send(res, 201, c), true;
    }
    if ((m = new RegExp(`^${SUP}/([0-9a-f-]{36})(?:/(assign|transition|escalate|notes))?$`).exec(path))) {
      const c = supportCases.get(m[1]);
      if (!m[2]) {
        if (!need("ops.support.view")) return true;
        if (!c) return err(res, 404, "OPS_NOT_FOUND"), true;
        return send(res, 200, { case: c, history: historyOf(supportEvents, c.id) }), true;
      }
      if (method !== "POST" || !need("ops.support.work")) return true;
      if (!c) return err(res, 404, "OPS_NOT_FOUND"), true;
      if (body.version && body.version !== c.version) return err(res, 409, "OPS_RECORD_CHANGED"), true;
      const ev = (event_type, extra = {}) => supportEvents.push({ id: c.id, event_type, actor_id: user.id, occurred_at: iso(), note: body.note, ...extra });
      if (m[2] === "assign") {
        const who = body.assignee_id ?? user.id;
        const assignee = staffUsers().find((u) => u.id === who);
        if (!assignee || !permissionsOf(assignee).has("ops.support.work")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "assignee_id", code: "NOT_ELIGIBLE" }] }), true;
        c.assigned_to = who;
        ev("ASSIGNED");
        if (c.status === "OPEN" || c.status === "REOPENED") {
          ev("STATUS_TRIAGE", { from_status: c.status, to_status: "TRIAGE" });
          c.status = "TRIAGE";
        }
      } else if (m[2] === "transition") {
        if (!(SUPPORT_EDGES[c.status] ?? []).includes(body.to) || body.to === "ESCALATED") return err(res, 409, "OPS_STATE"), true;
        if (["RESOLVED", "CLOSED"].includes(body.to) && len(body.note) < 5 && !c.resolution) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "REQUIRED" }] }), true;
        ev(`STATUS_${body.to}`, { from_status: c.status, to_status: body.to });
        if (body.to === "RESOLVED") c.resolution = body.note;
        if (body.to === "REOPENED") c.resolution = null;
        c.status = body.to;
      } else if (m[2] === "escalate") {
        if (!(SUPPORT_EDGES[c.status] ?? []).includes("ESCALATED")) return err(res, 409, "OPS_STATE"), true;
        ev("ESCALATED", { from_status: c.status, to_status: "ESCALATED" });
        Object.assign(c, { status: "ESCALATED", escalated_to: body.escalated_to, escalation_reference: body.escalation_reference ?? null });
      } else ev("NOTE_ADDED");
      c.version += 1;
      c.updated_at = iso();
      return send(res, 200, c), true;
    }
    // ---- staff ----
    if (method === "GET" && path === "/api/v1/admin/staff") {
      if (!need("staff.view")) return true;
      const s = (q.get("q") ?? "").toLowerCase();
      return send(res, 200, { items: staffUsers().filter((u) => !s || u.display_name.toLowerCase().includes(s) || u.id === s).map(staffMember), next_cursor: null }), true;
    }
    if (method === "GET" && path === "/api/v1/admin/staff/roles") {
      if (!need("staff.view")) return true;
      return send(res, 200, { items: Object.entries(ROLE_PERMISSIONS).map(([code, perms]) => ({ code, name: code.replace(/_/g, " ").toLowerCase(), permissions: perms,
        holders: staffUsers().filter((u) => (u.roles ?? []).includes(code)).length })), next_cursor: null }), true;
    }
    if ((m = /^\/api\/v1\/admin\/staff\/([0-9a-f-]{36})(\/sessions\/revoke)?$/.exec(path))) {
      const u = staffUsers().find((x) => x.id === m[1]);
      if (!m[2]) {
        if (!need("staff.view")) return true;
        if (!u) return err(res, 404, "STAFF_NOT_FOUND"), true;
        return send(res, 200, { member: staffMember(u), permissions: [...permissionsOf(u)].sort(), role_history: (u.roles ?? []).map((role) => ({ role, granted_by: OTHER_STAFF, approved_by: OTHER_STAFF,
          valid_from: iso(30 * 86400_000), expires_at: null, revoked_at: null, revoked_by: null, revoke_reason: null })),
          pending_role_requests: [...roleRequests.values()].filter((r) => r.target_user_id === u.id && r.status === "PENDING"),
          recent_security_events: [{ type: "login_succeeded", occurred_at: iso(60_000) }] }), true;
      }
      if (method !== "POST" || !need("session.revoke")) return true;
      if (!u) return err(res, 404, "STAFF_NOT_FOUND"), true;
      if (u.id === user.id) return err(res, 403, "SELF_ACTION_FORBIDDEN"), true;
      if (len(body.reason) < 5) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
      revokedStaff.add(u.id);
      return send(res, 200, { revoked: 1 }), true;
    }
    if ((m = /^\/api\/v1\/admin\/role-assignment-requests\/([0-9a-f-]{36})\/(approve|reject)$/.exec(path)) && method === "POST") {
      if (!need("role.assign.approve")) return true;
      const r = roleRequests.get(m[1]);
      if (!r || r.status !== "PENDING") return err(res, 409, "ROLE_REQUEST_NOT_PENDING"), true;
      if (r.requested_by === user.id || r.target_user_id === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      r.status = m[2] === "approve" ? "APPROVED" : "REJECTED";
      return send(res, 200, r), true;
    }
    // ---- access reviews ----
    const REV = "/api/v1/admin/access-reviews";
    const reviewView = (r) => ({ ...r, pending_items: r.items.filter((i) => i.decision === "PENDING").length });
    if (path === REV && method === "GET") return need("staff.view") && send(res, 200, { items: [...reviews.values()].map(reviewView).map((r) => without(r, "items")), next_cursor: null }), true;
    if (path === REV && method === "POST") {
      if (!need("access.review.run")) return true;
      if (!UUIDRE.test(body.reviewer_id ?? "")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reviewer_id", code: "INVALID_FORMAT" }] }), true;
      if (body.reviewer_id === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      const items = staffUsers().flatMap((u) => (u.roles ?? []).filter((r) => !body.scope_role || r === body.scope_role).map((role_code) => ({ id: randomUUID(), staff_user_id: u.id,
        role_code, decision: "PENDING", decided_by: null, decided_at: null, reason: null, revoke_request_id: null })));
      const r = { id: randomUUID(), title: body.title, scope_role: body.scope_role ?? null, status: "OPEN", created_by: user.id, reviewer_id: body.reviewer_id, due_at: null,
        completed_at: null, version: 1, created_at: iso(), items };
      reviews.set(r.id, r);
      return send(res, 201, reviewView(r)), true;
    }
    if ((m = new RegExp(`^${REV}/([0-9a-f-]{36})(?:/items/([0-9a-f-]{36})/decide|/(complete))?$`).exec(path))) {
      const r = reviews.get(m[1]);
      if (!m[2] && !m[3]) return need("staff.view") && (r ? send(res, 200, reviewView(r)) : err(res, 404, "OPS_NOT_FOUND")), true;
      if (method !== "POST") return false;
      if (m[3]) {
        if (!need("access.review.run")) return true;
        if (!r || r.items.some((i) => i.decision === "PENDING")) return err(res, 409, "OPS_STATE"), true;
        r.status = "COMPLETED";
        return send(res, 200, reviewView(r)), true;
      }
      if (!need("access.review.certify")) return true;
      const it = r?.items.find((i) => i.id === m[2]);
      if (!r || !it) return err(res, 404, "OPS_NOT_FOUND"), true;
      if (r.reviewer_id !== user.id) return err(res, 403, "OPS_NOT_REVIEWER"), true;
      if (it.staff_user_id === user.id) return err(res, 403, "OPS_CONFLICT_OF_INTEREST"), true;
      if (it.decision !== "PENDING") return err(res, 409, "OPS_STATE"), true;
      Object.assign(it, { decision: body.decision, decided_by: user.id, decided_at: iso(), reason: body.reason });
      if (body.decision === "REVOKE_RECOMMENDED") {
        const rr = { id: randomUUID(), action: "REVOKE", target_user_id: it.staff_user_id, role_code: it.role_code, requested_by: user.id, justification: body.reason, status: "PENDING",
          expires_at: iso(-7 * 86400_000), created_at: iso() };
        roleRequests.set(rr.id, rr);
        it.revoke_request_id = rr.id;
      }
      return send(res, 200, reviewView(r)), true;
    }
    // ---- audit ----
    if (method === "GET" && (path === "/api/v1/admin/audit/events" || path === "/api/v1/admin/audit/security-events")) {
      if (!need(path.endsWith("security-events") ? "security_audit.read" : "audit.read")) return true;
      const limit = Math.min(Number.parseInt(q.get("limit") ?? "50", 10) || 50, 200);
      const before = Number.parseInt(q.get("cursor") ?? "0", 10);
      let rows = audit.filter((e) => (!before || e.seq < before) && (!q.get("action") || e.action.startsWith(q.get("action"))) && (!q.get("correlation_id") || e.correlation_id === q.get("correlation_id")));
      const page = rows.slice(0, limit);
      return send(res, 200, { items: page, next_cursor: rows.length > limit ? String(page[page.length - 1].seq) : null }), true;
    }
    if (method === "POST" && path === "/api/v1/admin/audit/exports") {
      if (!need("audit.export")) return true;
      const from = Date.parse(body.from ?? ""), to = Date.parse(body.to ?? "");
      if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from || to - from > 31 * 86400_000) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "to", code: "OUT_OF_RANGE" }] }), true;
      const csv = "id,seq,occurred_at,actor_type,actor_id,action,target_type,target_id,outcome\n" + audit.slice(0, 5).map((e) => [e.id, e.seq, e.occurred_at, e.actor_type, e.actor_id, e.action, e.target_type, e.target_id, e.outcome].join(",")).join("\n");
      res.writeHead(200, { "Content-Type": body.format === "json" ? "application/json" : "text/csv; charset=utf-8", "Content-Disposition": `attachment; filename="fundzim-audit-mock.${body.format === "json" ? "json" : "csv"}"`, "Cache-Control": "no-store" });
      res.end(body.format === "json" ? JSON.stringify(audit.slice(0, 5)) : csv);
      return true;
    }
    return false;
  }

  return { handle, control };
}
