// In-memory mock of the Stage 5 verification API (docs/stage-5/interface-contracts.md §7) for the E2E suite.
// Shapes follow the contract; where the contract leaves a shape open (ReviewCase, compliance cases) this mock
// uses the frontend's reading of it (apps/web/src/lib/verification/types.ts). It enforces the rules the UI
// must cope with — editable states, ownership/membership (404 for others), staff-only admin routes (404 for
// non-staff, 403 without the permission), step-up for decisions and document views, assignment, four-eyes,
// session-bound 60-second document tickets — but it is NOT a security reference.
import { randomBytes, randomUUID } from "node:crypto";

const MAX_UPLOAD = 10 * 1024 * 1024;
const MALWARE_MARKER = "FUNDZIM-MOCK-MALWARE-MARKER";
const TICKET_TTL_MS = 60_000;

/** Mirrors contract §7.1 (+ Stage 4 seed). SUPPORT/ADMIN/SUPER_ADMIN hold none of these. */
const ROLE_PERMISSIONS = {
  KYC_REVIEWER: ["kyc.case.review", "kyc.document.view", "kyc.decision.record", "beneficiary.verification.decide", "org.verification.decide", "payout_destination.verify", "risk.view"],
  COMPLIANCE: ["kyc.document.view", "kyc.identity_number.reveal", "kyc.decision.record", "payout_destination.verify", "risk.view", "compliance.case.view", "case.manage", "case.create"],
};
const DECIDE_PERMISSION = { KYC: "kyc.decision.record", KYB: "org.verification.decide", BENEFICIARY: "beneficiary.verification.decide", PAYOUT_DESTINATION: "payout_destination.verify" };

const now = () => new Date().toISOString();

function mask(value, keep = 4) {
  const s = String(value);
  return `${"•".repeat(Math.max(4, s.length - keep))}${s.slice(-keep)}`;
}

function sniff(buf) {
  if (buf.length >= 4 && buf.subarray(0, 4).toString("latin1") === "%PDF") return "application/pdf";
  if (buf.length >= 8 && buf[0] === 0x89 && buf.subarray(1, 4).toString("latin1") === "PNG") return "image/png";
  if (buf.length >= 3 && buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff) return "image/jpeg";
  return null;
}

/** Minimal multipart/form-data parser (enough for the browser's FormData). */
function parseMultipart(raw, contentType) {
  const m = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(contentType ?? "");
  if (!m) return null;
  const boundary = Buffer.from(`--${m[1] ?? m[2]}`);
  const fields = {};
  let file = null;
  let pos = raw.indexOf(boundary);
  while (pos !== -1) {
    const start = pos + boundary.length;
    if (raw.subarray(start, start + 2).toString() === "--") break;
    const next = raw.indexOf(boundary, start);
    if (next === -1) break;
    const part = raw.subarray(start + 2, next - 2); // strip CRLF after boundary and before next
    const headerEnd = part.indexOf("\r\n\r\n");
    const headers = part.subarray(0, headerEnd).toString("utf8");
    const content = part.subarray(headerEnd + 4);
    const name = /name="([^"]*)"/i.exec(headers)?.[1];
    const filename = /filename="([^"]*)"/i.exec(headers)?.[1];
    const type = /content-type:\s*([^\r\n]+)/i.exec(headers)?.[1]?.trim();
    if (name === "file" && filename !== undefined) file = { filename, type, content };
    else if (name) fields[name] = content.toString("utf8");
    pos = next;
  }
  return { fields, file };
}

function ageYears(dob) {
  const [y, m, d] = dob.split("-").map(Number);
  const t = new Date();
  let age = t.getUTCFullYear() - y;
  if (t.getUTCMonth() + 1 < m || (t.getUTCMonth() + 1 === m && t.getUTCDate() < d)) age -= 1;
  return age;
}

