import { connection } from "next/server";

import { isApiError } from "@/lib/api/errors";
import { getServerApi } from "@/lib/api/server";
import type { HealthData, VersionData } from "@/lib/api/types";

import { ApiStatusView, type ApiStatusResult } from "./api-status-view";

/** Fetches API health (and version, best effort). Never throws: the page must render without the API. */
export async function loadApiStatus(): Promise<ApiStatusResult> {
  try {
    const api = getServerApi({ timeoutMs: 2_000, maxRetries: 1 });
    await api.get<HealthData>("/api/v1/health");
    let version: VersionData | null = null;
    try {
      version = (await api.get<VersionData>("/api/v1/version", { retries: 0 })).data;
    } catch {
      version = null;
    }
    return { state: "up", version };
  } catch (error) {
    return isApiError(error)
      ? { state: "down", code: error.code, requestId: error.requestId }
      : { state: "down", code: "CONFIGURATION_ERROR" };
  }
}

/** Server Component: request-time status of the backend (rendered inside a Suspense boundary). */
export async function ApiStatus() {
  await connection();
  const status = await loadApiStatus();
  return <ApiStatusView status={status} />;
}
