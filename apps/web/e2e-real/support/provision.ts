import { execFileSync } from "node:child_process";
import { randomBytes, randomInt } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

import { E2E_NOTE, REPO_ROOT, TEST_PASSWORD } from "./env";
import { pdfDocument, pngImage } from "./files";
import { ApiError, ApiSession, type ApiResult } from "./http";
import { tokenFrom, waitForMail } from "./mailpit";
import type { CampaignRef, Fixture, StaffCred } from "./state";
import { saveFixture, storageStatePath } from "./state";
import { totpCode } from "./totp";

/**
 * Provisioning through the REAL API (via the web proxy) and Mailpit — the TypeScript counterpart of the Go
 * integration helpers (tests/integration/identity_test.go, verification_test.go, campaigns_test.go). The only
 * direct database operation is the documented test-environment fixture scripts/e2e/reset-super-admins.sh
 * (run by scripts/e2e/bootstrap-admins.sh before the bootstrap ceremony).
 */

export function tag(n = 6): string {
  return randomBytes(8).toString("base64url").replace(/[^a-z0-9]/gi, "").toLowerCase().slice(0, n).padEnd(n, "x");
}

export function uniqueEmail(prefix: string): string {
  return `e2e-real-${prefix}-${Date.now().toString(36)}${tag(4)}@example.test`;
}

async function poll<T>(what: string, timeoutMs: number, fn: () => Promise<T | undefined | null | false>, intervalMs = 750): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}

function field<T = string>(r: ApiResult, name: string): T {
  return (r.data as Record<string, unknown>)[name] as T;
}

/** Runs async tasks with bounded concurrency, preserving order. */
export async function mapLimit<I, O>(items: I[], limit: number, fn: (item: I, index: number) => Promise<O>): Promise<O[]> {
  const out: O[] = new Array(items.length);
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, async () => {
      while (next < items.length) {
        const i = next++;
        out[i] = await fn(items[i]!, i);
      }
    }),
  );
  return out;
}

// ---------------------------------------------------------------- personal accounts ------------------

export interface PersonalUser {
  session: ApiSession;
  email: string;
  id: string;
  displayName: string;
}

async function login(session: ApiSession, email: string): Promise<string> {
  const r = await session.post("/auth/login", { email, password: TEST_PASSWORD }, 200);
  return field(r, "status");
}

/** Registers, verifies the email (Mailpit link token) and the phone (dev SMS mailbox), and signs in. */
export async function registerVerifiedUser(prefix: string, displayName: string): Promise<PersonalUser> {
  const email = uniqueEmail(prefix);
  const s = new ApiSession(`user:${prefix}`);
  const reg = await s.post("/auth/register", { email, password: TEST_PASSWORD, display_name: displayName, accept_terms: true });
  // auth.register.ip: 5 per hour per address (GCRA: one more every 12 minutes); the client waits when needed.
  if (reg.status !== 202) throw new ApiError("register", reg);
  const mail = await waitForMail(email, "Confirm your FundZim email");
  await s.post("/auth/verify-email", { token: tokenFrom(mail.text) }, 200);
  if ((await login(s, email)) !== "authenticated") throw new Error(`${email}: unexpected login status`);
  // A unique Econet-format number per account: 077 + 7 digits.
  const suffix = String(randomInt(0, 10_000_000)).padStart(7, "0");
  await s.post("/me/phone/verify-request", { phone: `077${suffix}` }, 202);
  const sms = await waitForMail(`26377${suffix}@sms.dev.invalid`, "");
  const code = /\b(\d{6})\b/.exec(sms.text)?.[1];
  if (!code) throw new Error(`no code in the dev SMS: ${sms.text}`);
  await s.post("/me/phone/verify-confirm", { phone: `+26377${suffix}`, code }, 200);
  const me = await s.get("/me", 200);
  return { session: s, email, id: field(me, "id"), displayName };
}

/** Age declaration (ADR-037) → BASIC_VERIFIED once the kyc consumer has evaluated it. */
export async function attestAge(u: PersonalUser) {
  await u.session.post("/me/age-attestation", { outcome: "ATTESTED", statement_version: "age-statement-v1" }, 201);
  await poll("BASIC_VERIFIED", 60_000, async () => {
    const r = await u.session.get("/kyc/status", 200);
    return ["BASIC_VERIFIED", "IDENTITY_VERIFIED"].includes(field(r, "level"));
  });
}

