import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { formatDateTime } from "@/components/account/format";
import { PublicImage } from "@/components/campaigns/public-image";
import { ShareControls } from "@/components/campaigns/share-controls";
import { StoryText } from "@/components/campaigns/story-text";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { PUBLIC_STATUS_NOTICE } from "@/lib/campaigns/labels";
import { formatGoal } from "@/lib/campaigns/money";
import { isSlug, publicMediaUrl } from "@/lib/campaigns/paths";
import { fetchPublicCampaign, fetchPublicUpdates, siteOrigin } from "@/lib/campaigns/public";
import { truncate } from "@/lib/campaigns/text";

const STATUS_SUFFIX: Record<string, string> = { PAUSED: " (paused)", COMPLETED: " (completed)" };

/**
 * Metadata from the published version only. Open Graph carries the title, summary and (when the canonical
 * site origin is configured) the cover image — never amounts raised or totals, which do not exist yet.
 */
export async function generateMetadata({ params }: PageProps<"/campaigns/[slug]">): Promise<Metadata> {
  const { slug } = await params;
  const campaign = isSlug(slug) ? await fetchPublicCampaign(slug).catch(() => null) : null;
  if (!campaign) return { title: "Page not found" };
  const title = `${campaign.title}${STATUS_SUFFIX[campaign.status] ?? ""}`;
  const description = truncate(campaign.summary, 200);
  const origin = siteOrigin();
  const path = `/campaigns/${campaign.slug}`;
  const image = campaign.cover ? publicMediaUrl(campaign.slug, campaign.cover.id) : null;
  return {
    title,
    description,
    ...(origin ? { metadataBase: origin, alternates: { canonical: path } } : {}),
    openGraph: {
      title,
      description,
      type: "website",
      siteName: "FundZim",
      ...(origin ? { url: path } : {}),
      ...(origin && image ? { images: [{ url: image, alt: campaign.cover?.alt_text || campaign.title }] } : {}),
    },
    twitter: { card: origin && image ? "summary_large_image" : "summary", title, description },
  };
}

/**
 * Public campaign page (published version only). Text is rendered as escaped plain text; no HTML from users.
 * There are no donations yet: the donate control is disabled and no totals or progress are shown.
 * Anything not public (including unpublished slugs) gets this app's 404.
 */
export default async function PublicCampaignPage({ params }: PageProps<"/campaigns/[slug]">) {
  const { slug } = await params;
  if (!isSlug(slug)) notFound();
  const campaign = await fetchPublicCampaign(slug);
  if (!campaign) notFound();
  const updates = await fetchPublicUpdates(campaign.slug);
  const goal = formatGoal(campaign.goal);
  const cover = campaign.cover ? publicMediaUrl(campaign.slug, campaign.cover.id) : null;
  const notice = PUBLIC_STATUS_NOTICE[campaign.status];
  const path = `/campaigns/${campaign.slug}`;

  return (
    <Container className="py-8 sm:py-12">
      <p className="mb-4">
        <Link href="/campaigns" className="font-semibold text-brand-700 underline">
          All campaigns
        </Link>
      </p>
      <article aria-labelledby="campaign-title" className="grid gap-8 lg:grid-cols-[1fr_22rem]">
        <div className="min-w-0 space-y-6">
          {cover ? (
            <PublicImage src={cover} alt={campaign.cover?.alt_text || `Cover photo for ${campaign.title}`} width={1200} height={675} priority className="aspect-video w-full rounded-card bg-surface-muted object-cover" />
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
              {campaign.status === "COMPLETED" ? <Badge tone="neutral">Completed</Badge> : null}
            </p>
            <h1 id="campaign-title" className="font-display text-3xl font-bold tracking-tight break-words text-ink-900 sm:text-4xl">
              {campaign.title}
            </h1>
            <p className="text-lg break-words text-ink-700">{campaign.summary}</p>
          </header>

          <section aria-labelledby="story-title" className="space-y-3">
            <h2 id="story-title" className="font-display text-2xl font-semibold text-ink-900">
              The story
            </h2>
            <StoryText text={campaign.story} className="text-lg leading-relaxed text-ink-700" />
          </section>

          {campaign.gallery.length > 0 ? (
            <section aria-labelledby="photos-title" className="space-y-3">
              <h2 id="photos-title" className="font-display text-2xl font-semibold text-ink-900">
                Photos
              </h2>
              <ul className="grid gap-4 sm:grid-cols-2">
                {campaign.gallery.map((m) => {
                  const src = publicMediaUrl(campaign.slug, m.id);
                  return src ? (
                    <li key={m.id}>
                      <PublicImage src={src} alt={m.alt_text || `Photo from ${campaign.title}`} width={640} height={360} className="aspect-video w-full rounded-card bg-surface-muted object-cover" />
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
            {updates.length === 0 ? <p className="text-ink-700">No updates yet.</p> : null}
            <ol className="space-y-4">
              {updates.map((u) => (
                <li key={u.id} className="rounded-card border border-line bg-surface p-5">
                  <h3 className="font-semibold break-words text-ink-900">{u.title}</h3>
                  <p className="text-sm text-ink-600">{formatDateTime(u.published_at ?? u.created_at)}</p>
                  <StoryText text={u.body} className="mt-2 text-ink-700" />
                </li>
              ))}
            </ol>
          </section>
        </div>

        <aside aria-label="About this campaign" className="space-y-4 lg:sticky lg:top-24 lg:self-start">
          <Card className="space-y-4">
            <div>
              <p className="text-sm font-semibold text-ink-600">Goal</p>
              <p className="font-display text-2xl font-bold text-ink-900">
                <span aria-hidden="true">{goal.text}</span>
                <span className="sr-only">{goal.accessibleText}</span>
              </p>
            </div>
            <button type="button" disabled className="inline-flex min-h-11 w-full cursor-not-allowed items-center justify-center rounded-full bg-surface-muted px-5 py-3 font-semibold text-ink-700">
              Donations are not yet available.
            </button>
            <p className="text-sm text-ink-700">FundZim is not accepting donations yet. Please do not send money to anyone on the strength of this page.</p>
            <dl className="space-y-3 text-ink-700">
              <div>
                <dt className="text-sm font-semibold text-ink-600">Organised by</dt>
                <dd className="break-words">{campaign.organisation?.display_name ?? campaign.organiser.display_name}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Beneficiary</dt>
                <dd className="break-words">{campaign.beneficiary.disclosure === "DISPLAY_NAME" && campaign.beneficiary.display_name ? campaign.beneficiary.display_name : "Details kept private"}</dd>
              </div>
              {campaign.published_at ? (
                <div>
                  <dt className="text-sm font-semibold text-ink-600">Published</dt>
                  <dd>{formatDateTime(campaign.published_at)}</dd>
                </div>
              ) : null}
            </dl>
          </Card>
          <Card className="space-y-2">
            <h2 className="font-semibold text-ink-900">Share this campaign</h2>
            <ShareControls title={campaign.title} path={path} />
          </Card>
        </aside>
      </article>
    </Container>
  );
}
