import type { Metadata } from "next";
import { Suspense } from "react";

import { CampaignGridSkeleton } from "@/components/campaigns/campaign-card";
import { CampaignResults } from "@/components/campaigns/campaign-results";
import { inputClasses } from "@/components/forms/text-field";
import { buttonClasses } from "@/components/ui/button";
import { Container } from "@/components/ui/container";
import { readListQuery } from "@/lib/campaigns/list-query";
import { fetchCategories } from "@/lib/campaigns/public";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "Explore campaigns",
    description: "Browse campaigns on FundZim that were reviewed before they were published. Donations are not yet available.",
    path: "/campaigns",
  });
}

/**
 * Discovery (GET /public/campaigns): live, listed campaigns only. The search form is a plain GET form (works
 * without JavaScript); results stream in behind a skeleton so the form shows at once.
 */
export default async function CampaignsListPage({ searchParams }: PageProps<"/campaigns">) {
  const query = readListQuery(await searchParams);
  const categories = await fetchCategories();
  const key = JSON.stringify(query);

  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-6">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Explore campaigns</h1>
          <p className="mt-2 text-lg text-ink-600">Every campaign here was reviewed before it was published. Donations are not yet available.</p>
        </div>
        <form method="get" action="/campaigns" role="search" aria-label="Search campaigns" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-2 lg:grid-cols-4 lg:items-end">
          <div className="space-y-1 sm:col-span-2">
            <label htmlFor="q" className="block font-semibold text-ink-900">
              Search
            </label>
            <input id="q" name="q" type="search" defaultValue={query.q} maxLength={100} enterKeyHint="search" className={inputClasses} />
          </div>
          <div className="space-y-1">
            <label htmlFor="category" className="block font-semibold text-ink-900">
              Category
            </label>
            <select id="category" name="category" defaultValue={categories.find((c) => c.code === query.category)?.slug ?? query.category} className={inputClasses}>
              <option value="">All categories</option>
              {categories.map((c) => (
                <option key={c.code} value={c.slug}>
                  {c.name}
                </option>
              ))}
            </select>
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
          <div className="sm:col-span-2 lg:col-span-4">
            <button type="submit" className={buttonClasses("primary")}>
              Search
            </button>
          </div>
        </form>
        <Suspense key={key} fallback={<CampaignGridSkeleton />}>
          <CampaignResults query={query} basePath="/campaigns" includeCategory trackSearch />
        </Suspense>
      </div>
    </Container>
  );
}
