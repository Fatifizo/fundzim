// In-memory mock of the Stage 8 staff payment API (docs/stage-8/interface-contracts.md §4, §5, §10) for the E2E
// suite. Shapes follow §10 exactly (money as digit strings + currency, `{items, next_cursor}` lists). It enforces
// what the UI must cope with: 404 for non-staff, 403 without the permission (role → permission as in migration
// 20261012090100_payments.sql), step-up on the marked actions, Idempotency-Key on refund requests, maker ≠ checker
// on refund decisions, re-queue only for DEAD_LETTERED/UNRECOGNISED, and the Stage 8 rule that an approved refund
// is never executed (`execution: "DISABLED"`). There is no endpoint that sets a payment's status. NOT a reference.
import { randomUUID } from "node:crypto";

export const ROLE_PERMISSIONS = {
  FINANCE: ["payment.view", "payment.operate", "refund.approve"],
  COMPLIANCE: ["payment.view", "refund.request"],
  SUPPORT: ["payment.view", "refund.request"],
  ADMIN: ["payment.sandbox.control"],
};
const PAGE_SIZE = 25;
const SANDBOX_ENABLED = true;
const PROVIDER = { code: "sandbox", display_name: "FundZim Sandbox", simulated: true };
const PROVIDER_ID = "0192a000-0000-7000-8000-0000000b0001";
/** A staff member who is not any test user: the maker of seeded refund requests. */
const OTHER_STAFF = { id: "0192a000-0000-7000-8000-00000000f001", display_name: "Maker Staff" };

const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const hex64 = () => randomUUID().replaceAll("-", "").repeat(2);

