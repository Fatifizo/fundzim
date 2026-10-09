import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { StaffCampaignTable } from "@/components/admin/campaign-table";
import { inputClasses } from "@/components/forms/text-field";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor, readStaffSummaries } from "@/lib/campaigns/normalise";
import { first, isCode, isCursor } from "@/lib/campaigns/paths";

export const metadata: Metadata = { title: "Campaign reviews", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/campaigns/review";
const ASSIGNED = [
  { value: "any", label: "Anyone or no one" },
  { value: "me", label: "Assigned to me" },
  { value: "unassigned", label: "Unassigned" },
] as const;

/** Review queue (contract §6, `campaign.review`). Non-staff and staff without the permission get a 404. */
export default async function CampaignReviewQueuePage({ searchParams }: PageProps<"/admin/campaigns/review">) {
  await requireStaff();
  const p = await searchParams;
  const assignedRaw = first(p.assigned);
  const assigned = assignedRaw === "me" || assignedRaw === "unassigned" ? assignedRaw : "any";
  const categoryRaw = first(p.category);
  const category = isCode(categoryRaw) ? categoryRaw : undefined;
  const cursorRaw = first(p.cursor);
  const cursor = isCursor(cursorRaw) ? cursorRaw : undefined;
  const q = new URLSearchParams();
  q.set("assigned", assigned);
  if (category) q.set("category", category);
  if (cursor) q.set("cursor", cursor);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/campaigns/review?${q.toString()}`, BASE);
  const items = readStaffSummaries(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  const next = nextRaw && isCursor(nextRaw) ? nextRaw : null;
  const nextQ = new URLSearchParams(q);
  if (next) nextQ.set("cursor", next);
  return (
    <AccountPage title="Campaign reviews" intro="Open reviews (queued, assigned or in review), oldest first. Open one to assign it, review it and record a decision. Other statuses are in All campaigns.">
      <form method="get" action={BASE} className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end" aria-label="Filter the queue">
        <div className="space-y-1">
          <label htmlFor="filter-assigned" className="block font-semibold text-ink-900">Assigned</label>
          <select id="filter-assigned" name="assigned" defaultValue={assigned} className={inputClasses}>
            {ASSIGNED.map((a) => (
              <option key={a.value} value={a.value}>{a.label}</option>
            ))}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor="filter-category" className="block font-semibold text-ink-900">Category code</label>
          <input id="filter-category" name="category" defaultValue={category ?? ""} className={inputClasses} autoCapitalize="characters" />
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>Apply filters</button>
        </div>
      </form>
      <StaffCampaignTable items={items} caption="Campaign review queue" />
      {next ? (
        <ButtonLink href={`${BASE}?${nextQ.toString()}`} variant="outline">
          Next page
        </ButtonLink>
      ) : null}
    </AccountPage>
  );
}
