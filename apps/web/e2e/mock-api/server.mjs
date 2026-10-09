// In-memory mock of the Stage 4 authentication API (docs/stage-4/interface-contracts.md §4) for E2E tests.
//
// Why a mock SERVER (not only Playwright route interception): protected pages verify the session
// server-side (Server Components call API_BASE_URL directly), which browser interception cannot reach.
// The Next.js server under test is started with API_BASE_URL pointing here, so requests go
// browser → Next.js /api/v1 proxy → this server, exercising the real proxy (cookies, CSRF header,
// X-Forwarded-For). Tests still use route interception for edge cases (e.g. 429).
//
// It enforces the contract's cookie + CSRF rules (Origin check, X-CSRF-Token == CSRF cookie on unsafe
// requests with a session) so the frontend's CSRF handling is actually tested. NOT a security reference.
import { randomBytes, randomUUID } from "node:crypto";
import http from "node:http";

import { createCampaignsMock } from "./campaigns.mjs";
import { createVerificationMock } from "./verification.mjs";

/** Widely published documentation example (base32), not a credential. */
const EXAMPLE_TOTP_BASE32 = "JBSWY3DP" + "EHPK3PXP";

const PORT = Number(process.env.MOCK_API_PORT ?? 3199);
const APP_ORIGIN = process.env.MOCK_APP_ORIGIN ?? "http://127.0.0.1:3101";
const STEP_UP_TTL_MS = 5 * 60_000;

export const FIXTURES = {
  password: "correct horse battery staple",
  totp: "246810",
  recoveryCode: "RCVR-0001-AAAA",
  verifyToken: "valid-verify-token",
  resetToken: "valid-reset-token",
  phoneCode: "135790",
  staffToken: "valid-staff-token",
};

/** @type {Map<string, any>} email → user */
const users = new Map();
/** @type {Map<string, {userId: string, csrf: string, createdAt: string, stepUpAt: number, mfa: boolean}>} */
const sessions = new Map();
/** @type {Map<string, string>} mfa challenge token → userId */
const challenges = new Map();
const enrollments = new Map();
let lastRequest = null;

function now() {
  return new Date().toISOString();
}

function addUser({ email, display_name, email_verified = true, mfa_enabled = false, password = FIXTURES.password, phone_verified = false, account_kind = "USER", roles = undefined }) {
  const user = {
    id: randomUUID(),
    account_kind,
    roles,
    email,
    email_verified,
    display_name,
    phone_masked: phone_verified ? "+263 77 *** **67" : null,
    phone_verified,
    mfa_enabled,
    created_at: "2026-09-01T08:00:00Z",
    password,
    recovery: mfa_enabled ? [FIXTURES.recoveryCode] : [],
    password_changed_at: null,
    events: [{ type: "login_succeeded", occurred_at: "2026-10-01T10:00:00Z" }],
  };
  users.set(email, user);
  return user;
}

addUser({ email: "user@example.test", display_name: "Tendai Moyo" });
addUser({ email: "mfa@example.test", display_name: "Rudo MFA", mfa_enabled: true });
addUser({ email: "unverified@example.test", display_name: "Farai New", email_verified: false });

const ME_FIELDS = ["id", "account_kind", "email", "email_verified", "display_name", "phone_masked", "phone_verified", "mfa_enabled", "created_at"];

function me(user) {
  const out = Object.fromEntries(ME_FIELDS.map((key) => [key, user[key]]));
  if (user.account_kind === "STAFF") out.roles = user.roles ?? [];
  return out;
}

function userById(id) {
  for (const user of users.values()) if (user.id === id) return user;
  return undefined;
}

