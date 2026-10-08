/**
 * Content Security Policy (docs/FRONTEND.md §8; closes security review F-04 / KI-06).
 *
 * Strict, nonce-based policy generated per request in src/proxy.ts. Next.js reads the nonce from the
 * request's Content-Security-Policy header and stamps it on every framework/page script and stylesheet
 * it renders. That only works for request-time renders, so the root layout opts the whole app into
 * request-time rendering (see src/app/layout.tsx). Verified by e2e/csp.spec.ts against the production build.
 *
 *  - script-src: nonce + 'strict-dynamic' (scripts loaded by nonced scripts are trusted; host allow-lists and
 *    'self' are ignored by CSP3 browsers). No 'unsafe-inline', and no 'unsafe-eval' outside `next dev`.
 *  - style-src: 'self' + nonce. No 'unsafe-inline': Tailwind compiles to a same-origin stylesheet and the
 *    app renders no inline style attributes. `next dev` keeps 'unsafe-inline' for styles (dev overlay).
 */
export interface CspOptions {
  nonce: string;
  isDev?: boolean;
}

export function buildCsp({ nonce, isDev = false }: CspOptions): string {
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${isDev ? " 'unsafe-eval'" : ""}`,
    `style-src 'self' ${isDev ? "'unsafe-inline'" : `'nonce-${nonce}'`}`,
    "img-src 'self' data: blob:",
    "font-src 'self'",
    `connect-src 'self'${isDev ? " ws: wss:" : ""}`,
    "manifest-src 'self'",
    "worker-src 'self'",
    "frame-src 'none'",
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'self'",
    "frame-ancestors 'none'",
  ].join("; ");
}

/** Policy for non-HTML responses (API proxy, health): nothing may load or frame them. */
export const NON_DOCUMENT_CSP = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'";

/** 128-bit random nonce, base64 (the CSP nonce grammar Next.js parses: [A-Za-z0-9+/_-]+={0,2}). */
export function generateNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
