import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";

import { CampaignGridSkeleton } from "@/components/campaigns/campaign-card";
import { DiscoveryResults } from "@/components/discovery/discovery-results";
import { ConnectionStatus } from "@/components/discovery/connection-status";
import { DiscoverySearchForm } from "@/components/discovery/search-form";
import { Container } from "@/components/ui/container";
import { fetchCategories } from "@/lib/campaigns/public";
import { isFiltered, readDiscoveryQuery } from "@/lib/discovery/query";
import { fetchLocations } from "@/lib/discovery/server";
import { pageMetadata } from "@/lib/seo/metadata";

const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

async function province(slug: string) {
  if (!SLUG.test(slug) || slug.length > 40) return null;
  const locations = await fetchLocations();
  if (!locations.ok) throw new Error("locations unavailable");
  const p = locations.data.provinces.find((x) => x.slug === slug);
  return p ? { province: p, towns: locations.data.towns.filter((t) => t.province_code === p.code), provinces: locations.data.provinces } : null;
}

export async function generateMetadata({ params, searchParams }: PageProps<"/campaigns/location/[province]">): Promise<Metadata> {
  const { province: slug } = await params;
  const found = await province(slug).catch(() => null);
  if (!found) return { title: "Page not found", robots: { index: false, follow: false } };
  const query = readDiscoveryQuery(await searchParams);
  return pageMetadata({
    title: `Campaigns in ${found.province.name} province`,
    description: `Campaigns on FundZim for causes in ${found.province.name} province, Zimbabwe. Every campaign was reviewed before it was published.`,
    path: `/campaigns/location/${found.province.slug}`,
    indexable: !isFiltered(query, ["province"]) && !query.cursor && !query.sort,
  });
}

/**
 * Campaigns for causes in one province (coarse location chosen by the organiser and reviewed; never an address and never
 * the visitor's location). Towns of the province filter further.
 */
export default async function ProvincePage({ params, searchParams }: PageProps<"/campaigns/location/[province]">) {
  const { province: slug } = await params;
  const found = await province(slug);
  if (!found) notFound();
  const base = `/campaigns/location/${found.province.slug}`;
  const query = { ...readDiscoveryQuery(await searchParams), province: found.province.slug };
  const town = found.towns.find((t) => t.slug === query.town);
  const categories = await fetchCategories();
  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-6">
        <nav aria-label="Breadcrumb">
          <Link href="/campaigns" className="inline-flex min-h-11 items-center text-sm font-semibold text-brand-700 underline">
            All campaigns
          </Link>
        </nav>
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">{town ? `${town.name}, ${found.province.name}` : `${found.province.name} province`}</h1>
          <p className="mt-2 max-w-3xl text-lg text-ink-600">
            Campaigns whose organisers said the cause is in {found.province.name} province. A location is chosen from a list and reviewed with the campaign; it is never an address.
          </p>
        </div>
        {found.towns.length > 0 ? (
          <nav aria-label={`Towns in ${found.province.name}`}>
            <ul className="flex flex-wrap gap-2">
              <li>
                <Link href={base} prefetch={false} aria-current={!town ? "page" : undefined} className="inline-flex min-h-11 items-center rounded-full border border-line bg-surface px-4 text-ink-900 aria-[current=page]:border-brand-700 aria-[current=page]:bg-brand-50">
                  Whole province
                </Link>
              </li>
              {found.towns.map((t) => (
                <li key={t.code}>
                  <Link href={`${base}?town=${t.slug}`} prefetch={false} aria-current={town?.slug === t.slug ? "page" : undefined} className="inline-flex min-h-11 items-center rounded-full border border-line bg-surface px-4 text-ink-900 aria-[current=page]:border-brand-700 aria-[current=page]:bg-brand-50">
                    {t.name}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        ) : null}
        <ConnectionStatus />
        <DiscoverySearchForm action={base} query={query} categories={categories} provinces={found.provinces} fixed={["province"]} label={`Search campaigns in ${found.province.name}`} />
        <Suspense key={JSON.stringify(query)} fallback={<CampaignGridSkeleton />}>
          <DiscoveryResults query={query} basePath={base} fixed={["province"]} surface="SEARCH" clearHref={base} />
        </Suspense>
      </div>
    </Container>
  );
}