let docSeq = 0;
async function uploadDocument(u: PersonalUser, caseId: string, docType: string, side: string): Promise<string> {
  const form = new FormData();
  form.append("subject_type", "KYC_CASE");
  form.append("subject_id", caseId);
  form.append("document_type", docType);
  form.append("side", side);
  form.append("file", new Blob([new Uint8Array(pdfDocument(`${docType}-${caseId}-${side}-${docSeq++}-${tag()}`))], { type: "application/pdf" }), `${docType.toLowerCase()}.pdf`);
  const r = await u.session.request("POST", "/verification/documents", { form, expect: 201 });
  return field(r, "id");
}

/** KYC case for IDENTITY_VERIFIED with an ID document and a selfie, scanned CLEAN by ClamAV, submitted. */
export async function submitKyc(u: PersonalUser, legal: { first: string; last: string }): Promise<string> {
  const created = await u.session.post("/kyc/cases", { target_level: "IDENTITY_VERIFIED" }, 201);
  const caseId = field(created, "id");
  const expiry = new Date(Date.now() + 3 * 365 * 24 * 3600 * 1000).toISOString().slice(0, 10);
  const idNumber = `63-${String(randomInt(0, 1_000_000)).padStart(6, "0")}A${String(randomInt(0, 100)).padStart(2, "0")}`;
  await u.session.patch(
    `/kyc/cases/${caseId}`,
    {
      legal_first_name: legal.first,
      legal_last_name: legal.last,
      date_of_birth: "1990-05-01",
      nationality: "ZW",
      country_of_residence: "ZW",
      id_document_type: "ZW_NATIONAL_ID",
      id_document_number: idNumber,
      id_document_expiry: expiry,
      residential_address: { line1: "12 Samora Machel Ave", city: "Harare", country: "ZW" },
    },
    created.headers.get("ETag") ?? "",
    200,
  );
  const docs = [await uploadDocument(u, caseId, "ZW_NATIONAL_ID", "FRONT"), await uploadDocument(u, caseId, "SELFIE", "NA")];
  for (const d of docs) {
    const st = await poll(`document ${d} scanned`, 180_000, async () => {
      const r = await u.session.get(`/verification/documents/${d}`, 200);
      const s = field(r, "status");
      return ["CLEAN", "REJECTED", "FAILED_SCAN", "DELETED"].includes(s) ? s : null;
    }, 1000);
    if (st !== "CLEAN") throw new Error(`identity document ${d} ended ${st} (is ClamAV healthy?)`);
  }
  await u.session.post(`/kyc/cases/${caseId}/submit`, {}, 200);
  return caseId;
}

/** A KYC reviewer assigns, starts and approves the case (as approvedKYC in verification_test.go). */
export async function approveKyc(reviewer: ApiSession, caseId: string, owner: PersonalUser) {
  const base = `/admin/verification/cases/${caseId}`;
  await reviewer.post(`${base}/assign`, {}, 200);
  await reviewer.post(`${base}/start-review`, {}, 200);
  await reviewer.post(`${base}/approve`, { reason_code: "DOCUMENTS_CONSISTENT", note: "ID document and selfie match (e2e)" }, 200);
  await poll("IDENTITY_VERIFIED", 60_000, async () => field(await owner.session.get("/kyc/status", 200), "level") === "IDENTITY_VERIFIED");
}

// ---------------------------------------------------------------- staff ------------------------------

/** Completes a staff invitation from its email: password + TOTP enrolment. Returns the TOTP secret. */
export async function acceptInvitation(email: string): Promise<string> {
  const mail = await waitForMail(email, "staff account invitation");
  const token = tokenFrom(mail.text);
  const s = new ApiSession(`invite:${email}`);
  const r = await s.post("/auth/staff-invitation/start", { token }, 200);
  const secret = field(r, "secret");
  await s.post("/auth/staff-invitation/finish", async () => ({ token, enrollment_id: field(r, "enrollment_id"), code: await totpCode(secret), password: TEST_PASSWORD }), 200);
  return secret;
}

