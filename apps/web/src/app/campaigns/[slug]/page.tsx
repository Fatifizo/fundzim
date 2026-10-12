import type { Metadata } from "next";
import Link from "next/link";
import { notFound, permanentRedirect } from "next/navigation";

import { TrackEvent } from "@/components/analytics/track-event";
import { formatDateTime } from "@/components/account/format";
import { PublicImage } from "@/components/campaigns/public-image";
import { ReportCampaign } from "@/components/campaigns/report-dialog";
import { ShareControls } from "@/components/campaigns/share-controls";
import { StoryText } from "@/components/campaigns/story-text";
import { TrustIndicators } from "@/components/campaigns/trust-indicators";
import { JsonLdScript } from "@/components/seo/json-ld";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { PUBLIC_STATUS_NOTICE } from "@/lib/campaigns/labels";
import { formatGoal } from "@/lib/campaigns/money";
import { IMAGE_SIZES, isSlug, isUuid, publicImageSources, publicMediaUrl } from "@/lib/campaigns/paths";
import type { PublicImageRef } from "@/lib/campaigns/types";
import { fetchPublicCampaign, fetchPublicUpdates } from "@/lib/campaigns/public";
import { truncate } from "@/lib/campaigns/text";
import { campaignJsonLd } from "@/lib/seo/json-ld";
import { pageMetadata } from "@/lib/seo/metadata";
import { absoluteUrl, campaignPath, siteOrigin } from "@/lib/site/url";

const STATUS_SUFFIX: Record<string, string> = { PAUSED: " (paused)", COMPLETED: " (ended)" };

/**
 * Metadata from the approved version only: title (with a paused/ended marker so old shares do not solicit
 * misleadingly), a safe summary, canonical URL and the approved cover (or the generic FundZim image) as an
 * absolute URL from PUBLIC_SITE_URL. Never totals or amounts raised (none exist).
 */
export async function generateMetadata({ params }: PageProps<"/campaigns/[slug]">): Promise<Metadata> {
  const { slug } = await params;
  const campaign = isSlug(slug) ? await fetchPublicCampaign(slug).catch(() => null) : null;
  if (!campaign) return { title: "Page not found", robots: { index: false, follow: false } };
  const title = `${campaign.title}${STATUS_SUFFIX[campaign.status] ?? ""}`;
  const cover = campaign.cover ? shareImagePath(campaign.slug, campaign.cover) : null;
  return pageMetadata({
    title,
    description: truncate(campaign.summary, 200),
    path: campaignPath(campaign.slug),
    image: cover ? { path: cover, alt: campaign.cover?.alt_text || campaign.title } : null,
    type: "article",
  });
}

/** Open Graph / JSON-LD image: the 1024 px variant when the API listed one, else the full derivative. */
function shareImagePath(slug: string, image: PublicImageRef): string | null {
  const base = publicMediaUrl(slug, image.id);
  return base && image.widths?.includes(1024) ? `${base}?w=1024` : base;
}

/**
 * Public campaign page (approved version only). Text is escaped plain text (React text nodes: no HTML, no
 * auto-linked URLs). No donations exist: the donate control is disabled and no totals or progress are shown.
 * Anything not public is this app's 404; a non-canonical slug redirects permanently to the canonical one.
 */
