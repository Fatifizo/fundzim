import type { Metadata } from "next";

import { ModuleOperations } from "@/components/admin/operations/module-operations";

export const metadata: Metadata = { title: "Campaign operations", robots: { index: false, follow: false, nocache: true } };

/** Campaign operations (`campaign.view`): moderation backlog, reports and reviews from the campaigns module. */
export default function CampaignOperationsPage() {
  return (
    <ModuleOperations
      path="/admin/operations/campaigns"
      title="Campaign operations"
      intro="Review and moderation backlogs and open reports. Decisions are taken in campaign review."
      modules={["campaigns"]}
      links={[
        { href: "/admin/campaigns/review", label: "Campaign reviews", permission: "campaign.review" },
        { href: "/admin/campaigns/reports", label: "Campaign reports", permission: "campaign.review" },
        { href: "/admin/campaigns/updates", label: "Update moderation", permission: "content.moderate" },
        { href: "/admin/campaigns", label: "All campaigns", permission: "campaign.view" },
      ]}
    />
  );
}
