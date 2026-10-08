import type { NextConfig } from "next";

const isDev = process.env.NODE_ENV === "development";

/**
 * Content Security Policy (static, set via headers()).
 *
 * Stage 3 deviation from docs/FRONTEND.md §8 (nonce-based CSP): a per-request nonce forces every page
 * to render dynamically and, per the bundled Next.js CSP guide, is incompatible with Partial
 * Prerendering, which `cacheComponents` enables. Next.js also emits inline bootstrap/flight scripts, so
 * without nonces `script-src` needs 'unsafe-inline'. Mitigations: no third-party origins anywhere,
 * no 'unsafe-eval' in production, object-src/base-uri/frame-ancestors locked down, and no user-generated
 * HTML is rendered in Stage 3. Revisit (nonce via proxy.ts, or hashes) before Stage 7 renders
 * owner-supplied content — tracked in apps/web/README.md.
 */
const csp = [
  "default-src 'self'",
  `script-src 'self' 'unsafe-inline'${isDev ? " 'unsafe-eval'" : ""}`,
  "style-src 'self' 'unsafe-inline'",
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

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
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
  cacheComponents: true,
  partialPrefetching: true,
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
    return [{ source: "/:path*", headers: securityHeaders }];
  },
  // NOTE: no rewrites() for /api/v1. next.config is evaluated at build time and serialised into the
  // standalone server, which would bake in the build-time API_BASE_URL. The runtime proxy lives in
  // src/app/api/v1/[...path]/route.ts instead.
};

export default nextConfig;
