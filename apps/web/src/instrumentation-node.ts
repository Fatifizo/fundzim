import { resolveApiBaseUrl } from "./lib/api/config";
import { CidrListError } from "./lib/net/ip";
import { TRUSTED_PROXY_ENV, trustedProxyCidrs } from "./lib/net/client-ip";

export { installPeerStamp } from "./lib/net/peer-stamp";

/**
 * Fail fast on misconfiguration in production so a container never serves pages that cannot reach the
 * API or that would mis-attribute client addresses. Skipped during `next build`: these are runtime settings.
 */
export function validateServerConfig(): void {
  if (process.env.NEXT_PHASE === "phase-production-build") return;
  if (process.env.NODE_ENV !== "production") return;
  try {
    resolveApiBaseUrl(process.env);
    trustedProxyCidrs(process.env);
  } catch (error) {
    const message =
      error instanceof CidrListError ? `${TRUSTED_PROXY_ENV}: ${error.message}` : error instanceof Error ? error.message : String(error);
    console.error(`[fundzim-web] FATAL configuration error: ${message}`);
    process.exit(1);
  }
}
