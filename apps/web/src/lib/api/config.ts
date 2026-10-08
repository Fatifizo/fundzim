/**
 * Resolution of the server-only API origin (`API_BASE_URL`).
 *
 * - `API_BASE_URL` is read at REQUEST time on the server (never inlined into the browser bundle: it has
 *   no NEXT_PUBLIC_ prefix). One container image therefore works in every environment.
 * - Development/test default: http://127.0.0.1:8080.
 * - Production (`NODE_ENV=production`): there is NO default. A missing or invalid value is a
 *   configuration error: the server refuses to start (src/instrumentation.ts) and, defensively, every
 *   API call fails with ApiConfigError instead of silently guessing a host.
 */
export const DEV_DEFAULT_API_BASE_URL = "http://127.0.0.1:8080";

export class ApiConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ApiConfigError";
  }
}

export interface ApiEnv {
  API_BASE_URL?: string;
  NODE_ENV?: string;
}

/** Returns the normalised API origin (scheme://host[:port], no trailing slash). */
export function resolveApiBaseUrl(env: ApiEnv): string {
  const raw = env.API_BASE_URL?.trim();
  if (!raw) {
    if (env.NODE_ENV === "production") {
      throw new ApiConfigError(
        "API_BASE_URL is not set. It is required in production (e.g. http://api:8080). See apps/web/README.md.",
      );
    }
    return DEV_DEFAULT_API_BASE_URL;
  }
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new ApiConfigError("API_BASE_URL is not a valid URL.");
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new ApiConfigError("API_BASE_URL must use http or https.");
  }
  if (url.username || url.password) {
    throw new ApiConfigError("API_BASE_URL must not contain credentials.");
  }
  if ((url.pathname !== "/" && url.pathname !== "") || url.search || url.hash) {
    throw new ApiConfigError("API_BASE_URL must be an origin only (no path, query or fragment).");
  }
  return url.origin;
}
