import { resolveApiBaseUrl } from "./lib/api/config";

/**
 * Fail fast on misconfiguration in production so a container never serves pages that cannot reach the
 * API. Skipped during `next build`: API_BASE_URL is a runtime setting, not needed to build the image.
 */
export function validateServerConfig(): void {
  if (process.env.NEXT_PHASE === "phase-production-build") return;
  if (process.env.NODE_ENV !== "production") return;
  try {
    resolveApiBaseUrl(process.env);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.error(`[fundzim-web] FATAL configuration error: ${message}`);
    process.exit(1);
  }
}