/** Staff sign-in through the API: password, then a TOTP code (staff always have MFA). */
export async function staffLogin(c: StaffCred): Promise<ApiSession> {
  const s = new ApiSession(`staff:${c.key}`);
  s.totpSecret = c.secret;
  const r = await s.post("/auth/login", { email: c.email, password: TEST_PASSWORD });
  if (r.status !== 200 || field(r, "status") !== "mfa_required") throw new ApiError(`staff login ${c.key}`, r);
  await s.post("/auth/mfa/verify", async () => ({ code: await totpCode(c.secret) }), 200);
  return s;
}

export interface StaffPool {
  fixture: Fixture;
  /** A signed-in API session for a staff member (reused across runs while the session is alive). */
  session(c: StaffCred): Promise<ApiSession>;
}

export function staffPool(fixture: Fixture): StaffPool {
  const live = new Map<string, ApiSession>();
  return {
    fixture,
    async session(c) {
      const cached = live.get(c.email);
      if (cached) return cached;
      const saved = fixture.sessions[c.email];
      if (saved) {
        const s = ApiSession.fromJSON(saved, c.secret);
        const me = await s.get("/me");
        if (me.status === 200 && field(me, "id") === c.id) {
          live.set(c.email, s);
          return s;
        }
      }
      // A session the specs signed in through the UI (Playwright storage state), when still alive.
      try {
        const stored = JSON.parse(fs.readFileSync(storageStatePath(c.key), "utf8")) as { cookies: { name: string; value: string; httpOnly: boolean }[] };
        const s = ApiSession.fromJSON({ label: `staff:${c.key}`, cookies: stored.cookies.map(({ name, value, httpOnly }) => ({ name, value, httpOnly })) }, c.secret);
        const me = await s.get("/me");
        if (me.status === 200 && field(me, "id") === c.id) {
          live.set(c.email, s);
          return s;
        }
      } catch {
        // no stored browser session
      }
      const s = await staffLogin(c);
      live.set(c.email, s);
      fixture.sessions[c.email] = s.toJSON();
      saveFixture(fixture);
      return s;
    },
  };
}

/** Invites a staff member (admin A), accepts the invitation, and grants `role` with maker-checker (A asks, B approves). */
export async function inviteStaff(pool: StaffPool, key: string, name: string, role: string | null): Promise<StaffCred> {
  const a = await pool.session(pool.fixture.staff.adminA!);
  const b = await pool.session(pool.fixture.staff.adminB!);
  const email = uniqueEmail(`staff-${key.toLowerCase()}`);
  const r = await a.post("/admin/staff", { email, display_name: name, justification: "real-stack e2e staff fixture" }, 201);
  const id = field(r, "user_id");
  const secret = await acceptInvitation(email);
  if (role) {
    const req = await a.post("/admin/role-assignment-requests", { user_id: id, role_code: role, action: "GRANT", justification: "real-stack e2e role fixture" }, 201);
    await b.post(`/admin/role-assignment-requests/${field(req, "id")}/approve`, { reason: "real-stack e2e fixture (second admin)" }, 200);
  }
  return { key, name, role, email, id, secret };
}

/**
 * The super-admin bootstrap ceremony: scripts/e2e/bootstrap-admins.sh revokes active SUPER_ADMIN
 * assignments (test-environment fixture, as resetSuperAdmins in the Go tests) and runs
 * `go run ./apps/api/cmd/fundzimctl bootstrap-admins`; both admins then accept their invitations.
 */
export async function bootstrapAdmins(fixture: Fixture) {
  const a = { key: "adminA", name: "E2E Admin A", role: "SUPER_ADMIN", email: uniqueEmail("admin-a"), id: "", secret: "" };
  const b = { key: "adminB", name: "E2E Admin B", role: "SUPER_ADMIN", email: uniqueEmail("admin-b"), id: "", secret: "" };
  const out = execFileSync(path.join(REPO_ROOT, "scripts/e2e/bootstrap-admins.sh"), [a.email, a.name, b.email, b.name], {
    cwd: REPO_ROOT,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    timeout: 600_000,
  });
  const m = /staff accounts ([0-9a-f-]{36}) and ([0-9a-f-]{36})/.exec(out);
  if (!m) throw new Error(`bootstrap-admins output not understood: ${out}`);
  a.id = m[1]!;
  b.id = m[2]!;
  a.secret = await acceptInvitation(a.email);
  b.secret = await acceptInvitation(b.email);
  fixture.staff.adminA = a;
  fixture.staff.adminB = b;
  saveFixture(fixture);
}

