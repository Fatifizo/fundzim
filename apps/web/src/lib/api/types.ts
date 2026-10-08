/**
 * Hand-maintained wire types for the FundZim `/api/v1` envelope (docs/api/api-design.md §4,
 * docs/api/error-model.md). The OpenAPI document `api/openapi/fundzim-v1.yaml` is the source of truth
 * (ADR-026). These types are intentionally small; when endpoint DTOs are needed (Stage 4+), generate
 * them from the OpenAPI document (see apps/web/README.md, "Generated API types") and keep this file
 * for the envelope only.
 */

/** Currencies supported in Stage 3. Extended from API configuration later, not hand-grown. */
export type CurrencyCode = "USD" | "ZWG";

/**
 * Money on the wire. `amount_minor` is a string of digits (never a JS number: precision is lost
 * beyond 2^53). See docs/MONEY.md §3.3.
 */
export interface Money {
  amount_minor: string;
  currency: CurrencyCode;
}

export interface ApiMeta {
  request_id: string;
  limit?: number;
  next_cursor?: string;
}

export interface ApiSuccess<T> {
  data: T;
  meta: ApiMeta;
}

export interface ApiErrorDetail {
  field: string;
  code: string;
}

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    retryable: boolean;
    details?: ApiErrorDetail[];
  };
  meta: { request_id: string };
}

/** GET /api/v1/health */
export interface HealthData {
  status: "ok";
}

/** GET /api/v1/ready (200). A 503 returns an ApiErrorBody with code SERVICE_UNAVAILABLE. */
export interface ReadyData {
  status: "ready";
}

/** GET /api/v1/version */
export interface VersionData {
  name: string;
  version: string;
  build: string;
  commit: string;
}
