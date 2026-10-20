import { notFound, permanentRedirect } from "next/navigation";

import { isCategorySlug } from "@/lib/campaigns/paths";
import { categoryPath } from "@/lib/site/url";

/** /campaigns/category/<slug> is an alias of the category page /categories/<slug> (one canonical URL per category). */
export default async function CategoryAlias({ params }: PageProps<"/campaigns/category/[slug]">) {
  const { slug } = await params;
  if (!isCategorySlug(slug)) notFound();
  permanentRedirect(categoryPath(slug));
}
