import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { StaffCampaignTable } from "@/components/admin/campaign-table";
import { inputClasses } from "@/components/forms/text-field";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { campaignStatusLabel } from "@/lib/campaigns/labels";
import { readNextCursor, readStaffSummaries } from "@/lib/campaigns/normalise";
import { first, isCursor } from "@/lib/campaigns/paths";
import { CAMPAIGN_STATUSES } from "@/lib/campaigns/types";

export const metadata: Metadata = { title: "All campaigns", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/campaigns";

/** All campaigns, any status, with search (`campaign.view`). */
export default async function AdminCampaignsPage({ searchParams }: PageProps<"/admin/campaigns">) {
  await requireStaff();
  const p = await searchParams;
  const qRaw = first(p.q)?.trim() ?? "";
  const qText = qRaw.length <= 120 ? qRaw : qRaw.slice(0, 120);
  const statusRaw = first(p.status);
  const status = statusRaw && (CAMPAIGN_STATUSES as readonly string[]).includes(statusRaw) ? statusRaw : undefined;
  const cursorRaw = first(p.cursor);
  const params = new URLSearchParams();
  if (qText) params.set("q", qText);
  if (status) params.set("status", status);
  if (isCursor(cursorRaw)) params.set("cursor", cursorRaw);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/campaigns${params.size ? `?${params.toString()}` : ""}`, BASE);
  const items = readStaffSummaries(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  const next = nextRaw && isCursor(nextRaw) ? nextRaw : null;
  const nextQ = new URLSearchParams(params);
  if (next) nextQ.set("cursor", next);
  return (
    <AccountPage title="All campaigns" intro="Search campaigns in every status.">
      <form method="get" action={BASE} role="search" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end" aria-label="Search campaigns">
        <div className="space-y-1">
          <label htmlFor="search-q" className="block font-semibold text-ink-900">Search</label>
          <input id="search-q" name="q" type="search" defaultValue={qText} maxLength={120} className={inputClasses} />
        </div>
        <div className="space-y-1">
          <label htmlFor="search-status" className="block font-semibold text-ink-900">Status</label>
          <select id="search-status" name="status" defaultValue={status ?? ""} className={inputClasses}>
            <option value="">Any status</option>
            {CAMPAIGN_STATUSES.map((s) => (
              <option key={s} value={s}>{campaignStatusLabel(s, "staff").label}</option>
            ))}
          </select>
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>Search</button>
        </div>
      </form>
      <StaffCampaignTable items={items} caption="Campaigns" />
      {next ? (
        <ButtonLink href={`${BASE}?${nextQ.toString()}`} variant="outline">
          Next page
        </ButtonLink>
      ) : null}
    </AccountPage>
  );
}
