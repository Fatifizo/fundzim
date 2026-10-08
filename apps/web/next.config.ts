import type { NextConfig } from "next";

/**
 * Content Security Policy for HTML documents is generated PER REQUEST with a nonce in src/proxy.ts
 * (src/lib/security/csp.ts; FRONTEND.md §8). It is deliberately NOT set here: a static policy would need
 * 'unsafe-inline' for Next.js's inline bootstrap scripts. Non-document routes the proxy does not run on
 * (the /api/v1 proxy, /healthz) get a "load nothing" policy below.
 */
const NON_DOCUMENT_CSP = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'";

const securityHeaders = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Permissions-Policy",
    value:
      "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), browsing-topics=()",
  },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  // Strict-Transport-Security is set by the TLS-terminating reverse proxy (docs/SECURITY.md §11),
  // not here: the container itself serves plain HTTP on a private network.
];

const nextConfig: NextConfig = {
  // cacheComponents (Partial Prerendering) is deliberately OFF: PPR serves a build-time static shell whose
  // framework scripts carry no nonce (blocked by the strict CSP), and a resumed render cannot change the
  // shell's HTTP status (404s became 200 and redirect() became client-side). Every document is rendered
  // per request instead (src/app/layout.tsx). Measured trade-offs: apps/web/README.md "Security headers".
  output: "standalone",
  poweredByHeader: false,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async headers() {
    return [
      { source: "/:path*", headers: securityHeaders },
      { source: "/api/:path*", headers: [{ key: "Content-Security-Policy", value: NON_DOCUMENT_CSP }] },
      { source: "/healthz", headers: [{ key: "Content-Security-Policy", value: NON_DOCUMENT_CSP }] },
    ];
  },
  // NOTE: no rewrites() for /api/v1. next.config is evaluated at build time and serialised into the
  // standalone server, which would bake in the build-time API_BASE_URL. The runtime proxy lives in
  // src/app/api/v1/[...path]/route.ts instead.
};

export default nextConfig;
