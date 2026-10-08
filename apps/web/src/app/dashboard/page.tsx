import type { Metadata } from "next";

import { StagePlaceholder } from "@/components/stage-placeholder";

export const metadata: Metadata = {
  title: "Your dashboard",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <StagePlaceholder
      title="Your dashboard"
      stage="Stages 4–6"
      description="The account dashboard is not available yet."
      planned={["Manage your campaigns and updates", "See donations to your campaigns, per currency", "Request payouts once verification and checks are complete"]}
    />
  );
}