async function isActiveSuperAdmin(pool: StaffPool, c: StaffCred | undefined): Promise<boolean> {
  if (!c) return false;
  try {
    const s = await pool.session(c);
    const me = await s.get("/me", 200);
    return (field<string[]>(me, "roles") ?? []).includes("SUPER_ADMIN");
  } catch {
    return false;
  }
}

/** Persistent staff roles; each is created once per database and re-validated on every run. */
export const PERSISTENT_STAFF: { key: string; name: string; role: string }[] = [
  { key: "kycReviewer", name: "E2E KYC Reviewer", role: "KYC_REVIEWER" },
  { key: "provReviewer", name: "E2E Provisioning Reviewer", role: "REVIEWER" },
  { key: "provCompliance", name: "E2E Provisioning Compliance", role: "COMPLIANCE" },
  { key: "moderator", name: "E2E Content Moderator", role: "ADMIN" },
  { key: "reviewer", name: "Rumbi Reviewer", role: "REVIEWER" },
  { key: "compliance", name: "Farai Compliance", role: "COMPLIANCE" },
];

async function holdsRole(pool: StaffPool, c: StaffCred | undefined, role: string): Promise<boolean> {
  if (!c) return false;
  try {
    const s = await pool.session(c);
    return (field<string[]>(await s.get("/me", 200), "roles") ?? []).includes(role);
  } catch {
    return false;
  }
}

/** Spare REVIEWERs for the linked-account test (a staff link is permanent, so each run consumes one). */
const LINK_CANDIDATE_BATCH = 4;

/**
 * Ensures every persistent role holder exists and at least one unlinked REVIEWER candidate is available;
 * recreates what no longer works. The super admins are only checked (and, if revoked, re-bootstrapped)
 * when something has to be created: every invitation and the ceremony spend the shared per-IP token budget
 * (auth.token.ip, 20 per 15 minutes), so a normal run creates no staff at all.
 */
export async function ensureStaff(pool: StaffPool, log: (m: string) => void) {
  const f = pool.fixture;
  f.linkCandidates ??= [];
  const missing: (typeof PERSISTENT_STAFF)[number][] = [];
  for (const want of PERSISTENT_STAFF) if (!(await holdsRole(pool, f.staff[want.key], want.role))) missing.push(want);
  const usable: StaffCred[] = [];
  // Validate candidates in order until one works (each check may cost a login); keep the rest for later runs.
  for (const [i, c] of f.linkCandidates.entries()) {
    if (!(await holdsRole(pool, c, "REVIEWER"))) continue;
    const link = await (await pool.session(c)).get("/admin/me/personal-account-link");
    if (link.status === 200 && !/"linked"\s*:\s*true/.test(link.text)) {
      usable.push(c, ...f.linkCandidates.slice(i + 1));
      break;
    }
  }
  f.linkCandidates = usable;
  saveFixture(f);
  if (missing.length === 0 && usable.length > 0) return;

  if (!(await isActiveSuperAdmin(pool, f.staff.adminA)) || !(await isActiveSuperAdmin(pool, f.staff.adminB))) {
    log("bootstrap ceremony: (re)creating two super admins");
    await bootstrapAdmins(f);
  }
  for (const want of missing) {
    log(`staff fixture: creating ${want.key} (${want.role})`);
    f.staff[want.key] = await inviteStaff(pool, want.key, want.name, want.role);
    delete f.sessions[f.staff[want.key]!.email];
    saveFixture(f);
  }
  if (usable.length === 0) {
    log(`staff fixture: creating ${LINK_CANDIDATE_BATCH} unlinked REVIEWER candidates for the linked-account test`);
    for (let i = 0; i < LINK_CANDIDATE_BATCH; i++) {
      const k = tag(5);
      f.linkCandidates.push(await inviteStaff(pool, `linked${k}`, `Linked Reviewer ${k.toUpperCase()}`, "REVIEWER"));
      saveFixture(f);
    }
  }
}

/** Takes one unlinked REVIEWER for this run (it becomes permanently linked to this run's owner). */
export function takeLinkCandidate(f: Fixture): StaffCred {
  const c = f.linkCandidates?.shift();
  if (!c) throw new Error("no link candidate available (ensureStaff must run first)");
  delete f.sessions[c.email];
  saveFixture(f);
  return c;
}