function parseCookies(header) {
  const out = {};
  for (const part of (header ?? "").split(";")) {
    const i = part.indexOf("=");
    if (i > 0) out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

function send(res, status, body, headers = {}) {
  const requestId = randomUUID();
  if (status === 204) {
    res.writeHead(204, { "X-Request-ID": requestId, ...headers });
    res.end();
    return;
  }
  const payload = JSON.stringify(status < 400 ? { data: body, meta: { request_id: requestId } } : { error: body, meta: { request_id: requestId } });
  res.writeHead(status, { "Content-Type": "application/json", "X-Request-ID": requestId, "Cache-Control": "no-store", ...headers });
  res.end(payload);
}

const err = (res, status, code, extra = {}, headers = {}) =>
  send(res, status, { code, message: code.toLowerCase().replaceAll("_", " "), retryable: status === 429 || status >= 500, ...extra }, headers);

function sessionCookies(token, csrf) {
  return [`fz_session=${token}; Path=/; HttpOnly; SameSite=Lax`, `fz_csrf=${csrf}; Path=/; SameSite=Lax`, "fz_mfa=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict"];
}

const clearCookies = ["fz_session=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax", "fz_csrf=; Path=/; Max-Age=0; SameSite=Lax", "fz_mfa=; Path=/; Max-Age=0; HttpOnly; SameSite=Strict"];

function createSession(res, user, mfa) {
  const token = randomBytes(24).toString("hex");
  const csrf = randomBytes(24).toString("hex");
  sessions.set(token, { userId: user.id, csrf, createdAt: now(), stepUpAt: 0, mfa });
  send(res, 200, { status: "authenticated", user: me(user) }, { "Set-Cookie": sessionCookies(token, csrf) });
}

async function readRaw(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  return Buffer.concat(chunks);
}

function parseJson(raw, contentType) {
  if (raw.length === 0 || /multipart\/form-data/i.test(contentType ?? "")) return {};
  try {
    return JSON.parse(raw.toString("utf8"));
  } catch {
    return {};
  }
}

async function readJson(req) {
  return parseJson(await readRaw(req), req.headers["content-type"]);
}

const verification = createVerificationMock({ send, err, userById, users, addUser, me, stepUpTtlMs: STEP_UP_TTL_MS });
const campaigns = createCampaignsMock({ send, err, userById, users, verification, stepUpTtlMs: STEP_UP_TTL_MS });

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", "http://mock");
  const path = url.pathname;
  const method = req.method ?? "GET";
  const cookies = parseCookies(req.headers.cookie);
  const sessionToken = cookies.fz_session;
  const session = sessionToken ? sessions.get(sessionToken) : undefined;
  const user = session ? userById(session.userId) : undefined;
  lastRequest = { path, method, xff: req.headers["x-forwarded-for"] ?? null, csrf: req.headers["x-csrf-token"] ?? null };

  if (path === "/__mock/health") return send(res, 200, { status: "ok" });
  if (path === "/api/v1/__mock/echo") {
    return send(res, 200, {
      x_forwarded_for: req.headers["x-forwarded-for"] ?? null,
      x_real_ip: req.headers["x-real-ip"] ?? null,
      forwarded: req.headers.forwarded ?? null,
      internal_headers: Object.keys(req.headers).filter((h) => h.startsWith("x-fz-peer-")),
    });
  }
  if (path === "/api/v1/__mock/last-request") return send(res, 200, lastRequest);
  if (path === "/api/v1/__mock/users" && method === "POST") {
    const body = await readJson(req);
    const created = addUser({ email: body.email, display_name: body.display_name ?? "Test User", mfa_enabled: !!body.mfa_enabled, email_verified: body.email_verified !== false, phone_verified: !!body.phone_verified });
    if (body.kyc_level) verification.setLevel(created.id, body.kyc_level);
    return send(res, 201, me(created));
  }
  if (path === "/api/v1/__mock/staff" && method === "POST") {
    const body = await readJson(req);
    const created = addUser({ email: body.email, display_name: body.display_name ?? "Staff Reviewer", mfa_enabled: true, account_kind: "STAFF", roles: body.roles ?? ["KYC_REVIEWER"] });
    return send(res, 201, me(created));
  }
  if (path.startsWith("/api/v1/__mock/") && method === "POST") {
    const controlBody = await readJson(req);
    if (await verification.control(path, controlBody, res)) return;
    if (await campaigns.control(path, controlBody, res)) return;
  }
  if (path === "/api/v1/health") return send(res, 200, { status: "ok" });
  if (path === "/api/v1/version") return send(res, 200, { name: "fundzim-api", version: "mock", build: "mock", commit: "mock" });

  // ---- CSRF (contract §4.1) ----
  const unsafe = !["GET", "HEAD", "OPTIONS"].includes(method);
  if (unsafe) {
    const origin = req.headers.origin;
    if ((origin && origin !== APP_ORIGIN) || req.headers["sec-fetch-site"] === "cross-site") return err(res, 403, "CSRF_ORIGIN_MISMATCH");
    if (session && req.headers["x-csrf-token"] !== session.csrf) return err(res, 403, "CSRF_TOKEN_INVALID");
  }

  const raw = unsafe ? await readRaw(req) : Buffer.alloc(0);
  const body = parseJson(raw, req.headers["content-type"]);
  const requireAuth = () => {
    if (!user) {
      err(res, 401, "AUTHENTICATION_REQUIRED");
      return false;
    }
    return true;
  };
  const requireStepUp = () => {
    if (!requireAuth()) return false;
    if (Date.now() - session.stepUpAt > STEP_UP_TTL_MS) {
      err(res, 403, "STEP_UP_REQUIRED");
      return false;
    }
    return true;
  };

  switch (`${method} ${path}`) {
    case "GET /api/v1/auth/session": {
      if (user) return send(res, 200, { authenticated: true, user: me(user) });
      return send(res, 200, { authenticated: false, mfa_pending: !!(cookies.fz_mfa && challenges.has(cookies.fz_mfa)) });
    }
    case "POST /api/v1/auth/register": {
      if (typeof body.password !== "string" || body.password.length < 12) {
        return err(res, 422, "PASSWORD_POLICY_VIOLATION", { details: [{ field: "password", code: "TOO_SHORT" }] });
      }
      if (!users.has(body.email)) {
        const created = addUser({ email: body.email, display_name: body.display_name, email_verified: false, password: body.password });
        if (body.age_attestation === true) campaigns.recordAttestation(created.id, "ATTESTED", "REGISTRATION");
      }
      return send(res, 202, { status: "verification_sent" });
    }
    case "POST /api/v1/auth/verify-email":
      return body.token === FIXTURES.verifyToken ? send(res, 200, { status: "verified" }) : err(res, 400, "TOKEN_INVALID");
    case "POST /api/v1/auth/resend-verification":
    case "POST /api/v1/auth/forgot-password":
      return send(res, 202, { status: path.endsWith("forgot-password") ? "reset_requested" : "verification_sent" });
    case "POST /api/v1/auth/reset-password":
      if (body.token !== FIXTURES.resetToken) return err(res, 400, "TOKEN_INVALID");
      return send(res, 200, { status: "password_reset" });
    case "POST /api/v1/auth/login": {
      if (body.email === "ratelimited@example.test") return err(res, 429, "RATE_LIMITED", {}, { "Retry-After": "30" });
      const found = users.get(body.email);
      if (!found || found.password !== body.password) return err(res, 401, "INVALID_CREDENTIALS");
      if (found.mfa_enabled) {
        const challenge = randomBytes(16).toString("hex");
        challenges.set(challenge, found.id);
        return send(res, 200, { status: "mfa_required", methods: ["totp", "recovery_code"] }, { "Set-Cookie": `fz_mfa=${challenge}; Path=/; HttpOnly; SameSite=Strict; Max-Age=300` });
      }
      return createSession(res, found, false);
    }
    case "POST /api/v1/auth/mfa/verify":
    case "POST /api/v1/auth/mfa/recovery": {
      const userId = cookies.fz_mfa ? challenges.get(cookies.fz_mfa) : undefined;
      if (!userId) return err(res, 401, "MFA_CHALLENGE_EXPIRED");
      const found = userById(userId);
      const ok = path.endsWith("verify") ? body.code === FIXTURES.totp : found.recovery.includes(body.recovery_code);
      if (!ok) return err(res, 401, "MFA_CODE_INVALID");
      challenges.delete(cookies.fz_mfa);
      return createSession(res, found, true);
    }
    case "POST /api/v1/auth/logout":
      if (!requireAuth()) return;
      sessions.delete(sessionToken);
      return send(res, 204, null, { "Set-Cookie": clearCookies });
    case "POST /api/v1/auth/logout-all":
      if (!requireAuth()) return;
      for (const [token, s] of sessions) if (s.userId === user.id) sessions.delete(token);
      return send(res, 204, null, { "Set-Cookie": clearCookies });
    case "POST /api/v1/auth/step-up/verify": {
      if (!requireAuth()) return;
      const ok = body.password !== undefined ? body.password === user.password : body.code === FIXTURES.totp;
      if (!ok) return err(res, 401, body.password !== undefined ? "INVALID_CREDENTIALS" : "MFA_CODE_INVALID");
      session.stepUpAt = Date.now();
      return send(res, 200, { step_up_expires_at: new Date(Date.now() + STEP_UP_TTL_MS).toISOString() });
    }
    case "GET /api/v1/me":
      if (!requireAuth()) return;
      return send(res, 200, me(user));
    case "PATCH /api/v1/me":
      if (!requireAuth()) return;
      user.display_name = String(body.display_name ?? "").trim() || user.display_name;
      return send(res, 200, me(user));
    case "POST /api/v1/me/password":
      if (!requireAuth()) return;
      if (body.current_password !== user.password) return err(res, 401, "INVALID_CREDENTIALS");
      user.password = body.new_password;
      user.password_changed_at = now();
      return send(res, 204, null);
    case "GET /api/v1/me/security":
      if (!requireAuth()) return;
      return send(res, 200, {
        email_verified: user.email_verified,
        phone_verified: user.phone_verified,
        mfa_enabled: user.mfa_enabled,
        recovery_codes_remaining: user.recovery.length,
        password_changed_at: user.password_changed_at,
        recent_events: user.events,
      });
    case "GET /api/v1/me/sessions":
      if (!requireAuth()) return;
      return send(
        res,
        200,
        [...sessions.entries()]
          .filter(([, s]) => s.userId === user.id)
          .map(([token, s]) => ({
            id: token.slice(0, 12),
            current: token === sessionToken,
            created_at: s.createdAt,
            last_seen_at: now(),
            ip_masked: "127.0.0.x",
            user_agent: "Playwright browser",
            auth_method: "password",
            mfa: s.mfa,
          })),
      );
    case "POST /api/v1/me/mfa/enroll": {
      if (!requireStepUp()) return;
      const enrollment_id = randomUUID();
      const secret = EXAMPLE_TOTP_BASE32;
      enrollments.set(enrollment_id, user.id);
      return send(res, 200, { enrollment_id, secret, otpauth_uri: `otpauth://totp/FundZim:${encodeURIComponent(user.email)}?secret=${secret}&issuer=FundZim` });
    }
    case "POST /api/v1/me/mfa/confirm":
      if (!requireStepUp()) return;
      if (enrollments.get(body.enrollment_id) !== user.id) return err(res, 404, "RESOURCE_NOT_FOUND");
      if (body.code !== FIXTURES.totp) return err(res, 401, "MFA_CODE_INVALID");
      user.mfa_enabled = true;
      user.recovery = Array.from({ length: 10 }, (_, i) => `CODE-${String(i).padStart(4, "0")}-${randomBytes(2).toString("hex").toUpperCase()}`);
      return send(res, 200, { recovery_codes: user.recovery });
    case "POST /api/v1/me/mfa/disable":
      if (!requireStepUp()) return;
      if (body.code !== FIXTURES.totp && !user.recovery.includes(body.code)) return err(res, 401, "MFA_CODE_INVALID");
      user.mfa_enabled = false;
      user.recovery = [];
      return send(res, 204, null);
    case "POST /api/v1/me/mfa/recovery-codes":
      if (!requireStepUp()) return;
      user.recovery = Array.from({ length: 10 }, (_, i) => `NEW-${String(i).padStart(4, "0")}-${randomBytes(2).toString("hex").toUpperCase()}`);
      return send(res, 200, { recovery_codes: user.recovery });
    case "POST /api/v1/me/phone/verify-request":
      if (!requireAuth()) return;
      return send(res, 202, { phone_masked: "+263 77 *** **67", expires_at: new Date(Date.now() + 5 * 60_000).toISOString() });
    case "POST /api/v1/me/phone/verify-confirm":
      if (!requireAuth()) return;
      if (body.code !== FIXTURES.phoneCode) return err(res, 422, "OTP_INVALID");
      user.phone_verified = true;
      user.phone_masked = "+263 77 *** **67";
      return send(res, 200, { phone_verified: true, phone_masked: user.phone_masked });
    case "POST /api/v1/auth/staff-invitation/start":
      if (body.token !== FIXTURES.staffToken) return err(res, 400, "TOKEN_INVALID");
      return send(res, 200, { email: "staff@example.test", display_name: "Staff Person", enrollment_id: "staff-enrol-1", secret: EXAMPLE_TOTP_BASE32, otpauth_uri: "otpauth://totp/FundZim:staff%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=FundZim" });
    case "POST /api/v1/auth/staff-invitation/finish":
      if (body.token !== FIXTURES.staffToken) return err(res, 400, "TOKEN_INVALID");
      if (typeof body.password !== "string" || body.password.length < 12) return err(res, 422, "PASSWORD_POLICY_VIOLATION", { details: [{ field: "password", code: "TOO_SHORT" }] });
      if (body.code !== FIXTURES.totp || body.enrollment_id !== "staff-enrol-1") return err(res, 401, "MFA_CODE_INVALID");
      return send(res, 200, { status: "activated", recovery_codes: Array.from({ length: 10 }, (_, i) => `STAFF-${String(i).padStart(4, "0")}`) });
    default: {
      const sessionMatch = /^\/api\/v1\/me\/sessions\/([^/]+)$/.exec(path);
      if (method === "DELETE" && sessionMatch) {
        if (!requireAuth()) return;
        for (const [token, s] of sessions) {
          if (token.slice(0, 12) === sessionMatch[1] && s.userId === user.id) {
            sessions.delete(token);
            return send(res, 204, null);
          }
        }
        return err(res, 404, "RESOURCE_NOT_FOUND");
      }
      if (await campaigns.handle({ req, res, method, path, url, user, session, body, raw })) return;
      if (await verification.handle({ req, res, method, path, url, user, session, sessionToken, body, raw })) return;
      return err(res, 404, "ROUTE_NOT_FOUND");
    }
  }
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`[mock-api] listening on http://127.0.0.1:${PORT} (app origin ${APP_ORIGIN})`);
});

for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
