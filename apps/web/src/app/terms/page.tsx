import type { Metadata } from "next";

import { ContentPage } from "@/components/content-page";
import { Alert } from "@/components/ui/alert";

export const metadata: Metadata = {
  title: "Terms of use (draft placeholder)",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <ContentPage
      title="Terms of use"
      notice={
        <Alert tone="notice" title="DRAFT — placeholder only">
          This is not the FundZim terms of use. Counsel will draft it before FundZim opens (LEGAL_REVIEW_REQUIRED).
        </Alert>
      }
    >
      <p>FundZim is a development preview. Campaigns can be prepared, reviewed, published and shared, but donations and payments are not available: no one can donate or receive funds through FundZim. Terms governing the use of FundZim will be published before the platform opens.</p>
      <p>Until then, please do not send money to anyone because of a FundZim campaign page, and report any campaign that worries you using the button on its page.</p>
    </ContentPage>
  );
}
