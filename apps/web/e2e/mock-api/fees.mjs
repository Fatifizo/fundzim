// In-memory mock of the Stage 12 fees API for the E2E suite (shapes as internal/fees). TEST fees only: every figure is
// labelled TEST, provider evidence is SYNTHETIC or UNVERIFIED (VERIFIED is refused), an unknown provider fee is null
// (never zero). FINANCE is the maker (fee.config.request, fee.adjust.request), BUSINESS_APPROVER the checker
// (fee.config.approve, fee.adjust.approve); maker ≠ checker; all four need a fresh step-up. A quote is not a charge.
// The arithmetic here is only good enough for the UI tests. NOT a reference implementation.
import { randomUUID } from "node:crypto";

const ROLE_PERMISSIONS = {
  FINANCE: ["fee.view", "fee.config.request", "fee.adjust.request"],
  BUSINESS_APPROVER: ["fee.view", "fee.config.approve", "fee.adjust.approve"],
  COMPLIANCE: ["fee.view"],
  ADMIN: ["fee.fixtures"],
};
const STEP_UP = new Set(["fee.config.request", "fee.config.approve", "fee.adjust.request", "fee.adjust.approve"]);
const UUID = "[0-9a-f-]{36}";
const SANDBOX_PROVIDER = "0192a000-0000-7000-8000-0000000b0001";
const OTHER_STAFF = "0192a000-0000-7000-8000-00000000f002";
const LABEL = "TEST — synthetic configuration, not FundZim's commercial rates";
const TX = { PLATFORM_FEE: "DONATION", PAYMENT_PROCESSING_FEE: "DONATION", PAYOUT_PROCESSING_FEE: "PAYOUT", REFUND_FEE: "REFUND", CHARGEBACK_FEE: "CHARGEBACK" };
const iso = (msAgo = 0) => new Date(Date.now() - msAgo).toISOString();
const hash = () => randomUUID().replaceAll("-", "") + randomUUID().replaceAll("-", "");

/** round(amount × bps / 10 000) half up, in BigInt (no floats). */
const pct = (amount, bps) => {
  const n = BigInt(amount) * BigInt(bps);
  const q = n / 10000n;
  return String(n % 10000n * 2n >= 10000n ? q + 1n : q);
};

