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
      <p>This development preview offers no service: you cannot create campaigns, donate or receive funds. Terms governing the use of FundZim will be published before the platform opens.</p>
    </ContentPage>
  );
}
