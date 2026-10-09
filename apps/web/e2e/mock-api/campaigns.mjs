// In-memory mock of the Stage 6 campaign API (docs/stage-6/interface-contracts.md §3, §5, §6, §9; ADR-036/037)
// for the E2E suite. Response shapes follow the Go handlers (internal/campaigns/{http,views,public,owner,review}.go,
// internal/campaigns/media, internal/campaigns/updates, internal/verification/age.go, internal/auth/stafflink.go). It enforces the rules the UI must cope with: owner/org access (404 CAMPAIGN_NOT_FOUND for others),
// the ADR-036 state machine (409 INVALID_STATUS), eligibility gates (422 NOT_ELIGIBLE with reasons), plain-text
// content rules (HTML_NOT_ALLOWED, UNSAFE_LINK), digit-string money, If-Match versions, staff permissions
// (404 for non-staff, 403 without the permission), step-up for decisions, assignment, four eyes for HIGH risk,
// self-decision via linked accounts, media processing states, and public visibility. NOT a security reference.
import { randomBytes, randomUUID } from "node:crypto";

import { parseMultipart } from "./verification.mjs";

const MAX_UPLOAD = 10 * 1024 * 1024;
const MALWARE_MARKER = "FUNDZIM-MOCK-MALWARE-MARKER";
const GOAL_MAX_MINOR = 100_000_000n; // 1,000,000.00 (mock policy value)
const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

const CATEGORIES = [
  { code: "MEDICAL", name: "Medical", description: "Treatment, surgery and care costs.", requires_organisation: false, risk_tier: "STANDARD", active: true },
  { code: "EDUCATION", name: "Education", description: "School fees, uniforms and learning costs.", requires_organisation: false, risk_tier: "STANDARD", active: true },
  { code: "FUNERAL", name: "Funeral", description: "Funeral and memorial costs.", requires_organisation: false, risk_tier: "STANDARD", active: true },
  { code: "COMMUNITY", name: "Community", description: "Projects that benefit a community.", requires_organisation: false, risk_tier: "ELEVATED", active: true },
  { code: "CHILD_WELFARE", name: "Children's welfare", description: "Support for a child's wellbeing.", requires_organisation: false, risk_tier: "HIGH", active: true },
  { code: "CHARITY", name: "Registered charity", description: "Programmes run by a verified organisation.", requires_organisation: true, risk_tier: "ELEVATED", active: true },
];
const CURRENCIES = [
  { code: "USD", minor_units: 2, display_symbol: "US$", available: true },
  { code: "ZWG", minor_units: 2, display_symbol: "ZiG", available: false }, // LR-043: minor units not verified
];

/** Stage 6 permissions per role (contract §8 + the Stage 4 seed). KYC_REVIEWER has none of these. */
const ROLE_PERMISSIONS = {
  REVIEWER: ["campaign.view", "campaign.review", "campaign.decide", "campaign.suspend", "campaign.unsuspend", "campaign.publish"],
  COMPLIANCE: ["campaign.view", "campaign.review", "campaign.decide", "campaign.decide.high", "campaign.suspend", "campaign.unsuspend", "campaign.publish"],
  ADMIN: ["campaign.view", "content.moderate", "campaign.category.manage"],
  SUPPORT: ["campaign.view"],
};
const ACTION_PERMISSION = {
  assign: "campaign.review", "start-review": "campaign.review", "request-changes": "campaign.review", escalate: "campaign.review",
  approve: "campaign.decide", reject: "campaign.decide", reopen: "campaign.decide", "second-approval": "campaign.decide.high",
  publish: "campaign.publish", suspend: "campaign.suspend", cancel: "campaign.suspend", reactivate: "campaign.unsuspend",
};
const STEP_UP_ACTIONS = new Set(["approve", "reject", "reopen", "second-approval", "publish", "suspend", "cancel", "reactivate"]);
const DECISION_ACTIONS = new Set(["request-changes", "approve", "second-approval", "reject", "escalate", "suspend", "reactivate", "publish", "reopen", "cancel"]);

const PUBLIC_STATUSES = new Set(["ACTIVE", "PAUSED", "COMPLETED"]);
const EDITABLE = new Set(["DRAFT", "CHANGES_REQUESTED", "ACTIVE", "PAUSED"]);
const VERIFIED_LEVELS = new Set(["IDENTITY_VERIFIED", "PAYOUT_VERIFIED"]);
const now = () => new Date().toISOString();

function publicCode() {
  const bytes = randomBytes(10);
  return Array.from(bytes, (b) => CROCKFORD[b % 32]).join("");
}
function slugify(title, code) {
  const base = title.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60).replace(/-+$/g, "") || "campaign";
  return `${base}-${code.toLowerCase()}`;
}
function sniffImage(buf) {
  if (buf.length >= 8 && buf[0] === 0x89 && buf.subarray(1, 4).toString("latin1") === "PNG") return "image/png";
  if (buf.length >= 3 && buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff) return "image/jpeg";
  if (buf.length >= 12 && buf.subarray(0, 4).toString("latin1") === "RIFF" && buf.subarray(8, 12).toString("latin1") === "WEBP") return "image/webp";
  return null;
}
/** A 1×1 PNG used as the processed derivative for every approved image. */
const PIXEL = Buffer.from("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d4944415478da63f8cfc0f01f0005000201a5e0c6d40000000049454e44ae426082", "hex");

function contentProblem(value) {
  if (/<\s*\/?\s*[a-zA-Z!][^>]*>/.test(value)) return "HTML_NOT_ALLOWED";
  if (/\b(?:javascript|data|vbscript)\s*:/i.test(value)) return "UNSAFE_LINK";
  if (/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/.test(value)) return "CONTROL_CHARACTERS";
  return null;
}
const LIMITS = { title: [10, 120], summary: [20, 300], story: [100, 20000] };

