import type { Metadata } from "next";

import { ContentPage } from "@/components/content-page";

export const metadata: Metadata = {
  title: "Contact",
};

export default function ContactPage() {
  return (
    <ContentPage
      title="Contact"
      intro="Contact channels for FundZim will be published before the platform opens."
    >
      <p>
        There is no contact form on this development preview, and FundZim does not collect messages or personal
        information here.
      </p>
      <p>
        FundZim will never ask you to send money, passwords or one-time codes by message. If anyone asks you to do so in
        FundZim&apos;s name, do not respond.
      </p>
    </ContentPage>
  );
}
