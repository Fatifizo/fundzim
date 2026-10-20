import { inputClasses } from "@/components/forms/text-field";
import { buttonClasses } from "@/components/ui/button";
import type { PublicCategory } from "@/lib/campaigns/types";
import {
  DISCOVERY_SORTS,
  ORGANISER_FILTERS,
  ORGANISER_LABELS,
  PUBLISHED_FILTERS,
  PUBLISHED_LABELS,
  SORT_LABELS,
  STATUS_FILTERS,
  STATUS_LABELS,
  type DiscoveryQuery,
} from "@/lib/discovery/query";
import type { LocationFacet } from "@/lib/discovery/types";

/**
 * Discovery search and filters: a plain GET form (works without JavaScript and on the slowest connections). The main
 * row is search, category, province and sort; status, publication date and organiser type sit behind "More filters"
 * (a native <details>, keyboard and screen-reader friendly). `fixed` hides a filter that the page itself sets.
 */
export function DiscoverySearchForm({
  action,
  query,
  categories,
  provinces,
  label = "Search campaigns",
  fixed = [],
}: {
  action: string;
  query: DiscoveryQuery;
  categories: PublicCategory[];
  provinces: LocationFacet[];
  label?: string;
  fixed?: ReadonlyArray<"category" | "province">;
}) {
  const more = query.status !== "" || query.published !== "" || query.organiser !== "";
  return (
    <form method="get" action={action} role="search" aria-label={label} className="space-y-4 rounded-card border border-line bg-surface p-4">
      {query.town ? <input type="hidden" name="town" value={query.town} /> : null}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4 lg:items-end">
        <div className="space-y-1 sm:col-span-2">
          <label htmlFor="q" className="block font-semibold text-ink-900">
            Search
          </label>
          <input id="q" name="q" type="search" defaultValue={query.q} maxLength={100} enterKeyHint="search" autoComplete="off" className={inputClasses} aria-describedby="q-hint" />
          <p id="q-hint" className="text-sm text-ink-600">
            Words in the title, story summary, category, place or organiser name.
          </p>
        </div>
        {fixed.includes("category") ? null : (
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
        )}
        {fixed.includes("province") ? null : (
          <div className="space-y-1">
            <label htmlFor="province" className="block font-semibold text-ink-900">
              Province
            </label>
            <select id="province" name="province" defaultValue={query.province} className={inputClasses}>
              <option value="">All of Zimbabwe</option>
              {provinces.map((p) => (
                <option key={p.code} value={p.slug}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
        )}
        <div className="space-y-1">
          <label htmlFor="sort" className="block font-semibold text-ink-900">
            Sort by
          </label>
          <select id="sort" name="sort" defaultValue={query.sort} className={inputClasses}>
            <option value="">{query.q ? SORT_LABELS.relevance : SORT_LABELS.newest} (default)</option>
            {DISCOVERY_SORTS.map((s) => (
              <option key={s} value={s}>
                {SORT_LABELS[s]}
              </option>
            ))}
          </select>
        </div>
      </div>
      <details open={more} className="group">
        <summary className="inline-flex min-h-11 cursor-pointer items-center font-semibold text-brand-700 underline">More filters</summary>
        <div className="mt-3 grid gap-4 sm:grid-cols-3">
          <div className="space-y-1">
            <label htmlFor="status" className="block font-semibold text-ink-900">
              Status
            </label>
            <select id="status" name="status" defaultValue={query.status} className={inputClasses}>
              <option value="">Any status</option>
              {STATUS_FILTERS.map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABELS[s]}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label htmlFor="published" className="block font-semibold text-ink-900">
              Published
            </label>
            <select id="published" name="published" defaultValue={query.published} className={inputClasses}>
              <option value="">Any time</option>
              {PUBLISHED_FILTERS.map((s) => (
                <option key={s} value={s}>
                  {PUBLISHED_LABELS[s]}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label htmlFor="organiser" className="block font-semibold text-ink-900">
              Organised by
            </label>
            <select id="organiser" name="organiser" defaultValue={query.organiser} className={inputClasses}>
              <option value="">Anyone</option>
              {ORGANISER_FILTERS.map((s) => (
                <option key={s} value={s}>
                  {ORGANISER_LABELS[s]}
                </option>
              ))}
            </select>
          </div>
        </div>
      </details>
      <div className="flex flex-wrap gap-3">
        <button type="submit" className={buttonClasses("primary")}>
          Search
        </button>
        <a href={action} className={buttonClasses("ghost")}>
          Clear
        </a>
      </div>
    </form>
  );
}
