// In-memory mock of the Stage 11 payouts API for the E2E suite (shapes as internal/payouts/views.go). TEST payouts
// only: every payout is test_payout=true, funds are TEST_ONLY and production availability is BLOCKED. FINANCE has
// payout.view/approve/reject/operate (step-up on actions); COMPLIANCE has payout.view; maker ≠ checker, one decision
// per approver per round, DUAL needs two distinct approvers. Owners need a fresh step-up to request or cancel and see
// only coarse reason categories. Nothing here moves money or marks a payout paid. NOT a reference implementation.
import { randomUUID } from "node:crypto";

export const ROLE_PERMISSIONS = {
  FINANCE: ["payout.view", "payout.approve", "payout.reject", "payout.operate"],
  COMPLIANCE: ["payout.view"],
};
const STEP_UP = new Set(["payout.approve", "payout.reject", "payout.operate"]);
const LABELS = { PAYOUT_REQUESTED: "Requested", PENDING_REVIEW: "Under review", APPROVED: "Approved", SUBMITTED: "Processing", PROCESSING: "Processing", UNKNOWN: "Processing — we're confirming with the provider", COMPLETED: "Paid (test)", FAILED: "Failed — funds returned to your balance", REJECTED: "Rejected", CANCELLED: "Cancelled", REVERSED: "Returned — please check your payout details" };
const ACTIVE = new Set(["PAYOUT_REQUESTED", "PENDING_REVIEW", "APPROVED", "SUBMITTED", "PROCESSING", "UNKNOWN"]);
const UUID = "[0-9a-f-]{36}";
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const checks = (fail = []) => Array.from({ length: 22 }, (_, i) => {
  const id = `EC-${String(i + 1).padStart(2, "0")}`;
  return { check_id: id, result: fail.includes(id) ? "FAIL" : id === "EC-14" ? "REVIEW" : "PASS", detail_code: fail.includes(id) ? "INSUFFICIENT_AVAILABLE" : id === "EC-14" ? "LIMIT_UNCONFIGURED" : "OK" };
});

