import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { DiscoveryOperations } from "@/components/admin/discovery/discovery-admin";
import { HealthBadge } from "@/components/admin/operations/ops";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { Alert } from "@/components/ui/alert";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readOverview } from "@/lib/discovery/admin";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Discovery", robots: { index: false, follow: false, nocache: true } };

const n = (v: number) => v.toLocaleString("en-GB");
const facts = (m: Record<string, number>, empty: string): Array<[string, string]> =>
  Object.keys(m).length === 0 ? [[empty, "0"]] : Object.entries(m).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => [humanise(k), n(v)]);

/**
 * Discovery operations (`discovery.view`): search index health and backlog, search and engagement counters (never query
 * text or personal data), abuse indicators, featured and ranking state, top trending with its score components. Every
 * figure is measured in this environment (development and test use) — none of it is a donation or business result.
 */
export default async function DiscoveryAdminPage() {
  await requireStaff();
  const o = readOverview((await serverGetOr404<unknown>("/api/v1/admin/discovery/overview", "/admin/discovery")).data);
  return (
    <AccountPage title="Discovery" intro="Search, featured, trending, referrals and engagement. Aggregates measured in this environment; none of these figures is money.">
      <Alert tone="info" title="Non-production data">
        Figures come from development and test use of this environment. Views, shares and clicks are never donations, and no
        donation is attributed to a share or referral.
      </Alert>
      <Section id="index" title="Search index">
        <Facts items={[
          ["Status", <HealthBadge key="s" status={o.index.status} />],
          ["Campaigns in search", n(o.index.discoverable)],
          ["Removed (ended, unlisted, restricted, suspended)", n(o.index.notDiscoverable)],
          ["Waiting for an update", n(o.index.backlog)],
          ["Oldest waiting since", o.index.oldestBacklogAt ? <When key="o" at={o.index.oldestBacklogAt} /> : "—"],
          ["Repeatedly failing", n(o.index.failing)],
          ["Last indexed", <When key="l" at={o.index.lastIndexedAt} />],
          ["Trending computed", <When key="t" at={o.index.trendingAt} />],
          ["Campaigns ranked as trending", n(o.index.trendingRanked)],
        ]} />
        <DiscoveryOperations />
      </Section>
      <Section id="search" title="Searches (last 7 days)" description="Counted when the API answers; the words searched are never stored.">
        <Facts items={[...facts(o.search, "Searches"), ...facts(o.searchSorts, "Sorts").map(([k, v]) => [`Sorted by ${k.toLowerCase()}`, v] as [string, string])]} />
      </Section>
      <Section id="engagement" title="Engagement (last 7 days)" description="Definitions are below. Automated clients are excluded and counted separately.">
        <Facts items={facts(o.engagement, "Events")} />
      </Section>
      <Section id="shares" title="Shares and referral visits by channel (last 7 days)">
        <Facts items={[...facts(o.shares, "Shares").map(([k, v]) => [`Shared via ${k}`, v] as [string, string]), ...facts(o.referrals, "Referral visits").map(([k, v]) => [`Visits from ${k} links`, v] as [string, string])]} />
        <Facts items={[["Referral links", o.referralCodes.enabled ? "On" : "Off (switched off or blocked in production pending LR-023)"], ["Active codes", n(o.referralCodes.active)],
          ["Revoked codes", n(o.referralCodes.revoked)], ["Expired codes", n(o.referralCodes.expired)]]} />
      </Section>
      <Section id="abuse" title="Abuse indicators (last 7 days)" description="Bot-filtered views, visitors over the daily cap, rate-limited clients, invalid referral codes, events for campaigns that are not public, and events dropped by the buffer.">
        <Facts items={facts(o.abuse, "Indicators")} />
      </Section>
      <Section id="featured" title="Featured placements">
        <Facts items={[["Shown now", n(o.featured.live)], ["Waiting for approval", n(o.featured.requested)], ["Approved, starting later", n(o.featured.scheduled)]]} />
        <p>
          <Link href="/admin/discovery/featured" className="font-semibold text-brand-700 underline">
            Manage featured placements
          </Link>
        </p>
      </Section>
      <Section id="trending" title="Trending now">
        <p className="text-ink-700">
          Weights in force: {o.ranking.source === "APPROVED_POLICY" ? `approved policy version ${o.ranking.version}` : "engineering default (no policy approved yet)"}.
          {o.ranking.pending > 0 ? ` ${o.ranking.pending} proposal(s) waiting.` : ""}{" "}
          <Link href="/admin/discovery/ranking" className="font-semibold text-brand-700 underline">
            Ranking policies
          </Link>
        </p>
        {o.trending.length === 0 ? (
          <p className="text-ink-700">No campaign is ranked yet (not enough unique visitors).</p>
        ) : (
          <TableShell caption="Top trending campaigns" head={["Campaign", "Score", "Unique visitors", "Unique sharers", "Age (hours)"]}>
            {o.trending.map((t) => (
              <tr key={t.campaignId} className="border-b border-line last:border-b-0">
                <td className="p-3 break-words">
                  <Link href={`/campaigns/${t.slug}`} className="text-brand-700 underline">
                    {t.title || t.slug}
                  </Link>
                </td>
                <td className="p-3">{t.score}</td>
                <td className="p-3">{n(t.uniqueVisitors)}</td>
                <td className="p-3">{n(t.uniqueSharers)}</td>
                <td className="p-3">{n(t.ageHours)}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="definitions" title="Definitions">
        <dl className="space-y-2 rounded-card border border-line bg-surface p-4">
          {Object.entries(o.definitions).map(([k, v]) => (
            <div key={k}>
              <dt className="font-semibold text-ink-900">{humanise(k)}</dt>
              <dd className="text-ink-700">{v}</dd>
            </div>
          ))}
        </dl>
      </Section>
    </AccountPage>
  );
}
