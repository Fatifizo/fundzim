// In-memory mock of the Stage 10 staff ledger API for the E2E suite (shapes as internal/ledger/*.go). TEST ledger
// only; FINANCE has ledger.view / account.manage / period.close / adjustment.create+approve; step-up enforced on
// actions; maker ≠ checker; a journal is reversed at most once. There is no free-form posting endpoint. NOT a reference.
import { randomUUID } from "node:crypto";

const ROLE_PERMISSIONS = {
  FINANCE: ["ledger.view", "ledger.account.manage", "ledger.period.close", "ledger.adjustment.create", "ledger.adjustment.approve", "ledger.reconciliation.operate"],
  COMPLIANCE: ["ledger.view"],
};
const STEP_UP = new Set(["ledger.account.manage", "ledger.period.close", "ledger.adjustment.create", "ledger.adjustment.approve", "ledger.reconciliation.operate"]);
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();

export function createLedgerMock({ send, err, stepUpTtlMs }) {
  const prov = "5a2c3b6e-1b7c-4e0f-9b2a-6f5a1c2d3e4f";
  const camp = "7e1d2c3b-4a5f-4b6c-8d7e-9f0a1b2c3d4e";
  const accounts = new Map();
  const journals = new Map();
  const adjustments = new Map();
  const recon = new Map();
  const actions = [];
  const addAccount = (code, cls, type, normal, kind, owner) => {
    const a = { id: randomUUID(), code, account_class: cls, type, normal_balance: normal, kind, currency: "USD", owner_type: owner ? (cls.includes("campaign") ? "CAMPAIGN" : "PROVIDER") : "PLATFORM", owner_id: owner, environment: "TEST", status: "ACTIVE", status_reason: null, non_negative: kind === "OBLIGATION", projection_mode: "SYNC", balance_minor: "0", debit_total_minor: "0", credit_total_minor: "0", entry_count: 0 };
    accounts.set(a.id, a);
    return a;
  };
  const clearing = addAccount(`asset:psp_clearing:${prov}`, "asset:psp_clearing", "ASSET", "DEBIT", "CLAIM_ON_PROVIDER", prov);
  const unsettled = addAccount(`liability:campaign_unsettled:${camp}`, "liability:campaign_unsettled", "LIABILITY", "CREDIT", "OBLIGATION", camp);
  const apply = (a, dir, amt) => {
    const n = BigInt(amt);
    if (dir === "DEBIT") a.debit_total_minor = String(BigInt(a.debit_total_minor) + n);
    else a.credit_total_minor = String(BigInt(a.credit_total_minor) + n);
    const d = BigInt(a.debit_total_minor), c = BigInt(a.credit_total_minor);
    a.balance_minor = String(a.normal_balance === "DEBIT" ? d - c : c - d);
    a.entry_count += 1;
  };
  const post = (rule, amount, reverses = null, target = unsettled) => {
    const id = randomUUID();
    const lines = reverses
      ? journals.get(reverses).lines.map((l) => ({ ...l, direction: l.direction === "DEBIT" ? "CREDIT" : "DEBIT", debit_minor: l.credit_minor, credit_minor: l.debit_minor }))
      : [
          { line_no: 1, account_id: clearing.id, account_code: clearing.code, account_class: clearing.account_class, direction: "DEBIT", debit_minor: amount, credit_minor: "0", currency: "USD" },
          { line_no: 2, account_id: target.id, account_code: target.code, account_class: target.account_class, direction: "CREDIT", debit_minor: "0", credit_minor: amount, currency: "USD" },
        ];
    for (const l of lines) apply(accounts.get(l.account_id), l.direction, l.direction === "DEBIT" ? l.debit_minor : l.credit_minor);
    const j = { id, posting_rule: rule, posting_rule_version: 1, environment: "TEST", currency: "USD", total_minor: lines.filter((l) => l.direction === "DEBIT").reduce((s, l) => String(BigInt(s) + BigInt(l.debit_minor)), "0"), line_count: lines.length, description: `TEST_ONLY synthetic fixture ${rule}`, source_system: reverses ? "staff" : "synthetic_fixture", source_type: reverses ? "adjustment" : "payment", source_id: randomUUID(), evidence_class: reverses ? "MANUALLY_REVIEWED" : "SIMULATED", idempotency_key: reverses ? `reversal:${reverses}` : `fixture:${id}:capture`, occurred_at: iso(60_000), posted_at: iso(), period_code: new Date().toISOString().slice(0, 7), is_prior_period: false, reverses_transaction_id: reverses, reversed_by_transaction_id: null, adjustment_id: null, test_only: true, reason: reverses ? "approved reversal" : null, created_by: "SYSTEM:fixture", metadata: { fixture: "TEST_ONLY" }, lines, source_references: [], balanced: true };
    journals.set(id, j);
    if (reverses) journals.get(reverses).reversed_by_transaction_id = id;
    return j;
  };
  const tb = () => {
    let dr = 0n, cr = 0n;
    const rows = [...accounts.values()].map((a) => {
      const net = BigInt(a.debit_total_minor) - BigInt(a.credit_total_minor);
      if (net >= 0n) dr += net; else cr -= net;
      return { account_id: a.id, code: a.code, account_class: a.account_class, type: a.type, debit_minor: net >= 0n ? String(net) : "0", credit_minor: net < 0n ? String(-net) : "0" };
    });
    return { currency: "USD", total_debit_minor: String(dr), total_credit_minor: String(cr), difference_minor: String(dr - cr), balanced: dr === cr, rows };
  };
  function seed() {
    // a fresh campaign account per seed: tests run in parallel against one mock
    const c = randomUUID();
    const acct = addAccount(`liability:campaign_unsettled:${c}`, "liability:campaign_unsettled", "LIABILITY", "CREDIT", "OBLIGATION", c);
    const j = post("DONATION_CAPTURED", "1000", null, acct);
    post("DONATION_CAPTURED", "2500", null, acct);
    const e = { id: randomUUID(), environment: "TEST", exception_type: "AMOUNT_MISMATCH", source_type: "payment", source_id: randomUUID(), transaction_id: j.id, expected_amount_minor: "1500", actual_amount_minor: "1000", currency: "USD", status: "OPEN", owner_id: null, detected_at: iso(7_200_000), age_seconds: 7200, resolution_note: null };
    recon.set(e.id, e);
    return { journal_id: j.id, account_id: acct.id, exception_id: e.id };
  }
  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const reasonOk = (v) => typeof v === "string" && [...v.trim()].length >= 10;

  async function handle({ req, res, method, path, user, session, body }) {
    if (!path.startsWith("/api/v1/admin/ledger")) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && !(session && Date.now() - session.stepUpAt <= stepUpTtlMs)) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    const B = "/api/v1/admin/ledger";
    let m;
    if (method === "GET") {
      if (!need("ledger.view")) return true;
      if (path === `${B}/overview`) {
        const t = tb();
        return send(res, 200, { environment: "TEST", production_posting_enabled: false, journals: journals.size, accounts: accounts.size, open_exceptions: [...recon.values()].filter((e) => e.status !== "RESOLVED").length, pending_adjustments: [...adjustments.values()].filter((a) => a.status === "PENDING_APPROVAL").length, trial_balance: [{ ...t, rows: [] }], integrity: [{ code: "L1", name: "every journal balances", status: "PASSED", checked: journals.size, violations: 0, sample: [] }, { code: "L10", name: "balance projections equal a recompute from entries", status: "PASSED", checked: accounts.size, violations: 0, sample: [] }], open_periods: [{ id: "0192a000-0000-7000-8000-00000000e001", code: new Date().toISOString().slice(0, 7), starts_at: iso(86400_000), ends_at: iso(-86400_000 * 20), status: "OPEN" }], synthetic_fixtures_enabled: true, notice: "TEST ledger: synthetic TEST_ONLY fixtures only. No real donation, refund, settlement or payout is recorded; no balance is withdrawable." }), true;
      }
      if (path === `${B}/accounts`) return send(res, 200, { items: [...accounts.values()], next_cursor: null }), true;
      if ((m = new RegExp(`^${B}/accounts/([0-9a-f-]{36})$`).exec(path))) {
        const a = accounts.get(m[1]);
        if (!a) return err(res, 404, "LEDGER_NOT_FOUND"), true;
        const items = [...journals.values()].filter((j) => j.lines.some((l) => l.account_id === a.id)).reverse();
        return send(res, 200, { account: a, activity: { items, next_cursor: null } }), true;
      }
      if (path === `${B}/journals`) return send(res, 200, { items: [...journals.values()].reverse(), next_cursor: null }), true;
      if ((m = new RegExp(`^${B}/journals/([0-9a-f-]{36})$`).exec(path))) {
        const j = journals.get(m[1]);
        return (j ? send(res, 200, j) : err(res, 404, "LEDGER_NOT_FOUND")), true;
      }
      if (path === `${B}/trial-balance`) return send(res, 200, { environment: "TEST", currencies: journals.size ? [tb()] : [] }), true;
      if (path === `${B}/adjustments`) return send(res, 200, { items: [...adjustments.values()].filter((a) => a.status === "PENDING_APPROVAL"), next_cursor: null }), true;
      if (path === `${B}/reconciliation`) return send(res, 200, { items: [...recon.values()], next_cursor: null }), true;
      if (path === `${B}/exceptions`) return send(res, 200, { integrity: [{ code: "L1", name: "every journal balances", status: "PASSED", checked: journals.size, violations: 0, sample: [] }], open_reconciliation_exceptions: [...recon.values()].filter((e) => e.status === "OPEN"), blocked_sources: [] }), true;
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    if (method === "POST") {
      if (path === `${B}/adjustments`) {
        if (!need("ledger.adjustment.create")) return true;
        if (!/^[0-9a-f-]{36}$/i.test(String(req.headers["idempotency-key"] ?? ""))) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        const target = journals.get(body.target_transaction_id);
        if (!target) return err(res, 404, "LEDGER_NOT_FOUND"), true;
        if (target.reversed_by_transaction_id) return err(res, 409, "LEDGER_ALREADY_REVERSED"), true;
        const a = { id: randomUUID(), kind: "REVERSAL", posting_rule: "REVERSAL", currency: "USD", target_transaction_id: target.id, reason_code: body.reason_code, reason: body.reason, status: "PENDING_APPROVAL", requested_by: user.id, requested_at: iso(), expires_at: iso(-86400_000) };
        adjustments.set(a.id, a);
        actions.push({ kind: "adjustment.requested", id: a.id });
        return send(res, 201, a), true;
      }
      if ((m = new RegExp(`^${B}/adjustments/([0-9a-f-]{36})/(approve|reject|withdraw)$`).exec(path))) {
        const [, id, act] = m;
        if (!need(act === "withdraw" ? "ledger.adjustment.create" : "ledger.adjustment.approve")) return true;
        const a = adjustments.get(id);
        if (!a) return err(res, 404, "LEDGER_NOT_FOUND"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        if (act !== "withdraw" && a.requested_by === user.id) return err(res, 403, "LEDGER_SELF_APPROVAL_FORBIDDEN"), true;
        if (act === "approve") {
          const r = post("REVERSAL", "0", a.target_transaction_id);
          Object.assign(a, { status: "POSTED", approved_by: user.id, reversal_transaction_id: r.id });
        } else a.status = act === "reject" ? "REJECTED" : "WITHDRAWN";
        actions.push({ kind: `adjustment.${act}`, id });
        return send(res, 200, a), true;
      }
      if ((m = new RegExp(`^${B}/accounts/([0-9a-f-]{36})/(freeze|unfreeze|close)$`).exec(path))) {
        if (!need("ledger.account.manage")) return true;
        const a = accounts.get(m[1]);
        if (!a) return err(res, 404, "LEDGER_NOT_FOUND"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        if (m[2] === "close" && a.balance_minor !== "0") return err(res, 409, "LEDGER_ACCOUNT_BALANCE_NOT_ZERO"), true;
        a.status = { freeze: "FROZEN", unfreeze: "ACTIVE", close: "CLOSED" }[m[2]];
        a.status_reason = body.reason;
        actions.push({ kind: `account.${m[2]}`, id: a.id });
        return send(res, 200, { account: a, activity: { items: [], next_cursor: null } }), true;
      }
      if ((m = new RegExp(`^${B}/reconciliation/([0-9a-f-]{36})/(investigate|resolve)$`).exec(path))) {
        if (!need("ledger.reconciliation.operate")) return true;
        const e = recon.get(m[1]);
        if (!e) return err(res, 404, "LEDGER_NOT_FOUND"), true;
        if (!reasonOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        if (m[2] === "investigate") Object.assign(e, { status: "INVESTIGATING", owner_id: user.id });
        else Object.assign(e, { status: "RESOLVED", resolution_note: body.reason });
        actions.push({ kind: `recon.${m[2]}`, id: e.id });
        return send(res, 200, { items: [...recon.values()], next_cursor: null }), true;
      }
      if (path.startsWith(`${B}/periods/`)) {
        if (!need("ledger.period.close")) return true;
        return send(res, 200, { items: [], next_cursor: null }), true;
      }
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  async function control(path, _body, res) {
    if (path === "/api/v1/__mock/ledger/seed") return send(res, 201, seed()), true;
    if (path === "/api/v1/__mock/ledger/log") return send(res, 200, { actions }), true;
    return false;
  }
  return { handle, control };
}
