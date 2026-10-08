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
      <p>This development preview does not create accounts, does not accept donations and does not ask you for personal information. A full privacy notice, describing what FundZim collects, why, how long it is kept and your rights, will be published before the platform opens.</p>
    </ContentPage>
  );
}
