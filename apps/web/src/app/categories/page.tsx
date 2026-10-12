import type { Metadata } from "next";

import { CategoryCard } from "@/components/campaigns/category-card";
import { ButtonLink } from "@/components/ui/button";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { fetchPublicCategories } from "@/lib/campaigns/public";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "Categories",
    description: "Browse FundZim campaigns by cause: medical, education, funerals, community projects and more.",
    path: "/categories",
  });
}

/** Category list with live counts (GET /public/categories). */
export default async function CategoriesPage() {
  const categories = await fetchPublicCategories();
  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-8">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Categories</h1>
          <p className="mt-2 max-w-2xl text-lg text-ink-600">Find campaigns by the kind of need they meet. Counts show campaigns that are published and listed right now.</p>
        </div>
        {!categories.ok ? (
          <ErrorState
            title="Categories could not be loaded"
            description="Please try again in a moment."
            action={
              <ButtonLink href="/categories" variant="outline">
                Try again
              </ButtonLink>
            }
          />
        ) : categories.data.length === 0 ? (
          <EmptyState title="No categories yet" description="Categories will appear here once they are set up." />
        ) : (
          <ul aria-label="Categories" className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {categories.data.map((c) => (
              <CategoryCard key={c.code} category={c} headingLevel={2} />
            ))}
          </ul>
        )}
      </div>
    </Container>
  );
}