export function createPayoutsMock({ send, err, stepUpTtlMs, users, getDestination }) {
  const campaigns = new Map(); // id -> { id, ownerId, settled }
  const payouts = new Map();
  const exceptions = new Map();
  const recon = new Map();
  const actions = [];
  const idem = new Map();

  const reserved = (c) => [...payouts.values()].filter((p) => p.campaign_id === c.id && ACTIVE.has(p.status)).reduce((s, p) => s + BigInt(p.amount_minor), 0n);
  const availability = (c) => {
    const r = reserved(c);
    const avail = BigInt(c.settled) - r;
    return { campaign_id: c.id, currency: "USD", environment: "TEST", funds_label: "TEST_ONLY", settled_released_minor: c.settled, refund_exposure_minor: "0", reserved_minor: String(r), available_minor: String(avail > 0n ? avail : 0n), payouts_enabled: true, production_status: "BLOCKED", blockers: [] };
  };
  const ownerView = (p) => ({
    id: p.id, campaign_id: p.campaign_id, currency: p.currency, amount_minor: p.amount_minor, purpose: p.purpose, status: p.status, status_label: LABELS[p.status],
    destination_masked: p.destination_masked, destination_rail: p.destination_rail, reason_category: p.status === "CANCELLED" && p.cancelledByOwner ? "OWNER_CANCELLED" : null,
    test_payout: true, funds_label: "TEST_ONLY", requested_at: p.requested_at, submitted_at: null, completed_at: null,
    can_cancel: p.status === "PENDING_REVIEW" || p.status === "APPROVED", version: p.version,
    // Stage 12: a synthetic 1 % organiser-borne payout fee estimate (the mock's own figure, not a provider rate)
    fee_estimate_minor: String(BigInt(p.amount_minor) / 100n), fee_basis: "ESTIMATED", fee_responsibility: "ORGANISER_COVERED",
    beneficiary_receives_minor: String(BigInt(p.amount_minor) - BigInt(p.amount_minor) / 100n),
  });
  const staffView = (p) => {
    const rest = { ...p };
    for (const k of ["approvals", "events", "cancelledByOwner", "ownerId", "decision", "reserve_tx", "release_tx", "reservation_id"]) delete rest[k];
    return { ...rest, reservation: { id: p.reservation_id, payout_request_id: p.id, campaign_id: p.campaign_id, currency: p.currency, amount_minor: p.amount_minor, account_scope: "TEST", status: ACTIVE.has(p.status) ? "ACTIVE" : "RELEASED", ledger_reserve_transaction_id: p.reserve_tx, ledger_submit_transaction_id: null, ledger_release_transaction_id: ACTIVE.has(p.status) ? null : p.release_tx, ledger_consume_transaction_id: null, expires_at: iso(-7 * 86400_000), created_at: p.requested_at } };
  };
  const event = (p, type, actor) => {
    p.events.push({ id: randomUUID(), event_type: type, from_status: p.events.at(-1)?.to_status ?? null, to_status: p.status, actor, occurred_at: iso() });
    p.version += 1;
  };
  function newPayout(c, { amount, requester, destination, purpose }) {
    const p = {
      id: randomUUID(), campaign_id: c.id, ownerId: c.ownerId, owner_user_id: c.ownerId, owner_organisation_id: null, beneficiary_id: null, currency: "USD", amount_minor: amount, purpose: purpose ?? null,
      requested_by: requester, requested_at: iso(), status: "PENDING_REVIEW", approval_policy: "DUAL", approval_round: 1, execution_environment: "LOCAL_SIMULATOR", test_payout: true, ledger_environment: "TEST",
      destination_id: destination.id, destination_version: destination.version ?? 1, destination_rail: destination.rail, destination_masked: destination.masked, destination_category: "MOBILE_MONEY_WALLET",
      provider_payout_ref: null, unknown_since: null, unknown_reason: null, needs_reconciliation: false, submitted_at: null, completed_at: null, failure_code: null, decision_reason_code: null, reversed_amount_minor: null,
      created_at: iso(), version: 1, reservation_id: randomUUID(), reserve_tx: randomUUID(), release_tx: randomUUID(), approvals: [], events: [], decision: { id: randomUUID(), phase: "REQUEST", outcome: "REVIEW", approval_tier: "DUAL", evaluated_at: iso(), record_hash: "9f2c".repeat(16), results: checks() },
    };
    p.events.push({ id: randomUUID(), event_type: "APPROVAL_REQUESTED", from_status: "PAYOUT_REQUESTED", to_status: "PENDING_REVIEW", actor: requester, occurred_at: iso() });
    payouts.set(p.id, p);
    return p;
  }
  const detail = (p) => ({
    payout: staffView(p), approvals: p.approvals, attempts: [], events: p.events, eligibility_decisions: [p.decision],
    exceptions: [...exceptions.values()].filter((e) => e.payout_request_id === p.id), ledger_journals: [{ idempotency_key: `payout:${p.id}:reserve`, transaction_id: p.reserve_tx }],
  });

  function seed(body) {
    const owner = body.owner_email ? users.get(body.owner_email) : null;
    const c = { id: randomUUID(), ownerId: owner?.id ?? randomUUID(), settled: body.settled_minor ?? "50000" };
    campaigns.set(c.id, c);
    const out = { campaign_id: c.id };
    if (body.with_payout !== false) {
      const p = newPayout(c, { amount: "12500", requester: c.ownerId, destination: { id: randomUUID(), rail: "ECOCASH", masked: "07*****567" }, purpose: "TEST_ONLY fixture" });
      const e = { id: randomUUID(), payout_request_id: p.id, campaign_id: c.id, exception_type: "STATUS_MISMATCH", severity: "SEV2", source: "RECONCILIATION", status: "OPEN", owner_id: null, resolution_code: null, detected_at: iso(3_600_000), resolved_at: null, age_seconds: 3600, evidence: { fixture: "TEST_ONLY" } };
      exceptions.set(e.id, e);
      const r = { id: randomUUID(), payout_request_id: p.id, mismatch_type: "PROVIDER_STATUS_MISSING", expected_amount_minor: "12500", actual_amount_minor: null, currency: "USD", provider_reference: null, detected_at: iso(3_600_000), detected_by_run: randomUUID() };
      recon.set(r.id, r);
      Object.assign(out, { payout_id: p.id, exception_id: e.id });
    }
    return out;
  }

  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const fresh = (session) => !!session && Date.now() - session.stepUpAt <= stepUpTtlMs;
  const textOk = (v, min = 5) => typeof v === "string" && [...v.trim()].length >= min && [...v.trim()].length <= 2000;

  function handleOwner({ req, res, method, path, user, session, body }) {
    let m;
    if (user.account_kind === "STAFF") return err(res, 404, "RESOURCE_NOT_FOUND"), true;
    const mine = (p) => p && p.ownerId === user.id;
    if (method === "GET" && path === "/api/v1/payout-requests") return send(res, 200, { items: [...payouts.values()].filter(mine).reverse().map(ownerView), next_cursor: null }), true;
    if ((m = new RegExp(`^/api/v1/payout-requests/(${UUID})(/timeline|/cancel)?$`).exec(path))) {
      const p = payouts.get(m[1]);
      if (!mine(p)) return err(res, 404, "PAYOUT_REQUEST_NOT_FOUND"), true;
      if (method === "GET" && !m[2]) return send(res, 200, ownerView(p)), true;
      if (method === "GET" && m[2] === "/timeline") return send(res, 200, { items: p.events.map((e) => ({ at: e.occurred_at, status: e.to_status, label: LABELS[e.to_status] })), next_cursor: null }), true;
      if (method === "POST" && m[2] === "/cancel") {
        if (!fresh(session)) return err(res, 403, "STEP_UP_REQUIRED"), true;
        if (p.status !== "PENDING_REVIEW" && p.status !== "APPROVED") return err(res, 409, "PAYOUT_STATE"), true;
        p.status = "CANCELLED";
        p.cancelledByOwner = true;
        event(p, "CANCELLED_BY_OWNER", user.id);
        actions.push({ kind: "owner.cancel", id: p.id });
        return send(res, 200, ownerView(p)), true;
      }
    }
    if ((m = new RegExp(`^/api/v1/campaigns/(${UUID})/(payout-eligibility|payout-requests)$`).exec(path))) {
      const c = campaigns.get(m[1]);
      if (!c || c.ownerId !== user.id) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      if (method === "GET" && m[2] === "payout-eligibility") return send(res, 200, availability(c)), true;
      if (method === "GET") return send(res, 200, { items: [...payouts.values()].filter((p) => p.campaign_id === c.id).reverse().map(ownerView), next_cursor: null }), true;
      if (method === "POST" && m[2] === "payout-requests") {
        const key = String(req.headers["idempotency-key"] ?? "");
        if (!/^[0-9a-f-]{36}$/i.test(key)) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), true;
        if (!fresh(session)) return err(res, 403, "STEP_UP_REQUIRED"), true;
        if (idem.has(key)) return send(res, 201, ownerView(payouts.get(idem.get(key)))), true;
        if (body.currency !== "USD" || typeof body.amount_minor !== "string" || !/^[1-9]\d{0,17}$/.test(body.amount_minor)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "amount_minor", code: "INVALID" }] }), true;
        const d = getDestination(body.destination_id);
        if (!d || d.owner.id !== user.id || d.status !== "VERIFIED" || d.currency !== "USD") return err(res, 422, "PAYOUT_NOT_ELIGIBLE", { details: [{ field: "reason_category", code: "DESTINATION_NOT_READY" }] }), true;
        if ([...payouts.values()].some((p) => p.campaign_id === c.id && ACTIVE.has(p.status))) return err(res, 409, "PAYOUT_IN_PROGRESS"), true;
        if (BigInt(body.amount_minor) > BigInt(availability(c).available_minor)) return err(res, 422, "PAYOUT_NOT_ELIGIBLE", { details: [{ field: "reason_category", code: "INSUFFICIENT_FUNDS" }] }), true;
        const p = newPayout(c, { amount: body.amount_minor, requester: user.id, destination: { id: d.id, rail: d.rail, masked: `07*****${String(d.account_identifier).slice(-3)}`, version: d.version }, purpose: body.purpose });
        idem.set(key, p.id);
        actions.push({ kind: "owner.request", id: p.id, amount_minor: p.amount_minor });
        return send(res, 201, ownerView(p)), true;
      }
    }
    return false;
  }

  async function handle({ req, res, method, path, url, user, session, body }) {
    const ownerPath = path.startsWith("/api/v1/payout-requests") || new RegExp(`^/api/v1/campaigns/${UUID}/payout-`).test(path);
    const staffPath = path.startsWith("/api/v1/admin/payout-requests") || path.startsWith("/api/v1/admin/payouts");
    if (!ownerPath && !staffPath) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (ownerPath) return handleOwner({ req, res, method, path, user, session, body }) || (err(res, 404, "ROUTE_NOT_FOUND"), true);
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && !fresh(session)) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const R = "/api/v1/admin/payout-requests";
    const O = "/api/v1/admin/payouts";
    let m;
    if (method === "GET") {
      if (!need("payout.view")) return true;
      if (path === R) {
        const status = url.searchParams.get("status");
        const items = [...payouts.values()].filter((x) => !status || x.status === status).reverse().map(staffView);
        return send(res, 200, { items, next_cursor: null }), true;
      }
      if ((m = new RegExp(`^${R}/(${UUID})$`).exec(path))) {
        const x = payouts.get(m[1]);
        return (x ? send(res, 200, detail(x)) : err(res, 404, "PAYOUT_REQUEST_NOT_FOUND")), true;
      }
      if (path === `${O}/overview`) {
        const by = {};
        for (const x of payouts.values()) by[x.status] = (by[x.status] ?? 0) + 1;
        const sev = {};
        for (const e of exceptions.values()) if (e.status !== "RESOLVED") sev[e.severity] = (sev[e.severity] ?? 0) + 1;
        const r = [...payouts.values()].filter((x) => ACTIVE.has(x.status)).reduce((s, x) => s + BigInt(x.amount_minor), 0n);
        return send(res, 200, { execution_environment: "LOCAL_SIMULATOR", live_execution: false, production_status: "BLOCKED", ledger_environment: "TEST", by_status: by, open_exceptions_by_severity: sev, active_reserved_minor_by_currency: { USD: String(r) }, settlement_reconciliation: "NOT_PERFORMED" }), true;
      }
      if (path === `${O}/reservations`) return send(res, 200, { items: [...payouts.values()].reverse().map((x) => staffView(x).reservation), next_cursor: null }), true;
      if (path === `${O}/exceptions`) return send(res, 200, { items: [...exceptions.values()].reverse(), next_cursor: null }), true;
      if (path === `${O}/reconciliation`) return send(res, 200, { items: [...recon.values()].reverse(), next_cursor: null }), true;
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    if (method !== "POST") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    if ((m = new RegExp(`^${R}/(${UUID})/(approve|reject|cancel|eligibility-checks|status-checks)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(act === "approve" ? "payout.approve" : act === "reject" ? "payout.reject" : "payout.operate")) return true;
      const x = payouts.get(id);
      if (!x) return err(res, 404, "PAYOUT_REQUEST_NOT_FOUND"), true;
      if (act === "approve" || act === "reject") {
        if (!textOk(body.reason) || body.conflict_attested !== true) return err(res, 422, "VALIDATION_FAILED"), true;
        if (x.status !== "PENDING_REVIEW") return err(res, 409, "PAYOUT_STATE"), true;
        if (x.requested_by === user.id || x.ownerId === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
        if (x.approvals.some((a) => a.approver_id === user.id && a.round === x.approval_round)) return err(res, 409, "ALREADY_DECIDED"), true;
        x.approvals.push({ id: randomUUID(), round: x.approval_round, approver_id: user.id, decision: act === "approve" ? "APPROVE" : "REJECT", reason: body.reason, conflict_attested: true, decided_at: iso() });
        if (act === "reject") {
          x.status = "REJECTED";
          event(x, "REJECTED", user.id);
        } else if (x.approvals.filter((a) => a.round === x.approval_round && a.decision === "APPROVE").length >= (x.approval_policy === "DUAL" ? 2 : 1)) {
          x.status = "APPROVED";
          event(x, "APPROVED", user.id);
        } else x.version += 1;
      } else if (act === "cancel") {
        if (!textOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        if (x.status !== "PENDING_REVIEW" && x.status !== "APPROVED") return err(res, 409, "PAYOUT_STATE"), true;
        x.status = "CANCELLED";
        event(x, "CANCELLED_BY_STAFF", user.id);
      } else if (act === "eligibility-checks") {
        x.decision = { ...x.decision, id: randomUUID(), phase: "RECHECK", evaluated_at: iso() };
      }
      actions.push({ kind: `payout.${act}`, id, actor: user.id });
      return send(res, 200, detail(x)), true;
    }
    if ((m = new RegExp(`^${O}/exceptions/(${UUID})/(investigate|resolve)$`).exec(path))) {
      if (!need("payout.operate")) return true;
      const e = exceptions.get(m[1]);
      if (!e) return err(res, 404, "PAYOUT_EXCEPTION_NOT_FOUND"), true;
      if (!textOk(body.note)) return err(res, 422, "VALIDATION_FAILED"), true;
      if (e.status === "RESOLVED") return err(res, 409, "PAYOUT_EXCEPTION_STATE"), true;
      if (m[2] === "investigate") Object.assign(e, { status: "INVESTIGATING", owner_id: user.id });
      else Object.assign(e, { status: "RESOLVED", resolution_code: body.resolution_code ?? "CONFIRMED_NO_ACTION", resolved_at: iso() });
      actions.push({ kind: `exception.${m[2]}`, id: e.id, actor: user.id });
      return send(res, 200, e), true;
    }
    if (path === `${O}/reconciliation/run`) {
      if (!need("payout.operate")) return true;
      actions.push({ kind: "reconciliation.run", actor: user.id });
      return send(res, 200, { run_id: randomUUID(), checked: payouts.size, mismatches: recon.size, settlement_reconciliation: "NOT_PERFORMED" }), true;
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  async function control(path, body, res) {
    if (path === "/api/v1/__mock/payouts/seed") return send(res, 201, seed(body ?? {})), true;
    if (path === "/api/v1/__mock/payouts/log") return send(res, 200, { actions }), true;
    return false;
  }
  return { handle, control };
}
