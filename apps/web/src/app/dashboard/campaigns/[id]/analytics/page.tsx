import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Alert } from "@/components/ui/alert";
import { requireUser } from "@/lib/auth/session";
import { first, isUuid } from "@/lib/campaigns/paths";
import { loadCampaignAnalytics } from "@/lib/discovery/server";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Campaign analytics", robots: { index: false, follow: false, nocache: true } };

const PERIODS = [7, 30, 90] as const;
const n = (v: number | undefined) => (v ?? 0).toLocaleString("en-GB");
// discrete bar widths: the CSP allows no inline style attributes
const BAR = ["w-0", "w-4", "w-8", "w-12", "w-16", "w-20", "w-24", "w-28", "w-32"] as const;

function Breakdown({ title, data, empty }: { title: string; data: Record<string, number>; empty: string }) {
  const rows = Object.entries(data).filter(([, v]) => v > 0).sort(([, a], [, b]) => b - a);
  return (
    <section aria-label={title} className="space-y-2 rounded-card border border-line bg-surface p-4">
      <h2 className="font-semibold text-ink-900">{title}</h2>
      {rows.length === 0 ? (
        <p className="text-ink-700">{empty}</p>
      ) : (
        <dl className="space-y-1">
          {rows.map(([k, v]) => (
            <div key={k} className="flex justify-between gap-3">
              <dt className="text-ink-700">{k === "" || k === "NONE" ? "Other" : humanise(k)}</dt>
              <dd className="font-semibold text-ink-900">{n(v)}</dd>
            </div>
          ))}
        </dl>
      )}
    </section>
  );
}

/**
 * Discovery analytics for the campaign's owner and organisation members (anyone else: 404). Aggregates only, with every
 * figure defined on the page; views and shares are never donations, and nothing identifies a visitor.
 */
export default async function CampaignAnalyticsPage({ params, searchParams }: PageProps<"/dashboard/campaigns/[id]/analytics">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `/dashboard/campaigns/${id}/analytics`;
  await requireUser(path);
  const raw = Number.parseInt(first((await searchParams).days) ?? "30", 10);
  const days = (PERIODS as readonly number[]).includes(raw) ? raw : 30;
  const a = await loadCampaignAnalytics(id, days, path);
  const ctr = a.clickThroughBp === null ? "No impressions yet" : `${(a.clickThroughBp / 100).toFixed(2)} %`;
  const maxViews = Math.max(1, ...a.daily.map((d) => d.views));
  return (
    <AccountPage title="Analytics" intro={`How people found and shared your campaign, ${a.from} to ${a.to}.`}>
      <Alert tone="info" title="What these numbers mean">
        Views, shares and clicks are counted without identifying anyone: each person counts once a day, automated traffic is
        excluded, and the figures come from use of this non-production environment. They are not donations — FundZim does not
        accept donations yet.
      </Alert>
      <nav aria-label="Period" className="flex flex-wrap gap-2">
        {PERIODS.map((p) => (
          <Link key={p} href={`${path}?days=${p}`} aria-current={p === days ? "page" : undefined} className="inline-flex min-h-11 items-center rounded-full border border-line px-4 text-ink-700 aria-[current=page]:border-brand-700 aria-[current=page]:bg-brand-50 aria-[current=page]:font-semibold">
            Last {p} days
          </Link>
        ))}
      </nav>
      <dl className="grid gap-3 sm:grid-cols-3">
        {[
          ["Views", a.totals.views],
          ["Unique visitors (daily)", a.totals.unique_visitors],
          ["Shares", a.totals.shares],
          ["Visits from shared links", a.totals.referral_visits],
          ["Shown in FundZim lists", a.totals.impressions],
          ["Opened from FundZim lists", a.totals.clicks],
        ].map(([label, value]) => (
          <div key={label as string} className="rounded-card border border-line bg-surface p-4">
            <dt className="text-sm font-semibold text-ink-600">{label}</dt>
            <dd className="font-display text-3xl font-bold text-ink-900">{n(value as number)}</dd>
          </div>
        ))}
      </dl>
      <p className="text-ink-700">
        Opened from lists per time shown: <strong>{ctr}</strong>. Automated visits excluded: {n(a.totals.bot_views_excluded)}.
      </p>
      <section aria-labelledby="daily-title" className="space-y-2">
        <h2 id="daily-title" className="font-display text-2xl font-semibold text-ink-900">
          Views per day
        </h2>
        <div className="overflow-x-auto rounded-card border border-line bg-surface" role="region" aria-label="Views per day (scrollable table)" tabIndex={0}>
          <table className="w-full min-w-[32rem] text-left text-ink-700">
            <caption className="sr-only">Views, unique visitors and shares per day</caption>
            <thead>
              <tr className="border-b border-line">
                <th scope="col" className="p-3">Day</th>
                <th scope="col" className="p-3">Views</th>
                <th scope="col" className="p-3">Unique visitors</th>
                <th scope="col" className="p-3">Shares</th>
              </tr>
            </thead>
            <tbody>
              {a.daily.map((d) => (
                <tr key={d.day} className="border-b border-line last:border-b-0">
                  <th scope="row" className="p-3 font-normal">{d.day}</th>
                  <td className="p-3">
                    <span className="flex items-center gap-2">
                      <span aria-hidden="true" className={`inline-block h-3 rounded bg-brand-700 ${BAR[Math.round((d.views / maxViews) * 8)]}`} />
                      {n(d.views)}
                    </span>
                  </td>
                  <td className="p-3">{n(d.unique_visitors)}</td>
                  <td className="p-3">{n(d.shares)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      <div className="grid gap-4 md:grid-cols-2">
        <Breakdown title="Shares by app" data={a.sharesByChannel} empty="No shares yet." />
        <Breakdown title="Visits from shared links, by app" data={a.referralsByChannel} empty="No visits from shared links yet." />
        <Breakdown title="Where views came from" data={a.trafficSources} empty="No views yet." />
        <Breakdown title="Shown in FundZim lists" data={a.impressionsBySurface} empty="Not shown in lists yet." />
      </div>
      <section aria-labelledby="defs-title" className="space-y-2">
        <h2 id="defs-title" className="font-display text-2xl font-semibold text-ink-900">
          Definitions
        </h2>
        <dl className="space-y-2 rounded-card border border-line bg-surface p-4">
          {Object.entries(a.definitions).map(([k, v]) => (
            <div key={k}>
              <dt className="font-semibold text-ink-900">{humanise(k)}</dt>
              <dd className="text-ink-700">{v}</dd>
            </div>
          ))}
        </dl>
      </section>
    </AccountPage>
  );
}
