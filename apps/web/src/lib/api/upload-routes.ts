/**
 * Allow-list of the only requests the same-origin /api/v1 proxy accepts with a large (upload-sized) body.
 * Everything else keeps the 1 MiB JSON cap. Matching is exact: method POST and a full-path regular
 * expression, so suffixes, extra segments, encoded slashes, dot segments or a trailing slash never match.
 *
 *  - `POST /api/v1/verification/documents` — identity documents (Stage 5, ADR-035 §5).
 *  - `POST /api/v1/campaigns/{uuid}/media` — campaign photos (Stage 6, contract §6). The campaign id must be
 *    a canonical UUID (hex and hyphens only).
 */
const UPLOAD_PATHS: readonly RegExp[] = [
  /^\/api\/v1\/verification\/documents$/,
  /^\/api\/v1\/campaigns\/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\/media$/,
];

export function isUploadRequest(method: string, pathname: string): boolean {
  return method === "POST" && UPLOAD_PATHS.some((re) => re.test(pathname));
}
