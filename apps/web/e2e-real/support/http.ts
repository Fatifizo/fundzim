import { WEB_URL } from "./env";
import { totpCode } from "./totp";

/**
 * A tiny cookie-jar HTTP client for the FundZim API, using Node's built-in fetch. Requests go through the
 * web container's same-origin proxy (WEB_URL + /api/v1/...), carry Origin = WEB_URL, and send the CSRF
 * header from the fz_csrf cookie exactly as the browser client does.
 *
 * A staff session can carry its TOTP secret: a 403 STEP_UP_REQUIRED is then answered with
 * POST /auth/step-up/verify {code} and the request is retried once (as the web UI does).
 */
export interface ApiErrorBody {
  code?: string;
  message?: string;
  details?: unknown;
}

export interface ApiResult<T = unknown> {
  status: number;
  data: T;
  error: ApiErrorBody | null;
  headers: Headers;
  text: string;
}

export class ApiError extends Error {
  constructor(
    readonly where: string,
    readonly result: ApiResult,
  ) {
    super(`${where}: HTTP ${result.status} ${result.error?.code ?? ""} ${JSON.stringify(result.error?.details ?? "")} ${result.text.slice(0, 400)}`);
    this.name = "ApiError";
  }
}

export interface StoredCookie {
  name: string;
  value: string;
  httpOnly: boolean;
}

// The API's coarse per-IP limit is 20 requests/s (burst 40) and every request of this suite comes from one
// address; provisioning stays well below it.
const MIN_INTERVAL_MS = 60;
/** Longest total wait for one request that keeps being rate limited. */
const MAX_RATE_LIMIT_WAIT_S = 900;
let nextSlot = 0;
async function pace() {
  const now = Date.now();
  const at = Math.max(now, nextSlot);
  nextSlot = at + MIN_INTERVAL_MS;
  if (at > now) await new Promise((r) => setTimeout(r, at - now));
}

export interface RequestOptions {
  /** JSON body, or a function producing it at send time (e.g. a TOTP code that must be fresh after a wait). */
  json?: unknown;
  form?: FormData;
  headers?: Record<string, string>;
  /** Expected status(es); anything else throws ApiError. Omit to return any status. */
  expect?: number | number[];
}

export class ApiSession {
  readonly cookies = new Map<string, StoredCookie>();
  /** TOTP secret for step-up (staff). */
  totpSecret?: string;

  constructor(readonly label: string) {}

  cookie(name: string): string | undefined {
    return this.cookies.get(name)?.value;
  }

  private store(headers: Headers) {
    for (const raw of headers.getSetCookie()) {
      const [pair, ...attrs] = raw.split(";");
      const eq = pair!.indexOf("=");
      if (eq < 0) continue;
      const name = pair!.slice(0, eq).trim();
      const value = pair!.slice(eq + 1).trim();
      const lower = attrs.map((a) => a.trim().toLowerCase());
      const expired = lower.some((a) => a === "max-age=0" || a.startsWith("max-age=-")) || value === "";
      if (expired) this.cookies.delete(name);
      else this.cookies.set(name, { name, value, httpOnly: lower.includes("httponly") });
    }
  }

  async request<T = unknown>(method: string, path: string, opts: RequestOptions = {}): Promise<ApiResult<T>> {
    let result = await this.sendPatiently<T>(method, path, opts);
    if (result.status === 403 && result.error?.code === "STEP_UP_REQUIRED" && this.totpSecret) {
      await this.stepUp();
      result = await this.sendPatiently<T>(method, path, opts);
    }
    const want = opts.expect === undefined ? null : Array.isArray(opts.expect) ? opts.expect : [opts.expect];
    if (want && !want.includes(result.status)) throw new ApiError(`${this.label} ${method} ${path}`, result as ApiResult);
    return result;
  }

  async stepUp() {
    if (!this.totpSecret) throw new Error(`${this.label}: no TOTP secret for step-up`);
    await this.sendPatiently("POST", "/auth/step-up/verify", { json: async () => ({ code: await totpCode(this.totpSecret!) }) }).then((r) => {
      if (r.status !== 200 && r.status !== 204) throw new ApiError(`${this.label} step-up`, r);
    });
  }

