import { PublicImage } from "@/components/campaigns/public-image";
import { Highlighted } from "@/components/discovery/highlighted";
import { ResultLink } from "@/components/discovery/result-link";
import { Badge } from "@/components/ui/badge";
import { formatGoal } from "@/lib/campaigns/money";
import { IMAGE_SIZES, publicImageSources } from "@/lib/campaigns/paths";
import { activeTrustIndicators } from "@/lib/campaigns/trust";
import type { PublicCampaignSummary } from "@/lib/campaigns/types";
import type { ClickSurface } from "@/lib/analytics/events";
import type { DiscoveryCard } from "@/lib/discovery/types";

/** A card's data: any public summary, with the discovery extras when the list came from the discovery API. */
export type CardData = PublicCampaignSummary & Partial<Pick<DiscoveryCard, "location" | "highlights" | "reason">>;

const STATUS_BADGE: Record<string, { label: string; tone: "brand" | "gold" | "neutral" }> = {
  ACTIVE: { label: "Active", tone: "brand" },
  PAUSED: { label: "Paused", tone: "gold" },
  COMPLETED: { label: "Ended", tone: "neutral" },
};

/**
 * Public campaign card: approved cover, title, summary, category, coarse location, organiser, goal with its currency,
 * status and only the trust indicators that are true; in discovery lists also why it is shown and the words that matched
 * a search. Never totals, donor counts or progress (none exist). The cover is decorative here (the title link names the
 * card), lazy-loaded with fixed dimensions (no CLS).
 */
export function CampaignCard({ campaign, headingLevel = 3, priority = false, surface }: { campaign: CardData; headingLevel?: 2 | 3; priority?: boolean; surface?: ClickSurface }) {
  const cover = campaign.cover ? publicImageSources(campaign.slug, campaign.cover.id, campaign.cover.widths, 640) : null;
  const goal = formatGoal(campaign.goal);
  const status = STATUS_BADGE[campaign.status];
  const trust = activeTrustIndicators(campaign.trust);
  const Heading = `h${headingLevel}` as const;
  return (
    <li className="relative flex min-w-0 flex-col overflow-hidden rounded-card border border-line bg-surface shadow-card focus-within:ring-2 focus-within:ring-brand-700">
      {cover ? (
        <PublicImage src={cover.src} srcSet={cover.srcSet} sizes={IMAGE_SIZES.card} alt="" width={640} height={360} priority={priority} className="aspect-video w-full bg-surface-muted object-cover" />
      ) : (
        <div aria-hidden="true" className="aspect-video w-full bg-[linear-gradient(135deg,var(--color-brand-100),var(--color-gold-200))]" />
      )}
      <div className="flex flex-1 flex-col gap-3 p-5">
        <p className="flex flex-wrap gap-2">
          <Badge tone="neutral">{campaign.category.name || campaign.category.code}</Badge>
          {status ? <Badge tone={status.tone}>{status.label}</Badge> : null}
        </p>
        {campaign.reason ? <p className="text-sm font-semibold text-brand-800">{campaign.reason.text}</p> : null}
        <Heading className="font-display text-xl font-semibold break-words text-ink-900">
          <ResultLink href={`/campaigns/${campaign.slug}`} slug={campaign.slug} surface={surface} className="underline-offset-4 hover:underline focus-visible:outline-none">
            <Highlighted text={campaign.title} segments={campaign.highlights?.title} />
          </ResultLink>
        </Heading>
        <p className="line-clamp-3 break-words text-ink-700">
          <Highlighted text={campaign.summary} segments={campaign.highlights?.summary} />
        </p>
        {campaign.location ? (
          <p className="text-sm text-ink-600">
            <span className="sr-only">Location: </span>
            {campaign.location.kind === "TOWN" ? `${campaign.location.name}, ${campaign.location.province_name}` : `${campaign.location.province_name} province`}
          </p>
        ) : null}
        {campaign.organiser ? <p className="text-sm break-words text-ink-600">By {campaign.organiser.display_name}</p> : null}
        {trust.length > 0 ? (
          <ul aria-label="Checks completed" className="flex flex-wrap gap-2">
            {trust.map((t) => (
              <li key={t.key}>
                <Badge tone="info">{t.short}</Badge>
              </li>
            ))}
          </ul>
        ) : null}
        <p className="mt-auto text-sm font-semibold text-ink-700">
          Goal: <span aria-hidden="true">{goal.text}</span>
          <span className="sr-only">{goal.accessibleText}</span>
        </p>
      </div>
    </li>
  );
}

/**
 * `priorityFirst`: the first card's cover loads eagerly with high fetch priority. Use it where the grid starts
 * near the top of the page (search results, category pages): on a phone that cover is often the LCP element,
 * and lazy loading would start it late and let it share bandwidth with the covers below (measured in
 * docs/stage-7/performance.md §8). Every other cover stays lazy.
 */
export function CampaignGrid({ campaigns, label, headingLevel = 3, priorityFirst = false, surface }: { campaigns: CardData[]; label: string; headingLevel?: 2 | 3; priorityFirst?: boolean; surface?: ClickSurface }) {
  return (
    <ul aria-label={label} className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
      {campaigns.map((c, i) => (
        <CampaignCard key={c.slug} campaign={c} headingLevel={headingLevel} priority={priorityFirst && i === 0} surface={surface} />
      ))}
    </ul>
  );
}

/** Loading placeholder with the same footprint as the grid (no layout shift when results arrive). */
export function CampaignGridSkeleton({ count = 6 }: { count?: number }) {
  return (
    <div role="status" aria-live="polite" className="space-y-3">
      <span className="sr-only">Loading campaigns…</span>
      <ul aria-hidden="true" className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: count }, (_, i) => (
          <li key={i} className="overflow-hidden rounded-card border border-line bg-surface shadow-card">
            <div className="aspect-video w-full animate-pulse bg-surface-muted" />
            <div className="space-y-3 p-5">
              <div className="h-6 w-24 animate-pulse rounded-full bg-surface-muted" />
              <div className="h-6 w-3/4 animate-pulse rounded bg-surface-muted" />
              <div className="h-4 w-full animate-pulse rounded bg-surface-muted" />
              <div className="h-4 w-2/3 animate-pulse rounded bg-surface-muted" />
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
