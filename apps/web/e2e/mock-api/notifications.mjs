// In-memory mock of the Stage 15 notifications API for the E2E suite (shapes as internal/notifications). TEST data only.
// Role → permission mapping as migration 20261018090000. Staff mutations need a fresh step-up. Only good enough for the UI
// tests. NOT a reference implementation.
import { randomUUID } from "node:crypto";

export const ROLE_PERMISSIONS = {
  ADMIN: ["communications.view", "communications.template.request", "communications.replay", "communications.suppression.manage"],
  SUPPORT: ["communications.view"],
  COMPLIANCE: ["communications.view", "communications.template.approve", "communications.consent.view"],
  SECURITY_ADMIN: ["communications.view", "communications.replay", "communications.suppression.manage"],
};
const STEP_UP = new Set(["communications.template.request", "communications.template.approve", "communications.replay", "communications.suppression.manage"]);
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const USER_CATS = ["SECURITY", "CAMPAIGN", "VERIFICATION", "PAYMENT", "WITHDRAWAL", "PAYOUT", "SUPPORT", "MARKETING"];
const POLICY = {
  SECURITY: { ch: ["IN_APP", "EMAIL", "SMS"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP", "EMAIL"] },
  CAMPAIGN: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  VERIFICATION: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  PAYMENT: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  WITHDRAWAL: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  PAYOUT: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  SUPPORT: { ch: ["IN_APP", "EMAIL"], on: ["IN_APP", "EMAIL"], mand: ["IN_APP"] },
  MARKETING: { ch: ["IN_APP", "EMAIL", "SMS", "WHATSAPP"], on: [], mand: [] },
};

export function createNotificationsMock({ send, err, stepUpTtlMs, permissionsOf }) {
  const inbox = new Map(); // userId → items
  const prefs = new Map(); // userId → Map key → bool
  const consents = new Map(); // userId → Map key → record
  const deadLetters = [];
  const templates = [];
  const versions = new Map(); // templateId → versions
  const suppressions = [];
  const delivery = (over = {}) => ({ id: randomUUID(), notification_id: randomUUID(), template_key: "CAMPAIGN_APPROVED", category: "CAMPAIGN",
    recipient_user_id: randomUUID(), test_data: false, channel: "EMAIL", status: "DELIVERED", provider_code: "email_simulator", destination_masked: "t*****@example.test",
    decision: "ROUTED", suppression_reason: null, attempt_count: 1, max_attempts: 6, next_attempt_at: null, provider_message_id: "sim_1", submitted_at: iso(60_000),
    accepted_at: iso(60_000), delivered_at: iso(50_000), read_at: null, failed_at: null, failure_code: null, conflicting_evidence: false, version: 3, created_at: iso(60_000), ...over });
  const deliveries = [delivery(), delivery({ status: "SUPPRESSED", suppression_reason: "CONSENT_MISSING", channel: "SMS", decision: "SUPPRESSED:CONSENT_MISSING",
    provider_code: null, attempt_count: 0, delivered_at: null, accepted_at: null }), delivery({ status: "FAILED_PERMANENT", failure_code: "PROVIDER_BOUNCED" })];
  const seedTemplate = (key, category, title, body) => {
    const t = { id: randomUUID(), key, category, sensitive: false, allowed_variables: [], origin: "STAGE_6_NOTICE" };
    templates.push(t);
    versions.set(t.id, [{ id: randomUUID(), version_no: 1, locale: "en", status: "ACTIVE", email_subject: title, email_text: body, sms_text: null, whatsapp_template_name: null,
      whatsapp_language: null, whatsapp_provider_status: "NOT_SUBMITTED", inapp_title: title, inapp_body: body, content_hash: "ab".repeat(32), approval_source: "MIGRATION_BASELINE",
      created_by: null, submitted_by: null, approved_by: null, decision_reason: null, activated_at: iso(86400_000), sms_segments: null, version: 1, created_at: iso(86400_000) }]);
  };
  seedTemplate("CAMPAIGN_APPROVED", "CAMPAIGN", "Your campaign was approved", "Your campaign was approved. Sign in to FundZim to publish it.");
  seedTemplate("CAMPAIGN_PAUSED", "CAMPAIGN", "Your campaign is paused", "Your campaign is paused. You can resume it from your dashboard.");

  const itemsFor = (user) => {
    if (!inbox.has(user.id)) {
      inbox.set(user.id, [
        { id: randomUUID(), category: "CAMPAIGN", title: "Your campaign was approved", body: "Your campaign was approved. Sign in to FundZim to publish it.", priority: "NORMAL",
          deep_link: "/dashboard", related_type: null, related_id: null, test_data: false, read_at: null, created_at: iso(3600_000) },
        { id: randomUUID(), category: "PAYMENT", title: "[TEST] Thank you: your donation is confirmed", body: "Your donation of USD 12.50 was confirmed by the payment provider.",
          priority: "NORMAL", deep_link: null, related_type: null, related_id: null, test_data: true, read_at: null, created_at: iso(7200_000) },
        { id: randomUUID(), category: "SECURITY", title: "Your FundZim password was changed", body: "The password for your FundZim account was just changed.",
          priority: "HIGH", deep_link: "/settings/security", related_type: null, related_id: null, test_data: false, read_at: iso(60_000), created_at: iso(86400_000) },
      ]);
    }
    return inbox.get(user.id);
  };
  const prefView = (user) => {
    const set = prefs.get(user.id) ?? new Map();
    return USER_CATS.flatMap((cat) => POLICY[cat].ch.map((ch) => {
      const k = `${cat}|${ch}`;
      const mand = POLICY[cat].mand.includes(ch);
      return { category: cat, channel: ch, enabled: mand || (set.has(k) ? set.get(k) : POLICY[cat].on.includes(ch)), mandatory: mand,
        consent_required: cat === "MARKETING" || ch === "SMS" || ch === "WHATSAPP", available: cat !== "MARKETING" };
    }));
  };
  const consentView = (user) => {
    const m = consents.get(user.id) ?? new Map();
    return ["EMAIL", "SMS", "WHATSAPP"].flatMap((ch) => ["SERVICE_MESSAGES", "MARKETING"].map((purpose) => {
      const r = m.get(`${ch}|${purpose}`);
      return { channel: ch, purpose, status: r?.status ?? "NOT_REQUESTED", policy_version: r ? "consent-v1-engineering-placeholder" : null,
        current_policy_version: "consent-v1-engineering-placeholder", granted_at: r?.granted_at ?? null, withdrawn_at: r?.withdrawn_at ?? null, expires_at: null,
        available: purpose !== "MARKETING", version: 1 };
    }));
  };

  async function control(path, body, res) {
    if (path === "/api/v1/__mock/notifications/dead-letter") {
      const d = delivery({ status: "FAILED_PERMANENT", failure_code: "PROVIDER_TEMPORARY_FAILURE", attempt_count: 6 });
      deliveries.push(d);
      const x = { id: randomUUID(), delivery_id: d.id, failure_category: "RETRIES_EXHAUSTED", attempt_count: 6, last_error: "PROVIDER_TEMPORARY_FAILURE",
        next_action: "OPERATOR_REVIEW", review_status: "OPEN", reviewed_by: null, reviewed_at: null, review_reason: null, created_at: iso(), delivery: d,
        blocked: body?.blocked ?? null };
      deadLetters.push(x);
      return send(res, 201, { id: x.id, delivery_id: d.id }), true;
    }
    if (path === "/api/v1/__mock/notifications/template") {
      // a template of the test's own (the mock is shared by parallel projects)
      const key = `E2E_PAUSED_${randomUUID().slice(0, 8).toUpperCase()}`;
      seedTemplate(key, "CAMPAIGN", "Your campaign is paused", "Your campaign is paused. You can resume it from your dashboard.");
      return send(res, 201, { key }), true;
    }
    return false;
  }

  async function handle({ res, method, path, url, user, session, body }) {
    const userPath = path.startsWith("/api/v1/notifications") || path.startsWith("/api/v1/communications/");
    const staffPath = path.startsWith("/api/v1/admin/communications");
    if (!userPath && !staffPath) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    let m;
    if (userPath) {
      const list = itemsFor(user);
      if (method === "GET" && path === "/api/v1/notifications") {
        let items = [...list];
        if (url.searchParams.get("unread") === "true") items = items.filter((i) => !i.read_at);
        if (url.searchParams.get("category")) items = items.filter((i) => i.category === url.searchParams.get("category"));
        return send(res, 200, { items, next_cursor: null }), true;
      }
      if (method === "GET" && path === "/api/v1/notifications/unread-count") return send(res, 200, { unread: list.filter((i) => !i.read_at).length }), true;
      if (method === "POST" && path === "/api/v1/notifications/read-all") {
        let n = 0;
        for (const i of list) {
          if (!i.read_at) {
            i.read_at = iso();
            n++;
          }
        }
        return send(res, 200, { marked_read: n }), true;
      }
      if ((m = /^\/api\/v1\/notifications\/([0-9a-f-]{36})\/read$/.exec(path)) && method === "POST") {
        const it = list.find((i) => i.id === m[1]);
        if (!it) return err(res, 404, "NOTIFICATION_NOT_FOUND"), true;
        it.read_at = it.read_at ?? iso();
        return send(res, 200, { read: true }), true;
      }
      if (user.account_kind !== "USER") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      if (path === "/api/v1/notifications/preferences" && method === "GET") return send(res, 200, { items: prefView(user), quiet_hours: "NOT_AVAILABLE", marketing_enabled: false }), true;
      if (path === "/api/v1/notifications/preferences" && method === "PUT") {
        const set = prefs.get(user.id) ?? new Map();
        for (const c of body?.changes ?? []) {
          const pol = POLICY[c.category];
          if (!pol || !pol.ch.includes(c.channel)) return err(res, 422, "VALIDATION_FAILED"), true;
          if (!c.enabled && pol.mand.includes(c.channel)) return err(res, 422, "NOTIFICATION_MANDATORY"), true;
          set.set(`${c.category}|${c.channel}`, c.enabled);
        }
        prefs.set(user.id, set);
        return send(res, 200, { items: prefView(user), quiet_hours: "NOT_AVAILABLE", marketing_enabled: false }), true;
      }
      if (path === "/api/v1/communications/consent" && method === "GET") return send(res, 200, { items: consentView(user), policy_version: "consent-v1-engineering-placeholder", notice: "placeholder" }), true;
      if (path === "/api/v1/communications/consent" && method === "POST") {
        if (body?.purpose === "MARKETING" && body?.granted) return err(res, 422, "MARKETING_NOT_AVAILABLE"), true;
        const mm = consents.get(user.id) ?? new Map();
        mm.set(`${body.channel}|${body.purpose}`, body.granted ? { status: "GRANTED", granted_at: iso() } : { status: "WITHDRAWN", withdrawn_at: iso() });
        consents.set(user.id, mm);
        return send(res, 200, consentView(user).find((c) => c.channel === body.channel && c.purpose === body.purpose)), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    // staff
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = permissionsOf(user);
    const fresh = !!session && Date.now() - session.stepUpAt <= stepUpTtlMs;
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && method !== "GET" && !fresh) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const A = "/api/v1/admin/communications";
    if (method === "GET" && path === `${A}/overview`) {
      if (!need("communications.view")) return true;
      return send(res, 200, { generated_at: iso(), environment: "TEST", live_messaging_enabled: false, marketing_enabled: false,
        deliveries_by_status: { DELIVERED: 1, SUPPRESSED: 1, FAILED_PERMANENT: 1 }, deliveries_by_channel_24h: { EMAIL: 2, SMS: 1 }, dead_letters_open: deadLetters.filter((d) => d.review_status === "OPEN").length,
        retry_backlog: 0, oldest_retry_age_seconds: null, queue_backlog: 0, oldest_queued_age_seconds: null, attempts_last_1h: 0, failed_attempts_last_1h: 0, error_rate_last_1h: null,
        latency_p50_ms_24h: 12, latency_p95_ms_24h: 40, consent_restricted_24h: 1, webhook_rejections_24h: 0, conflicting_evidence: 0,
        workers: [{ worker_id: "worker:mock", last_seen_at: iso(2000), status: "HEALTHY" }], providers: [], notice: "TEST environment. No production messaging provider is selected." }), true;
    }
    if (method === "GET" && path === `${A}/deliveries`) return need("communications.view") && send(res, 200, { items: deliveries, next_cursor: null }), true;
    if ((m = new RegExp(`^${A}/deliveries/([0-9a-f-]{36})$`).exec(path))) {
      if (!need("communications.view")) return true;
      const d = deliveries.find((x) => x.id === m[1]);
      if (!d) return err(res, 404, "NOTIFICATION_NOT_FOUND"), true;
      return send(res, 200, { delivery: d, template_version_no: 1, attempts: [{ attempt_no: 1, provider_code: "email_simulator", outcome: "ACCEPTED", error_code: null, provider_message_id: "sim_1",
        latency_ms: 12, started_at: iso(60_000), finished_at: iso(59_000) }], provider_evidence: [{ provider_event_id: randomUUID(), reported_status: "DELIVERED", occurred_at: iso(50_000),
        received_at: iso(50_000), applied: true, not_applied_reason: null, applied_at: iso(50_000) }] }), true;
    }
    if (method === "GET" && path === `${A}/dead-letters`) {
      if (!need("communications.view")) return true;
      const st = url.searchParams.get("status") ?? "OPEN";
      const view = (d) => {
        const c = { ...d };
        delete c.blocked;
        return c;
      };
      return send(res, 200, { items: deadLetters.filter((d) => st === "ALL" || d.review_status === st).map(view), next_cursor: null }), true;
    }
    if ((m = new RegExp(`^${A}/dead-letters/([0-9a-f-]{36})/(replay|discard)$`).exec(path)) && method === "POST") {
      if (!need("communications.replay")) return true;
      const d = deadLetters.find((x) => x.id === m[1]);
      if (!d || d.review_status !== "OPEN") return err(res, 409, "NOTIFICATION_STATE"), true;
      if (m[2] === "discard") {
        d.review_status = "DISCARDED";
        return send(res, 200, { discarded: true }), true;
      }
      if (d.blocked) return send(res, 200, { replayed: false, blocked_reason: d.blocked, delivery_status: "FAILED_PERMANENT" }), true;
      d.review_status = "REPLAYED";
      return send(res, 200, { replayed: true, delivery_status: "QUEUED" }), true;
    }
    if (method === "GET" && path === `${A}/providers`) {
      if (!need("communications.view")) return true;
      const pv = (code, ch, name, readiness, simulated) => ({ provider_code: code, channel: ch, display_name: name, environment: "TEST", readiness, simulated, capabilities: {},
        credential_ref: null, known_limitations: simulated ? "Sends nothing." : "No provider selected.", active_in_this_process: simulated, attempts_24h: simulated ? 3 : 0,
        failed_attempts_24h: 0, health: readiness === "NOT_SELECTED" ? "NOT_CONFIGURED" : "HEALTHY" });
      return send(res, 200, { items: [pv("email_simulator", "EMAIL", "E-mail simulator (TEST_ONLY)", "CONFIGURED_TEST", true),
        pv("email_production", "EMAIL", "Production e-mail provider", "NOT_SELECTED", false)], live_messaging_enabled: false, next_cursor: null }), true;
    }
    if (method === "GET" && path === `${A}/reports`) {
      if (!need("communications.view")) return true;
      return send(res, 200, { from: iso(30 * 86400_000), to: iso(), environment: "TEST", rows: [{ group: "channel", key: "EMAIL", count: 2 }, { group: "rate", key: "delivery_success", count: 1, rate: 0.5 }],
        latency_p50_ms: 12, latency_p95_ms: 40, limitations: ["TEST environment: simulators and development senders only"] }), true;
    }
    if (method === "GET" && path === `${A}/suppressions`) return need("communications.view") && send(res, 200, { items: suppressions.filter((s) => !s.lifted_at), next_cursor: null }), true;
    if (method === "POST" && path === `${A}/suppressions`) {
      if (!need("communications.suppression.manage")) return true;
      suppressions.push({ id: randomUUID(), channel: body.channel, user_id: body.user_id, reason: body.reason, source: "STAFF", created_at: iso(), lifted_at: null, lift_reason: null });
      return send(res, 201, { suppressed: true }), true;
    }
    if ((m = new RegExp(`^${A}/suppressions/([0-9a-f-]{36})/lift$`).exec(path)) && method === "POST") {
      if (!need("communications.suppression.manage")) return true;
      const s = suppressions.find((x) => x.id === m[1]);
      if (!s || s.lifted_at) return err(res, 409, "NOTIFICATION_STATE"), true;
      Object.assign(s, { lifted_at: iso(), lift_reason: body.reason });
      return send(res, 200, { lifted: true }), true;
    }
    if (method === "GET" && path === `${A}/consent`) {
      if (!need("communications.consent.view")) return true;
      return send(res, 200, { summary: [{ channel: "SMS", purpose: "SERVICE_MESSAGES", status: "GRANTED", count: 1 }], records: null, policy_version: "consent-v1-engineering-placeholder" }), true;
    }
    if (method === "GET" && path === `${A}/templates`) {
      if (!need("communications.view")) return true;
      return send(res, 200, { items: templates.map((t) => ({ ...t, active_version: versions.get(t.id).find((v) => v.status === "ACTIVE")?.version_no ?? null,
        pending_status: versions.get(t.id).find((v) => ["DRAFT", "PENDING_APPROVAL", "APPROVED"].includes(v.status))?.status ?? null })), next_cursor: null }), true;
    }
    if ((m = new RegExp(`^${A}/templates/([0-9a-f-]{36})$`).exec(path))) {
      if (!need("communications.view")) return true;
      const t = templates.find((x) => x.id === m[1]);
      if (!t) return err(res, 404, "NOTIFICATION_NOT_FOUND"), true;
      return send(res, 200, { ...t, active_version: versions.get(t.id).find((v) => v.status === "ACTIVE")?.version_no ?? null, versions: [...versions.get(t.id)].reverse() }), true;
    }
    if ((m = new RegExp(`^${A}/templates/([0-9a-f-]{36})/versions$`).exec(path)) && method === "POST") {
      if (!need("communications.template.request")) return true;
      const vs = versions.get(m[1]);
      if (!vs) return err(res, 404, "NOTIFICATION_NOT_FOUND"), true;
      if (vs.some((v) => ["DRAFT", "PENDING_APPROVAL", "APPROVED"].includes(v.status))) return err(res, 409, "NOTIFICATION_TEMPLATE_STATE"), true;
      if (/\{\{\s*(?!display_name\b)[a-z_]+\s*\}\}/.test(`${body.inapp_title} ${body.inapp_body}`)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "inapp_body", code: "INVALID" }] }), true;
      const v = { ...vs[0], id: randomUUID(), version_no: vs.length + 1, status: "DRAFT", inapp_title: body.inapp_title, inapp_body: body.inapp_body, email_subject: body.email_subject,
        email_text: body.email_text, sms_text: body.sms_text, approval_source: "MAKER_CHECKER", created_by: user.id, submitted_by: null, approved_by: null, version: 1, created_at: iso() };
      vs.push(v);
      return send(res, 201, v), true;
    }
    if ((m = new RegExp(`^${A}/template-versions/([0-9a-f-]{36})/([a-z-]+)$`).exec(path)) && method === "POST") {
      const all = [...versions.entries()];
      const hit = all.find(([, vs]) => vs.some((v) => v.id === m[1]));
      if (!hit) return err(res, 404, "NOTIFICATION_NOT_FOUND"), true;
      const vs = hit[1];
      const v = vs.find((x) => x.id === m[1]);
      const act = m[2];
      if (act === "preview") {
        if (!need("communications.view")) return true;
        return send(res, 200, { in_app: { title: v.inapp_title, body: v.inapp_body }, email: { subject: v.email_subject, text: v.email_text } }), true;
      }
      const perm = ["submit", "whatsapp-simulator"].includes(act) ? "communications.template.request" : "communications.template.approve";
      if (!need(perm)) return true;
      const maker = v.created_by === user.id || v.submitted_by === user.id;
      const edges = { submit: ["DRAFT", "PENDING_APPROVAL"], approve: ["PENDING_APPROVAL", "APPROVED"], reject: ["PENDING_APPROVAL", "RETIRED"], activate: ["APPROVED", "ACTIVE"], retire: [null, "RETIRED"] };
      const e = edges[act];
      if (!e || (e[0] && v.status !== e[0])) return err(res, 409, "NOTIFICATION_STATE"), true;
      if (act !== "submit" && maker) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (act === "activate") for (const o of vs) if (o.status === "ACTIVE") o.status = "RETIRED";
      if (act === "submit") v.submitted_by = user.id;
      if (act === "approve") v.approved_by = user.id;
      v.status = e[1];
      v.version += 1;
      return send(res, 200, v), true;
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  return { handle, control };
}