  /**
   * The API refuses a rate-limited request before doing anything (429 RATE_LIMITED), so waiting and sending it
   * again is safe. The limiter is GCRA (continuous refill, e.g. auth.register.ip regains one registration every
   * 12 minutes). The wait comes from the identity throttle's message when it states one, else Retry-After. Every request of this suite shares one client address, so a full first-time
   * provisioning can touch the per-IP token and login limits; short waits are absorbed here, long ones fail.
   */
  private async sendPatiently<T>(method: string, path: string, opts: RequestOptions): Promise<ApiResult<T>> {
    let waited = 0;
    for (;;) {
      const r = await this.send<T>(method, path, opts);
      if (r.status !== 429) return r;
      // The message carries the real remaining window ("Try again in 25 seconds."); Retry-After carries the same wait.
      const fromMessage = Number(/in (\d+) seconds?/.exec(r.error?.message ?? "")?.[1] ?? "");
      const after = fromMessage || Number(r.headers.get("Retry-After") ?? "") || 30;
      if (after > 760 || waited + after > MAX_RATE_LIMIT_WAIT_S) return r;
      console.log(`[real-stack] ${this.label} ${method} ${path}: rate limited, waiting ${after} s`);
      await new Promise((res) => setTimeout(res, (after + 1) * 1000));
      waited += after + 1;
    }
  }

  private async send<T>(method: string, path: string, opts: RequestOptions): Promise<ApiResult<T>> {
    await pace();
    const headers: Record<string, string> = { Accept: "application/json", Origin: WEB_URL, ...(opts.headers ?? {}) };
    const cookieHeader = [...this.cookies.values()].map((c) => `${c.name}=${c.value}`).join("; ");
    if (cookieHeader) headers.Cookie = cookieHeader;
    const csrf = this.cookie("fz_csrf");
    if (csrf && method !== "GET" && method !== "HEAD") headers["X-CSRF-Token"] = csrf;
    let body: string | FormData | undefined;
    if (opts.json !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(typeof opts.json === "function" ? await (opts.json as () => Promise<unknown>)() : opts.json);
    } else if (opts.form) {
      body = opts.form;
    }
    const url = `${WEB_URL}/api/v1${path}`;
    let res: Response | undefined;
    for (let attempt = 0; ; attempt++) {
      try {
        res = await fetch(url, { method, headers, body, redirect: "manual", signal: AbortSignal.timeout(120_000) });
        break;
      } catch (error) {
        // Only connection-level failures of idempotent reads are retried; a mutation is never replayed.
        if (method !== "GET" || attempt >= 2) throw new Error(`${this.label} ${method} ${path}: ${String(error)}`);
        await new Promise((r) => setTimeout(r, 1000));
      }
    }
    this.store(res.headers);
    const text = await res.text();
    let data: unknown = null;
    let error: ApiErrorBody | null = null;
    if (text) {
      try {
        const env = JSON.parse(text) as { data?: unknown; error?: ApiErrorBody };
        data = env.data ?? null;
        error = env.error ?? null;
      } catch {
        // non-JSON body: kept in text
      }
    }
    return { status: res.status, data: data as T, error, headers: res.headers, text };
  }

  get<T = unknown>(path: string, expect?: number | number[]) {
    return this.request<T>("GET", path, { expect });
  }

  post<T = unknown>(path: string, json?: unknown | (() => Promise<unknown>), expect?: number | number[], headers?: Record<string, string>) {
    return this.request<T>("POST", path, { json: json ?? {}, expect, headers });
  }

  patch<T = unknown>(path: string, json: unknown, etag: string, expect?: number | number[]) {
    return this.request<T>("PATCH", path, { json, expect, headers: { "If-Match": etag } });
  }

  /** Cookies in the shape Playwright's BrowserContext.addCookies expects (host-only, for WEB_URL). */
  playwrightCookies() {
    const url = new URL(WEB_URL);
    return [...this.cookies.values()].map((c) => ({
      name: c.name,
      value: c.value,
      domain: url.hostname,
      path: "/",
      httpOnly: c.httpOnly,
      secure: url.protocol === "https:",
      sameSite: "Lax" as const,
    }));
  }

  toJSON(): { label: string; cookies: StoredCookie[] } {
    return { label: this.label, cookies: [...this.cookies.values()] };
  }

  static fromJSON(saved: { label: string; cookies: StoredCookie[] }, totpSecret?: string): ApiSession {
    const s = new ApiSession(saved.label);
    for (const c of saved.cookies) s.cookies.set(c.name, c);
    s.totpSecret = totpSecret;
    return s;
  }
}

/** Anonymous GET of a public API path (no cookies). */
export async function publicGet<T = unknown>(path: string): Promise<ApiResult<T>> {
  return new ApiSession("anonymous").get<T>(path);
}
