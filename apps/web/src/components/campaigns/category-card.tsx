import Link from "next/link";

import type { PublicCategory } from "@/lib/campaigns/types";

export function liveCountText(count: number | null): string | null {
  if (count === null) return null;
  return count === 1 ? "1 live campaign" : `${count.toLocaleString("en-GB")} live campaigns`;
}

/** Category link with its live count (exact, from GET /public/categories). */
export function CategoryCard({ category, headingLevel = 3 }: { category: PublicCategory; headingLevel?: 2 | 3 }) {
  const Heading = `h${headingLevel}` as const;
  const count = liveCountText(category.live_campaigns);
  return (
    <li className="relative flex min-w-0 flex-col gap-2 rounded-card border border-line bg-surface p-5 shadow-card focus-within:ring-2 focus-within:ring-brand-700">
      <Heading className="font-display text-xl font-semibold break-words text-ink-900">
        <Link href={`/categories/${category.slug}`} className="underline-offset-4 after:absolute after:inset-0 after:content-[''] hover:underline focus-visible:outline-none">
          {category.name}
        </Link>
      </Heading>
      {category.description ? <p className="break-words text-ink-700">{category.description}</p> : null}
      {count ? <p className="mt-auto text-sm font-semibold text-brand-800">{count}</p> : null}
    </li>
  );
}
