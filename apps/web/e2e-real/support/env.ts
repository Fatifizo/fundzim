import fs from "node:fs";
import path from "node:path";

/**
 * Real-stack E2E environment. Everything runs against the compose stack that must already be up
 * (`docker compose up -d --build`); nothing here starts or mocks a service.
 *
 * The web origin is http://localhost:3000 (not 127.0.0.1): the API checks Origin against APP_PUBLIC_URL.
 * Provisioning calls go through the web container's same-origin proxy (/api/v1/*), exactly like a browser.
 */
export const WEB_URL = (process.env.FUNDZIM_E2E_WEB_URL ?? "http://localhost:3000").replace(/\/$/, "");
export const MAILPIT_URL = (process.env.FUNDZIM_E2E_MAILPIT_URL ?? "http://127.0.0.1:8025").replace(/\/$/, "");

/** Repository root (apps/web/e2e-real/support → ../../../..). */
export const REPO_ROOT = path.resolve(__dirname, "..", "..", "..", "..");

/** Git-ignored working state: persistent staff fixture, this run's data, used TOTP steps. */
export const STATE_DIR = path.resolve(__dirname, "..", ".state");

/**
 * Password for every account the suite creates (synthetic test accounts on a local/CI stack only).
 * Built from words so it is obviously not a credential.
 */
export const TEST_PASSWORD = ["amber", "kettle", "orbit", "tamarind", "lantern", "57"].join(" ");

/** Same reason codes and notes everywhere so failures are easy to read in the audit log. */
export const E2E_NOTE = "real-stack E2E provisioning (synthetic data)";

/** A value from the process environment, else from the repository .env that docker compose interpolated. */
function composeSetting(name: string): string | undefined {
  if (process.env[name] !== undefined) return process.env[name];
  try {
    const line = fs
      .readFileSync(path.join(REPO_ROOT, ".env"), "utf8")
      .split("\n")
      .find((l) => l.startsWith(`${name}=`));
    return line?.slice(name.length + 1).replace(/\s+#.*$/, "").trim();
  } catch {
    return undefined;
  }
}

/**
 * The web container's PUBLIC_SITE_URL (canonical, Open Graph and share URLs) and SITE_INDEXING (robots and
 * noindex), as compose passes them (compose defaults: http://localhost:3000 and disallow). The CI job sets
 * SITE_INDEXING=allow to assert the full indexing behaviour.
 */
export const PUBLIC_SITE_URL = (process.env.FUNDZIM_E2E_PUBLIC_SITE_URL ?? composeSetting("PUBLIC_SITE_URL") ?? "http://localhost:3000").replace(/\/$/, "");
export const SITE_INDEXING_ALLOWED = (composeSetting("SITE_INDEXING") ?? "disallow").toLowerCase() === "allow";