export default async function PublicCampaignPage({ params }: PageProps<"/campaigns/[slug]">) {
  const { slug } = await params;
  if (!isSlug(slug)) notFound();
  const campaign = await fetchPublicCampaign(slug);
  if (!campaign) notFound();
  if (campaign.canonical_slug !== slug) permanentRedirect(campaignPath(campaign.canonical_slug));

  const updates = await fetchPublicUpdates(campaign.slug);
  const goal = formatGoal(campaign.goal);
  const cover = campaign.cover ? publicImageSources(campaign.slug, campaign.cover.id, campaign.cover.widths, 1024) : null;
  const shareImage = campaign.cover ? shareImagePath(campaign.slug, campaign.cover) : null;
  const notice = PUBLIC_STATUS_NOTICE[campaign.status];
  const origin = siteOrigin();
  const canonicalUrl = absoluteUrl(campaignPath(campaign.slug), origin);
  const organiserName = campaign.organisation?.display_name ?? campaign.organiser.display_name;

  return (
    <Container className="py-8 sm:py-12">
      <TrackEvent event={{ type: "CAMPAIGN_PAGE_VIEW", slug: campaign.slug }} />
      {origin ? (
        <JsonLdScript
          data={campaignJsonLd(
            { slug: campaign.slug, title: campaign.title, summary: campaign.summary, imageUrl: shareImage ? absoluteUrl(shareImage, origin) : null, publishedAt: campaign.published_at, categoryName: campaign.category.name || null },
            origin,
          )}
        />
      ) : null}
      <nav aria-label="Breadcrumb" className="mb-4">
        <ol className="flex flex-wrap items-center gap-2 text-sm">
          <li>
            <Link href="/campaigns" className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
              Campaigns
            </Link>
          </li>
          {campaign.category.slug ? (
            <li className="flex items-center gap-2">
              <span aria-hidden="true">/</span>
              <Link href={`/categories/${campaign.category.slug}`} className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
                {campaign.category.name}
              </Link>
            </li>
          ) : null}
        </ol>
      </nav>
      <article aria-labelledby="campaign-title" className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-6">
          {cover ? (
            <PublicImage src={cover.src} srcSet={cover.srcSet} sizes={IMAGE_SIZES.cover} alt={campaign.cover?.alt_text || `Cover photo for ${campaign.title}`} width={1200} height={675} priority className="aspect-video w-full rounded-card bg-surface-muted object-cover" />
          ) : null}
          {notice ? (
            <Alert tone="notice" title={notice.title}>
              {notice.body}
            </Alert>
          ) : null}
          <header className="space-y-3">
            <p className="flex flex-wrap gap-2">
              <Badge tone="neutral">{campaign.category.name || campaign.category.code}</Badge>
              {campaign.status === "PAUSED" ? <Badge tone="gold">Paused</Badge> : null}
              {campaign.status === "COMPLETED" ? <Badge tone="neutral">Ended</Badge> : null}
            </p>
            <h1 id="campaign-title" className="font-display text-3xl font-bold tracking-tight break-words text-ink-900 sm:text-4xl">
              {campaign.title}
            </h1>
            <p className="text-lg break-words text-ink-700">{campaign.summary}</p>
            {campaign.published_at ? <p className="text-ink-600">Published {formatDateTime(campaign.published_at)}</p> : null}
          </header>

          <section aria-labelledby="story-title" className="space-y-3">
            <h2 id="story-title" className="font-display text-2xl font-semibold text-ink-900">
              The story
            </h2>
            <StoryText text={campaign.story} className="max-w-prose text-lg leading-relaxed text-ink-700" />
          </section>

          {campaign.gallery.length > 0 ? (
            <section aria-labelledby="photos-title" className="space-y-3">
              <h2 id="photos-title" className="font-display text-2xl font-semibold text-ink-900">
                Photos
              </h2>
              <ul className="grid gap-4 sm:grid-cols-2">
                {campaign.gallery.map((m) => {
                  const img = publicImageSources(campaign.slug, m.id, m.widths, 640);
                  return img ? (
                    <li key={m.id}>
                      <PublicImage src={img.src} srcSet={img.srcSet} sizes={IMAGE_SIZES.gallery} alt={m.alt_text || `Photo from ${campaign.title}`} width={640} height={360} className="aspect-video w-full rounded-card bg-surface-muted object-cover" />
                    </li>
                  ) : null;
                })}
              </ul>
            </section>
          ) : null}

          <section aria-labelledby="updates-title" className="space-y-3">
            <h2 id="updates-title" className="font-display text-2xl font-semibold text-ink-900">
              Updates
            </h2>
            {!updates.ok ? <p className="text-ink-700">Updates could not be loaded right now.</p> : updates.data.length === 0 ? <p className="text-ink-700">No updates yet.</p> : null}
            {updates.ok && updates.data.length > 0 ? (
              <ol className="space-y-4">
                {updates.data.map((u) => (
                  <li key={u.id} className="space-y-2 rounded-card border border-line bg-surface p-5">
                    <h3 className="font-semibold break-words text-ink-900">{u.title}</h3>
                    <p className="text-sm text-ink-600">{formatDateTime(u.published_at)}</p>
                    <StoryText text={u.body} className="text-ink-700" />
                    {u.media_ids.filter(isUuid).length > 0 ? (
                      <ul className="grid gap-3 sm:grid-cols-2">
                        {u.media_ids.filter(isUuid).map((id, i) => {
                          const img = publicImageSources(campaign.slug, id, null, 640);
                          return img ? (
                            <li key={id}>
                              <PublicImage src={img.src} srcSet={img.srcSet} sizes={IMAGE_SIZES.gallery} alt={`Photo ${i + 1} from the update “${truncate(u.title, 80)}”`} width={640} height={360} className="aspect-video w-full rounded-card bg-surface-muted object-cover" />
                            </li>
                          ) : null;
                        })}
                      </ul>
                    ) : null}
                  </li>
                ))}
              </ol>
            ) : null}
          </section>
        </div>

        <aside aria-label="About this campaign" className="min-w-0 space-y-4 lg:sticky lg:top-24 lg:self-start">
          <Card className="space-y-4">
            <div>
              <p className="text-sm font-semibold text-ink-600">Goal</p>
              <p className="font-display text-2xl font-bold break-words text-ink-900">
                <span aria-hidden="true">{goal.text}</span>
                <span className="sr-only">{goal.accessibleText}</span>
              </p>
            </div>
            <button type="button" disabled aria-describedby="donations-note" className="inline-flex min-h-11 w-full cursor-not-allowed items-center justify-center rounded-full bg-surface-muted px-5 py-3 font-semibold text-ink-700">
              Donations are not yet available.
            </button>
            <p id="donations-note" className="text-sm text-ink-700">
              FundZim does not accept donations yet. Please do not send money to anyone because of this page, and never pay
              someone who contacts you claiming to collect for it.
            </p>
            <dl className="space-y-3 text-ink-700">
              <div>
                <dt className="text-sm font-semibold text-ink-600">Organised by</dt>
                <dd className="break-words">{organiserName}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Beneficiary</dt>
                <dd className="break-words">{campaign.beneficiary.disclosure === "DISPLAY_NAME" && campaign.beneficiary.display_name ? campaign.beneficiary.display_name : "Details kept private"}</dd>
              </div>
            </dl>
          </Card>
          <TrustIndicators trust={campaign.trust} />
          <Card className="space-y-2">
            <h2 className="font-semibold text-ink-900">Share this campaign</h2>
            <ShareControls slug={campaign.slug} title={campaign.title} summary={campaign.summary} url={canonicalUrl} showConfigNote={process.env.NODE_ENV !== "production"} />
            {!canonicalUrl && process.env.NODE_ENV === "production" ? <p className="text-sm text-ink-700">Sharing is not available right now.</p> : null}
          </Card>
          <div>
            <ReportCampaign slug={campaign.slug} title={campaign.title} />
          </div>
        </aside>
      </article>
    </Container>
  );
}
