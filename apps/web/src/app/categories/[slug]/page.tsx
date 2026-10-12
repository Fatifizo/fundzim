import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";

import { TrackEvent } from "@/components/analytics/track-event";
import { liveCountText } from "@/components/campaigns/category-card";
import { CampaignGridSkeleton } from "@/components/campaigns/campaign-card";
import { CampaignResults } from "@/components/campaigns/campaign-results";
import { inputClasses } from "@/components/forms/text-field";
import { Alert } from "@/components/ui/alert";
import { buttonClasses } from "@/components/ui/button";
import { Container } from "@/components/ui/container";
import { readListQuery } from "@/lib/campaigns/list-query";
import { isCategorySlug } from "@/lib/campaigns/paths";
import { fetchCategory } from "@/lib/campaigns/public";
import { pageMetadata } from "@/lib/seo/metadata";
import { categoryPath } from "@/lib/site/url";

export async function generateMetadata({ params }: PageProps<"/categories/[slug]">): Promise<Metadata> {
  const { slug } = await params;
  const category = isCategorySlug(slug) ? await fetchCategory(slug).catch(() => null) : null;
  if (!category) return { title: "Page not found", robots: { index: false, follow: false } };
  return pageMetadata({
    title: `${category.name} campaigns`,
    description: category.description ?? `Browse ${category.name.toLowerCase()} campaigns on FundZim. Every campaign is reviewed before it is published.`,
    path: categoryPath(category.slug),
  });
}

/** A category's description, live count and its campaign listing (same search, sort and pagination). */
export default async function CategoryPage({ params, searchParams }: PageProps<"/categories/[slug]">) {
  const { slug } = await params;
  if (!isCategorySlug(slug)) notFound();
  const category = await fetchCategory(slug);
  if (!category) notFound();
  const query = { ...readListQuery(await searchParams), category: category.slug };
  const path = categoryPath(category.slug);
  const count = liveCountText(category.live_campaigns);

  return (
    <Container className="py-10 sm:py-14">
      <TrackEvent event={{ type: "CATEGORY_VIEW", category: category.slug }} />
      <div className="space-y-6">
        <nav aria-label="Breadcrumb">
          <Link href="/categories" className="inline-flex min-h-11 items-center text-sm font-semibold text-brand-700 underline">
            All categories
          </Link>
        </nav>
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight break-words text-ink-900 sm:text-4xl">{category.name}</h1>
          {category.description ? <p className="mt-2 max-w-2xl text-lg break-words text-ink-600">{category.description}</p> : null}
          {count ? <p className="mt-2 font-semibold text-brand-800">{count}</p> : null}
        </div>
        {category.sensitive ? (
          <Alert tone="info" title="Please be respectful">
            Campaigns in this category can describe health, family or other personal circumstances. Share them with care and
            respect the privacy of the people involved.
          </Alert>
        ) : null}
        <form method="get" action={path} role="search" aria-label={`Search ${category.name} campaigns`} className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-[1fr_auto_auto] sm:items-end">
          <div className="space-y-1">
            <label htmlFor="q" className="block font-semibold text-ink-900">
              Search in {category.name}
            </label>
            <input id="q" name="q" type="search" defaultValue={query.q} maxLength={100} enterKeyHint="search" className={inputClasses} />
          </div>
          <div className="space-y-1">
            <label htmlFor="sort" className="block font-semibold text-ink-900">
              Sort by
            </label>
            <select id="sort" name="sort" defaultValue={query.sort} className={inputClasses}>
              <option value="published">Recently published</option>
              <option value="created">Recently created</option>
            </select>
          </div>
          <button type="submit" className={buttonClasses("primary")}>
            Search
          </button>
        </form>
        <Suspense key={JSON.stringify(query)} fallback={<CampaignGridSkeleton />}>
          <CampaignResults query={query} basePath={path} includeCategory={false} trackSearch />
        </Suspense>
      </div>
    </Container>
  );
}
