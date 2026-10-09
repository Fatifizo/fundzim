import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { formatDateTime } from "@/components/account/format";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { inputClasses } from "@/components/forms/text-field";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { requireUser, serverGet, serverGetOptional } from "@/lib/auth/session";
import { formatGoal } from "@/lib/campaigns/money";
import { readCampaigns } from "@/lib/campaigns/normalise";
import { first, isUuid } from "@/lib/campaigns/paths";
import type { MyOrganisation } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Your campaigns", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/campaigns";

export default async function CampaignsPage({ searchParams }: PageProps<"/dashboard/campaigns">) {
  await requireUser(PATH);
  const orgRaw = first((await searchParams).organisation_id);
  const organisationId = isUuid(orgRaw) ? orgRaw : undefined;
  const [raw, organisations] = await Promise.all([
    serverGet<unknown>(`/api/v1/campaigns/mine${organisationId ? `?organisation_id=${organisationId}` : ""}`, PATH),
    serverGetOptional<MyOrganisation[]>("/api/v1/me/organisations", PATH, []),
  ]);
  const campaigns = readCampaigns(raw);
  const orgName = (id: string | null) => (id ? organisations.find((o) => o.id === id)?.display_name ?? "An organisation" : "You");
  return (
    <AccountPage title="Your campaigns" intro="Campaigns you run yourself or for organisations you belong to.">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        {organisations.length > 0 ? (
          <form method="get" action={PATH} className="flex flex-col gap-3 sm:flex-row sm:items-end" aria-label="Filter campaigns">
            <div className="space-y-1">
              <label htmlFor="filter-org" className="block font-semibold text-ink-900">
                Show campaigns of
              </label>
              <select id="filter-org" name="organisation_id" defaultValue={organisationId ?? ""} className={inputClasses}>
                <option value="">You and all your organisations</option>
                {organisations.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.display_name}
                  </option>
                ))}
              </select>
            </div>
            <button type="submit" className={buttonClasses("outline")}>
              Apply
            </button>
          </form>
        ) : (
          <span />
        )}
        <ButtonLink href="/dashboard/campaigns/new">Start a campaign</ButtonLink>
      </div>
      {campaigns.length === 0 ? (
        <EmptyState title="No campaigns yet" description="Start a campaign to raise funds for yourself, someone you support, or your organisation. Reviewers check every campaign before it can be published." />
      ) : (
        <ul className="grid gap-4 md:grid-cols-2" aria-label="Campaigns">
          {campaigns.map((c) => (
            <Card as="li" key={c.id} className="space-y-2">
              <p className="flex flex-wrap items-center gap-2">
                <CampaignStatusBadge status={String(c.status)} />
                {c.re_review_required ? <span className="text-sm text-ink-700">Changes waiting for review</span> : null}
              </p>
              <h2 className="font-display text-xl font-semibold break-words text-ink-900">
                <Link href={`/dashboard/campaigns/${c.id}`} className="underline-offset-4 hover:underline">
                  {c.title || "Untitled draft"}
                </Link>
              </h2>
              <p className="text-ink-700">Goal: {formatGoal(c.goal).text}</p>
              <p className="text-sm text-ink-600">
                Run by {orgName(c.organisation_id)} · Last changed {formatDateTime(c.updated_at ?? c.created_at)}
              </p>
            </Card>
          ))}
        </ul>
      )}
    </AccountPage>
  );
}
