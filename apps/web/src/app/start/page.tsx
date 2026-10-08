import type { Metadata } from "next";

import { StagePlaceholder } from "@/components/stage-placeholder";

export const metadata: Metadata = {
  title: "Start a fundraiser",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <StagePlaceholder
      title="Start a fundraiser"
      stage="Stage 6"
      description="Creating a campaign is not available yet. Campaign creation and review are being built."
      planned={["Create a campaign for yourself, someone you know, or a verified organisation", "Identity verification before a campaign can receive funds", "Review of every campaign before it is published"]}
    />
  );
}
