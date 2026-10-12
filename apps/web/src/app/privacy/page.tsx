import type { Metadata } from "next";

import { ContentPage } from "@/components/content-page";
import { Alert } from "@/components/ui/alert";

export const metadata: Metadata = {
  title: "Privacy notice (draft placeholder)",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <ContentPage
      title="Privacy notice"
      notice={
        <Alert tone="notice" title="DRAFT — placeholder only">
          This is not the FundZim privacy notice. Counsel will draft it before FundZim opens (LEGAL_REVIEW_REQUIRED).
        </Alert>
      }
    >
      <p>A full privacy notice, describing what FundZim collects, why, how long it is kept and your rights, will be published before the platform opens. Until then, this page describes in plain terms what the development preview does.</p>
      <h2>Reading and sharing campaigns</h2>
      <p>FundZim counts some public events — a campaign page was viewed, a share button was used, a search was made, a category was opened — as daily totals. It does not use cookies for this, does not store your IP address or an identifier with these counts, does not record what you searched for, and sends nothing at all when your browser asks not to be tracked (Do Not Track or Global Privacy Control). There are no third-party trackers or advertising scripts.</p>
      <h2>Reporting a campaign</h2>
      <p>A report contains the reason you chose and any details you wrote. An email address is optional; if you give one, it is stored encrypted, used only to contact you about that report, and never shown to the organiser.</p>
      <h2>Offline support</h2>
      <p>To load faster, your browser may keep a copy of FundZim&apos;s styles and scripts and a simple &ldquo;you are offline&rdquo; page. Campaign pages, account pages and your data are never stored on your device this way.</p>
    </ContentPage>
  );
}
