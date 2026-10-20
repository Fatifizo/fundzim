import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";

import { liveCountText } from "@/components/campaigns/category-card";
import { CampaignGridSkeleton } from "@/components/campaigns/campaign-card";
import { DiscoveryResults } from "@/components/discovery/discovery-results";
import { ConnectionStatus } from "@/components/discovery/connection-status";
import { DiscoverySearchForm } from "@/components/discovery/search-form";
import { TrendingStrip } from "@/components/discovery/strips";
import { JsonLdScript } from "@/components/seo/json-ld";
import { Alert } from "@/components/ui/alert";
import { Container } from "@/components/ui/container";
import { isCategorySlug } from "@/lib/campaigns/paths";
import { fetchCategories, fetchCategory } from "@/lib/campaigns/public";
import { isFiltered, readDiscoveryQuery } from "@/lib/discovery/query";
import { fetchLocations } from "@/lib/discovery/server";
import { breadcrumbJsonLd } from "@/lib/seo/json-ld";
import { pageMetadata } from "@/lib/seo/metadata";
import { categoryPath, siteOrigin } from "@/lib/site/url";

export async function generateMetadata({ params, searchParams }: PageProps<"/categories/[slug]">): Promise<Metadata> {
  const { slug } = await params;
  const category = isCategorySlug(slug) ? await fetchCategory(slug).catch(() => null) : null;
  if (!category) return { title: "Page not found", robots: { index: false, follow: false } };
  const query = readDiscoveryQuery(await searchParams);
  return pageMetadata({
    title: `${category.name} campaigns`,
    description: category.description ?? `Browse ${category.name.toLowerCase()} campaigns on FundZim. Every campaign is reviewed before it is published.`,
    path: categoryPath(category.slug),
    indexable: !isFiltered(query, ["category"]) && !query.cursor && !query.sort,
  });
}

/** A category's description, live count, popular campaigns and its listing (discovery search, filters and pagination). */
export default async function CategoryPage({ params, searchParams }: PageProps<"/categories/[slug]">) {
  const { slug } = await params;
  if (!isCategorySlug(slug)) notFound();
  const category = await fetchCategory(slug);
  if (!category) notFound();
  const query = { ...readDiscoveryQuery(await searchParams), category: category.slug };
  const path = categoryPath(category.slug);
  const count = liveCountText(category.live_campaigns);
  const [categories, locations] = await Promise.all([fetchCategories(), fetchLocations()]);
  const origin = siteOrigin();
  const landing = !isFiltered(query, ["category"]) && !query.cursor && !query.sort;

  return (
    <Container className="py-10 sm:py-14">
      {origin ? <JsonLdScript data={breadcrumbJsonLd([{ name: "Campaigns", path: "/campaigns" }, { name: category.name, path }], origin)} /> : null}
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
        <ConnectionStatus />
        <DiscoverySearchForm action={path} query={query} categories={categories} provinces={locations.ok ? locations.data.provinces : []} fixed={["category"]} label={`Search ${category.name} campaigns`} />
        {landing ? (
          <Suspense fallback={null}>
            <TrendingStrip category={category.slug} />
          </Suspense>
        ) : null}
        <Suspense key={JSON.stringify(query)} fallback={<CampaignGridSkeleton />}>
          <DiscoveryResults query={query} basePath={path} fixed={["category"]} surface="CATEGORY" clearHref={path} />
        </Suspense>
      </div>
    </Container>
  );
}
