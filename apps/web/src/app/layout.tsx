import type { Metadata, Viewport } from "next";
import { connection } from "next/server";

import { DevPreviewBanner } from "@/components/layout/dev-preview-banner";
import { RegisterServiceWorker } from "@/components/pwa/register-service-worker";
import { SiteFooter } from "@/components/layout/site-footer";
import { SiteHeader } from "@/components/layout/site-header";

import { publicRobots } from "@/lib/seo/metadata";

import "./globals.css";

/**
 * Site-wide defaults. Indexing is opt-in per deployment (PUBLIC_SITE_URL + SITE_INDEXING=allow, see
 * src/lib/site/url.ts): until then every page is noindex, as in the development preview. Private pages
 * (dashboard, settings, admin, sign-in and account flows) set noindex themselves regardless.
 */
export function generateMetadata(): Metadata {
  return {
    title: {
      template: "%s · FundZim",
      default: "FundZim — Fund anyone in Zimbabwe, from anywhere",
    },
    description: "FundZim is a Zimbabwe-first donation crowdfunding platform under development. Donations are not available yet.",
    applicationName: "FundZim",
    robots: publicRobots(),
    icons: {
      apple: [{ url: "/icons/apple-touch-icon.png", sizes: "180x180", type: "image/png" }],
    },
    appleWebApp: { title: "FundZim", statusBarStyle: "default" },
    formatDetection: { telephone: false },
  };
}

export const viewport: Viewport = {
  themeColor: "#1b5e40",
  width: "device-width",
  initialScale: 1,
};

/**
 * Strict CSP (F-04): every document is rendered at request time (`connection()`), so Next.js can stamp the
 * per-request nonce from src/proxy.ts on its scripts and stylesheets. A prerendered page would contain
 * framework scripts without a nonce, which 'strict-dynamic' blocks. Trade-off: no static/CDN-cached HTML;
 * the pages are small, every authenticated page is per-user anyway, and the header shows the session.
 */
export default async function RootLayout({ children }: LayoutProps<"/">) {
  await connection();
  return (
    <html lang="en-ZW" className="h-full antialiased">
      <body className="flex min-h-full flex-col">
        <a
          href="#main"
          className="sr-only z-50 rounded-full bg-brand-700 px-5 py-3 font-semibold text-white focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
        >
          Skip to main content
        </a>
        <DevPreviewBanner />
        <SiteHeader />
        <main id="main" tabIndex={-1} className="flex-1 focus:outline-none">
          {children}
        </main>
        <SiteFooter />
        <RegisterServiceWorker />
      </body>
    </html>
  );
}
