import Link from "next/link";

import { formatDateTime } from "@/components/account/format";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise } from "@/lib/campaigns/labels";
import type { StaffCampaignSummary } from "@/lib/campaigns/types";

/** Staff campaign table (queue and search). Titles are user content: rendered as escaped text only. */
export function StaffCampaignTable({ items, caption }: { items: StaffCampaignSummary[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No campaigns match" description="Change the filters or check again later." />;
  return (
    <div className="overflow-x-auto rounded-card border border-line bg-surface">
      <table className="w-full min-w-[44rem] text-left text-ink-700">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr className="border-b border-line text-sm text-ink-600">
            <th scope="col" className="p-3">Campaign</th>
            <th scope="col" className="p-3">Status</th>
            <th scope="col" className="p-3">Category</th>
            <th scope="col" className="p-3">Risk tier</th>
            <th scope="col" className="p-3">Assigned to</th>
            <th scope="col" className="p-3">Submitted</th>
          </tr>
        </thead>
        <tbody>
          {items.map((c) => (
            <tr key={c.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-semibold">
                <Link href={`/admin/campaigns/review/${encodeURIComponent(c.id)}`} className="break-words text-brand-700 underline">
                  {c.title || "Untitled"}
                </Link>
                {c.owner_display_name ? <span className="block text-sm font-normal text-ink-600">by {c.owner_display_name}</span> : null}
              </th>
              <td className="p-3">
                <CampaignStatusBadge status={c.status} audience="staff" />
                {c.awaiting_second_approval ? <span className="mt-1 block"><Badge tone="info">Awaiting second approval</Badge></span> : null}
                {c.escalated ? <span className="mt-1 block"><Badge tone="gold">Escalated</Badge></span> : null}
              </td>
              <td className="p-3">{humanise(c.category)}</td>
              <td className="p-3">{c.risk_tier ? <Badge tone={c.risk_tier === "STANDARD" ? "neutral" : "gold"}>{humanise(c.risk_tier)}</Badge> : "—"}</td>
              <td className="p-3">{c.assigned_to?.display_name ?? "Unassigned"}</td>
              <td className="p-3">{formatDateTime(c.submitted_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
