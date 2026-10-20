import type { Metadata } from "next";
import Link from "next/link";
import { Suspense } from "react";

import { CampaignGridSkeleton } from "@/components/campaigns/campaign-card";
import { DiscoveryResults } from "@/components/discovery/discovery-results";
import { ConnectionStatus } from "@/components/discovery/connection-status";
import { DiscoverySearchForm } from "@/components/discovery/search-form";
import { FeaturedStrip, TrendingStrip } from "@/components/discovery/strips";
import { Container } from "@/components/ui/container";
import { fetchCategories } from "@/lib/campaigns/public";
import { isFiltered, readDiscoveryQuery } from "@/lib/discovery/query";
import { fetchLocations } from "@/lib/discovery/server";
import { pageMetadata } from "@/lib/seo/metadata";

export async function generateMetadata({ searchParams }: PageProps<"/campaigns">): Promise<Metadata> {
  const query = readDiscoveryQuery(await searchParams);
  return pageMetadata({
    title: query.q ? `Search: ${query.q}` : "Explore campaigns",
    description: "Browse campaigns on FundZim by category and place in Zimbabwe. Every campaign was reviewed before it was published. Donations are not yet available.",
    path: "/campaigns",
    indexable: !isFiltered(query) && !query.cursor && !query.sort,
  });
}

/**
 * The campaign directory (GET /discovery/campaigns): search, filters (category, province, status, publication date,
 * organiser type), sorts and keyset pagination. Without a query or filter, the first page also shows featured and
 * trending campaigns and quick links to categories and provinces. Server-rendered; results stream in behind a skeleton.
 */
export default async function CampaignsListPage({ searchParams }: PageProps<"/campaigns">) {
  const query = readDiscoveryQuery(await searchParams);
  const [categories, locations] = await Promise.all([fetchCategories(), fetchLocations()]);
  const provinces = locations.ok ? locations.data.provinces : [];
  const landing = !isFiltered(query) && !query.cursor && !query.sort;

  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-8">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Explore campaigns</h1>
          <p className="mt-2 text-lg text-ink-600">Every campaign here was reviewed before it was published. Donations are not yet available.</p>
        </div>
        <ConnectionStatus />
        <DiscoverySearchForm action="/campaigns" query={query} categories={categories} provinces={provinces} />
        {landing ? (
          <>
            <nav aria-label="Browse by category" className="space-y-2">
              <h2 className="font-semibold text-ink-900">Browse by category</h2>
              <ul className="flex flex-wrap gap-2">
                {categories.map((c) => (
                  <li key={c.code}>
                    <Link href={`/categories/${c.slug}`} prefetch={false} className="inline-flex min-h-11 items-center rounded-full border border-line bg-surface px-4 font-semibold text-ink-900 hover:border-brand-700 hover:bg-brand-50">
                      {c.name}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
            {provinces.length > 0 ? (
              <nav aria-label="Browse by province" className="space-y-2">
                <h2 className="font-semibold text-ink-900">Browse by province</h2>
                <ul className="flex flex-wrap gap-2">
                  {provinces.map((p) => (
                    <li key={p.code}>
                      <Link href={`/campaigns/location/${p.slug}`} prefetch={false} className="inline-flex min-h-11 items-center rounded-full border border-line bg-surface px-4 text-ink-900 hover:border-brand-700 hover:bg-brand-50">
                        {p.name}
                        {p.count > 0 ? <span className="ml-2 text-sm text-ink-600">({p.count})</span> : null}
                      </Link>
                    </li>
                  ))}
                </ul>
              </nav>
            ) : null}
            <Suspense fallback={<CampaignGridSkeleton count={3} />}>
              <FeaturedStrip />
            </Suspense>
            <Suspense fallback={<CampaignGridSkeleton count={3} />}>
              <TrendingStrip />
            </Suspense>
            <h2 className="font-display text-2xl font-semibold text-ink-900">Recently published</h2>
          </>
        ) : null}
        <Suspense key={JSON.stringify(query)} fallback={<CampaignGridSkeleton />}>
          <DiscoveryResults query={query} basePath="/campaigns" surface="SEARCH" />
        </Suspense>
      </div>
    </Container>
  );
}
