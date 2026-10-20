import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { PlacementActions, PlacementRequestForm } from "@/components/admin/discovery/discovery-admin";
import { Section, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { first, isUuid } from "@/lib/campaigns/paths";
import { readPlacements, type Placement } from "@/lib/discovery/admin";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Featured placements", robots: { index: false, follow: false, nocache: true } };

function PlacementItem({ p }: { p: Placement }) {
  return (
    <li className="space-y-2 rounded-card border border-line bg-surface p-5">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone="neutral">Position {p.position}</Badge>
        <Badge tone={p.status === "APPROVED" ? "brand" : p.status === "REQUESTED" ? "gold" : "neutral"}>{humanise(p.status)}</Badge>
        {p.live ? <Badge tone="brand">Shown now</Badge> : null}
        {p.promotable === false ? <Badge tone="gold">Not shown: campaign not promotable</Badge> : null}
      </div>
      <h3 className="font-semibold break-words text-ink-900">
        <Link href={`/admin/campaigns/review/${encodeURIComponent(p.campaignId)}`} className="text-brand-700 underline">
          {p.campaignTitle || p.campaignId}
        </Link>
      </h3>
      <p className="text-sm break-words text-ink-700">Reason: {p.reason}</p>
      <p className="text-sm text-ink-600">
        Window: <When at={p.startsAt} /> to <When at={p.endsAt} /> · requested <When at={p.requestedAt} />
      </p>
      {p.decisionNote ? <p className="text-sm break-words text-ink-600">Decision note: {p.decisionNote}</p> : null}
      {p.removalReason ? <p className="text-sm break-words text-ink-600">Removed: {p.removalReason}</p> : null}
      {/* always mounted: the confirmation survives the refresh that closes the placement */}
      <PlacementActions id={p.id} status={p.status} />
    </li>
  );
}

/**
 * Featured placements (`discovery.view` to see; request, approve and remove need their own permissions with step-up).
 * Maker-checker: the requester (or a linked account) can never approve. Replaces the Stage 7 single-person staff picks.
 */
export default async function FeaturedPlacementsPage({ searchParams }: PageProps<"/admin/discovery/featured">) {
  await requireStaff();
  const idRaw = first((await searchParams).campaign_id);
  const items = readPlacements((await serverGetOr404<unknown>("/api/v1/admin/discovery/featured", "/admin/discovery/featured")).data);
  const openCount = items.filter((p) => p.status === "REQUESTED" || p.status === "APPROVED").length;
  return (
    <AccountPage title="Featured placements" intro="Placements are requested by one team member and approved by another. They show only inside their window, and only while the campaign is active, public and free of restrictions and holds.">
      <Section id="placements" title="Placements" description={`${openCount} open (waiting for approval or approved). Closed placements follow, newest first.`}>
        {items.length === 0 ? (
          <EmptyState title="No placements yet" description="The public featured list then shows the most recently published campaigns." />
        ) : (
          // one list ordered by the API (open first): an item keeps its place in the tree when it closes
          <ol className="space-y-3" aria-label="Featured placements">
            {items.map((p) => (
              <PlacementItem key={p.id} p={p} />
            ))}
          </ol>
        )}
      </Section>
      <Section id="request" title="Request a placement" description="Featuring is not an endorsement. Choose campaigns that show the range of causes on FundZim; nobody can pay for it.">
        <div className="rounded-card border border-line bg-surface p-5">
          <PlacementRequestForm initialCampaignId={isUuid(idRaw) ? idRaw : ""} />
        </div>
      </Section>
    </AccountPage>
  );
}