export function createVerificationMock({ send, err, userById, users, me }) {
  const profiles = new Map(); // userId -> {level, status}
  const kycCases = new Map();
  const documents = new Map();
  const orgs = new Map();
  const kybCases = new Map();
  const beneficiaries = new Map();
  const destinations = new Map();
  const complianceCases = new Map();
  const tickets = new Map(); // ticket -> {docId, sessionToken, exp}

  const profile = (userId) => profiles.get(userId) ?? { level: "BASIC_VERIFIED", status: "ACTIVE" };
  const setLevel = (userId, level) => profiles.set(userId, { ...profile(userId), level });
  const perms = (user) => (user?.account_kind === "STAFF" ? new Set((user.roles ?? []).flatMap((r) => ROLE_PERMISSIONS[r] ?? [])) : new Set());
  const ref = (user) => ({ id: user.id, display_name: user.display_name });

  // ---- documents ----
  function docStatus(doc) {
    if (doc.status === "DELETED") return "DELETED";
    const age = Date.now() - doc.createdMs;
    if (age < 400) return "QUARANTINED";
    if (age < 1200) return "SCANNING";
    return doc.malicious ? "REJECTED" : "CLEAN";
  }
  function docView(doc) {
    const status = docStatus(doc);
    return {
      id: doc.id,
      subject_type: doc.subject_type,
      subject_id: doc.subject_id,
      document_type: doc.document_type,
      side: doc.side,
      status,
      media_type: doc.media_type,
      size_bytes: doc.size_bytes,
      uploaded_at: doc.uploaded_at,
      rejected_reason: status === "REJECTED" ? "MALWARE_DETECTED" : null,
    };
  }
  const docsFor = (type, id) => [...documents.values()].filter((d) => d.subject_type === type && d.subject_id === id && d.status !== "DELETED").map(docView);

  function requirementsFrom(reqs, type, id) {
    const docs = docsFor(type, id);
    return reqs.map((r) => ({
      ...r,
      satisfied: r.sides.length === 0 ? docs.some((d) => d.document_type === r.document_type && d.status === "CLEAN") : r.sides.every((s) => docs.some((d) => d.document_type === r.document_type && d.side === s && d.status === "CLEAN")),
    }));
  }
  function missingDocs(reqs, type, id) {
    const docs = docsFor(type, id);
    const out = [];
    for (const r of reqs) {
      const sides = r.sides.length ? r.sides : [null];
      for (const s of sides) {
        const matching = docs.filter((d) => d.document_type === r.document_type && (s === null || d.side === s));
        const field = `documents.${r.document_type}${s ? `.${s}` : ""}`;
        if (matching.length === 0) out.push({ field, code: "DOCUMENT_REQUIRED" });
        else if (!matching.some((d) => d.status === "CLEAN")) out.push({ field, code: "DOCUMENT_NOT_CLEAN" });
      }
    }
    return out;
  }

  // ---- KYC ----
  function kycRequirements(c) {
    const type = c.identity?.id_document_type ?? "ZW_NATIONAL_ID";
    return [{ document_type: type, sides: type === "ZW_NATIONAL_ID" ? ["FRONT", "BACK"] : [] }];
  }
  function kycView(c) {
    const identity = c.identity
      ? { ...c.identity, id_document_number_masked: c.identity.id_document_number ? mask(c.identity.id_document_number) : null }
      : null;
    if (identity) delete identity.id_document_number;
    return {
      id: c.id, kind: "KYC", status: c.status, target_level: "IDENTITY_VERIFIED", policy_version: 1, identity,
      documents: docsFor("KYC_CASE", c.id), requirements: requirementsFrom(kycRequirements(c), "KYC_CASE", c.id),
      information_requests: c.information_requests, decision: c.decision, submitted_at: c.submitted_at,
      created_at: c.created_at, updated_at: c.updated_at, version: c.version,
    };
  }
  const FINAL = new Set(["REJECTED", "EXPIRED", "REVOKED", "WITHDRAWN"]);
  const EDITABLE = new Set(["DRAFT", "ADDITIONAL_INFORMATION_REQUIRED"]);
  const currentKyc = (userId) => [...kycCases.values()].filter((c) => c.userId === userId).sort((a, b) => (a.created_at < b.created_at ? 1 : -1))[0] ?? null;

  function newReview() {
    return { assigned_to: null, history: [], pending_approval: null };
  }
  function log(subject, actor, action, extra = {}) {
    subject.review.history.push({ id: randomUUID(), action, actor: actor ? { type: actor.account_kind === "STAFF" ? "STAFF" : "USER", display_name: actor.display_name } : { type: "SYSTEM", display_name: null }, occurred_at: now(), reason_code: extra.reason_code ?? null, note: extra.note ?? null });
  }
  function touch(c) {
    c.updated_at = now();
    c.version += 1;
  }

  function createKycCase(user) {
    const c = { id: randomUUID(), userId: user.id, status: "DRAFT", identity: null, information_requests: [], decision: null, submitted_at: null, created_at: now(), updated_at: now(), version: 1, review: newReview() };
    kycCases.set(c.id, c);
    log(c, user, "kyc.case_created");
    return c;
  }

  function kycSubmitProblems(c, user) {
    const details = [];
    const i = c.identity ?? {};
    for (const f of ["legal_first_name", "legal_last_name", "date_of_birth", "nationality", "country_of_residence", "id_document_type", "id_document_number", "residential_address"]) {
      if (!i[f]) details.push({ field: f, code: "MISSING_FIELD" });
    }
    if (i.date_of_birth && ageYears(i.date_of_birth) < 18) details.push({ field: "date_of_birth", code: "UNDERAGE" });
    if (i.id_document_expiry && i.id_document_expiry < now().slice(0, 10)) details.push({ field: "id_document_expiry", code: "DOCUMENT_EXPIRED" });
    details.push(...missingDocs(kycRequirements(c), "KYC_CASE", c.id));
    void user;
    return details;
  }

  // ---- organisations / KYB ----
  function orgView(org, userId) {
    const role = org.members.get(userId);
    return {
      id: org.id, display_name: org.display_name, slug: org.slug, org_type: org.org_type, status: "ACTIVE", my_role: role,
      my_permissions: role === "ORG_ADMIN" ? ["org.view", "org.settings.manage", "org.member.manage"] : ["org.view"], created_at: org.created_at,
    };
  }
  const KYB_REQ = [{ document_type: "REGISTRATION_CERTIFICATE", sides: [] }];
  function kybView(c, isAdmin = true) {
    return {
      id: c.id, kind: "KYB", organisation_id: c.orgId, status: c.status, target_level: "ORG_VERIFIED", details: c.details,
      persons: isAdmin ? c.persons.map(({ id_document_number, ...p }) => ({ ...p, id_document_number_masked: id_document_number ? mask(id_document_number) : null })) : [],
      documents: isAdmin ? [...docsFor("KYB_CASE", c.id), ...c.persons.flatMap((p) => docsFor("KYB_PERSON", p.id))] : [],
      requirements: isAdmin ? requirementsFrom(KYB_REQ, "KYB_CASE", c.id) : [],
      information_requests: isAdmin ? c.information_requests : [], decision: c.decision, submitted_at: c.submitted_at,
      created_at: c.created_at, updated_at: c.updated_at, version: c.version,
    };
  }
  const currentKyb = (orgId) => [...kybCases.values()].filter((c) => c.orgId === orgId).sort((a, b) => (a.created_at < b.created_at ? 1 : -1))[0] ?? null;

  // ---- beneficiaries ----
  function benRequirements(b) {
    switch (b.beneficiary_type) {
      case "MINOR":
        return [{ document_type: "BIRTH_CERTIFICATE", sides: [] }];
      case "INDIVIDUAL":
        return [{ document_type: "CONSENT_LETTER", sides: [] }];
      case "INCAPACITATED_ADULT":
        return [{ document_type: "POWER_OF_ATTORNEY", sides: [] }];
      case "SELF":
        return [];
      default:
        return [{ document_type: "OTHER", sides: [] }];
    }
  }
  function benView(b) {
    return {
      id: b.id, owner: b.owner, beneficiary_type: b.beneficiary_type, kind: ["ORGANISATION", "INSTITUTION", "COMMUNITY_GROUP"].includes(b.beneficiary_type) ? "ORGANISATION" : "INDIVIDUAL",
      display_name: b.display_name, full_name: b.full_name, date_of_birth: b.date_of_birth, relationship: b.relationship, authority_basis: b.authority_basis,
      verification: { status: b.status, risk_level: b.beneficiary_type === "MINOR" ? "ENHANCED" : "STANDARD", requires_second_approval: b.beneficiary_type === "MINOR", information_requests: b.information_requests, decision: b.decision },
      documents: docsFor("BENEFICIARY", b.id), requirements: requirementsFrom(benRequirements(b), "BENEFICIARY", b.id),
      created_at: b.created_at, updated_at: b.updated_at, version: b.version,
    };
  }

  // ---- payout destinations ----
  const BANK = new Set(["BANK_TRANSFER", "ZIMSWITCH"]);
  const PROVIDERS = { ECOCASH: "EcoCash", ONEMONEY: "OneMoney", INNBUCKS: "InnBucks", OMARI: "O'mari", BANK_TRANSFER: "Bank", ZIMSWITCH: "ZimSwitch" };
  function validAccount(rail, id) {
    return BANK.has(rail) ? /^[0-9A-Za-z]{6,34}$/.test(id) : /^(?:\+?263|0)7\d{8}$/.test(id);
  }
  function destView(d) {
    return {
      id: d.id, owner: d.owner, payee: d.payee, category: BANK.has(d.rail) ? "BANK_ACCOUNT" : "MOBILE_MONEY_WALLET", rail: d.rail, provider_name: PROVIDERS[d.rail],
      currency: d.currency, holder_name: d.holder_name, masked_identifier: mask(d.account_identifier), status: d.status, checks: d.checks,
      last_reviewed_at: d.last_reviewed_at, eligible_for_payout: false, created_at: d.created_at, updated_at: d.updated_at, version: d.version,
    };
  }

  // ---- ownership checks ----
  function ownsSubject(user, type, id) {
    if (!user) return null;
    if (type === "KYC_CASE") {
      const c = kycCases.get(id);
      return c && c.userId === user.id ? { editable: EDITABLE.has(c.status) } : null;
    }
    if (type === "KYB_CASE" || type === "KYB_PERSON") {
      const c = type === "KYB_CASE" ? kybCases.get(id) : [...kybCases.values()].find((k) => k.persons.some((p) => p.id === id));
      if (!c) return null;
      const org = orgs.get(c.orgId);
      return org?.members.get(user.id) === "ORG_ADMIN" ? { editable: EDITABLE.has(c.status) } : null;
    }
    if (type === "BENEFICIARY") {
      const b = beneficiaries.get(id);
      if (!b) return null;
      const ok = b.owner.type === "USER" ? b.owner.id === user.id : orgs.get(b.owner.id)?.members.get(user.id) === "ORG_ADMIN";
      return ok ? { editable: EDITABLE.has(b.status) } : null;
    }
    if (type === "PAYOUT_DESTINATION") {
      const d = destinations.get(id);
      if (!d) return null;
      const ok = d.owner.type === "USER" ? d.owner.id === user.id : orgs.get(d.owner.id)?.members.get(user.id) === "ORG_ADMIN";
      return ok ? { editable: d.status !== "RETIRED" } : null;
    }
    return null;
  }

  // ---- reviewer view over every subject ----
  function allReviewSubjects() {
    const out = [];
    for (const c of kycCases.values()) out.push({ type: "KYC", obj: c, status: c.status, subject: { type: "USER", id: c.userId, display_name: userById(c.userId)?.display_name } });
    for (const c of kybCases.values()) out.push({ type: "KYB", obj: c, status: c.status, subject: { type: "ORGANISATION", id: c.orgId, display_name: orgs.get(c.orgId)?.display_name } });
    for (const b of beneficiaries.values()) out.push({ type: "BENEFICIARY", obj: b, status: b.status, subject: { type: "BENEFICIARY", id: b.id, display_name: b.display_name } });
    for (const d of destinations.values()) out.push({ type: "PAYOUT_DESTINATION", obj: d, status: d.status, subject: { type: "PAYOUT_DESTINATION", id: d.id, display_name: `${PROVIDERS[d.rail]} ${mask(d.account_identifier)}` } });
    return out.filter((s) => s.obj.submitted_at);
  }
  function summary(s) {
    return {
      id: s.obj.id, type: s.type, status: s.obj.review.pending_approval ? "AWAITING_SECOND_APPROVAL" : s.status, subject: s.subject,
      assigned_to: s.obj.review.assigned_to, risk_level: s.type === "BENEFICIARY" && s.obj.beneficiary_type === "MINOR" ? "ENHANCED" : "STANDARD",
      submitted_at: s.obj.submitted_at, updated_at: s.obj.updated_at, requires_second_approval: s.type === "BENEFICIARY" && s.obj.beneficiary_type === "MINOR",
    };
  }
  function reviewDetail(s) {
    const base = { ...summary(s), status: s.status, history: s.obj.review.history, pending_approval: s.obj.review.pending_approval, risk: { level: summary(s).risk_level, signals: [] } };
    if (s.type === "KYC" || s.type === "KYB") {
      // Backend: KYC/KYB detail reports four-eyes state in `review` (not `pending_approval`).
      const pa = s.obj.review.pending_approval;
      base.pending_approval = undefined;
      base.review = { risk_level: s.obj.fourEyes ? "ENHANCED" : "STANDARD", requires_second_approval: !!s.obj.fourEyes, pending_outcome: pa ? "APPROVE" : null, pending_decided_by: pa ? pa.approved_by.id : null };
      base.risk_level = base.review.risk_level;
      base.requires_second_approval = !!s.obj.fourEyes;
    }
    if (s.type === "KYC") {
      const v = kycView(s.obj);
      return { ...base, identity: v.identity, documents: v.documents, requirements: v.requirements, information_requests: v.information_requests, decision: v.decision };
    }
    if (s.type === "KYB") {
      const v = kybView(s.obj);
      return { ...base, details: v.details, persons: v.persons, documents: v.documents, requirements: v.requirements, information_requests: v.information_requests, decision: v.decision };
    }
    if (s.type === "BENEFICIARY") {
      const v = benView(s.obj);
      return { ...base, subject_summary: [{ label: "Beneficiary type", value: v.beneficiary_type }, { label: "Relationship", value: v.relationship.type }, { label: "Authority", value: v.authority_basis }], documents: v.documents, requirements: v.requirements, information_requests: v.information_requests, decision: v.verification.decision };
    }
    const v = destView(s.obj);
    return { ...base, subject_summary: [{ label: "Rail", value: v.rail }, { label: "Account", value: v.masked_identifier }, { label: "Holder", value: v.holder_name }, { label: "Ownership check", value: v.checks.ownership }], documents: docsFor("PAYOUT_DESTINATION", v.id), information_requests: [], decision: null };
  }

  function applyDecision(s, actor, outcome, extra) {
    const o = s.obj;
    o.review.pending_approval = null;
    o.decision = { outcome, reason_code: extra.reason_code ?? null, message: outcome === "REJECTED" ? extra.user_message ?? null : null, decided_at: now() };
    if (s.type === "PAYOUT_DESTINATION") {
      o.decision = null;
      o.last_reviewed_at = now();
      if (outcome === "APPROVED") {
        o.checks.compliance = "APPROVED";
        if (o.checks.ownership === "PENDING") o.checks.ownership = "CONFIRMED";
        o.status = o.checks.ownership === "CONFIRMED" ? "VERIFIED" : "PENDING_VERIFICATION";
      } else {
        o.checks.compliance = "REJECTED";
        o.status = "REJECTED";
      }
    } else {
      o.status = outcome;
      if (outcome === "APPROVED" && s.type === "KYC") setLevel(o.userId, "IDENTITY_VERIFIED");
      if (outcome === "APPROVED" && s.type === "KYB") orgs.get(o.orgId).level = "ORG_VERIFIED";
    }
    touch(o);
    log(o, actor, outcome === "APPROVED" ? "verification.approved" : "verification.rejected", extra);
  }

  // =====================================================================================================
  async function handle({ req, res, method, path, url, user, session, sessionToken, body, raw }) {
    const isApi = path.startsWith("/api/v1/");
    if (!isApi) return false;
    const p = path.slice("/api/v1".length);
    const seg = p.split("/").filter(Boolean);
    const userOnly = () => {
      if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), false;
      if (user.account_kind === "STAFF") return err(res, 403, "PERMISSION_DENIED"), false;
      return true;
    };
    const stepUpFresh = () => session && Date.now() - session.stepUpAt <= 5 * 60_000;

    // ------------------------------------------------------------------ KYC
    if (p === "/kyc/status" && method === "GET") {
      if (!userOnly()) return true;
      const c = currentKyc(user.id);
      const pr = profile(user.id);
      const verified = pr.level === "IDENTITY_VERIFIED" || pr.level === "PAYOUT_VERIFIED";
      return send(res, 200, { level: pr.level, status: pr.status, risk_level: "STANDARD", case: c ? kycView(c) : null, gates: { create_draft: true, submit_campaign: verified, withdraw: false } }), true;
    }
    if (p === "/kyc/cases" && method === "POST") {
      if (!userOnly()) return true;
      if (!user.email_verified || !user.phone_verified) return err(res, 422, "PREREQUISITES_NOT_MET"), true;
      const c = currentKyc(user.id);
      if (c && !FINAL.has(c.status)) return err(res, 409, "CASE_ALREADY_OPEN"), true;
      return send(res, 201, kycView(createKycCase(user))), true;
    }
    if (p === "/kyc/cases/current" && method === "GET") {
      if (!userOnly()) return true;
      const c = currentKyc(user.id);
      return send(res, 200, c ? kycView(c) : null), true;
    }
    if (seg[0] === "kyc" && seg[1] === "cases" && seg[2]) {
      if (!userOnly()) return true;
      const c = kycCases.get(seg[2]);
      if (!c || c.userId !== user.id) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (method === "PATCH" && seg.length === 3) {
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        const ifMatch = req.headers["if-match"];
        if (ifMatch && ifMatch !== `"${c.version}"`) return err(res, 409, "VERSION_CONFLICT"), true;
        const details = [];
        if (body.date_of_birth && !/^\d{4}-\d{2}-\d{2}$/.test(body.date_of_birth)) details.push({ field: "date_of_birth", code: "INVALID_FORMAT" });
        if (body.id_document_number && /[^0-9A-Za-z -]/.test(body.id_document_number)) details.push({ field: "id_document_number", code: "INVALID_FORMAT" });
        if (details.length) return err(res, 422, "VALIDATION_FAILED", { details }), true;
        c.identity = { ...(c.identity ?? {}), ...body };
        touch(c);
        return send(res, 200, kycView(c)), true;
      }
      if (method === "POST" && seg[3] === "submit") {
        if (c.status === "SUBMITTED") return send(res, 200, kycView(c)), true; // idempotent retry
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        const problems = kycSubmitProblems(c, user);
        if (problems.length) return err(res, 422, "SUBMISSION_INCOMPLETE", { details: problems }), true;
        for (const r of c.information_requests) if (!r.responded_at) r.responded_at = now();
        c.status = "SUBMITTED";
        c.submitted_at = c.submitted_at ?? now();
        touch(c);
        log(c, user, c.information_requests.length ? "kyc.case_resubmitted" : "kyc.case_submitted");
        return send(res, 200, kycView(c)), true;
      }
      if (method === "POST" && seg[3] === "withdraw") {
        if (!["DRAFT", "SUBMITTED", "ADDITIONAL_INFORMATION_REQUIRED"].includes(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        c.status = "WITHDRAWN";
        touch(c);
        log(c, user, "kyc.case_withdrawn");
        return send(res, 200, kycView(c)), true;
      }
      return false;
    }

    // ------------------------------------------------------------------ documents
    if (p === "/verification/documents" && method === "POST") {
      if (!userOnly()) return true;
      const form = parseMultipart(raw, req.headers["content-type"]);
      if (!form || !form.file) return err(res, 400, "MALFORMED_REQUEST"), true;
      const { subject_type, subject_id, document_type, side } = form.fields;
      const owned = ownsSubject(user, subject_type, subject_id);
      if (!owned) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (!owned.editable) return err(res, 409, "CASE_NOT_EDITABLE"), true;
      const content = form.file.content;
      if (content.length === 0) return err(res, 422, "EMPTY_FILE"), true;
      if (content.length > MAX_UPLOAD) return err(res, 413, "FILE_TOO_LARGE"), true;
      const sniffed = sniff(content);
      if (!sniffed) return err(res, 422, "UNSUPPORTED_FILE_TYPE"), true;
      if (form.file.type && form.file.type !== "application/octet-stream" && form.file.type !== sniffed) return err(res, 422, "FILE_TYPE_MISMATCH"), true;
      const doc = {
        id: randomUUID(), subject_type, subject_id, document_type, side: side || null, status: "QUARANTINED", media_type: sniffed, size_bytes: content.length,
        uploaded_at: now(), createdMs: Date.now(), content, malicious: content.includes(MALWARE_MARKER), uploader: user.id,
      };
      documents.set(doc.id, doc);
      return send(res, 201, docView(doc)), true;
    }
    if (p === "/verification/documents" && method === "GET") {
      if (!userOnly()) return true;
      const type = url.searchParams.get("subject_type");
      const id = url.searchParams.get("subject_id");
      if (!ownsSubject(user, type, id)) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      return send(res, 200, docsFor(type, id)), true;
    }
    if (seg[0] === "verification" && seg[1] === "documents" && seg[2]) {
      const doc = documents.get(seg[2]);
      const isStaffViewer = perms(user).has("kyc.document.view");
      const owner = doc && user && ownsSubject(user, doc.subject_type, doc.subject_id);
      if (seg[3] === "content" && method === "GET") {
        const t = tickets.get(url.searchParams.get("ticket") ?? "");
        if (!doc || !t || t.docId !== doc.id || t.sessionToken !== sessionToken) return err(res, 403, "DOCUMENT_TICKET_INVALID"), true;
        if (t.exp < Date.now()) return err(res, 403, "DOCUMENT_TICKET_EXPIRED"), true;
        tickets.delete(url.searchParams.get("ticket"));
        res.writeHead(200, {
          "Content-Type": doc.media_type, "Content-Disposition": `attachment; filename="document-${doc.id.slice(0, 8)}${doc.media_type === "application/pdf" ? ".pdf" : doc.media_type === "image/png" ? ".png" : ".jpg"}"`,
          "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "sandbox", "Cache-Control": "no-store",
        });
        res.end(doc.content);
        return true;
      }
      if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
      if (!doc || doc.status === "DELETED" || (!owner && !isStaffViewer)) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (method === "GET" && seg.length === 3) return send(res, 200, docView(doc)), true;
      if (method === "POST" && seg[3] === "access") {
        if (!owner && !stepUpFresh()) return err(res, 403, "STEP_UP_REQUIRED"), true;
        if (docStatus(doc) !== "CLEAN") return err(res, 409, "DOCUMENT_NOT_AVAILABLE"), true;
        const ticket = randomBytes(18).toString("base64url");
        tickets.set(ticket, { docId: doc.id, sessionToken, exp: Date.now() + TICKET_TTL_MS });
        return send(res, 200, { url: `/api/v1/verification/documents/${doc.id}/content?ticket=${ticket}`, expires_at: new Date(Date.now() + TICKET_TTL_MS).toISOString() }), true;
      }
      if (method === "DELETE" && seg.length === 3) {
        if (!owner) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
        if (!owner.editable) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        doc.status = "DELETED";
        return send(res, 204, null), true;
      }
      return false;
    }

    // ------------------------------------------------------------------ organisations + KYB
    if (p === "/me/organisations" && method === "GET") {
      if (!userOnly()) return true;
      return send(res, 200, [...orgs.values()].filter((o) => o.members.has(user.id)).map((o) => orgView(o, user.id))), true;
    }
    if (p === "/organisations" && method === "POST") {
      if (!userOnly()) return true;
      if (!user.email_verified) return err(res, 403, "EMAIL_NOT_VERIFIED"), true;
      const name = String(body.display_name ?? "").trim();
      if (name.length < 2 || !body.org_type) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: name.length < 2 ? "display_name" : "org_type", code: "INVALID" }] }), true;
      const org = { id: randomUUID(), display_name: name, slug: name.toLowerCase().replace(/[^a-z0-9]+/g, "-"), org_type: body.org_type, created_at: now(), members: new Map([[user.id, "ORG_ADMIN"]]), level: "ORG_UNVERIFIED" };
      orgs.set(org.id, org);
      return send(res, 201, orgView(org, user.id)), true;
    }
    if (seg[0] === "organisations" && seg[1]) {
      if (!userOnly()) return true;
      const org = orgs.get(seg[1]);
      const role = org?.members.get(user.id);
      if (!org || !role) return err(res, 404, "ORGANISATION_NOT_FOUND"), true;
      const isAdmin = role === "ORG_ADMIN";
      if (seg.length === 2 && method === "GET") return send(res, 200, orgView(org, user.id)), true;
      if (seg[2] !== "kyb") return false;
      const c = currentKyb(org.id);
      if (seg.length === 3 && method === "GET") return send(res, 200, { level: org.level, status: "ACTIVE", case: c ? kybView(c, isAdmin) : null }), true;
      if (!isAdmin) return err(res, 403, "PERMISSION_DENIED"), true;
      if (seg.length === 3 && method === "POST") {
        if (c && !FINAL.has(c.status)) return err(res, 409, "CASE_ALREADY_OPEN"), true;
        const k = { id: randomUUID(), orgId: org.id, status: "DRAFT", details: { registered_name: null, trading_name: null, registration_number: null, registry: null, country_of_registration: null, registered_address: null }, persons: [], information_requests: [], decision: null, submitted_at: null, created_at: now(), updated_at: now(), version: 1, review: newReview() };
        kybCases.set(k.id, k);
        log(k, user, "kyb.case_created");
        return send(res, 201, kybView(k)), true;
      }
      if (!c) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (seg.length === 3 && method === "PATCH") {
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        c.details = { ...c.details, ...body };
        touch(c);
        return send(res, 200, kybView(c)), true;
      }
      if (seg[3] === "persons" && method === "POST") {
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        const bp = body.ownership_bp;
        if (bp !== undefined && (!Number.isInteger(bp) || bp < 0 || bp > 10000)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "ownership_bp", code: "INVALID" }] }), true;
        const person = { id: randomUUID(), full_name: body.full_name, roles: body.roles ?? [], ownership_bp: bp ?? null, id_document_type: body.id_document_type ?? null, id_document_number: body.id_document_number ?? null };
        c.persons.push(person);
        touch(c);
        const { id_document_number, ...rest } = person;
        return send(res, 201, { ...rest, id_document_number_masked: id_document_number ? mask(id_document_number) : null }), true;
      }
      if (seg[3] === "persons" && seg[4] && method === "DELETE") {
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        c.persons = c.persons.filter((x) => x.id !== seg[4]);
        touch(c);
        return send(res, 204, null), true;
      }
      if (seg[3] === "submit" && method === "POST") {
        if (!EDITABLE.has(c.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        const pr = profile(user.id);
        if (pr.level !== "IDENTITY_VERIFIED" && pr.level !== "PAYOUT_VERIFIED") return err(res, 422, "REPRESENTATIVE_NOT_VERIFIED"), true;
        const details = [];
        for (const f of ["registered_name", "registration_number", "registry", "country_of_registration", "registered_address"]) if (!c.details[f]) details.push({ field: f, code: "MISSING_FIELD" });
        if (c.persons.length === 0) details.push({ field: "persons", code: "MISSING_FIELD" });
        details.push(...missingDocs(KYB_REQ, "KYB_CASE", c.id));
        if (details.length) return err(res, 422, "SUBMISSION_INCOMPLETE", { details }), true;
        for (const r of c.information_requests) if (!r.responded_at) r.responded_at = now();
        c.status = "SUBMITTED";
        c.submitted_at = c.submitted_at ?? now();
        touch(c);
        log(c, user, "kyb.case_submitted");
        return send(res, 200, kybView(c)), true;
      }
      if (seg[3] === "withdraw" && method === "POST") {
        c.status = "WITHDRAWN";
        touch(c);
        return send(res, 200, kybView(c)), true;
      }
      return false;
    }

    // ------------------------------------------------------------------ beneficiaries
    if (p === "/beneficiaries" && method === "GET") {
      if (!userOnly()) return true;
      const mine = [...beneficiaries.values()].filter((b) => (b.owner.type === "USER" ? b.owner.id === user.id : orgs.get(b.owner.id)?.members.has(user.id)));
      return send(res, 200, mine.map(benView)), true;
    }
    if (p === "/beneficiaries" && method === "POST") {
      if (!userOnly()) return true;
      let owner = { type: "USER", id: user.id };
      if (body.owner_organisation_id) {
        if (orgs.get(body.owner_organisation_id)?.members.get(user.id) !== "ORG_ADMIN") return err(res, 404, "ORGANISATION_NOT_FOUND"), true;
        owner = { type: "ORGANISATION", id: body.owner_organisation_id };
      }
      const details = [];
      if (!body.beneficiary_type) details.push({ field: "beneficiary_type", code: "REQUIRED" });
      if (!body.display_name) details.push({ field: "display_name", code: "REQUIRED" });
      if (!body.relationship?.type) details.push({ field: "relationship.type", code: "REQUIRED" });
      if (details.length) return err(res, 422, "VALIDATION_FAILED", { details }), true;
      const b = {
        id: randomUUID(), owner, beneficiary_type: body.beneficiary_type, display_name: body.display_name, full_name: body.full_name ?? null, date_of_birth: body.date_of_birth ?? null,
        relationship: { type: body.relationship.type, description: body.relationship.description ?? null }, authority_basis: body.authority_basis, status: "DRAFT",
        information_requests: [], decision: null, submitted_at: null, created_at: now(), updated_at: now(), version: 1, review: newReview(),
      };
      beneficiaries.set(b.id, b);
      return send(res, 201, benView(b)), true;
    }
    if (seg[0] === "beneficiaries" && seg[1]) {
      if (!userOnly()) return true;
      const b = beneficiaries.get(seg[1]);
      if (!b || !ownsSubject(user, "BENEFICIARY", b.id)) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (method === "GET" && seg.length === 2) return send(res, 200, benView(b)), true;
      if (method === "PATCH" && seg.length === 2) {
        if (!EDITABLE.has(b.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        for (const k of ["display_name", "full_name", "date_of_birth", "relationship", "authority_basis"]) if (body[k] !== undefined) b[k] = body[k];
        touch(b);
        return send(res, 200, benView(b)), true;
      }
      if (method === "POST" && seg[2] === "submit") {
        if (!EDITABLE.has(b.status)) return err(res, 409, "CASE_NOT_EDITABLE"), true;
        const details = missingDocs(benRequirements(b), "BENEFICIARY", b.id);
        if (details.length) return err(res, 422, "SUBMISSION_INCOMPLETE", { details }), true;
        for (const r of b.information_requests) if (!r.responded_at) r.responded_at = now();
        b.status = "SUBMITTED";
        b.submitted_at = b.submitted_at ?? now();
        touch(b);
        log(b, user, "beneficiary.verification_submitted");
        return send(res, 200, benView(b)), true;
      }
      return false;
    }

    // ------------------------------------------------------------------ payout destinations
    if (p === "/payout-destinations" && method === "GET") {
      if (!userOnly()) return true;
      const mine = [...destinations.values()].filter((d) => d.status !== "RETIRED" && ownsSubject(user, "PAYOUT_DESTINATION", d.id));
      return send(res, 200, mine.map(destView)), true;
    }
    if (p === "/payout-destinations" && method === "POST") {
      if (!userOnly()) return true;
      let owner = { type: "USER", id: user.id };
      if (body.owner_organisation_id) {
        if (orgs.get(body.owner_organisation_id)?.members.get(user.id) !== "ORG_ADMIN") return err(res, 404, "ORGANISATION_NOT_FOUND"), true;
        owner = { type: "ORGANISATION", id: body.owner_organisation_id };
      }
      if (!validAccount(body.rail, String(body.account_identifier ?? ""))) return err(res, 422, "INVALID_ACCOUNT_FORMAT", { details: [{ field: "account_identifier", code: "INVALID_FORMAT" }] }), true;
      const dup = [...destinations.values()].some((d) => d.status !== "RETIRED" && d.owner.id === owner.id && d.rail === body.rail && d.account_identifier === body.account_identifier);
      if (dup) return err(res, 409, "DESTINATION_EXISTS"), true;
      const d = {
        id: randomUUID(), owner, payee: { type: body.payee?.type ?? "OWNER", beneficiary_id: body.payee?.beneficiary_id ?? null }, rail: body.rail, currency: body.currency,
        holder_name: body.holder_name, account_identifier: body.account_identifier, bank_code: body.bank_code ?? null, status: "UNVERIFIED",
        checks: { format_validated: true, ownership: "NOT_STARTED", compliance: "PENDING" }, last_reviewed_at: null, submitted_at: null,
        created_at: now(), updated_at: now(), version: 1, review: newReview(),
      };
      destinations.set(d.id, d);
      return send(res, 201, destView(d)), true;
    }
    if (seg[0] === "payout-destinations" && seg[1]) {
      if (!userOnly()) return true;
      const d = destinations.get(seg[1]);
      if (!d || d.status === "RETIRED" || !ownsSubject(user, "PAYOUT_DESTINATION", d.id)) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (method === "GET" && seg.length === 2) return send(res, 200, destView(d)), true;
      if (method === "PATCH" && seg.length === 2) {
        if (body.account_identifier !== undefined && !validAccount(d.rail, body.account_identifier)) return err(res, 422, "INVALID_ACCOUNT_FORMAT"), true;
        for (const k of ["holder_name", "account_identifier", "bank_code"]) if (body[k] !== undefined) d[k] = body[k];
        d.status = "PENDING_VERIFICATION";
        d.checks = { format_validated: true, ownership: "NOT_STARTED", compliance: "PENDING" };
        touch(d);
        return send(res, 200, destView(d)), true;
      }
      if (method === "POST" && seg[2] === "verification") {
        const ids = Array.isArray(body.document_ids) ? body.document_ids : [];
        if (body.method === "PROVIDER_LOOKUP") d.checks.ownership = "PROVIDER_CONFIRMATION_REQUIRED";
        else {
          if (ids.length === 0) return err(res, 422, "SUBMISSION_INCOMPLETE", { details: [{ field: "document_ids", code: "DOCUMENT_REQUIRED" }] }), true;
          d.checks.ownership = "PENDING";
        }
        d.status = "PENDING_VERIFICATION";
        d.submitted_at = d.submitted_at ?? now();
        touch(d);
        log(d, user, "payouts.destination_verification_requested");
        return send(res, 200, destView(d)), true;
      }
      if (method === "DELETE" && seg.length === 2) {
        d.status = "RETIRED";
        touch(d);
        return send(res, 204, null), true;
      }
      return false;
    }

    // ------------------------------------------------------------------ reviewer
    if (seg[0] === "admin" && seg[1] === "verification") {
      if (!user || user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      const ps = perms(user);
      if (seg[2] !== "cases") return false;
      if (!seg[3] && method === "GET") {
        if (!ps.has("kyc.case.review")) return err(res, 403, "PERMISSION_DENIED"), true;
        const type = url.searchParams.get("type");
        const status = url.searchParams.get("status");
        const assigned = url.searchParams.get("assigned") ?? "any";
        const limit = Math.min(parseInt(url.searchParams.get("limit") ?? "25", 10) || 25, 100);
        const cursor = url.searchParams.get("cursor");
        let list = allReviewSubjects().map((s) => summary(s));
        if (type) list = list.filter((s) => s.type === type);
        if (status) list = list.filter((s) => s.status === status);
        if (assigned === "me") list = list.filter((s) => s.assigned_to?.id === user.id);
        if (assigned === "unassigned") list = list.filter((s) => !s.assigned_to);
        list.sort((a, b) => (a.submitted_at < b.submitted_at ? 1 : -1));
        let offset = 0;
        if (cursor) {
          const m = /^[0-9TZ:.+-]+\|([0-9a-f-]{36})$/.exec(cursor);
          if (!m) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "cursor", code: "INVALID_FORMAT" }] }), true;
          offset = list.findIndex((x) => x.id === m[1]) + 1;
        }
        const page = list.slice(offset, offset + limit);
        const last = page[page.length - 1];
        const requestId = randomUUID();
        res.writeHead(200, { "Content-Type": "application/json", "X-Request-ID": requestId, "Cache-Control": "no-store" });
        res.end(JSON.stringify({ data: page, meta: { request_id: requestId, limit, ...(offset + limit < list.length && last ? { next_cursor: `${last.submitted_at}|${last.id}` } : {}) } }));
        return true;
      }
      const s = allReviewSubjects().find((x) => x.obj.id === seg[3]);
      if (!s) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (!ps.has("kyc.case.review") && !(seg[4] === "reveal-identity-number" && ps.has("kyc.identity_number.reveal"))) return err(res, 403, "PERMISSION_DENIED"), true;
      if (method === "GET" && seg.length === 4) return send(res, 200, reviewDetail(s)), true;
      if (method !== "POST") return false;
      const o = s.obj;
      const action = seg[4];
      const mineAssigned = o.review.assigned_to?.id === user.id;
      const needsDecide = ["approve", "second-approval", "reject", "suspend", "reinstate", "reopen", "revoke"].includes(action);
      if (needsDecide && !ps.has(DECIDE_PERMISSION[s.type])) return err(res, 403, "PERMISSION_DENIED"), true;
      if ((needsDecide || action === "reveal-identity-number") && !stepUpFresh()) return err(res, 403, "STEP_UP_REQUIRED"), true;
      if ([...["escalate", "return"], ...(needsDecide ? [action] : [])].includes(action)) {
        const note = String(body.note ?? "").trim();
        if (note.length < 3 || note.length > 5000) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "note", code: "INVALID_LENGTH" }] }), true;
      }
      if (action === "assign" && body.assignee_id && body.assignee_id !== user.id) {
        const target = userById(body.assignee_id);
        if (!target || !perms(target).has("kyc.case.review")) return err(res, 422, "ASSIGNEE_NOT_ELIGIBLE"), true;
      }
      const status = s.status;
      switch (action) {
        case "assign":
          if (FINAL.has(status)) return err(res, 409, "CASE_STATE_CHANGED"), true;
          o.review.assigned_to = ref(user);
          log(o, user, "verification.assigned");
          touch(o);
          return send(res, 200, reviewDetail(s)), true;
        case "start-review":
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (s.type === "PAYOUT_DESTINATION") return send(res, 200, { status: "ok" }), true; // assignment starts the review
          if (status !== "SUBMITTED") return err(res, 409, "CASE_STATE_CHANGED"), true;
          o.status = "UNDER_REVIEW";
          touch(o);
          log(o, user, "verification.review_started");
          return send(res, 200, reviewDetail(s)), true;
        case "request-info":
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (status !== "UNDER_REVIEW") return err(res, 409, "CASE_STATE_CHANGED"), true;
          o.status = "ADDITIONAL_INFORMATION_REQUIRED";
          o.information_requests.push({ id: randomUUID(), message: String(body.message ?? ""), items: Array.isArray(body.items) ? body.items : [], requested_at: now(), responded_at: null });
          touch(o);
          log(o, user, "verification.information_requested");
          return send(res, 200, reviewDetail(s)), true;
        case "approve": {
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (!["UNDER_REVIEW", "ESCALATED", "PENDING_VERIFICATION"].includes(status) || o.review.pending_approval) return err(res, 409, "CASE_STATE_CHANGED"), true;
          if ((s.type === "BENEFICIARY" && o.beneficiary_type === "MINOR") || o.fourEyes) {
            o.review.pending_approval = { approved_by: ref(user), approved_at: now() };
            touch(o);
            log(o, user, "verification.first_approval", body);
            return send(res, 202, { status: "AWAITING_SECOND_APPROVAL" }), true;
          }
          applyDecision(s, user, "APPROVED", body);
          return send(res, 200, reviewDetail(s)), true;
        }
        case "second-approval":
          if (!o.review.pending_approval) return err(res, 409, "CASE_STATE_CHANGED"), true;
          if (o.review.pending_approval.approved_by.id === user.id) return err(res, 403, "SECOND_APPROVER_MUST_DIFFER"), true;
          applyDecision(s, user, "APPROVED", body);
          return send(res, 200, reviewDetail(s)), true;
        case "reject":
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (!["UNDER_REVIEW", "ESCALATED", "PENDING_VERIFICATION"].includes(status)) return err(res, 409, "CASE_STATE_CHANGED"), true;
          applyDecision(s, user, "REJECTED", body);
          return send(res, 200, reviewDetail(s)), true;
        case "escalate": {
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (status !== "UNDER_REVIEW") return err(res, 409, "CASE_STATE_CHANGED"), true;
          o.status = "ESCALATED";
          touch(o);
          log(o, user, "verification.escalated", body);
          // Shape of internal/compliance (case_number, bare-id assignee, links with roles, resolution.status).
          const cc = {
            id: randomUUID(), case_number: `CC-${String(complianceCases.size + 1).padStart(4, "0")}`, case_type: `${s.type === "PAYOUT_DESTINATION" ? "PAYOUT_DESTINATION" : s.type}_REVIEW`,
            status: "OPEN", severity: "S2", confidentiality: "NORMAL", source: "KYC_ESCALATION", opening_reason_code: body.reason_code ?? "OTHER",
            opened_by: { type: "SYSTEM", id: null, job: "kyc.case_escalated" }, opened_at: now(), assigned_to: null, assigned_at: null, resolution: null, closure: null,
            links: [
              { id: randomUUID(), subject_type: s.subject.type, subject_id: s.subject.id, role: "PRIMARY_SUBJECT", linked_at: now() },
              { id: randomUUID(), subject_type: `${s.type}_CASE`, subject_id: o.id, role: "RELATED_OBJECT", linked_at: now() },
            ],
            version: 1, created_at: now(), updated_at: now(), events: [], notes: [],
          };
          complianceCases.set(cc.id, cc);
          return send(res, 200, reviewDetail(s)), true;
        }
        case "suspend":
        case "reinstate":
        case "revoke":
        case "reopen":
        case "return": {
          if (!mineAssigned) return err(res, 409, "NOT_ASSIGNED"), true;
          if (s.type === "PAYOUT_DESTINATION" && !["suspend", "reinstate"].includes(action)) return err(res, 409, "ACTION_NOT_ALLOWED"), true;
          const allowed = { suspend: ["APPROVED", "VERIFIED"], reinstate: ["SUSPENDED"], revoke: ["APPROVED", "SUSPENDED"], reopen: ["SUSPENDED"], return: ["ESCALATED"] }[action];
          if (!allowed.includes(status)) return err(res, 409, "CASE_STATE_CHANGED"), true;
          o.status = { suspend: "SUSPENDED", reinstate: s.type === "PAYOUT_DESTINATION" ? "VERIFIED" : "APPROVED", revoke: "REVOKED", reopen: "UNDER_REVIEW", return: "UNDER_REVIEW" }[action];
          touch(o);
          log(o, user, `verification.${action}`, body);
          return send(res, 200, reviewDetail(s)), true;
        }
        case "reveal-identity-number":
          if (!ps.has("kyc.identity_number.reveal")) return err(res, 403, "PERMISSION_DENIED"), true;
          if (String(body.justification ?? "").trim().length < 10) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "justification", code: "TOO_SHORT" }] }), true;
          if (s.type !== "KYC" || !o.identity?.id_document_number) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
          log(o, user, "kyc.identity_number_revealed", { note: "justification recorded" });
          return send(res, 200, { id_document_number: o.identity.id_document_number }), true;
        default:
          return false;
      }
    }

    // ------------------------------------------------------------------ compliance
    if (seg[0] === "admin" && seg[1] === "compliance" && seg[2] === "cases") {
      if (!user || user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      const ps = perms(user);
      if (!ps.has("compliance.case.view")) return err(res, 403, "PERMISSION_DENIED"), true;
      const view = (c) => ({ ...c });
      if (!seg[3] && method === "GET") {
        let list = [...complianceCases.values()];
        const status = url.searchParams.get("status");
        const severity = url.searchParams.get("severity");
        const assigned = url.searchParams.get("assigned") ?? "any";
        if (status) list = list.filter((c) => c.status === status);
        if (severity) list = list.filter((c) => c.severity === severity);
        if (assigned === "me") list = list.filter((c) => c.assigned_to?.id === user.id);
        if (assigned === "unassigned") list = list.filter((c) => !c.assigned_to);
        const SUMMARY_KEYS = ["id", "case_number", "case_type", "severity", "status", "confidentiality", "source", "opening_reason_code", "opened_by", "opened_at", "assigned_to", "assigned_at", "resolution", "closure", "links", "version", "created_at", "updated_at"];
        return send(res, 200, list.map((c) => Object.fromEntries(SUMMARY_KEYS.map((k) => [k, c[k]])))), true;
      }
      const c = complianceCases.get(seg[3]);
      if (!c) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      if (method === "GET" && seg.length === 4) return send(res, 200, view(c)), true;
      if (method !== "POST") return false;
      if (!ps.has("case.manage")) return err(res, 403, "PERMISSION_DENIED"), true;
      const action = seg[4];
      if (["resolve", "approve-resolution", "close", "reopen"].includes(action) && !stepUpFresh()) return err(res, 403, "STEP_UP_REQUIRED"), true;
      const event = (a, extra = {}) => c.events.push({ id: randomUUID(), case_version: c.version, event_type: a, from_status: null, to_status: c.status, actor_type: "STAFF", actor_id: user.id, actor_job: null, reason_code: extra.reason_code ?? null, payload: {}, occurred_at: now() });
      const mine = c.assigned_to === user.id;
      const reasonOk = /^[A-Z][A-Z0-9_]{2,63}$/.test(String(body.reason_code ?? ""));
      if (["request-info", "escalate", "resolve", "close", "reopen"].includes(action) && !reasonOk) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason_code", code: "INVALID_FORMAT" }] }), true;
      const move = (to) => {
        c.status = to;
        c.updated_at = now();
      };
      switch (action) {
        case "assign":
          c.assigned_to = user.id;
          c.assigned_at = now();
          if (c.status === "OPEN") move("ASSIGNED");
          event("ASSIGNED");
          break;
        case "start":
          if (!mine) return err(res, 409, "NOT_ASSIGNED"), true;
          if (!["ASSIGNED", "AWAITING_INFORMATION", "ESCALATED"].includes(c.status)) return err(res, 409, "CASE_STATE_CHANGED"), true;
          move("IN_REVIEW");
          event("STATUS_CHANGED");
          break;
        case "request-info":
          if (!mine) return err(res, 409, "NOT_ASSIGNED"), true;
          move("AWAITING_INFORMATION");
          event("INFO_REQUESTED", body);
          break;
        case "escalate":
          if (!mine) return err(res, 409, "NOT_ASSIGNED"), true;
          move("ESCALATED");
          if (body.severity) c.severity = body.severity;
          event("ESCALATED", body);
          break;
        case "notes":
          c.notes.push({ id: randomUUID(), author_id: user.id, visibility: "COMPLIANCE", body: String(body.body ?? ""), created_at: now() });
          event("NOTE_ADDED");
          break;
        case "resolve":
          if (!mine) return err(res, 409, "NOT_ASSIGNED"), true;
          {
            const checker = ["RESTRICT", "SUSPEND", "OFFBOARD", "CONFIRMED_FRAUD"].includes(body.decision);
            c.resolution = { decision: body.decision, reason_code: body.reason_code, status: checker ? "PROPOSED" : "APPROVED", requires_approval: checker, decided_by: user.id, decided_at: now(), approved_by: checker ? null : user.id, approved_at: checker ? null : now() };
          }
          move("RESOLVED");
          event("RESOLVED", body);
          break;
        case "approve-resolution":
          if (!c.resolution || c.resolution.status !== "PROPOSED") return err(res, 409, "CASE_STATE_CHANGED"), true;
          if (c.resolution.decided_by === user.id) return err(res, 403, "SELF_APPROVAL_FORBIDDEN"), true;
          c.resolution = { ...c.resolution, status: "APPROVED", approved_by: user.id, approved_at: now() };
          event("RESOLUTION_APPROVED");
          break;
        case "close":
          if (c.resolution?.status !== "APPROVED") return err(res, 409, "RESOLUTION_NOT_APPROVED"), true;
          move("CLOSED");
          c.closure = { closed_by: user.id, closed_at: now(), reason_code: body.reason_code };
          event("CLOSED", body);
          break;
        case "reopen":
          move("IN_REVIEW");
          c.resolution = null;
          c.closure = null;
          event("REOPENED", body);
          break;
        default:
          return false;
      }
      return send(res, 200, view(c)), true;
    }
    return false;
  }

  // =====================================================================================================
  /** Test-only control endpoints (`/api/v1/__mock/...`). */
  async function control(path, body, res) {
    if (path === "/api/v1/__mock/kyc-submitted") {
      const user = users.get(body.email);
      if (!user) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      const c = createKycCase(user);
      c.fourEyes = body.four_eyes === true;
      c.identity = {
        legal_first_name: body.first_name ?? "Chipo", legal_last_name: body.last_name ?? "Ncube", date_of_birth: "1990-04-18", nationality: "ZW", country_of_residence: "ZW",
        id_document_type: "ZW_NATIONAL_ID", id_document_number: "63-123456 A 12", id_document_expiry: null,
        residential_address: { line1: "12 Samora Machel Ave", line2: null, city: "Harare", province: "Harare", postal_code: null, country: "ZW" },
      };
      const png = Buffer.from("89504e470d0a1a0a0000000d49484452", "hex");
      for (const side of ["FRONT", "BACK"]) {
        const doc = { id: randomUUID(), subject_type: "KYC_CASE", subject_id: c.id, document_type: "ZW_NATIONAL_ID", side, status: "QUARANTINED", media_type: "image/png", size_bytes: png.length, uploaded_at: now(), createdMs: Date.now() - 10_000, content: png, malicious: false, uploader: user.id };
        documents.set(doc.id, doc);
      }
      c.status = "SUBMITTED";
      c.submitted_at = now();
      log(c, user, "kyc.case_submitted");
      return send(res, 201, { case_id: c.id }), true;
    }
    const m = /^\/api\/v1\/__mock\/review\/([^/]+)\/request-info$/.exec(path);
    if (m) {
      const s = allReviewSubjects().find((x) => x.obj.id === m[1]);
      if (!s) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      s.obj.status = "ADDITIONAL_INFORMATION_REQUIRED";
      s.obj.information_requests.push({ id: randomUUID(), message: String(body.message ?? "Please upload a clearer photo."), items: body.items ?? [], requested_at: now(), responded_at: null });
      touch(s.obj);
      return send(res, 200, { status: s.obj.status }), true;
    }
    const cc = /^\/api\/v1\/__mock\/compliance-case-for\/([^/]+)$/.exec(path);
    if (cc) {
      const found = [...complianceCases.values()].find((c) => c.links.some((l) => l.subject_id === cc[1]));
      if (!found) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      return send(res, 200, { id: found.id, case_number: found.case_number }), true;
    }
    if (path === "/api/v1/__mock/org-member") {
      const org = orgs.get(body.org_id);
      const user = users.get(body.email);
      if (!org || !user) return err(res, 404, "RESOURCE_NOT_FOUND"), true;
      org.members.set(user.id, body.role ?? "ORG_MEMBER");
      return send(res, 200, { ok: true }), true;
    }
    return false;
  }

  return { handle, control, setLevel, me };
}