export function createCampaignsMock({ send, err, userById, users, verification, stepUpTtlMs }) {
  const campaigns = new Map();
  const media = new Map();
  const updates = new Map();
  const attestations = new Map(); // userId -> [{outcome, ...}]
  const idempotency = new Map(); // `${userId}:${key}` -> campaignId
  const restricted = new Set(); // userIds with a compliance restriction (generic)
  const staffLinks = new Map(); // staffId -> {email, token, status, requested_at, linked_at, personalUserId}

  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const ref = (user) => ({ id: user.id, display_name: user.display_name });
  const level = (userId) => verification.getProfile(userId).level;
  const attestation = (userId) => (attestations.get(userId) ?? []).at(-1) ?? null;
  const category = (code) => CATEGORIES.find((c) => c.code === code && c.active);

  function recordAttestation(userId, outcome, source) {
    const list = attestations.get(userId) ?? [];
    list.push({ outcome, statement_version: "v1", adult_age: 18, source, attested_at: now(), assurance: "SELF_ATTESTED" });
    attestations.set(userId, list);
    const user = userById(userId);
    const current = level(userId);
    if (outcome === "ATTESTED" && current === "UNVERIFIED" && user?.email_verified && user?.phone_verified) verification.setLevel(userId, "BASIC_VERIFIED");
    if (outcome === "DECLINED" && current === "BASIC_VERIFIED") verification.setLevel(userId, "UNVERIFIED");
  }

  // ---- media ----
  function mediaStatus(m) {
    if (m.removed) return "REMOVED";
    const age = Date.now() - m.createdMs;
    if (age < 300) return "UPLOADED";
    if (age < 700) return "QUARANTINED";
    if (age < 1500) return "SCANNING";
    return m.malicious ? "REJECTED" : "APPROVED";
  }
  const mediaView = (m) => {
    const status = mediaStatus(m);
    return {
      id: m.id, campaign_id: m.campaignId, kind: m.kind, position: m.position, alt_text: m.alt_text, status, ...(status === "REJECTED" ? { rejected_reason: "MALWARE_DETECTED" } : {}),
      ...(status === "APPROVED" ? { content_type: "image/png", width: 1, height: 1, content_url: `/api/v1/campaigns/${m.campaignId}/media/${m.id}/content` } : {}),
      ...(m.removed ? { removed_at: m.removed_at ?? now(), removed_reason: m.removed_reason ?? "REMOVED_BY_OWNER" } : {}), version: 1, created_at: m.created_at, updated_at: m.created_at,
    };
  };
  const mediaOf = (c) => [...media.values()].filter((m) => m.campaignId === c.id);
  const liveMedia = (c) => mediaOf(c).filter((m) => mediaStatus(m) !== "REMOVED");
  const coverApproved = (c) => liveMedia(c).some((m) => m.kind === "COVER" && mediaStatus(m) === "APPROVED");

  // ---- access ----
  function isMember(user, c) {
    if (!user || user.account_kind === "STAFF") return false;
    if (c.ownerUserId) return c.ownerUserId === user.id;
    return !!verification.getOrg(c.orgId)?.members.has(user.id);
  }
  function canWrite(user, c) {
    if (c.ownerUserId) return c.ownerUserId === user.id;
    return verification.getOrg(c.orgId)?.members.get(user.id) === "ORG_ADMIN";
  }
  function ownerUser(c) {
    return c.ownerUserId ? userById(c.ownerUserId) : userById(c.created_by);
  }
  function involves(staff, c) {
    const link = staffLinks.get(staff.id);
    if (!link || link.status !== "LINKED") return false;
    const personal = userById(link.personalUserId);
    return personal ? isMember(personal, c) : false;
  }

  // ---- eligibility (contract §5) ----
  function evaluate(action, user, c) {
    const reasons = [];
    const add = (code, field = null) => reasons.push({ code, ...(field ? { field } : {}), message: code.toLowerCase().replaceAll("_", " ") });
    if (!user)
      return { action, allowed: false, reasons: [{ code: "NOT_AUTHENTICATED", message: "" }], policy_version: "1" };
    if (restricted.has(user.id) || (c && restricted.has(c.id))) add("ACCOUNT_RESTRICTED");
    const lv = level(user.id);
    if (lv === "UNVERIFIED") add(attestation(user.id)?.outcome === "ATTESTED" ? "BASIC_VERIFICATION_REQUIRED" : "AGE_ATTESTATION_REQUIRED");
    if (["SUBMIT_FOR_REVIEW", "APPROVE", "PUBLISH", "REACTIVATE"].includes(action) && c) {
      if (!VERIFIED_LEVELS.has(lv) && lv !== "UNVERIFIED") add("IDENTITY_VERIFICATION_REQUIRED");
      if (lv === "UNVERIFIED") add("IDENTITY_VERIFICATION_REQUIRED");
      if (c.orgId && verification.getOrg(c.orgId)?.level !== "ORG_VERIFIED") add("ORGANISATION_VERIFICATION_REQUIRED");
      if (category(c.category)?.requires_organisation && !c.orgId) add("CATEGORY_REQUIRES_ORGANISATION");
      for (const f of ["title", "summary", "story"]) if ([...(c[f] ?? "")].length < LIMITS[f][0]) add("MISSING_FIELD", f);
      if (!c.beneficiary) add("BENEFICIARY_REQUIRED");
      else if (action !== "SUBMIT_FOR_REVIEW" && verification.getBeneficiary(c.beneficiary.beneficiary_id)?.status !== "APPROVED") add("BENEFICIARY_NOT_VERIFIED");
      if (!coverApproved(c)) add("COVER_IMAGE_REQUIRED");
      if (liveMedia(c).some((m) => ["UPLOADED", "QUARANTINED", "SCANNING"].includes(mediaStatus(m)))) add("MEDIA_NOT_READY");
      if (action === "SUBMIT_FOR_REVIEW" && c.resubmission_count >= 3) add("RESUBMISSION_LIMIT_REACHED");
    }
    return { action, allowed: reasons.length === 0, reasons, policy_version: "1" };
  }

  function beneficiaryView(c) {
    if (!c.beneficiary) return null;
    const b = verification.getBeneficiary(c.beneficiary.beneficiary_id);
    return { id: c.beneficiary.link_id, ...c.beneficiary, display_name: b?.display_name ?? null, beneficiary_type: b?.beneficiary_type ?? null, verification_status: b?.status ?? null };
  }

  function ownerView(c) {
    return {
      id: c.id, public_code: c.public_code, slug: c.slug, status: c.status, visibility: c.visibility, category: c.category,
      organisation_id: c.orgId, owner: c.orgId ? { type: "ORGANISATION", id: c.orgId } : { type: "USER", id: c.ownerUserId },
      title: c.title, summary: c.summary, story: c.story, goal: { amount_minor: c.goal_amount_minor, currency: c.goal_currency },
      re_review_pending: c.re_review_required, resubmission_count: c.resubmission_count, donations: { available: false, message: "Donations are not yet available." },
      beneficiary: beneficiaryView(c), feedback: c.review_feedback,
      completion_reason: c.completion_reason, created_at: c.created_at, submitted_at: c.submitted_at, approved_at: c.approved_at,
      published_at: c.published_at, paused_at: c.paused_at, completed_at: c.completed_at, cancelled_at: c.cancelled_at, archived_at: c.archived_at,
      updated_at: c.updated_at, version: c.version,
    };
  }

  function transition(c, to, actor, extra = {}) {
    c.history.push({ version: c.version + 1, event_type: "STATUS_CHANGED", from_status: c.status, to_status: to, actor_type: actor ? (actor.account_kind === "STAFF" ? "STAFF" : "USER") : "SYSTEM", actor_id: actor?.id ?? null, reason_code: extra.reason_code ?? null, occurred_at: now() });
    c.status = to;
    c.version += 1;
    c.updated_at = now();
  }
  function snapshot(c) {
    c.approved = { title: c.title, summary: c.summary, story: c.story, category: c.category, goal_amount_minor: c.goal_amount_minor, goal_currency: c.goal_currency, beneficiary: c.beneficiary ? { ...c.beneficiary } : null, media_ids: liveMedia(c).filter((m) => mediaStatus(m) === "APPROVED").map((m) => m.id) };
  }

  // ---- validation ----
  function validateContent(body, partial) {
    const details = [];
    for (const f of ["title", "summary", "story"]) {
      if (body[f] === undefined) {
        if (!partial && f !== "story") details.push({ field: f, code: "REQUIRED" });
        continue;
      }
      if (typeof body[f] !== "string") { details.push({ field: f, code: "INVALID_FORMAT" }); continue; }
      const n = [...body[f].trim()].length;
      if (n < LIMITS[f][0] || n > LIMITS[f][1]) details.push({ field: f, code: "INVALID_LENGTH" });
      const problem = contentProblem(body[f]);
      if (problem) details.push({ field: f, code: problem });
    }
    if (body.category !== undefined && !category(body.category)) details.push({ field: "category", code: "INVALID_VALUE" });
    if (!partial && body.category === undefined) details.push({ field: "category", code: "REQUIRED" });
    if (body.goal !== undefined || !partial) {
      const g = body.goal;
      if (!g || typeof g.amount_minor !== "string" || !/^[1-9][0-9]{0,18}$/.test(g.amount_minor)) details.push({ field: "goal.amount_minor", code: "INVALID_FORMAT" });
      if (!g || typeof g.currency !== "string") details.push({ field: "goal.currency", code: "REQUIRED" });
    }
    if (body.visibility !== undefined && !["PUBLIC", "UNLISTED"].includes(body.visibility)) details.push({ field: "visibility", code: "INVALID_VALUE" });
    return details;
  }
  function goalProblem(g) {
    if (!g) return null;
    const cur = CURRENCIES.find((x) => x.code === g.currency);
    if (!cur || !cur.available) return "CURRENCY_NOT_AVAILABLE";
    if (BigInt(g.amount_minor) > GOAL_MAX_MINOR) return "GOAL_OUT_OF_RANGE";
    return null;
  }

  function newCampaign(user, body) {
    const code = publicCode();
    const c = {
      id: randomUUID(), public_code: code, slug: null, ownerUserId: body.organisation_id ? null : user.id, orgId: body.organisation_id ?? null, created_by: user.id,
      category: body.category, title: body.title.trim(), summary: body.summary.trim(), story: "", goal_amount_minor: body.goal.amount_minor, goal_currency: body.goal.currency,
      status: "DRAFT", visibility: "PUBLIC", risk_tier: category(body.category)?.risk_tier ?? "STANDARD", beneficiary: null,
      review: { id: randomUUID(), assigned_to: null, pending_outcome: null, pending_decided_by: null, escalated: false, compliance_case_id: null },
      review_feedback: null, history: [], approved: null, resubmission_count: 0, re_review_required: false, completion_reason: null,
      created_at: now(), submitted_at: null, approved_at: null, published_at: null, paused_at: null, completed_at: null, cancelled_at: null, archived_at: null, updated_at: now(), version: 1,
    };
    c.history.push({ version: 1, event_type: "CREATED", from_status: null, to_status: "DRAFT", actor_type: "USER", actor_id: user.id, reason_code: null, occurred_at: now() });
    campaigns.set(c.id, c);
    return c;
  }

  // ---- views for staff and public ----
  function summary(c) {
    return { id: c.id, slug: c.slug ?? "", title: c.title, category: c.category, goal: { amount_minor: c.goal_amount_minor, currency: c.goal_currency }, status: c.status, owner: c.orgId ? { type: "ORGANISATION", id: c.orgId } : { type: "USER", id: c.ownerUserId }, created_at: c.created_at, submitted_at: c.submitted_at, published_at: c.published_at, version: c.version };
  }
  function staffSummary(c) {
    return {
      campaign_id: c.id, review_id: c.review.id, title: c.title, category: c.category, status: c.status, review_status: c.status, review_kind: c.resubmission_count > 0 ? "RESUBMISSION" : "INITIAL",
      risk_tier: c.risk_tier, assigned_to: c.review.assigned_to?.id ?? null, escalated: c.review.escalated, awaiting_second_approval: !!c.review.pending_decided_by, queued_at: c.submitted_at,
    };
  }
  /** Contract (coordinator update): {campaign, risk_tier, owner, beneficiary, restriction, media, review, versions, history, eligibility {APPROVE, PUBLISH}, policy}. */
  function staffDetail(c) {
    const b = beneficiaryView(c);
    const owner = ownerUser(c);
    return {
      campaign: ownerView(c),
      risk_tier: c.risk_tier,
      owner: { type: c.orgId ? "ORGANISATION" : "USER", id: c.orgId ?? c.ownerUserId, display_name: c.orgId ? verification.getOrg(c.orgId)?.display_name : owner?.display_name },
      beneficiary: b ? { id: b.beneficiary_id, type: b.beneficiary_type, display_name: b.display_name, verification_status: b.verification_status, disclosure: b.disclosure, consent_declared: b.consent_declared } : null,
      restriction: restricted.has(c.ownerUserId) || restricted.has(c.id) ? "RESTRICTED" : "",
      media: { CoverApproved: coverApproved(c), Pending: 0, Rejected: 0 },
      review: { id: c.review.id, kind: c.resubmission_count > 0 ? "RESUBMISSION" : "INITIAL", status: "IN_REVIEW", policy_version: "1", risk_tier: c.risk_tier, assigned_to: c.review.assigned_to?.id ?? null, pending_decided_by: c.review.pending_decided_by, requires_second_approval: c.risk_tier === "HIGH", escalated: c.review.escalated, version_id: null },
      versions: { reviewed: null, approved: c.approved ? "approved" : null },
      history: c.history,
      eligibility: { APPROVE: evaluate("APPROVE", owner, c), PUBLISH: evaluate("PUBLISH", owner, c) },
      policy: { version: "1", screening: "NOT_PERFORMED" },
    };
  }
  const isPublic = (c) => PUBLIC_STATUSES.has(c.status) && c.approved && c.slug;
  function publicView(c) {
    const a = c.approved;
    const owner = ownerUser(c);
    const b = a.beneficiary ? verification.getBeneficiary(a.beneficiary.beneficiary_id) : null;
    const approvedMedia = a.media_ids.map((id) => media.get(id)).filter((m) => m && mediaStatus(m) === "APPROVED");
    const cat = category(a.category) ?? { code: a.category, name: a.category };
    const disclosed = a.beneficiary?.disclosure === "DISPLAY_NAME" && b && b.beneficiary_type !== "MINOR";
    const ids = [...approvedMedia.filter((m) => m.kind === "COVER"), ...approvedMedia.filter((m) => m.kind !== "COVER")].map((m) => m.id);
    return {
      slug: c.slug, title: a.title, summary: a.summary, story: a.story, category: cat.code,
      goal: { amount_minor: a.goal_amount_minor, currency: a.goal_currency }, status: c.status,
      organiser: c.orgId ? { type: "ORGANISATION", display_name: verification.getOrg(c.orgId)?.display_name } : { type: "INDIVIDUAL", display_name: owner?.display_name ?? "Organiser" },
      beneficiary: disclosed ? { disclosed: true, display_name: b.display_name } : { disclosed: false },
      cover_media_id: ids[0] ?? null, media_ids: ids,
      published_at: c.published_at, completed_at: c.completed_at, donations: { available: false, message: "Donations are not yet available." },
    };
  }

  function paginate(res, list, url, mapper, defaultLimit = 25, itemsShape = false) {
    const limit = Math.min(Math.max(parseInt(url.searchParams.get("limit") ?? String(defaultLimit), 10) || defaultLimit, 1), 100);
    const cursor = url.searchParams.get("cursor");
    let offset = 0;
    if (cursor) {
      const m = /^o:(\d+)$/.exec(cursor);
      if (!m) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "cursor", code: "INVALID_FORMAT" }] });
      offset = Number(m[1]);
    }
    const page = list.slice(offset, offset + limit).map(mapper);
    const requestId = randomUUID();
    res.writeHead(200, { "Content-Type": "application/json", "X-Request-ID": requestId, "Cache-Control": "no-store" });
    const nextCursor = offset + limit < list.length ? `o:${offset + limit}` : null;
    const data = itemsShape ? { items: page, next_cursor: nextCursor } : page;
    res.end(JSON.stringify({ data, meta: { request_id: requestId, limit, ...(nextCursor && !itemsShape ? { next_cursor: nextCursor } : {}) } }));
  }

  function sendImage(res, contentType = "image/png") {
    res.writeHead(200, { "Content-Type": contentType, "Cache-Control": "public, max-age=60", "X-Content-Type-Options": "nosniff" });
    res.end(PIXEL);
  }

  // =====================================================================================================
  async function handle({ req, res, method, path, url, user, session, body, raw }) {
    if (!path.startsWith("/api/v1/")) return false;
    const p = path.slice("/api/v1".length);
    const seg = p.split("/").filter(Boolean);
    const stepUpFresh = () => session && Date.now() - session.stepUpAt <= stepUpTtlMs;
    const personal = () => {
      if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), false;
      if (user.account_kind === "STAFF") return err(res, 404, "CAMPAIGN_NOT_FOUND"), false;
      return true;
    };

    // ---------------------------------------------------------------- reference data (public)
    if (p === "/campaign-categories" && method === "GET") return send(res, 200, CATEGORIES.filter((c) => c.active).map((c) => ({ code: c.code, name: c.name, description: c.description, requires_organisation: c.requires_organisation }))), true;
    if (p === "/campaign-currencies" && method === "GET") return send(res, 200, CURRENCIES.filter((c) => c.available).map((c) => ({ code: c.code, minor_units: c.minor_units, display_symbol: c.display_symbol }))), true;

    // ---------------------------------------------------------------- age attestation
    if (p === "/me/age-attestation") {
      if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
      const ageView = (unmet) => {
        const a = attestation(user.id);
        return { adult_age: 18, statement_version: "v1", attestation: a, assurance: a?.outcome === "ATTESTED" ? "SELF_ATTESTED" : "NONE", verified_dob_below_adult_age: false, level: level(user.id), ...(unmet ? { basic_unmet: unmet } : {}) };
      };
      if (method === "GET") return send(res, 200, ageView()), true;
      if (method === "POST") {
        if (!["ATTESTED", "DECLINED"].includes(body.outcome)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "outcome", code: "INVALID_VALUE" }] }), true;
        if (body.statement_version !== "v1") return err(res, 422, "STATEMENT_VERSION_NOT_CURRENT"), true;
        recordAttestation(user.id, body.outcome, "DASHBOARD");
        const unmet = [...(user.email_verified ? [] : ["EMAIL_NOT_VERIFIED"]), ...(user.phone_verified ? [] : ["PHONE_NOT_VERIFIED"]), ...(attestation(user.id)?.outcome === "ATTESTED" ? [] : ["AGE_ATTESTATION_REQUIRED"])];
        return send(res, 201, ageView(unmet)), true;
      }
    }

    // ---------------------------------------------------------------- staff links
    if (p === "/admin/me/personal-account-link") {
      if (!user || user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      const link = staffLinks.get(user.id);
      const masked = (l) => l.email.replace(/^(.).*(@.*)$/, "$1•••$2");
      const view = (l) => ({ linked: l?.status === "LINKED", personal_email_masked: l?.status === "LINKED" ? masked(l) : null, linked_at: l?.linked_at ?? null, pending: l?.status === "PENDING" ? { email_masked: masked(l), expires_at: l.expires_at } : null });
      if (method === "GET") return send(res, 200, view(link)), true;
      if (method === "POST") {
        if (!stepUpFresh()) return err(res, 403, "STEP_UP_REQUIRED"), true;
        if (link?.status === "LINKED") return err(res, 409, "STAFF_ALREADY_LINKED"), true;
        if (typeof body.email !== "string" || !body.email.includes("@")) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "email", code: "INVALID_EMAIL" }] }), true;
        const next = { email: body.email, token: randomBytes(32).toString("base64url"), status: "PENDING", requested_at: now(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), linked_at: null, personalUserId: null };
        staffLinks.set(user.id, next);
        return send(res, 202, { status: "confirmation_sent_if_eligible", expires_at: next.expires_at }), true;
      }
    }
    if (p === "/me/staff-link/confirm" && method === "POST") {
      if (!personal()) return true;
      const entry = [...staffLinks.entries()].find(([, l]) => l.token === body.token && l.status === "PENDING");
      if (!entry) return err(res, 400, "TOKEN_INVALID"), true;
      if (entry[1].email !== user.email || !user.email_verified) return err(res, 403, "STAFF_LINK_EMAIL_MISMATCH"), true;
      entry[1].status = "LINKED";
      entry[1].linked_at = now();
      entry[1].personalUserId = user.id;
      return send(res, 200, { status: "linked", staff_user_id: entry[0] }), true;
    }

    // ---------------------------------------------------------------- public
    if (seg[0] === "public" && seg[1] === "campaigns") {
      if (method !== "GET") return err(res, 405, "METHOD_NOT_ALLOWED"), true;
      if (seg.length === 2) {
        const q = (url.searchParams.get("q") ?? "").toLowerCase();
        const cat = url.searchParams.get("category");
        const sort = url.searchParams.get("sort") === "created" ? "created_at" : "published_at";
        const list = [...campaigns.values()]
          .filter((c) => isPublic(c) && c.visibility === "PUBLIC")
          .filter((c) => !cat || c.approved.category === cat)
          .filter((c) => !q || `${c.approved.title} ${c.approved.summary}`.toLowerCase().includes(q))
          .sort((a, b) => (a[sort] < b[sort] ? 1 : -1));
        paginate(res, list, url, (c) => {
          const v = publicView(c);
          return { slug: v.slug, title: v.title, summary: v.summary, category: v.category, goal: v.goal, status: v.status, cover_media_id: v.cover_media_id, published_at: v.published_at, created_at: c.created_at };
        }, 12, true);
        return true;
      }
      const c = [...campaigns.values()].find((x) => x.slug === seg[2]);
      if (!c || !isPublic(c)) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      if (seg.length === 3) return send(res, 200, publicView(c)), true;
      if (seg[3] === "updates" && seg.length === 4) return send(res, 200, { updates: [...updates.values()].filter((u) => u.campaignId === c.id && u.status === "PUBLISHED").map((u) => ({ id: u.id, title: u.title, body: u.body, media_ids: [], published_at: u.published_at, organiser_name: ownerUser(c)?.display_name })), next_cursor: null }), true;
      if (seg[3] === "media" && seg[4] && seg.length === 5) {
        const m = media.get(seg[4]);
        if (!m || m.campaignId !== c.id || !c.approved.media_ids.includes(m.id) || mediaStatus(m) !== "APPROVED") return err(res, 404, "MEDIA_NOT_FOUND"), true;
        return sendImage(res), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }

    // ---------------------------------------------------------------- owner
    if (p === "/campaigns" && method === "POST") {
      if (!personal()) return true;
      const key = req.headers["idempotency-key"];
      if (key && idempotency.has(`${user.id}:${key}`)) return send(res, 201, ownerView(campaigns.get(idempotency.get(`${user.id}:${key}`)))), true;
      const details = validateContent(body, false);
      if (details.length) return err(res, 422, "VALIDATION_FAILED", { details }), true;
      const gp = goalProblem(body.goal);
      if (gp) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: gp === "GOAL_OUT_OF_RANGE" ? "goal.amount_minor" : "goal.currency", code: gp }] }), true;
      if (body.organisation_id && verification.getOrg(body.organisation_id)?.members.get(user.id) !== "ORG_ADMIN") return err(res, 422, "NOT_ELIGIBLE", { details: [{ code: "NOT_ORGANISATION_ADMIN", field: null, message: "x" }] }), true;
      const ev = evaluate("CREATE_DRAFT", user, null);
      if (!ev.allowed) return err(res, 422, "NOT_ELIGIBLE", { details: ev.reasons }), true;
      const c = newCampaign(user, body);
      if (key) idempotency.set(`${user.id}:${key}`, c.id);
      return send(res, 201, ownerView(c), { ETag: `"${c.version}"` }), true;
    }
    if (p === "/campaigns/mine" && method === "GET") {
      if (!personal()) return true;
      const org = url.searchParams.get("organisation_id");
      const list = [...campaigns.values()].filter((c) => isMember(user, c) && (!org || c.orgId === org) && c.status !== "ARCHIVED").sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
      return send(res, 200, list.map(summary)), true;
    }
    if (seg[0] === "campaigns" && seg[1]) {
      if (!personal()) return true;
      const c = campaigns.get(seg[1]);
      if (!c || !isMember(user, c)) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      const write = method !== "GET";
      if (write && !canWrite(user, c)) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      const etag = { ETag: `"${c.version}"` };

      if (seg.length === 2 && method === "GET") return send(res, 200, ownerView(c), etag), true;
      if (seg.length === 3 && seg[2] === "eligibility" && method === "GET") {
        const action = url.searchParams.get("action");
        if (!["CREATE_DRAFT", "EDIT_DRAFT", "SUBMIT_FOR_REVIEW", "PUBLISH", "UPDATE_PUBLISHED", "REACTIVATE"].includes(action)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "action", code: "INVALID_VALUE" }] }), true;
        return send(res, 200, evaluate(action, user, c)), true;
      }
      if (seg.length === 2 && method === "PATCH") {
        const ifMatch = req.headers["if-match"];
        if (!ifMatch) return err(res, 422, "IF_MATCH_REQUIRED"), true;
        if (ifMatch.replace(/"/g, "") !== String(c.version)) return err(res, 409, "CAMPAIGN_STATE_CHANGED"), true;
        const onlyVisibility = Object.keys(body).length === 1 && body.visibility !== undefined;
        if (!onlyVisibility && !EDITABLE.has(c.status)) return err(res, 409, "INVALID_STATUS"), true;
        if (onlyVisibility && ["CANCELLED", "ARCHIVED", "REJECTED", "SUSPENDED"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
        const details = validateContent(body, true);
        if (details.length) return err(res, 422, "VALIDATION_FAILED", { details }), true;
        const gp = goalProblem(body.goal);
        if (gp) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: gp === "GOAL_OUT_OF_RANGE" ? "goal.amount_minor" : "goal.currency", code: gp }] }), true;
        for (const f of ["title", "summary", "story"]) if (body[f] !== undefined) c[f] = body[f].trim();
        if (body.category !== undefined) c.category = body.category;
        if (body.goal) { c.goal_amount_minor = body.goal.amount_minor; c.goal_currency = body.goal.currency; }
        if (body.visibility) c.visibility = body.visibility;
        if (["ACTIVE", "PAUSED"].includes(c.status) && !onlyVisibility) c.re_review_required = true;
        c.version += 1;
        c.updated_at = now();
        return send(res, 200, ownerView(c), { ETag: `"${c.version}"` }), true;
      }

      // lifecycle
      const LIFE = {
        submit: { from: ["DRAFT", "CHANGES_REQUESTED"], to: "SUBMITTED", ev: "SUBMIT_FOR_REVIEW" },
        withdraw: { from: ["SUBMITTED"], to: "DRAFT" },
        cancel: { from: ["DRAFT", "CHANGES_REQUESTED", "APPROVED", "ACTIVE", "PAUSED"], to: "CANCELLED" },
        publish: { from: ["APPROVED"], to: "ACTIVE", ev: "PUBLISH" },
        pause: { from: ["ACTIVE"], to: "PAUSED" },
        resume: { from: ["PAUSED"], to: "ACTIVE", ev: "REACTIVATE" },
        complete: { from: ["ACTIVE", "PAUSED"], to: "COMPLETED" },
        archive: { from: ["COMPLETED", "CANCELLED", "REJECTED"], to: "ARCHIVED" },
        revise: { from: ["REJECTED"], to: "DRAFT" },
      };
      if (seg.length === 3 && method === "POST" && LIFE[seg[2]]) {
        const rule = LIFE[seg[2]];
        if (!rule.from.includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
        if (seg[2] === "complete" && !["ORGANISER_COMPLETED", "OTHER"].includes(body.reason)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_VALUE" }] }), true;
        if (rule.ev) {
          const ev = evaluate(rule.ev, user, c);
          if (!ev.allowed) return err(res, 422, "NOT_ELIGIBLE", { details: ev.reasons }), true;
        }
        if (seg[2] === "revise" && c.resubmission_count >= 3) return err(res, 422, "NOT_ELIGIBLE", { details: [{ code: "RESUBMISSION_LIMIT_REACHED", field: null, message: "x" }] }), true;
        if (seg[2] === "submit") {
          if (c.status === "CHANGES_REQUESTED" || c.review_feedback?.outcome === "REJECTED") c.resubmission_count += 1;
          c.submitted_at = now();
          c.review = { ...c.review, id: randomUUID(), assigned_to: null, pending_outcome: null, pending_decided_by: null };
        }
        if (seg[2] === "publish") { c.slug = c.slug ?? slugify(c.approved.title, c.public_code); c.published_at = now(); }
        if (seg[2] === "pause") c.paused_at = now();
        if (seg[2] === "complete") { c.completed_at = now(); c.completion_reason = body.reason; }
        if (seg[2] === "cancel") c.cancelled_at = now();
        if (seg[2] === "archive") c.archived_at = now();
        transition(c, rule.to, user);
        return send(res, 200, ownerView(c), { ETag: `"${c.version}"` }), true;
      }

      // beneficiaries
      if (seg[2] === "beneficiaries") {
        if (seg.length === 3 && method === "POST") {
          if (!["DRAFT", "CHANGES_REQUESTED", "ACTIVE", "PAUSED"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          const b = verification.getBeneficiary(body.beneficiary_id);
          const sameOwner = b && (c.orgId ? b.owner.type === "ORGANISATION" && b.owner.id === c.orgId : b.owner.type === "USER" && b.owner.id === c.ownerUserId);
          if (!["NONE", "DISPLAY_NAME"].includes(body.disclosure)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "disclosure", code: "INVALID_VALUE" }] }), true;
          if (body.disclosure === "DISPLAY_NAME" && body.consent_declared !== true) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "consent_declared", code: "CONSENT_REQUIRED" }] }), true;
          if (!sameOwner) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "beneficiary_id", code: "BENEFICIARY_NOT_AUTHORISED" }] }), true;
          if (c.beneficiary && c.beneficiary.beneficiary_id !== b.id && ["ACTIVE", "PAUSED"].includes(c.status) && !body.reason) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "REQUIRED" }] }), true;
          c.beneficiary = { link_id: randomUUID(), beneficiary_id: b.id, disclosure: body.disclosure, consent_declared: body.consent_declared === true };
          if (["ACTIVE", "PAUSED"].includes(c.status)) c.re_review_required = true;
          c.version += 1;
          return send(res, 200, ownerView(c), { ETag: `"${c.version}"` }), true;
        }
        if (seg.length === 4 && method === "DELETE") {
          if (c.status !== "DRAFT") return err(res, 409, "INVALID_STATUS"), true;
          c.beneficiary = null;
          c.version += 1;
          return send(res, 204, null), true;
        }
      }

      // media
      if (seg[2] === "media") {
        if (seg.length === 3 && method === "GET") return send(res, 200, { media: mediaOf(c).map(mediaView) }), true;
        if (seg.length === 3 && method === "POST") {
          if (!EDITABLE.has(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          const form = parseMultipart(raw, req.headers["content-type"]);
          if (!form || !form.file) return err(res, 400, "MALFORMED_REQUEST"), true;
          const kind = form.fields.kind;
          const alt = (form.fields.alt_text ?? "").trim();
          if (!["COVER", "GALLERY"].includes(kind)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "kind", code: "INVALID_VALUE" }] }), true;
          if (!["true", "false"].includes(form.fields.depicts_minor)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "depicts_minor", code: "INVALID_VALUE" }] }), true;
          if (form.fields.depicts_minor === "true") return err(res, 422, "MINOR_MEDIA_NOT_SUPPORTED"), true;
          if (alt.length < 3 || alt.length > 250 || contentProblem(alt)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "alt_text", code: "INVALID_LENGTH" }] }), true;
          if (kind === "COVER" && liveMedia(c).some((m) => m.kind === "COVER" && mediaStatus(m) !== "REJECTED")) return err(res, 409, "COVER_ALREADY_EXISTS"), true;
          const content = form.file.content;
          if (content.length === 0) return err(res, 422, "EMPTY_FILE"), true;
          if (content.length > MAX_UPLOAD) return err(res, 413, "FILE_TOO_LARGE"), true;
          const type = sniffImage(content);
          if (!type || type === "image/webp") return err(res, 422, "UNSUPPORTED_FILE_TYPE"), true;
          const m = { id: randomUUID(), campaignId: c.id, kind, position: Number(form.fields.position ?? 0) || 0, alt_text: alt, created_at: now(), createdMs: Date.now(), malicious: content.includes(Buffer.from(MALWARE_MARKER)), removed: false };
          media.set(m.id, m);
          return send(res, 201, mediaView(m)), true;
        }
        const m = seg[3] ? media.get(seg[3]) : null;
        if (!m || m.campaignId !== c.id) return err(res, 404, "MEDIA_NOT_FOUND"), true;
        if (seg.length === 5 && seg[4] === "content" && method === "GET") return mediaStatus(m) === "APPROVED" ? (sendImage(res), true) : (err(res, 404, "MEDIA_NOT_AVAILABLE"), true);
        if (seg.length === 4 && method === "PATCH") {
          if (typeof body.alt_text === "string") m.alt_text = body.alt_text.trim();
          if (Number.isInteger(body.position)) m.position = body.position;
          return send(res, 200, mediaView(m)), true;
        }
        if (seg.length === 4 && method === "DELETE") {
          m.removed = true;
          return send(res, 204, null), true;
        }
      }

      // updates
      if (seg[2] === "updates") {
        if (seg.length === 3 && method === "GET") return send(res, 200, { updates: [...updates.values()].filter((u) => u.campaignId === c.id && u.status !== "DELETED").map(updateView), next_cursor: null }), true;
        if (seg.length === 3 && method === "POST") {
          if (!PUBLIC_STATUSES.has(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          const details = [];
          for (const [f, [min, max]] of [["title", [3, 120]], ["body", [10, 5000]]]) {
            const v = typeof body[f] === "string" ? body[f].trim() : "";
            if (v.length < min || v.length > max) details.push({ field: f, code: "INVALID_LENGTH" });
            else if (contentProblem(v)) details.push({ field: f, code: contentProblem(v) });
          }
          if (details.length) return err(res, 422, "VALIDATION_FAILED", { details }), true;
          const publish = body.publish === true;
          const u = { id: randomUUID(), campaignId: c.id, title: body.title.trim(), body: body.body.trim(), status: publish ? "PUBLISHED" : "DRAFT", moderated: false, created_at: now(), published_at: publish ? now() : null };
          updates.set(u.id, u);
          return send(res, 201, updateView(u)), true;
        }
        const u = seg[3] ? updates.get(seg[3]) : null;
        if (!u || u.campaignId !== c.id || u.status === "DELETED") return err(res, 404, "UPDATE_NOT_FOUND"), true;
        if (seg.length === 4 && method === "DELETE") {
          u.status = "DELETED";
          return send(res, 204, null), true;
        }
        if (seg.length === 4 && method === "PATCH") {
          if (typeof body.title === "string") u.title = body.title.trim();
          if (typeof body.body === "string") u.body = body.body.trim();
          return send(res, 200, updateView(u)), true;
        }
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }

    // ---------------------------------------------------------------- staff
    if (seg[0] === "admin" && (seg[1] === "campaigns" || seg[1] === "campaign-categories")) {
      if (!user || user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      const ps = perms(user);
      const need = (perm) => {
        if (!ps.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
        return true;
      };
      // updates moderation
      if (seg[2] === "updates") {
        if (!need("content.moderate")) return true;
        if (seg[3] === "moderation" && method === "GET") {
          const list = [...updates.values()].filter((u) => u.status === "PUBLISHED" && !u.moderated);
          return send(res, 200, { updates: list.map((u) => ({ ...updateView(u), campaign_id: u.campaignId, author_id: ownerUser(campaigns.get(u.campaignId))?.id, moderation_reasons: [], campaign_status: campaigns.get(u.campaignId)?.status })), next_cursor: null }), true;
        }
        const u = updates.get(seg[3]);
        if (!u) return err(res, 404, "UPDATE_NOT_FOUND"), true;
        if (method === "POST" && (seg[4] === "approve" || seg[4] === "hide")) {
          const note = String(body.note ?? "").trim();
          if (!/^[A-Z][A-Z0-9_]{1,63}$/.test(String(body.reason_code ?? "")) || note.length < 3 || note.length > 5000) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
          u.moderated = true;
          if (seg[4] === "hide") u.status = "HIDDEN";
          return send(res, 200, updateView(u)), true;
        }
        return err(res, 404, "ROUTE_NOT_FOUND"), true;
      }
      if (seg[1] === "campaigns" && seg.length === 2 && method === "GET") {
        if (!need("campaign.view")) return true;
        const q = (url.searchParams.get("q") ?? "").toLowerCase();
        const status = url.searchParams.get("status");
        const list = [...campaigns.values()].filter((c) => (!status || c.status === status) && (!q || c.title.toLowerCase().includes(q))).sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
        const limit = Math.min(Math.max(parseInt(url.searchParams.get("limit") ?? "50", 10) || 50, 1), 100);
        return send(res, 200, list.slice(0, limit).map(summary)), true;
      }
      if (seg[2] === "review" && seg.length === 3 && method === "GET") {
        if (!need("campaign.review")) return true;
        const assigned = url.searchParams.get("assigned") ?? "any";
        const cat = url.searchParams.get("category");
        const list = [...campaigns.values()]
          .filter((c) => ["SUBMITTED", "UNDER_REVIEW"].includes(c.status))
          .filter((c) => assigned === "any" || (assigned === "me" ? c.review.assigned_to?.id === user.id : !c.review.assigned_to))
          .filter((c) => !cat || c.category === cat)
          .sort((a, b) => ((a.submitted_at ?? "") > (b.submitted_at ?? "") ? 1 : -1));
        paginate(res, list, url, staffSummary, 25, true);
        return true;
      }
      if (seg[2] && seg[3] === "review" && seg.length === 4 && method === "GET") {
        if (!need("campaign.review")) return true;
        const c = campaigns.get(seg[2]);
        if (!c) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
        return send(res, 200, staffDetail(c)), true;
      }
      const c = seg[2] ? campaigns.get(seg[2]) : null;
      if (!c) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      if (seg[3] === "media" && seg[4] === "all" && seg.length === 5 && method === "GET") {
        if (!need("campaign.view")) return true;
        return send(res, 200, { media: mediaOf(c).map(mediaView) }), true;
      }
      if (method !== "POST") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      if (seg[3] === "media" && seg[4] && seg[5] === "remove" && seg.length === 6) {
        if (!need("content.moderate")) return true;
        const m = media.get(seg[4]);
        if (!m || m.campaignId !== c.id) return err(res, 404, "MEDIA_NOT_FOUND"), true;
        const note = String(body.note ?? "").trim();
        if (note.length < 3 || note.length > 5000) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
        m.removed = true;
        m.removed_reason = body.reason_code;
        return send(res, 200, mediaView(m)), true;
      }
      const action = seg[3];
      if (seg.length !== 4 || !ACTION_PERMISSION[action]) return err(res, 404, "ROUTE_NOT_FOUND"), true;
      if (!need(ACTION_PERMISSION[action])) return true;
      if (STEP_UP_ACTIONS.has(action) && !stepUpFresh()) return err(res, 403, "STEP_UP_REQUIRED"), true;
      if (DECISION_ACTIONS.has(action)) {
        const note = String(body.note ?? "").trim();
        if (!/^[A-Z][A-Z0-9_]{1,63}$/.test(String(body.reason_code ?? ""))) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason_code", code: "INVALID_FORMAT" }] }), true;
        if (note.length < 3 || note.length > 5000) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
      }
      if (involves(user, c)) return err(res, 403, "SELF_DECISION_FORBIDDEN"), true;
      const assignedToMe = c.review.assigned_to?.id === user.id;
      const fb = (outcome) => ({ outcome, reason_code: body.reason_code, user_message: body.user_message ?? null, decided_at: now() });
      const respond = (status = 200) => send(res, status, { campaign_id: c.id, status: c.status, awaiting_second_approval: status === 202 });
      switch (action) {
        case "assign":
          if (!["SUBMITTED", "UNDER_REVIEW"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          c.review.assigned_to = ref(user);
          c.history.push({ version: c.version, event_type: "REVIEW_ASSIGNED", from_status: c.status, to_status: c.status, actor_type: "STAFF", actor_id: user.id, reason_code: body.reason_code ?? null, occurred_at: now() });
          return respond(), true;
        case "start-review":
          if (c.status !== "SUBMITTED") return err(res, 409, "INVALID_STATUS"), true;
          if (!assignedToMe) return err(res, 409, "NOT_ASSIGNED"), true;
          transition(c, "UNDER_REVIEW", user, body);
          return respond(), true;
        case "request-changes":
        case "reject":
        case "approve": {
          if (c.status !== "UNDER_REVIEW") return err(res, 409, "INVALID_STATUS"), true;
          if (!assignedToMe) return err(res, 409, "NOT_ASSIGNED"), true;
          if (action !== "approve" && String(body.user_message ?? "").trim().length < 10) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "user_message", code: "INVALID_LENGTH" }] }), true;
          if (action === "request-changes") { c.review_feedback = fb("CHANGES_REQUESTED"); transition(c, "CHANGES_REQUESTED", user, body); return respond(), true; }
          if (action === "reject") { c.review_feedback = fb("REJECTED"); transition(c, "REJECTED", user, body); return respond(), true; }
          const ev = evaluate("APPROVE", ownerUser(c), c);
          if (!ev.allowed) return err(res, 422, "NOT_ELIGIBLE", { details: ev.reasons }), true;
          if (c.risk_tier === "HIGH") {
            c.review.pending_outcome = "APPROVE";
            c.review.pending_decided_by = user.id;
            c.history.push({ version: c.version, event_type: "FIRST_APPROVAL", from_status: c.status, to_status: c.status, actor_type: "STAFF", actor_id: user.id, reason_code: body.reason_code, occurred_at: now() });
            return respond(202), true;
          }
          snapshot(c);
          c.approved_at = now();
          c.review_feedback = null;
          transition(c, "APPROVED", user, body);
          return respond(), true;
        }
        case "second-approval":
          if (c.status !== "UNDER_REVIEW" || !c.review.pending_outcome) return err(res, 409, "INVALID_STATUS"), true;
          if (c.review.pending_decided_by === user.id) return err(res, 403, "SECOND_APPROVER_MUST_DIFFER"), true;
          snapshot(c);
          c.approved_at = now();
          c.review.pending_outcome = null;
          c.review.pending_decided_by = null;
          transition(c, "APPROVED", user, body);
          return respond(), true;
        case "escalate":
          if (!["SUBMITTED", "UNDER_REVIEW"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          c.review.escalated = true;
          c.review.compliance_case_id = randomUUID();
          c.history.push({ version: c.version, event_type: "REVIEW_ESCALATED", from_status: c.status, to_status: c.status, actor_type: "STAFF", actor_id: user.id, reason_code: body.reason_code, occurred_at: now() });
          return respond(), true;
        case "publish": {
          if (c.status !== "APPROVED") return err(res, 409, "INVALID_STATUS"), true;
          const ev = evaluate("PUBLISH", ownerUser(c), c);
          if (!ev.allowed) return err(res, 422, "NOT_ELIGIBLE", { details: ev.reasons }), true;
          c.slug = c.slug ?? slugify(c.approved.title, c.public_code);
          c.published_at = now();
          transition(c, "ACTIVE", user, body);
          return respond(), true;
        }
        case "suspend":
          if (!["APPROVED", "ACTIVE", "PAUSED"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          c.suspended_at = now();
          transition(c, "SUSPENDED", user, body);
          return respond(), true;
        case "reactivate": {
          if (c.status !== "SUSPENDED") return err(res, 409, "INVALID_STATUS"), true;
          const ev = evaluate("REACTIVATE", ownerUser(c), c);
          if (!ev.allowed) return err(res, 422, "NOT_ELIGIBLE", { details: ev.reasons }), true;
          transition(c, "ACTIVE", user, body);
          return respond(), true;
        }
        case "reopen":
          if (c.status !== "REJECTED") return err(res, 409, "INVALID_STATUS"), true;
          transition(c, "UNDER_REVIEW", user, body);
          return respond(), true;
        case "cancel":
          if (!["SUBMITTED", "UNDER_REVIEW", "APPROVED", "ACTIVE", "PAUSED", "SUSPENDED"].includes(c.status)) return err(res, 409, "INVALID_STATUS"), true;
          transition(c, "CANCELLED", user, body);
          return respond(), true;
        default:
          return err(res, 404, "ROUTE_NOT_FOUND"), true;
      }
    }
    return false;
  }

  function updateView(u) {
    return { id: u.id, title: u.title, body: u.body, status: u.status, created_at: u.created_at, published_at: u.published_at };
  }

  // =====================================================================================================
  /** Test-only control endpoints (`/api/v1/__mock/...`), e.g. to seed campaigns in a given state. */
  async function control(path, body, res) {
    if (path === "/api/v1/__mock/beneficiary") {
      const user = users.get(body.email);
      if (!user) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      const b = verification.seedBeneficiary(user, { display_name: body.display_name ?? "Chipo Moyo", beneficiary_type: body.beneficiary_type ?? "SELF", status: body.status ?? "APPROVED" });
      return send(res, 201, { id: b.id }), true;
    }
    if (path === "/api/v1/__mock/campaign") {
      // Seeds a campaign in `status` (SUBMITTED, APPROVED or ACTIVE …), with an approved cover and an approved
      // beneficiary. Content is NOT validated here on purpose, so tests can prove the web app renders hostile
      // text inertly even if a backend check were bypassed.
      const user = users.get(body.email);
      if (!user) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      const b = verification.seedBeneficiary(user, { display_name: body.beneficiary_name ?? "Chipo Moyo", beneficiary_type: "SELF", status: "APPROVED" });
      const c = newCampaign(user, { title: body.title ?? "Help Chipo finish secondary school", summary: body.summary ?? "School fees and uniforms for Chipo's final year at secondary school.", category: body.category ?? "EDUCATION", goal: { amount_minor: body.amount_minor ?? "150000", currency: "USD" } });
      c.story = body.story ?? "Chipo is in her final year of secondary school in Harare.\n\nHer family cannot cover the last two terms of fees after her mother lost her job. The funds pay the school directly.";
      c.beneficiary = { link_id: randomUUID(), beneficiary_id: b.id, disclosure: body.disclosure ?? "NONE", consent_declared: true };
      const m = { id: randomUUID(), campaignId: c.id, kind: "COVER", position: 0, alt_text: body.alt_text ?? "Chipo outside her school", created_at: now(), createdMs: Date.now() - 60_000, malicious: false, removed: false };
      media.set(m.id, m);
      const status = body.status ?? "SUBMITTED";
      c.submitted_at = now();
      if (status !== "DRAFT") transition(c, "SUBMITTED", user);
      if (["APPROVED", "ACTIVE", "PAUSED", "COMPLETED", "SUSPENDED"].includes(status)) {
        snapshot(c);
        c.approved_at = now();
        c.status = "APPROVED";
      }
      if (["ACTIVE", "PAUSED", "COMPLETED", "SUSPENDED"].includes(status)) {
        c.slug = slugify(c.title, c.public_code);
        c.published_at = now();
        c.status = status;
      }
      if (body.visibility) c.visibility = body.visibility;
      if (body.restricted) restricted.add(user.id);
      return send(res, 201, { id: c.id, slug: c.slug }), true;
    }
    const info = /^\/api\/v1\/__mock\/campaign\/([^/]+)$/.exec(path);
    if (info) {
      const c = campaigns.get(info[1]);
      if (!c) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      return send(res, 200, { id: c.id, slug: c.slug, status: c.status, goal: { amount_minor: c.goal_amount_minor, currency: c.goal_currency }, story: c.story }), true;
    }
    if (path === "/api/v1/__mock/staff-link-token") {
      const staff = users.get(body.staff_email);
      const link = staff ? staffLinks.get(staff.id) : null;
      if (!link) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      return send(res, 200, { token: link.token }), true;
    }
    if (path === "/api/v1/__mock/age-attestation") {
      const user = users.get(body.email);
      if (!user) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      return send(res, 200, { attestation: attestation(user.id) }), true;
    }
    return false;
  }

  return { handle, control, recordAttestation };
}
