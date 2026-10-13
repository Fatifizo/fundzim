import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, IntentsTable, NextPage, PAYMENTS_BASE, toOptions } from "@/components/admin/payments/common";
import { inputClasses } from "@/components/forms/text-field";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam, pick, uuidParam } from "@/lib/payments/labels";
import { readIntents } from "@/lib/payments/normalise";
import { INTENT_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Donation intents", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/intents`;

/** Donation intents (`payment.view`). Filters: status and campaign id. */
export default async function DonationIntentsPage({ searchParams }: PageProps<"/admin/payments/intents">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, INTENT_STATUSES);
  const campaignId = uuidParam(p.campaign_id);
  const filters = { status, campaign_id: campaignId };
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/intents${buildQuery({ ...filters, cursor: cursorParam(p.cursor) })}`, BASE);
  const items = readIntents(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  return (
    <AccountPage title="Donation intents" intro="A donor's intention to give an amount to a campaign. Each can have several payment attempts.">
      <FilterForm action={BASE} label="Filter donation intents" fields={[{ name: "status", label: "Status", value: status, options: toOptions(INTENT_STATUSES) }]} />
      <form method="get" action={BASE} aria-label="Find by campaign" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-[1fr_auto] sm:items-end">
        {status ? <input type="hidden" name="status" value={status} /> : null}
        <div className="space-y-1">
          <label htmlFor="filter-campaign" className="block font-semibold text-ink-900">
            Campaign id
          </label>
          <input id="filter-campaign" name="campaign_id" defaultValue={campaignId ?? ""} className={inputClasses} autoComplete="off" spellCheck={false} />
        </div>
        <button type="submit" className={buttonClasses("outline")}>
          Find
        </button>
      </form>
      <IntentsTable items={items} caption="Donation intents" />
      <NextPage base={BASE} query={filters} next={nextRaw && isCursor(nextRaw) ? nextRaw : null} />
    </AccountPage>
  );
}