export function createFeesMock({ send, err, stepUpTtlMs, users }) {
  const campaigns = new Map(); // id -> { id, ownerId }
  const policies = new Map();
  const versions = new Map();
  const schedules = new Map();
  const quotes = new Map();
  const assessments = new Map();
  const adjustments = new Map();
  const recon = new Map();
  const runs = [];
  const idem = new Map();
  const actions = [];
  let globalSeeded = false;

  const terms = (v) => ({ model: v.model, rate_bps: v.rate_bps ?? null, fixed_minor: v.fixed_minor ?? null, min_fee_minor: v.min_fee_minor ?? null, max_fee_minor: v.max_fee_minor ?? null, tiers: [], rounding_mode: v.rounding_mode ?? "HALF_UP" });
  const policyView = (p) => {
    const vs = [...versions.values()].filter((v) => v.policy_id === p.id).sort((a, b) => b.version_no - a.version_no);
    const active = vs.find((v) => v.status === "ACTIVE" && Date.parse(v.effective_from) <= Date.now());
    return { ...p, versions: vs.map(versionView), active_version_id: active?.id ?? null };
  };
  const versionView = (v) => ({ ...v, terms: terms(v) });
  const specificity = (p) => (p.campaign_id ? 16 : 0) + (p.campaign_category ? 8 : 0) + (p.provider_id ? 4 : 0) + (p.payment_method ? 2 : 0);

  function resolve(category, currency, campaignId, method) {
    const cands = [...versions.values()]
      .filter((v) => v.status === "ACTIVE" && Date.parse(v.effective_from) <= Date.now())
      .map((v) => ({ v, p: policies.get(v.policy_id) }))
      .filter(({ p }) => p.category === category && p.currency === currency && (!p.campaign_id || p.campaign_id === campaignId) && (!p.payment_method || p.payment_method === method))
      .sort((a, b) => specificity(b.p) - specificity(a.p) || Date.parse(b.v.effective_from) - Date.parse(a.v.effective_from));
    return cands[0]?.v ?? null;
  }
  const fee = (v, amount) => {
    if (!v || v.model === "ZERO_FEE") return "0";
    let f = v.model === "FIXED" ? BigInt(v.fixed_minor) : BigInt(pct(amount, v.rate_bps ?? 0)) + (v.model === "PERCENTAGE_PLUS_FIXED" ? BigInt(v.fixed_minor) : 0n);
    if (v.min_fee_minor && f < BigInt(v.min_fee_minor)) f = BigInt(v.min_fee_minor);
    if (v.max_fee_minor && f > BigInt(v.max_fee_minor)) f = BigInt(v.max_fee_minor);
    return String(f);
  };
  const split = (amount, resp) => {
    if (amount === null) return { donor: resp === "DONOR_COVERED" || resp === "SHARED" ? null : "0", organiser: resp === "ORGANISER_COVERED" || resp === "SHARED" ? null : "0", platform: resp === "PLATFORM_ABSORBED" ? null : "0" };
    const a = BigInt(amount);
    if (resp === "DONOR_COVERED") return { donor: amount, organiser: "0", platform: "0" };
    if (resp === "ORGANISER_COVERED") return { donor: "0", organiser: amount, platform: "0" };
    if (resp === "PLATFORM_ABSORBED") return { donor: "0", organiser: "0", platform: amount };
    const d = (a + 1n) / 2n;
    return { donor: String(d), organiser: String(a - d), platform: "0" };
  };
  const add = (...xs) => (xs.some((x) => x === null) ? null : String(xs.reduce((s, x) => s + BigInt(x), 0n)));

  function computeQuote(user, campaignId, amount, currency, method) {
    const pv = resolve("PLATFORM_FEE", currency, campaignId, method);
    const cv = resolve("PAYMENT_PROCESSING_FEE", currency, campaignId, method);
    const sch = method ? [...schedules.values()].find((s) => s.status === "ACTIVE" && s.fee_type === "TRANSACTION" && s.currency === currency && (!s.payment_method || s.payment_method === method)) : null;
    const platformFee = fee(pv, amount);
    const providerFee = sch ? pct(amount, sch.rate_bps) : null;
    const pp = pv ? split(platformFee, pv.responsibility) : { donor: "0", organiser: "0", platform: "0" };
    const vp = cv ? split(providerFee, cv.responsibility) : { donor: null, organiser: null, platform: null };
    const charge = add(amount, pp.donor, vp.donor);
    const alloc = vp.organiser === null ? null : String(BigInt(amount) - BigInt(pp.organiser) - BigInt(vp.organiser));
    const net = vp.platform === null ? null : String(BigInt(platformFee) - BigInt(vp.platform));
    const q = {
      id: randomUUID(), userId: user.id, campaign_id: campaignId, transaction_type: "DONATION", currency, amount_minor: amount, payment_method: method,
      platform_fee_minor: platformFee, platform_fee_basis: pv ? "QUOTED" : "NOT_CONFIGURED", provider_fee_minor: providerFee, provider_fee_basis: providerFee === null ? "UNKNOWN" : "ESTIMATED",
      donor_charge_minor: charge, campaign_allocation_minor: alloc, platform_net_minor: net,
      components: [
        { category: "PLATFORM_FEE", basis: pv ? "QUOTED" : "NOT_CONFIGURED", amount_minor: pv ? platformFee : null, responsibility: pv?.responsibility ?? "", donor_part_minor: pp.donor, organiser_part_minor: pp.organiser, platform_part_minor: pp.platform, rounding_mode: pv?.rounding_mode ?? "", note: pv ? "" : "no active platform-fee policy" },
        { category: "PAYMENT_PROCESSING_FEE", basis: providerFee === null ? "UNKNOWN" : "ESTIMATED", amount_minor: providerFee, responsibility: cv?.responsibility ?? "", donor_part_minor: vp.donor, organiser_part_minor: vp.organiser, platform_part_minor: vp.platform, rounding_mode: sch ? "HALF_UP" : "", note: sch ? "estimated from SYNTHETIC provider fee evidence" : "no provider fee evidence" },
      ],
      status: "ACTIVE", expires_at: iso(-15 * 60_000), donation_intent_id: null, calculation_id: randomUUID(), algorithm_version: "fees-calc/1", system_generated: false,
      fee_environment: "TEST", label: LABEL, notice: "A fee quote is an estimate for a TEST donation. It is not a charge, and nothing has been paid.", created_at: iso(),
    };
    quotes.set(q.id, q);
    return q;
  }
  const quoteView = (q) => {
    const rest = { ...q };
    delete rest.userId;
    return rest;
  };

  function newPolicy(body, by) {
    const p = { id: randomUUID(), code: body.code, name: body.name, description: body.description ?? "", category: body.category, transaction_type: TX[body.category], currency: body.currency ?? "USD",
      payment_method: body.payment_method ?? null, provider_id: null, campaign_category: null, campaign_id: body.campaign_id ?? null, fee_environment: "TEST", created_by: by, created_at: iso() };
    p.specificity = specificity(p);
    policies.set(p.id, p);
    return p;
  }
  function newVersion(p, body, by) {
    const no = [...versions.values()].filter((v) => v.policy_id === p.id).length + 1;
    const v = { id: randomUUID(), policy_id: p.id, category: p.category, version_no: no, status: "DRAFT", model: body.model, rate_bps: body.rate_bps ?? null, fixed_minor: body.fixed_minor ?? null,
      min_fee_minor: body.min_fee_minor ?? null, max_fee_minor: body.max_fee_minor ?? null, rounding_mode: body.rounding_mode ?? "HALF_UP", responsibility: body.responsibility,
      donor_share_bps: body.donor_share_bps ?? null, refund_treatment: body.refund_treatment ?? null, effective_from: body.effective_from ?? iso(), effective_to: null, algorithm_version: "fees-calc/1",
      terms_hash: null, fee_environment: "TEST", created_by: by, submitted_by: null, submitted_at: null, approved_by: null, approved_at: null, approval_reason: null, rejected_by: null, rejection_reason: null,
      activated_by: null, activated_at: null, suspended_by: null, suspension_reason: null, retired_by: null, retirement_reason: null, created_at: iso() };
    versions.set(v.id, v);
    return v;
  }
  function activeGlobal(body) {
    const p = newPolicy(body, OTHER_STAFF);
    const v = newVersion(p, body, OTHER_STAFF);
    Object.assign(v, { status: "ACTIVE", effective_from: iso(3_600_000), terms_hash: hash(), submitted_by: OTHER_STAFF, submitted_at: iso(), approved_by: "0192a000-0000-7000-8000-00000000f003", approved_at: iso(), approval_reason: "Seeded TEST policy", activated_by: OTHER_STAFF, activated_at: iso() });
  }
  function seedGlobal() {
    if (globalSeeded) return;
    globalSeeded = true;
    activeGlobal({ code: "seed.platform.usd", name: "Seeded platform fee (TEST)", category: "PLATFORM_FEE", currency: "USD", model: "PERCENTAGE", rate_bps: 500, responsibility: "ORGANISER_COVERED", refund_treatment: "PROPORTIONAL" });
    activeGlobal({ code: "seed.processing.usd", name: "Seeded provider fee bearer (TEST)", category: "PAYMENT_PROCESSING_FEE", currency: "USD", model: "PROVIDER_PASS_THROUGH", responsibility: "ORGANISER_COVERED" });
    activeGlobal({ code: "seed.payout.usd", name: "Seeded payout fee bearer (TEST)", category: "PAYOUT_PROCESSING_FEE", currency: "USD", model: "PROVIDER_PASS_THROUGH", responsibility: "ORGANISER_COVERED" });
    const s = { id: randomUUID(), provider_id: SANDBOX_PROVIDER, provider_is_sandbox: true, contract_reference: "SANDBOX-SYNTHETIC", schedule_version: "1", currency: "USD", payment_method: "ECOCASH", fee_type: "TRANSACTION",
      model: "PERCENTAGE", rate_bps: 200, rounding_mode: null, rounding_known: false, effective_from: iso(3_600_000), effective_to: null, evidence_source: "Synthetic TEST fixture", evidence_reference: null,
      confirmation_status: "SYNTHETIC", status: "ACTIVE", fee_environment: "TEST", created_by: OTHER_STAFF, approved_by: "0192a000-0000-7000-8000-00000000f003", created_at: iso() };
    schedules.set(s.id, s);
  }
  const scheduleView = (s) => ({ ...s, terms: terms(s) });

  function seed(body) {
    seedGlobal();
    const owner = body.owner_email ? users.get(body.owner_email) : null;
    const c = { id: randomUUID(), ownerId: owner?.id ?? randomUUID() };
    campaigns.set(c.id, c);
    const donation = randomUUID();
    const a = { id: randomUUID(), source_type: "SYNTHETIC_DONATION", source_id: donation, category: "PLATFORM_FEE", campaign_id: c.id, currency: "USD", basis_minor: "10000", fee_minor: "500",
      basis: "QUOTED", responsibility: "ORGANISER_COVERED", donor_part_minor: "0", organiser_part_minor: "500", platform_part_minor: "0", policy_version_id: null, provider_schedule_id: null,
      calculation_id: randomUUID(), quote_id: null, payment_method: "ECOCASH", provider_id: SANDBOX_PROVIDER, accounting_status: "POSTED", test_payment: true, synthetic: true, fee_environment: "TEST", assessed_at: iso(60_000) };
    const pa = { ...a, id: randomUUID(), category: "PAYMENT_PROCESSING_FEE", fee_minor: null, basis: "UNKNOWN", donor_part_minor: "0", organiser_part_minor: null, platform_part_minor: "0", accounting_status: "NOT_APPLICABLE" };
    assessments.set(a.id, a);
    assessments.set(pa.id, pa);
    const adj = { id: randomUUID(), assessment_id: a.id, currency: "USD", reason: "PROVIDER_CORRECTION", direction: "DECREASE", amount_minor: "50", source_type: "staff_request", source_id: randomUUID(),
      basis_minor: null, calculation: {}, note: `TEST_ONLY seeded correction request ${c.id.slice(0, 8)}`, status: "PENDING_APPROVAL", actor_type: "STAFF", requested_by: OTHER_STAFF, decided_by: null, decided_at: null,
      decision_reason: null, accounting_status: "NOT_APPLICABLE", ledger_adjustment_id: null, fee_environment: "TEST", created_at: iso(30_000) };
    adjustments.set(adj.id, adj);
    const r = { id: randomUUID(), run_id: randomUUID(), kind: "UNRECONCILED_PROVIDER_FEE", severity: "SEV3", assessment_id: pa.id, adjustment_id: null, ledger_transaction_id: null, currency: "USD",
      expected: {}, observed: {}, detail: `no provider-reported fee for campaign ${c.id.slice(0, 8)}`, status: "OPEN", detected_at: iso(10_000), resolved_by: null, resolved_at: null, resolution_note: null };
    recon.set(r.id, r);
    return { campaign_id: c.id, assessment_id: a.id, adjustment_id: adj.id, recon_id: r.id, sandbox_provider_id: SANDBOX_PROVIDER };
  }

  const net = (a) => {
    if (a.fee_minor === null) return null;
    let n = BigInt(a.fee_minor);
    for (const j of adjustments.values()) if (j.assessment_id === a.id && (j.status === "APPLIED" || j.status === "APPROVED")) n += j.direction === "INCREASE" ? BigInt(j.amount_minor) : -BigInt(j.amount_minor);
    return String(n);
  };
  const assessmentView = (a) => ({ ...a, net_fee_minor: net(a), label: LABEL });
  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const fresh = (session) => !!session && Date.now() - session.stepUpAt <= stepUpTtlMs;
  const textOk = (v, min = 5) => typeof v === "string" && [...v.trim()].length >= min && [...v.trim()].length <= 2000;
  const activePolicies = (campaignId) =>
    [...versions.values()].filter((v) => v.status === "ACTIVE").map((v) => ({ v, p: policies.get(v.policy_id) })).filter(({ p }) => !p.campaign_id || p.campaign_id === campaignId)
      .map(({ v, p }) => ({ version_id: v.id, policy_id: p.id, name: p.name, category: p.category, transaction_type: p.transaction_type, currency: p.currency, scope: p.campaign_id ? `campaign ${p.campaign_id}` : "platform-wide",
        terms: terms(v), responsibility: v.responsibility, donor_share_bps: v.donor_share_bps, refund_treatment: v.refund_treatment, effective_from: v.effective_from, fee_environment: "TEST", label: LABEL }));
  const idemReplay = (req, res, scope) => {
    const key = String(req.headers["idempotency-key"] ?? "");
    if (!/^[0-9a-f-]{36}$/i.test(key)) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), { done: true };
    const prior = idem.get(`${scope}:${key}`);
    if (prior) return send(res, 201, prior()), { done: true };
    return { done: false, remember: (view) => idem.set(`${scope}:${key}`, view) };
  };

  function handleUser({ req, res, method, path, url, user, body }) {
    let m;
    if (method === "GET" && path === "/api/v1/fees/policies/active") {
      seedGlobal();
      const ccy = url.searchParams.get("currency");
      return send(res, 200, { items: activePolicies(null).filter((p) => !ccy || p.currency === ccy), next_cursor: null, fee_environment: "TEST", label: LABEL }), true;
    }
    if (method === "POST" && path === "/api/v1/fees/quotes") {
      const r = idemReplay(req, res, `quote:${user.id}`);
      if (r.done) return true;
      if (typeof body.amount_minor !== "string" || !/^[1-9]\d{0,17}$/.test(body.amount_minor)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "amount_minor", code: "INVALID_FORMAT" }] }), true;
      const c = campaigns.get(body.campaign_id);
      if (!c) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      if (body.currency !== "USD") return err(res, 422, "CURRENCY_NOT_SUPPORTED"), true;
      if (body.payment_method && !["ECOCASH", "CARD"].includes(body.payment_method)) return err(res, 422, "PAYMENT_METHOD_UNAVAILABLE"), true;
      seedGlobal();
      const q = computeQuote(user, c.id, body.amount_minor, "USD", body.payment_method ?? null);
      r.remember(() => quoteView(q));
      actions.push({ kind: "quote.create", id: q.id, actor: user.id, campaign_id: c.id });
      return send(res, 201, quoteView(q)), true;
    }
    if (method === "GET" && (m = new RegExp(`^/api/v1/fees/quotes/(${UUID})$`).exec(path))) {
      const q = quotes.get(m[1]);
      if (!q || q.userId !== user.id) return err(res, 404, "FEE_NOT_FOUND"), true;
      return send(res, 200, quoteView(q)), true;
    }
    if (method === "GET" && (m = new RegExp(`^/api/v1/campaigns/(${UUID})/fees$`).exec(path))) {
      const c = campaigns.get(m[1]);
      if (!c || c.ownerId !== user.id) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      const as = [...assessments.values()].filter((a) => a.campaign_id === c.id);
      // organiser projections (internal/fees OrganiserAssessment / OrganiserAdjustment): no internal references, staff identities or notes
      const ORG_FIELDS = ["id", "source_type", "category", "currency", "basis_minor", "fee_minor", "basis", "responsibility", "donor_part_minor",
        "organiser_part_minor", "platform_part_minor", "net_fee_minor", "accounting_status", "test_payment", "synthetic", "fee_environment", "assessed_at", "label"];
      const orgAssessment = (a) => {
        const v = assessmentView(a);
        return Object.fromEntries(ORG_FIELDS.map((k) => [k, v[k]]));
      };
      const orgAdjustment = (j) => ({ id: j.id, assessment_id: j.assessment_id, currency: j.currency, reason: j.reason, direction: j.direction,
        amount_minor: j.amount_minor, basis_minor: j.basis_minor, status: j.status, created_at: j.created_at });
      return send(res, 200, { campaign_id: c.id, fee_environment: "TEST", label: LABEL, policies: activePolicies(c.id), assessments: as.map(orgAssessment),
        adjustments: [...adjustments.values()].filter((j) => as.some((a) => a.id === j.assessment_id) && (j.status === "APPLIED" || j.status === "APPROVED"))
          .map(orgAdjustment),
        restrictions: ["Donations are not yet available: no fee is charged and nothing here is a real donation.", "Provider fees are estimates from synthetic or unverified evidence, or UNKNOWN."] }), true;
    }
    return false;
  }

  async function handle({ req, res, method, path, url, user, session, body }) {
    const userPath = path.startsWith("/api/v1/fees/") || new RegExp(`^/api/v1/campaigns/${UUID}/fees$`).test(path);
    const staffPath = path.startsWith("/api/v1/admin/fees") || path === "/api/v1/admin/finance/revenue";
    if (!userPath && !staffPath) return false;
    if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
    if (userPath) return handleUser({ req, res, method, path, url, user, body }) || (err(res, 404, "ROUTE_NOT_FOUND"), true);
    if (user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    const p = perms(user);
    const need = (perm) => {
      if (!p.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
      if (STEP_UP.has(perm) && !fresh(session)) return err(res, 403, "STEP_UP_REQUIRED"), false;
      return true;
    };
    seedGlobal();
    const A = "/api/v1/admin/fees";
    let m;
    if (method === "GET") {
      if (!need("fee.view")) return true;
      if (path === `${A}/overview`) {
        const by = (xs) => xs.reduce((o, x) => ((o[x.status] = (o[x.status] ?? 0) + 1), o), {});
        return send(res, 200, { fee_environment: "TEST", production_fees: "BLOCKED", label: LABEL, policy_versions_by_status: by([...versions.values()]), provider_schedules_by_status: by([...schedules.values()]),
          active_quotes: [...quotes.values()].filter((q) => q.status === "ACTIVE").length, assessments: assessments.size, pending_adjustments: [...adjustments.values()].filter((j) => j.status === "PENDING_APPROVAL").length,
          open_reconciliation_items: [...recon.values()].filter((r) => r.status === "OPEN").length, latest_reconciliation: runs.at(-1) ?? null,
          notes: ["No fee model is decided (PD-01, LR-028); tax treatment is open (LR-016, LR-045, LR-059): no fee is charged to anyone."] }), true;
      }
      if (path === `${A}/policies`) return send(res, 200, { items: [...policies.values()].reverse().map(policyView), next_cursor: null }), true;
      if ((m = new RegExp(`^${A}/policies/(${UUID})$`).exec(path))) {
        const x = policies.get(m[1]);
        return (x ? send(res, 200, policyView(x)) : err(res, 404, "FEE_NOT_FOUND")), true;
      }
      if (path === `${A}/provider-schedules`) return send(res, 200, { items: [...schedules.values()].reverse().map(scheduleView), next_cursor: null }), true;
      if (path === `${A}/quotes`) return send(res, 200, { items: [...quotes.values()].reverse().map(quoteView), next_cursor: null }), true;
      if (path === `${A}/assessments`) return send(res, 200, { items: [...assessments.values()].reverse().map(assessmentView), next_cursor: null }), true;
      if ((m = new RegExp(`^${A}/assessments/(${UUID})$`).exec(path))) {
        const a = assessments.get(m[1]);
        if (!a) return err(res, 404, "FEE_NOT_FOUND"), true;
        const journals = a.accounting_status === "POSTED" ? [{ id: randomUUID(), assessment_id: a.id, adjustment_id: null, ledger_transaction_id: randomUUID(), journal_key: `fee_fixture:${a.source_id}:capture`, posting_rule: "DONATION_CAPTURED", purpose: "CAPTURE", currency: a.currency, fee_amount_minor: a.fee_minor, policy_version_id: null, recorded_at: a.assessed_at }] : [];
        return send(res, 200, { assessment: assessmentView(a), journals, adjustments: [...adjustments.values()].filter((j) => j.assessment_id === a.id),
          events: [{ id: randomUUID(), aggregate_type: "fee_assessment", aggregate_id: a.id, event_type: "FEE_ASSESSED", actor_type: "SYSTEM", actor_id: "fees", detail: {}, occurred_at: a.assessed_at }] }), true;
      }
      if (path === `${A}/adjustments`) return send(res, 200, { items: [...adjustments.values()].reverse(), next_cursor: null }), true;
      if (path === `${A}/reconciliation`) return send(res, 200, { items: [...recon.values()].reverse(), next_cursor: null, latest_run: runs.at(-1) ?? null, settlement_reconciliation: "NOT_PERFORMED" }), true;
      if (path === "/api/v1/admin/finance/revenue") {
        const by = {};
        for (const a of assessments.values()) {
          const c = (by[a.currency] ??= { currency: a.currency, quoted_platform_fees_minor: "0", quotes: 0, assessed_platform_fees_minor: 0n, platform_fee_assessments: 0, assessed_on_test_payments_minor: "0", assessed_on_synthetic_donations_minor: 0n, refunded_platform_fees_minor: "0", chargeback_reversed_platform_fees_minor: "0", manual_adjustments_net_minor: "0", provider_fees_estimated_minor: "0", provider_fees_unknown: 0, payout_fees_minor: "0", chargeback_fee_exposure_minor: "0", recognised_revenue_minor: 0n, revenue_reversals_minor: "0", provider_fee_expense_minor: "0", net_revenue_minor: "0", cash_received: "NOT_AVAILABLE" });
          if (a.category === "PLATFORM_FEE") {
            c.assessed_platform_fees_minor += BigInt(a.fee_minor);
            c.assessed_on_synthetic_donations_minor += BigInt(a.fee_minor);
            c.recognised_revenue_minor += BigInt(a.fee_minor);
            c.platform_fee_assessments += 1;
          } else if (a.fee_minor === null) c.provider_fees_unknown += 1;
        }
        const currencies = Object.values(by).map((c) => ({ ...c, assessed_platform_fees_minor: String(c.assessed_platform_fees_minor), assessed_on_synthetic_donations_minor: String(c.assessed_on_synthetic_donations_minor), recognised_revenue_minor: String(c.recognised_revenue_minor), net_revenue_minor: String(c.recognised_revenue_minor) }));
        return send(res, 200, { fee_environment: "TEST", label: "TEST — synthetic data, not FundZim's financial performance", from: iso(365 * 86400_000), to: iso(), currencies, by_period: [], by_payment_method: [], by_provider: [],
          notes: ["Quoted fees are not charges and not revenue.", "Currencies are never combined: there is no approved FX policy."] }), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    if (method !== "POST") return err(res, 404, "ROUTE_NOT_FOUND"), true;
    if (path === `${A}/policies`) {
      if (!need("fee.config.request")) return true;
      const r = idemReplay(req, res, "policy");
      if (r.done) return true;
      if (["CURRENCY_CONVERSION_FEE", "OTHER_APPROVED_FEE"].includes(body.category)) return err(res, 422, "FEE_CATEGORY_NOT_ENABLED"), true;
      if (!/^[a-z][a-z0-9_.]{2,63}$/.test(body.code ?? "") || !TX[body.category]) return err(res, 422, "VALIDATION_FAILED"), true;
      if ([...policies.values()].some((x) => x.code === body.code)) return err(res, 409, "FEE_POLICY_CODE_TAKEN"), true;
      const x = newPolicy(body, user.id);
      r.remember(() => policyView(x));
      actions.push({ kind: "policy.create", id: x.id, actor: user.id });
      return send(res, 201, policyView(x)), true;
    }
    if ((m = new RegExp(`^${A}/policies/(${UUID})/versions$`).exec(path))) {
      if (!need("fee.config.request")) return true;
      const r = idemReplay(req, res, "version");
      if (r.done) return true;
      const x = policies.get(m[1]);
      if (!x) return err(res, 404, "FEE_NOT_FOUND"), true;
      if ([...versions.values()].some((v) => v.policy_id === x.id && ["DRAFT", "PENDING_APPROVAL"].includes(v.status))) return err(res, 409, "FEE_POLICY_CONFLICT"), true;
      const v = newVersion(x, body, user.id);
      r.remember(() => versionView(v));
      actions.push({ kind: "version.create", id: v.id, actor: user.id });
      return send(res, 201, versionView(v)), true;
    }
    if ((m = new RegExp(`^${A}/policy-versions/(${UUID})/(submit|approve|reject|activate|suspend|reactivate|retire)$`).exec(path))) {
      const [, id, act] = m;
      if (!need(["approve", "reject", "reactivate"].includes(act) ? "fee.config.approve" : "fee.config.request")) return true;
      const v = versions.get(id);
      if (!v) return err(res, 404, "FEE_NOT_FOUND"), true;
      const from = { submit: ["DRAFT"], approve: ["PENDING_APPROVAL"], reject: ["PENDING_APPROVAL"], activate: ["APPROVED"], suspend: ["ACTIVE"], reactivate: ["SUSPENDED"], retire: ["DRAFT", "APPROVED", "ACTIVE", "SUSPENDED"] }[act];
      if (!from.includes(v.status)) return err(res, 409, "FEE_POLICY_STATE"), true;
      if (["approve", "reject", "suspend", "reactivate", "retire"].includes(act) && !textOk(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
      if ((act === "approve" || act === "reject") && (v.created_by === user.id || v.submitted_by === user.id)) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if ((act === "approve" || act === "reject") && body.terms_hash && body.terms_hash !== v.terms_hash) return err(res, 409, "FEE_TERMS_CHANGED"), true;
      if (act === "reactivate" && v.suspended_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      if (act === "submit") Object.assign(v, { status: "PENDING_APPROVAL", submitted_by: user.id, submitted_at: iso(), terms_hash: hash() });
      if (act === "approve") Object.assign(v, { status: "APPROVED", approved_by: user.id, approved_at: iso(), approval_reason: body.reason });
      if (act === "reject") Object.assign(v, { status: "REJECTED", rejected_by: user.id, rejection_reason: body.reason });
      if (act === "activate") Object.assign(v, { status: "ACTIVE", activated_by: user.id, activated_at: iso() });
      if (act === "suspend") Object.assign(v, { status: "SUSPENDED", suspended_by: user.id, suspension_reason: body.reason });
      if (act === "reactivate") Object.assign(v, { status: "ACTIVE" });
      if (act === "retire") Object.assign(v, { status: "RETIRED", retired_by: user.id, retirement_reason: body.reason });
      if (["activate", "suspend", "reactivate", "retire"].includes(act)) for (const q of quotes.values()) if (q.status === "ACTIVE") q.status = "INVALIDATED";
      actions.push({ kind: `version.${act}`, id, actor: user.id, terms_hash_bound: !!body.terms_hash });
      return send(res, 200, versionView(v)), true;
    }
    if (path === `${A}/provider-schedules`) {
      if (!need("fee.config.request")) return true;
      const r = idemReplay(req, res, "schedule");
      if (r.done) return true;
      if (body.confirmation_status === "VERIFIED") return err(res, 422, "PROVIDER_FEE_VERIFICATION_UNAVAILABLE"), true;
      if (body.confirmation_status === "SYNTHETIC" && body.provider_id !== SANDBOX_PROVIDER) return err(res, 422, "PROVIDER_FEE_SYNTHETIC_REAL_PROVIDER"), true;
      const s = { id: randomUUID(), provider_id: body.provider_id, provider_is_sandbox: body.provider_id === SANDBOX_PROVIDER, contract_reference: body.contract_reference, schedule_version: body.schedule_version ?? "1",
        currency: body.currency ?? "USD", payment_method: body.payment_method ?? null, fee_type: body.fee_type, model: body.model, rate_bps: body.rate_bps, rounding_mode: body.rounding_mode ?? null,
        rounding_known: !!body.rounding_mode, effective_from: body.effective_from ?? iso(), effective_to: null, evidence_source: body.evidence_source, evidence_reference: body.evidence_reference ?? null,
        confirmation_status: body.confirmation_status, status: "PENDING_APPROVAL", fee_environment: "TEST", created_by: user.id, approved_by: null, created_at: iso() };
      schedules.set(s.id, s);
      r.remember(() => scheduleView(s));
      actions.push({ kind: "schedule.create", id: s.id, actor: user.id });
      return send(res, 201, scheduleView(s)), true;
    }
    if ((m = new RegExp(`^${A}/provider-schedules/(${UUID})/(approve|retire)$`).exec(path))) {
      if (!need(m[2] === "approve" ? "fee.config.approve" : "fee.config.request")) return true;
      const s = schedules.get(m[1]);
      if (!s) return err(res, 404, "FEE_NOT_FOUND"), true;
      if (m[2] === "approve") {
        if (s.status !== "PENDING_APPROVAL") return err(res, 409, "FEE_POLICY_STATE"), true;
        if (s.created_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
        Object.assign(s, { status: "ACTIVE", approved_by: user.id });
      } else {
        if (!textOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
        if (s.status === "RETIRED") return err(res, 409, "FEE_POLICY_STATE"), true;
        s.status = "RETIRED";
      }
      actions.push({ kind: `schedule.${m[2]}`, id: s.id, actor: user.id });
      return send(res, 200, scheduleView(s)), true;
    }
    if ((m = new RegExp(`^${A}/assessments/(${UUID})/adjustments$`).exec(path))) {
      if (!need("fee.adjust.request")) return true;
      const r = idemReplay(req, res, "adjustment");
      if (r.done) return true;
      const a = assessments.get(m[1]);
      if (!a) return err(res, 404, "FEE_NOT_FOUND"), true;
      if (a.fee_minor === null) return err(res, 422, "FEE_AMOUNT_UNKNOWN"), true;
      if (!textOk(body.note) || !/^[1-9]\d{0,17}$/.test(body.amount_minor ?? "")) return err(res, 422, "VALIDATION_FAILED"), true;
      if (body.direction === "DECREASE" && BigInt(body.amount_minor) > BigInt(net(a))) return err(res, 422, "FEE_ADJUSTMENT_EXCEEDS_ASSESSMENT"), true;
      const j = { id: randomUUID(), assessment_id: a.id, currency: a.currency, reason: body.reason, direction: body.direction, amount_minor: body.amount_minor, source_type: "staff_request", source_id: randomUUID(),
        basis_minor: null, calculation: {}, note: body.note, status: "PENDING_APPROVAL", actor_type: "STAFF", requested_by: user.id, decided_by: null, decided_at: null, decision_reason: null,
        accounting_status: "NOT_APPLICABLE", ledger_adjustment_id: null, fee_environment: "TEST", created_at: iso() };
      adjustments.set(j.id, j);
      r.remember(() => j);
      actions.push({ kind: "adjustment.request", id: j.id, actor: user.id });
      return send(res, 201, j), true;
    }
    if ((m = new RegExp(`^${A}/adjustments/(${UUID})/(approve|reject)$`).exec(path))) {
      if (!need("fee.adjust.approve")) return true;
      const j = adjustments.get(m[1]);
      if (!j) return err(res, 404, "FEE_NOT_FOUND"), true;
      if (!textOk(body.reason)) return err(res, 422, "VALIDATION_FAILED"), true;
      if (j.status !== "PENDING_APPROVAL") return err(res, 409, "FEE_ADJUSTMENT_STATE"), true;
      if (j.requested_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
      Object.assign(j, { status: m[2] === "approve" ? "APPROVED" : "REJECTED", decided_by: user.id, decided_at: iso(), decision_reason: body.reason });
      actions.push({ kind: `adjustment.${m[2]}`, id: j.id, actor: user.id });
      return send(res, 200, j), true;
    }
    if (path === `${A}/reconciliation/run`) {
      if (!need("fee.adjust.request")) return true;
      const run = { id: randomUUID(), triggered_by: `staff:${user.id}`, started_at: iso(), finished_at: iso(), checked: assessments.size, matched: assessments.size - [...recon.values()].filter((r) => r.status === "OPEN").length, mismatches: 0, settlement_reconciliation: "NOT_PERFORMED" };
      runs.push(run);
      actions.push({ kind: "reconciliation.run", actor: user.id });
      return send(res, 200, run), true;
    }
    if ((m = new RegExp(`^${A}/reconciliation/(${UUID})/resolve$`).exec(path))) {
      if (!need("fee.adjust.request")) return true;
      const r = recon.get(m[1]);
      if (!r) return err(res, 404, "FEE_NOT_FOUND"), true;
      if (!textOk(body.note)) return err(res, 422, "VALIDATION_FAILED"), true;
      if (r.status !== "OPEN") return err(res, 409, "FEE_RECONCILIATION_STATE"), true;
      Object.assign(r, { status: "RESOLVED", resolved_by: user.id, resolved_at: iso(), resolution_note: body.note });
      actions.push({ kind: "reconciliation.resolve", id: r.id, actor: user.id });
      return send(res, 200, r), true;
    }
    return err(res, 404, "ROUTE_NOT_FOUND"), true;
  }

  async function control(path, body, res) {
    if (path === "/api/v1/__mock/fees/seed") return send(res, 201, seed(body ?? {})), true;
    if (path === "/api/v1/__mock/fees/log") return send(res, 200, { actions }), true;
    return false;
  }
  return { handle, control };
}
