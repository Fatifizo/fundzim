import "server-only";

import { createApiClient, type ApiClient } from "./client";
import { resolveApiBaseUrl } from "./config";

/**
 * Server-side API client (Server Components, Route Handlers). Calls the API origin directly at
 * `API_BASE_URL`, read at request time. Importing this module from a Client Component fails the build
 * (`server-only`).
 */
export function getServerApi(overrides: { timeoutMs?: number; maxRetries?: number } = {}): ApiClient {
  return createApiClient({
    baseUrl: resolveApiBaseUrl(process.env),
    ...overrides,
  });
}

export function getApiBaseUrl(): string {
  return resolveApiBaseUrl(process.env);
}
