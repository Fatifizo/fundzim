import type { Metadata, Viewport } from "next";
import { connection } from "next/server";

import { DevPreviewBanner } from "@/components/layout/dev-preview-banner";
import { SiteFooter } from "@/components/layout/site-footer";
import { SiteHeader } from "@/components/layout/site-header";

import "./globals.css";

export const metadata: Metadata = {
  title: {
    template: "%s · FundZim",
    default: "FundZim — Fund anyone in Zimbabwe, from anywhere",
  },
  description:
    "FundZim is a Zimbabwe-first donation crowdfunding platform under development. It is not yet open for fundraising.",
  applicationName: "FundZim",
  // Site-wide noindex while this is a development preview: nothing here is a real campaign or a live
  // service, and search engines must not present it as one. Remove deliberately at launch (Stage 20).
  robots: { index: false, follow: false, nocache: true },
};

export const viewport: Viewport = {
  themeColor: "#1b5e40",
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
      </body>
    </html>
  );
}
