import type { Metadata } from "next";
import Link from "next/link";

import { ContentPage } from "@/components/content-page";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({ title: "Contact", description: "How to reach FundZim, and how to report a campaign.", path: "/contact" });
}

export default function ContactPage() {
  return (
    <ContentPage title="Contact" intro="Contact channels for FundZim will be published before donations open.">
      <h2>Worried about a campaign?</h2>
      <p>
        Use the <strong>Report this campaign</strong> button on the campaign&apos;s page. Reports go straight to the review
        team. See <Link href="/safety">safety on FundZim</Link> for what happens next.
      </p>

      <h2>Other questions</h2>
      <p>
        There is no contact form on this development preview yet, and FundZim does not collect messages here. A support
        channel will be published before donations open.
      </p>

      <h2>Stay safe</h2>
      <p>
        FundZim will never ask you to send money, passwords or one-time codes by message. If anyone asks you to do so in
        FundZim&apos;s name, do not respond.
      </p>
    </ContentPage>
  );
}