export function createPaymentsMock({ send, err, stepUpTtlMs }) {
  const intents = new Map();
  const attempts = new Map(); // id -> AdminAttempt detail (without the joined lists)
  const calls = new Map(); // paymentId -> ProviderCall[]
  const transactions = new Map(); // paymentId -> transaction
  const references = new Map(); // paymentId -> references[]
  const sessionsByPayment = new Map();
  const inbox = new Map();
  const events = []; // {subject_id, ...Event}
  const recons = new Map();
  const refunds = new Map(); // id -> {..., requested_by: {id, display_name}}
  const rejections = [];
  const idempotency = new Map(); // `${userId}:${key}` -> refund id
  const sandbox = new Map(); // paymentId -> SandboxState
  const statusQueries = []; // {payment_id, reason}
  const sandboxWebhooks = []; // {payment_id, body}
  const providerActions = []; // Stage 9: {provider_id, action, reason}
  const providerState = { sandbox_status: "ENABLED", outage: false };

  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());

  function page(all, url) {
    const offset = /^o(\d{1,6})$/.exec(url.searchParams.get("cursor") ?? "")?.[1];
    const start = offset ? Number(offset) : 0;
    const items = all.slice(start, start + PAGE_SIZE);
    return { items, next_cursor: start + PAGE_SIZE < all.length ? `o${start + PAGE_SIZE}` : null };
  }

  function event(subjectId, subjectType, from, to, extra = {}) {
    events.push({
      subject_id: subjectId,
      id: randomUUID(),
      subject_type: subjectType,
      from_status: from,
      to_status: to,
      applied: true,
      not_applied_reason: null,
      transition_code: from ? `${from}->${to}` : `->${to}`,
      source: "SYNC",
      provider_event_id: null,
      raw_status: null,
      actor_type: "SYSTEM",
      reason: null,
      correlation_id: randomUUID(),
      occurred_at: iso(),
      recorded_at: iso(),
      ...extra,
    });
  }
  const eventsOf = (id) => events.filter((e) => e.subject_id === id).map((e) => omit(e, ["subject_id"]));

  const refundView = (r) => ({
    id: r.id,
    payment_id: r.payment_id,
    amount_minor: r.amount_minor,
    currency: r.currency,
    reason_code: r.reason_code,
    reason_text: r.reason_text,
    status: r.status,
    requested_by: r.requested_by,
    requested_at: r.requested_at,
    decided_by: r.decided_by,
    decided_at: r.decided_at,
    decision_reason: r.decision_reason,
    expires_at: r.expires_at,
    execution: "DISABLED",
  });
  const omit = (obj, keys) => Object.fromEntries(Object.entries(obj).filter(([k]) => !keys.includes(k)));
  const INBOX_DETAIL_ONLY = ["payment_id", "payload_redacted", "raw_body_sha256", "dispatched_to", "parked_subject_id", "parked_until_status", "park_deadline_at", "next_attempt_at", "headers_subset"];
  const inboxView = (w) => omit(w, INBOX_DETAIL_ONLY);
  const inboxDetail = (w) => omit(w, ["payment_id"]);
  const attemptDetail = (a) => ({
    ...a,
    provider_calls: calls.get(a.id) ?? [],
    transaction: transactions.get(a.id) ?? null,
    references: references.get(a.id) ?? [],
    session: sessionsByPayment.get(a.id) ?? null,
    webhooks: [...inbox.values()].filter((w) => w.payment_id === a.id).map(inboxView),
    events: eventsOf(a.id),
    reconciliation: [...recons.values()].filter((r) => r.payment_id === a.id),
    refund_requests: [...refunds.values()].filter((r) => r.payment_id === a.id).map(refundView),
  });
  const attemptView = (a) => omit(a, ["provider_payment_ref", "last_authoritative_source", "poll_attempts", "next_poll_at", "poll_horizon_at"]);

  function addIntent({ amount_minor, status = "OPEN", simulated = true }) {
    const id = randomUUID();
    const it = { id, campaign_id: randomUUID(), donor_user_id: randomUUID(), amount_minor, currency: "USD", status, is_anonymous: false, simulated, expires_at: iso(-3600_000), created_at: iso(60_000), updated_at: iso(30_000) };
    intents.set(id, it);
    event(id, "DONATION_INTENT", null, "OPEN", { source: "DONOR", actor_type: "DONOR" });
    return it;
  }

  function addAttempt(intent, { status, unknown_reason = null, needs_reconciliation = false, method = "ECOCASH" }) {
    const id = randomUUID();
    const a = {
      id,
      donation_intent_id: intent.id,
      campaign_id: intent.campaign_id,
      attempt_number: 1,
      method,
      method_category: "MOBILE_MONEY",
      status,
      amount_minor: intent.amount_minor,
      currency: intent.currency,
      provider: PROVIDER,
      simulated: true,
      needs_reconciliation,
      unknown_since: status === "UNKNOWN" ? iso(20 * 60_000) : null,
      unknown_reason: status === "UNKNOWN" ? unknown_reason : null,
      failure_code: null,
      created_at: iso(25 * 60_000),
      updated_at: iso(10 * 60_000),
      provider_payment_ref: `sbx_${id.slice(0, 8)}`,
      last_authoritative_source: status === "UNKNOWN" ? null : "WEBHOOK",
      poll_attempts: status === "UNKNOWN" ? 4 : 0,
      next_poll_at: status === "UNKNOWN" ? iso(-60_000) : null,
      poll_horizon_at: status === "UNKNOWN" ? iso(-23 * 3600_000) : null,
    };
    attempts.set(id, a);
    return a;
  }

  /** A fresh, independent set of records per call so parallel tests never share mutable state. */
  function seed() {
    // 1. A simulated payment in UNKNOWN (create call timed out), with a dead-lettered webhook and a reconciliation reference.
    const i1 = addIntent({ amount_minor: "2550" });
    const unknown = addAttempt(i1, { status: "UNKNOWN", unknown_reason: "TIMEOUT", needs_reconciliation: true });
    calls.set(unknown.id, [
      { id: randomUUID(), attempt_no: 1, operation: "CREATE", classification: "OUTCOME_UNKNOWN", error_class: "INDETERMINATE", unknown_reason: "TIMEOUT", http_status: null, provider_status_raw: null, provider_reference_returned: null, latency_ms: 30000, correlation_id: randomUUID(), started_at: iso(25 * 60_000), deadline_at: iso(24 * 60_000), completed_at: iso(24 * 60_000) },
      { id: randomUUID(), attempt_no: 2, operation: "GET_STATUS", classification: "ACCEPTED", error_class: null, unknown_reason: null, http_status: 200, provider_status_raw: "limbo", provider_reference_returned: unknown.provider_payment_ref, latency_ms: 142, correlation_id: randomUUID(), started_at: iso(15 * 60_000), deadline_at: iso(14 * 60_000), completed_at: iso(15 * 60_000) },
    ]);
    transactions.set(unknown.id, { provider_transaction_ref: `txn_${unknown.id.slice(0, 8)}`, raw_status: "limbo", raw_status_at: iso(15 * 60_000), mapped_status: "UNKNOWN", mapping_version: "sandbox-2026-10-01", last_source: "POLL", reported_amount_minor: "2550", reported_currency: "USD", payer_instrument_masked: "+263 77 *** **12", updated_at: iso(15 * 60_000) });
    references.set(unknown.id, [{ ref_kind: "PAYMENT", reference: unknown.provider_payment_ref, source: "POLL", recorded_at: iso(15 * 60_000) }]);
    sessionsByPayment.set(unknown.id, { id: randomUUID(), kind: "HANDSET_APPROVAL", status: "OPEN", redirect_url: null, expires_at: iso(-10 * 60_000), returned_at: null });
    event(i1.id, "DONATION_INTENT", "OPEN", "OPEN", { applied: false, not_applied_reason: "NOT_AN_EDGE", transition_code: null, source: "INTERNAL" });
    event(unknown.id, "PAYMENT_INTENT", null, "CREATED");
    event(unknown.id, "PAYMENT_INTENT", "CREATED", "UNKNOWN", { reason: "Create call timed out" });
    event(unknown.id, "PAYMENT_INTENT", "UNKNOWN", "PENDING", { applied: false, not_applied_reason: "LOWER_RANK", transition_code: null, source: "WEBHOOK", provider_event_id: `evt_${unknown.id.slice(0, 6)}`, raw_status: "pending", actor_type: "PROVIDER" });
    const deadLetter = {
      id: randomUUID(),
      payment_id: unknown.id,
      provider: PROVIDER,
      provider_event_id: `evt_dl_${unknown.id.slice(0, 6)}`,
      event_class: "PAYMENT",
      event_type_raw: "payment.status_changed",
      our_reference: unknown.id,
      provider_reference: unknown.provider_payment_ref,
      status: "DEAD_LETTERED",
      attempts: 8,
      last_error_code: "HANDLER_ERROR",
      simulated: true,
      signature_key_slot: "CURRENT",
      verification_method: "SIGNATURE",
      received_at: iso(18 * 60_000),
      processed_at: null,
      payload_redacted: { event: "payment.status_changed", status: "pending", payer: "[REDACTED]" },
      raw_body_sha256: hex64(),
      dispatched_to: "payments",
      parked_subject_id: null,
      parked_until_status: null,
      park_deadline_at: null,
      next_attempt_at: null,
      headers_subset: { "content-type": "application/json", "fundzim-sandbox-signature": "[REDACTED]" },
    };
    inbox.set(deadLetter.id, deadLetter);
    const recon = { id: randomUUID(), payment_id: unknown.id, reason: "UNKNOWN_HORIZON_EXCEEDED", status: "OPEN", expected_amount_minor: "2550", expected_currency: "USD", reported_amount_minor: null, reported_currency: null, provider_status_raw: "limbo", provider_reference: unknown.provider_payment_ref, evidence_at: iso(15 * 60_000), created_at: iso(12 * 60_000), resolved_at: null, resolution_note: null };
    recons.set(recon.id, recon);
    sandbox.set(unknown.id, { provider_payment_ref: unknown.provider_payment_ref, our_reference: unknown.id, scenario: "limbo", raw_status: "limbo", amount_minor: "2550", currency: "USD", method: "ECOCASH", created_at: iso(25 * 60_000), updated_at: iso(15 * 60_000), simulated: true });

    // 2. A succeeded simulated payment with a refund request waiting for approval (requested by another staff member).
    const i2 = addIntent({ amount_minor: "125050", status: "PAID" });
    const succeeded = addAttempt(i2, { status: "SUCCEEDED" });
    calls.set(succeeded.id, [{ id: randomUUID(), attempt_no: 1, operation: "CREATE", classification: "ACCEPTED", error_class: null, unknown_reason: null, http_status: 201, provider_status_raw: "pending", provider_reference_returned: succeeded.provider_payment_ref, latency_ms: 87, correlation_id: randomUUID(), started_at: iso(40 * 60_000), deadline_at: iso(39 * 60_000), completed_at: iso(40 * 60_000) }]);
    transactions.set(succeeded.id, { provider_transaction_ref: `txn_${succeeded.id.slice(0, 8)}`, raw_status: "paid", raw_status_at: iso(35 * 60_000), mapped_status: "SUCCEEDED", mapping_version: "sandbox-2026-10-01", last_source: "WEBHOOK", reported_amount_minor: "125050", reported_currency: "USD", payer_instrument_masked: null, updated_at: iso(35 * 60_000) });
    event(succeeded.id, "PAYMENT_INTENT", null, "CREATED");
    event(succeeded.id, "PAYMENT_INTENT", "CREATED", "PENDING");
    event(succeeded.id, "PAYMENT_INTENT", "PENDING", "SUCCEEDED", { source: "WEBHOOK", raw_status: "paid", actor_type: "PROVIDER", provider_event_id: `evt_ok_${succeeded.id.slice(0, 6)}` });
    event(succeeded.id, "PAYMENT_INTENT", "SUCCEEDED", "SUCCEEDED", { applied: false, not_applied_reason: "DUPLICATE", transition_code: null, source: "WEBHOOK", raw_status: "paid", actor_type: "PROVIDER", provider_event_id: `evt_ok_${succeeded.id.slice(0, 6)}` });
    const refund = { id: randomUUID(), payment_id: succeeded.id, amount_minor: "50000", currency: "USD", reason_code: "DONOR_REQUEST", reason_text: "Donor asked for part of the gift back.", status: "PENDING_APPROVAL", requested_by: OTHER_STAFF, requested_at: iso(5 * 60_000), decided_by: null, decided_at: null, decision_reason: null, expires_at: iso(-7 * 24 * 3600_000) };
    refunds.set(refund.id, refund);
    event(refund.id, "REFUND_REQUEST", null, "REQUESTED", { source: "STAFF", actor_type: "STAFF" });

    // 3. A rejected webhook request (no payload).
    rejections.unshift({ id: randomUUID(), provider_code: "sandbox", reason_code: "SIGNATURE_INVALID", body_bytes: 412, body_sha256: hex64(), received_at: iso(3 * 60_000) });

    return { unknown_payment_id: unknown.id, succeeded_payment_id: succeeded.id, dead_lettered_inbox_id: deadLetter.id, reconciliation_id: recon.id, refund_request_id: refund.id, intent_id: i1.id };
  }

  seed();
  rejections.push({ id: randomUUID(), provider_code: "unknownpsp", reason_code: "UNKNOWN_PROVIDER", body_bytes: 77, body_sha256: null, received_at: iso(2 * 3600_000) });

  const newestFirst = (a, b) => String(b.created_at ?? b.received_at).localeCompare(String(a.created_at ?? a.received_at));

  function overview() {
    const byStatus = (rows) => rows.reduce((acc, r) => ({ ...acc, [r.status]: (acc[r.status] ?? 0) + 1 }), {});
    const all = [...attempts.values()];
    return {
      attempts_by_status: byStatus(all),
      unknown_count: all.filter((a) => a.status === "UNKNOWN").length,
      needs_reconciliation_count: all.filter((a) => a.needs_reconciliation).length,
      inbox_by_status: byStatus([...inbox.values()]),
      rejections_24h: rejections.length,
      open_refund_requests: [...refunds.values()].filter((r) => ["REQUESTED", "PENDING_APPROVAL", "ON_HOLD"].includes(r.status)).length,
      providers: [{ ...PROVIDER, status: "ENABLED", circuit: "CLOSED" }],
      live_execution: { enabled: false, constant: false, kill_switch: false },
      sandbox_enabled: SANDBOX_ENABLED,
    };
  }

  // Stage 9: health is derived from observations; with a declared outage it is UNAVAILABLE.
  function health() {
    return providerState.outage
      ? { status: "UNAVAILABLE", reason: "OUTAGE_DECLARED", window_seconds: 900, calls: 3, errors: 0, timeouts: 0, webhooks_verified: 2, p95_latency_ms: 41, circuit: "CLOSED", outage_declared: true, credentials_usable: true, last_observed_at: iso(60_000) }
      : { status: "HEALTHY", reason: "RECENT_CALLS_SUCCEEDED", window_seconds: 900, calls: 3, errors: 0, timeouts: 0, webhooks_verified: 2, p95_latency_ms: 41, circuit: "CLOSED", outage_declared: false, credentials_usable: true, last_observed_at: iso(60_000) };
  }

  function providerRow() {
    return {
      ...PROVIDER,
      id: PROVIDER_ID,
      status: "ENABLED",
      sandbox_status: providerState.sandbox_status,
      selection_status: "PENDING",
      selection_adr: null,
      confirmation_mode: "WEBHOOK_SIGNED",
      circuit: "CLOSED",
      allowlisted: true,
      adapter_registered: true,
      usable: providerState.sandbox_status === "ENABLED" && !providerState.outage,
      contract: { basis: "SIMULATED", sources: ["docs/stage-8/provider-interface.md §6"] },
      health: health(),
    };
  }

  function providers() {
    return {
      execution_environment: "LOCAL_SIMULATOR",
      allowlist: ["sandbox"],
      items: [
        {
          ...providerRow(),
          accounts: [{ id: "0192a000-0000-7000-8000-0000000a0001", environment: "SANDBOX", label: "Local simulator", status: "ENABLED", api_credentials_secret_ref: "secretref://fundzim/sandbox/api", webhook_secret_ref: "secretref://fundzim/sandbox/webhook-current", webhook_secret_ref_previous: null, secrets_resolvable: true, credential_scope_ok: true, webhook_replay_window_seconds: 300 }],
          capabilities: [
            { id: "0192a000-0000-7000-8000-0000000c0001", method: "ECOCASH", method_category: "MOBILE_MONEY", currency: "USD", custody_model: "PSP_POOL", evidence_status: "VERIFIED", evidence_source: "Sandbox contract (internal/psp/sandbox)", pcr_ref: null, routable: true, capability_version: 1, flags: { refund_api: false, partial_refund: false, idempotent_create: true, supports_cancel: true, signed_webhooks: true, status_api: true, separate_capture: false, documented_expiry: true } },
            { id: "0192a000-0000-7000-8000-0000000c0002", method: "CARD", method_category: "CARD", currency: "USD", custody_model: "PSP_POOL", evidence_status: "VERIFIED", evidence_source: "Sandbox contract (internal/psp/sandbox)", pcr_ref: null, routable: true, capability_version: 1, flags: { refund_api: false, partial_refund: false, idempotent_create: true, supports_cancel: false, signed_webhooks: true, status_api: true, separate_capture: false, documented_expiry: true } },
          ],
        },
      ],
      live_execution: { enabled: false, constant: false, kill_switch: false },
      sandbox_enabled: SANDBOX_ENABLED,
    };
  }

  const UUID = "[0-9a-fA-F-]{36}";
  const re = (s) => new RegExp(`^/api/v1/admin/payments${s}$`);
  const ROUTES = {
    intent: re(`/intents/(${UUID})`),
    attempt: re(`/attempts/(${UUID})`),
    statusQuery: re(`/attempts/(${UUID})/status-query`),
    createRefund: re(`/attempts/(${UUID})/refund-requests`),
    webhook: re(`/webhooks/(${UUID})`),
    requeue: re(`/webhooks/(${UUID})/requeue`),
    await: re(`/reconciliation/(${UUID})/await-settlement`),
    refundAction: re(`/refund-requests/(${UUID})/(approve|reject|hold|resume|withdraw)`),
    sandboxState: re(`/sandbox/attempts/(${UUID})`),
    sandboxWebhook: re(`/sandbox/attempts/(${UUID})/webhooks`),
    provider: re(`/providers/(${UUID})`),
    providerAction: re(`/providers/(${UUID})/(enable-sandbox|disable|outage|outage/resolve)`),
    evidence: re(`/attempts/(${UUID})/evidence`),
  };
  const reasonOk = (v) => typeof v === "string" && [...v.trim()].length >= 3 && [...v.trim()].length <= 2000;

  /** Returns true when the request was handled. */
  async function handle({ req, res, method, path, url, user, session, body }) {
    if (!path.startsWith("/api/v1/admin/payments")) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (p.has(perm)) return true;
      err(res, 403, "PERMISSION_DENIED");
      return false;
    };
    const stepUp = () => {
      if (session && Date.now() - session.stepUpAt <= stepUpTtlMs) return true;
      err(res, 403, "STEP_UP_REQUIRED");
      return false;
    };
    const filter = (rows, keys) => rows.filter((r) => keys.every((k) => !url.searchParams.get(k) || String(r[k]) === url.searchParams.get(k)));
    let m;

    if (method === "GET") {
      if (path === "/api/v1/admin/payments/overview") return need("payment.view") && send(res, 200, overview()), true;
      if (path === "/api/v1/admin/payments/providers") return need("payment.view") && send(res, 200, providers()), true;
      if ((m = ROUTES.provider.exec(path))) {
        if (!need("payment.view")) return true;
        if (m[1] !== PROVIDER_ID) return err(res, 404, "PAYMENT_PROVIDER_NOT_FOUND"), true;
        const p = providers().items[0];
        return send(res, 200, { ...p, execution_environment: "LOCAL_SIMULATOR", recent_health_events: [{ id: randomUUID(), operation: "CREATE_PAYMENT", method: "ECOCASH", currency: "USD", event_kind: "TIMEOUT", error_class: "INDETERMINATE", latency_ms: 30000, detail: {}, staff_declared: false, observed_at: iso(120_000) }], error_categories_24h: { PROVIDER_TIMEOUT: 1, PAYMENT_DECLINED: 0 } }), true;
      }
      if (path === "/api/v1/admin/payments/health") {
        if (!need("payment.view")) return true;
        return send(res, 200, { execution_environment: "LOCAL_SIMULATOR", live_execution: { enabled: false, constant: false, kill_switch: false }, secret_manager: "HEALTHY", items: [{ id: PROVIDER_ID, provider: PROVIDER, usable: providerRow().usable, health: health() }] }), true;
      }
      if ((m = ROUTES.evidence.exec(path))) {
        if (!need("payment.view")) return true;
        const a = attempts.get(m[1]);
        if (!a) return err(res, 404, "PAYMENT_NOT_FOUND"), true;
        const first = randomUUID();
        return send(res, 200, { items: [
          { id: first, source: "SYNC", execution_environment: "LOCAL_SIMULATOR", provider_reference: "sbx_pay_1", provider_event_id: null, raw_status: "pending", mapped_status: "PENDING", mapped: true, mapping_version: "sandbox-map-v1", reported_amount_minor: null, reported_currency: null, provider_occurred_at: null, observed_at: iso(300_000), consistency: "FIRST", contradicts_evidence_id: null },
          { id: randomUUID(), source: "WEBHOOK", execution_environment: "LOCAL_SIMULATOR", provider_reference: "sbx_pay_1", provider_event_id: "sbx_evt_00000000000000000001", raw_status: "limbo", mapped_status: "UNKNOWN", mapped: false, mapping_version: "sandbox-map-v1", reported_amount_minor: a.amount_minor, reported_currency: a.currency, provider_occurred_at: iso(200_000), observed_at: iso(200_000), consistency: "CONTRADICTS", contradicts_evidence_id: first },
        ] }), true;
      }
      if (path === "/api/v1/admin/payments/intents") {
        if (!need("payment.view")) return true;
        return send(res, 200, page(filter([...intents.values()], ["status", "campaign_id"]).sort(newestFirst), url)), true;
      }
      if ((m = ROUTES.intent.exec(path))) {
        if (!need("payment.view")) return true;
        const it = intents.get(m[1]);
        if (!it) return err(res, 404, "DONATION_INTENT_NOT_FOUND"), true;
        const its = [...attempts.values()].filter((a) => a.donation_intent_id === it.id).map(attemptView);
        return send(res, 200, { ...it, attempts: its, events: eventsOf(it.id) }), true;
      }
      if (path === "/api/v1/admin/payments/attempts") {
        if (!need("payment.view")) return true;
        let rows = filter([...attempts.values()], ["status", "method"]);
        if (url.searchParams.get("needs_reconciliation") === "true") rows = rows.filter((a) => a.needs_reconciliation);
        return send(res, 200, page(rows.sort(newestFirst).map(attemptView), url)), true;
      }
      if (path === "/api/v1/admin/payments/unknown") {
        if (!need("payment.view")) return true;
        const rows = [...attempts.values()].filter((a) => a.status === "UNKNOWN").sort((a, b) => a.unknown_since.localeCompare(b.unknown_since));
        return send(res, 200, page(rows.map(attemptView), url)), true;
      }
      if ((m = ROUTES.attempt.exec(path))) {
        if (!need("payment.view")) return true;
        const a = attempts.get(m[1]);
        return (a ? send(res, 200, attemptDetail(a)) : err(res, 404, "PAYMENT_NOT_FOUND")), true;
      }
      if (path === "/api/v1/admin/payments/webhooks") {
        if (!need("payment.view")) return true;
        return send(res, 200, page(filter([...inbox.values()], ["status"]).sort(newestFirst).map(inboxView), url)), true;
      }
      if ((m = ROUTES.webhook.exec(path))) {
        if (!need("payment.view")) return true;
        const w = inbox.get(m[1]);
        return (w ? send(res, 200, inboxDetail(w)) : err(res, 404, "RESOURCE_NOT_FOUND")), true;
      }
      if (path === "/api/v1/admin/payments/webhook-rejections") return need("payment.view") && send(res, 200, page(rejections, url)), true;
      if (path === "/api/v1/admin/payments/reconciliation") {
        if (!need("payment.view")) return true;
        return send(res, 200, page(filter([...recons.values()], ["status"]).sort(newestFirst), url)), true;
      }
      if (path === "/api/v1/admin/payments/refund-requests") {
        if (!need("payment.view")) return true;
        const rows = filter([...refunds.values()], ["status"]).sort((a, b) => b.requested_at.localeCompare(a.requested_at));
        return send(res, 200, page(rows.map(refundView), url)), true;
      }
      if ((m = ROUTES.sandboxState.exec(path))) {
        if (!SANDBOX_ENABLED) return err(res, 404, "SANDBOX_DISABLED"), true;
        if (!need("payment.sandbox.control")) return true;
        const s = sandbox.get(m[1]);
        return (s ? send(res, 200, s) : err(res, 404, "PAYMENT_NOT_FOUND")), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }

    if (method === "POST") {
      if ((m = ROUTES.statusQuery.exec(path))) {
        if (!need("payment.operate") || !stepUp()) return true;
        if (!attempts.has(m[1])) return err(res, 404, "PAYMENT_NOT_FOUND"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
        statusQueries.push({ payment_id: m[1], reason: body.reason });
        return send(res, 202, { payment_id: m[1], scheduled: true }), true; // never changes the status
      }
      if ((m = ROUTES.requeue.exec(path))) {
        if (!need("payment.operate") || !stepUp()) return true;
        const w = inbox.get(m[1]);
        if (!w) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
        if (!["DEAD_LETTERED", "UNRECOGNISED"].includes(w.status)) return err(res, 409, "INBOX_EVENT_NOT_REQUEUEABLE"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
        w.status = "RECEIVED";
        w.attempts = 0;
        w.last_error_code = null;
        return send(res, 200, inboxDetail(w)), true;
      }
      if ((m = ROUTES.await.exec(path))) {
        if (!need("payment.operate") || !stepUp()) return true;
        const r = recons.get(m[1]);
        if (!r) return err(res, 404, "RECONCILIATION_REFERENCE_NOT_FOUND"), true;
        if (r.status !== "OPEN") return err(res, 409, "INVALID_STATUS"), true;
        if (!reasonOk(body.note)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
        r.status = "AWAITING_SETTLEMENT_DATA";
        return send(res, 200, r), true;
      }
      if ((m = ROUTES.createRefund.exec(path))) {
        if (!need("refund.request")) return true;
        const key = req.headers["idempotency-key"];
        if (typeof key !== "string" || !/^[0-9a-f-]{36}$/i.test(key)) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), true;
        const prior = idempotency.get(`${user.id}:${key}`);
        if (prior) return send(res, 201, refundView(refunds.get(prior))), true;
        const a = attempts.get(m[1]);
        if (!a) return err(res, 404, "PAYMENT_NOT_FOUND"), true;
        if (!["SUCCEEDED", "PARTIALLY_REFUNDED"].includes(a.status)) return err(res, 409, "REFUND_NOT_ALLOWED"), true;
        if (typeof body.amount_minor !== "string" || !/^[1-9]\d{0,18}$/.test(body.amount_minor)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "amount_minor", code: "INVALID_FORMAT" }] }), true;
        const committed = [...refunds.values()].filter((r) => r.payment_id === a.id && r.status === "APPROVED").reduce((s, r) => s + BigInt(r.amount_minor), 0n);
        if (BigInt(body.amount_minor) + committed > BigInt(a.amount_minor)) return err(res, 422, "REFUND_EXCEEDS_REFUNDABLE"), true;
        if (!reasonOk(body.reason_text) || typeof body.reason_code !== "string") return err(res, 422, "VALIDATION_FAILED"), true;
        const r = { id: randomUUID(), payment_id: a.id, amount_minor: body.amount_minor, currency: a.currency, reason_code: body.reason_code, reason_text: body.reason_text.trim(), status: "PENDING_APPROVAL", requested_by: { id: user.id, display_name: user.display_name }, requested_at: iso(), decided_by: null, decided_at: null, decision_reason: null, expires_at: iso(-7 * 24 * 3600_000) };
        refunds.set(r.id, r);
        idempotency.set(`${user.id}:${key}`, r.id);
        return send(res, 201, refundView(r)), true;
      }
      if ((m = ROUTES.refundAction.exec(path))) {
        const [, id, action] = m;
        const r = refunds.get(id);
        if (action === "withdraw") {
          if (!need("refund.request")) return true;
          if (!r) return err(res, 404, "REFUND_REQUEST_NOT_FOUND"), true;
          if (r.requested_by.id !== user.id) return err(res, 403, "PERMISSION_DENIED"), true;
          if (!["REQUESTED", "PENDING_APPROVAL"].includes(r.status)) return err(res, 409, "INVALID_STATUS"), true;
        } else {
          if (!need("refund.approve") || !stepUp()) return true;
          if (!r) return err(res, 404, "REFUND_REQUEST_NOT_FOUND"), true;
          if (r.requested_by.id === user.id) return err(res, 403, "REFUND_SELF_APPROVAL_FORBIDDEN"), true;
          const from = { approve: ["PENDING_APPROVAL"], reject: ["PENDING_APPROVAL", "ON_HOLD"], hold: ["PENDING_APPROVAL"], resume: ["ON_HOLD"] }[action];
          if (!from.includes(r.status)) return err(res, 409, "INVALID_STATUS"), true;
        }
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
        const to = { approve: "APPROVED", reject: "REJECTED", hold: "ON_HOLD", resume: "PENDING_APPROVAL", withdraw: "WITHDRAWN" }[action];
        event(r.id, "REFUND_REQUEST", r.status, to, { source: "STAFF", actor_type: "STAFF", reason: body.reason });
        r.status = to;
        if (["approve", "reject"].includes(action)) {
          r.decided_by = { id: user.id, display_name: user.display_name };
          r.decided_at = iso();
          r.decision_reason = body.reason.trim();
        }
        return send(res, 200, refundView(r)), true; // approval never executes a refund (no EXECUTING edge)
      }
      if ((m = ROUTES.providerAction.exec(path))) {
        if (!need("payment.operate") || !stepUp()) return true;
        if (m[1] !== PROVIDER_ID) return err(res, 404, "PAYMENT_PROVIDER_NOT_FOUND"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
        const action = m[2];
        if (action === "outage" && providerState.outage) return err(res, 409, "PROVIDER_OUTAGE_STATE"), true;
        if (action === "outage/resolve" && !providerState.outage) return err(res, 409, "PROVIDER_OUTAGE_STATE"), true;
        if (action === "outage") providerState.outage = true;
        if (action === "outage/resolve") providerState.outage = false;
        if (action === "disable") providerState.sandbox_status = "DISABLED";
        if (action === "enable-sandbox") providerState.sandbox_status = "ENABLED";
        providerActions.push({ provider_id: m[1], action, reason: body.reason });
        return send(res, 200, { ...providers().items[0], execution_environment: "LOCAL_SIMULATOR", recent_health_events: [], error_categories_24h: {} }), true;
      }
      if ((m = ROUTES.sandboxWebhook.exec(path))) {
        if (!SANDBOX_ENABLED) return err(res, 404, "SANDBOX_DISABLED"), true;
        if (!need("payment.sandbox.control")) return true;
        if (!sandbox.has(m[1])) return err(res, 404, "PAYMENT_NOT_FOUND"), true;
        if (!["pending", "action_required", "paid", "failed", "expired", "cancelled", "limbo"].includes(body.status)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "status", code: "INVALID_VALUE" }] }), true;
        sandboxWebhooks.push({ payment_id: m[1], body });
        return send(res, 202, { scheduled: body.copies ?? 1 }), true;
      }
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  /** Test-only control endpoints. */
  async function control(path, _body, res) {
    if (path === "/api/v1/__mock/payments/seed") return send(res, 201, seed()), true;
    if (path === "/api/v1/__mock/payments/log") return send(res, 200, { status_queries: statusQueries, sandbox_webhooks: sandboxWebhooks, provider_actions: providerActions }), true;
    return false;
  }

  return { handle, control };
}