// ---------------------------------------------------------------- campaigns --------------------------

export const STORY =
  "Tariro is a bright student in Harare who needs help with school fees for the coming year. " +
  "The funds raised will cover tuition, uniforms and books, paid directly against the school's fee statement.";

export interface CampaignSpec {
  title: string;
  category: string;
  summary?: string;
  story?: string;
  goalMinor?: string;
}

let imageSeed = randomInt(0, 1000);

/** Creates a complete draft: story, the owner's SELF beneficiary (disclosure NONE) and an APPROVED cover. */
export async function createReadyCampaign(owner: PersonalUser, beneficiaryId: string, spec: CampaignSpec, withCover = true): Promise<CampaignRef> {
  const r = await owner.session.post(
    "/campaigns",
    {
      title: spec.title,
      summary: spec.summary ?? "Help with school fees for the coming school year in Harare.",
      category: spec.category,
      goal: { amount_minor: spec.goalMinor ?? "150000", currency: "USD" },
    },
    201,
  );
  const id = field(r, "id");
  await owner.session.patch(`/campaigns/${id}`, { story: spec.story ?? STORY }, r.headers.get("ETag") ?? "", 200);
  await owner.session.post(`/campaigns/${id}/beneficiaries`, { beneficiary_id: beneficiaryId, disclosure: "NONE" }, 200);
  if (withCover) await uploadCover(owner, id);
  return { id, title: spec.title, slug: null, category: spec.category };
}

/** Uploads a PNG cover and waits until the media pipeline (ClamAV scan + re-encode) approves it. */
export async function uploadCover(owner: PersonalUser, campaignId: string): Promise<string> {
  const form = new FormData();
  form.append("kind", "COVER");
  form.append("alt_text", "Students outside a school building");
  form.append("depicts_minor", "false");
  form.append("file", new Blob([new Uint8Array(pngImage(imageSeed++))], { type: "image/png" }), "cover.png");
  const r = await owner.session.request("POST", `/campaigns/${campaignId}/media`, { form, expect: 201 });
  const mediaId = field(r, "id");
  await poll(`cover ${mediaId} approved`, 180_000, async () => {
    const list = await owner.session.get(`/campaigns/${campaignId}/media`, 200);
    const d = list.data as { media?: { id: string; status: string }[]; items?: { id: string; status: string }[] };
    const m = (d.items ?? d.media ?? []).find((x) => x.id === mediaId);
    if (m && ["REJECTED", "REMOVED", "FAILED"].includes(m.status)) throw new Error(`cover ${mediaId} ended ${m.status}`);
    return m?.status === "APPROVED";
  }, 1000);
  return mediaId;
}

export async function submitCampaign(owner: PersonalUser, c: CampaignRef) {
  await owner.session.post(`/campaigns/${c.id}/submit`, {}, 200);
}

export async function campaignStatus(owner: PersonalUser, id: string): Promise<{ status: string; slug: string | null; etag: string }> {
  const r = await owner.session.get(`/campaigns/${id}`, 200);
  return { status: field(r, "status"), slug: field(r, "slug") ?? null, etag: r.headers.get("ETag") ?? "" };
}

export const decision = (code: string) => ({ reason_code: code, note: E2E_NOTE });

/** Reviewer approval (plus a COMPLIANCE second approval for HIGH-tier categories), then the owner publishes. */
export async function approveAndPublish(owner: PersonalUser, reviewer: ApiSession, compliance: ApiSession, c: CampaignRef): Promise<CampaignRef> {
  await reviewer.post(`/admin/campaigns/${c.id}/assign`, {}, 200);
  await reviewer.post(`/admin/campaigns/${c.id}/start-review`, {}, 200);
  const r = await reviewer.post(`/admin/campaigns/${c.id}/approve`, decision("MEETS_POLICY"), [200, 202]);
  if (r.status === 202) await compliance.post(`/admin/campaigns/${c.id}/second-approval`, { note: "second approval (e2e provisioning)" }, 200);
  await owner.session.post(`/campaigns/${c.id}/publish`, {}, 200);
  const st = await campaignStatus(owner, c.id);
  if (st.status !== "ACTIVE" || !st.slug) throw new Error(`campaign ${c.id} not live after publish: ${st.status}`);
  return { ...c, slug: st.slug };
}
