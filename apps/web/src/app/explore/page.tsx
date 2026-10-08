import type { Metadata } from "next";

import { StagePlaceholder } from "@/components/stage-placeholder";

export const metadata: Metadata = {
  title: "Explore campaigns",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <StagePlaceholder
      title="Explore campaigns"
      stage="Stage 7"
      description="Browsing and searching reviewed campaigns is not available yet. There are no live campaigns in this development preview."
      planned={["Browse reviewed, published campaigns by category and location", "Campaign pages that load quickly on mobile and preview well on WhatsApp", "Raised amounts shown per currency (USD and ZiG), never combined"]}
    />
  );
}
